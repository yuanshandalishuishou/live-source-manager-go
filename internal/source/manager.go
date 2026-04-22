package source

import (
	"context"
	"crypto/md5"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/models"
	"live-source-manager-go/internal/rules"
	"live-source-manager-go/pkg/logger"
	"live-source-manager-go/pkg/utils"
)

// Manager 源管理器
type Manager struct {
	cfg        *config.Config
	log        *logger.Logger
	db         *sql.DB
	rulesMgr   *rules.Manager
	httpClient *http.Client
	onlineDir  string
}

// NewManager 创建源管理器
func NewManager(cfg *config.Config, log *logger.Logger, db *sql.DB, rulesMgr *rules.Manager) *Manager {
	client := &http.Client{Timeout: 60 * time.Second}
	onlineDir := "/config/online"
	os.MkdirAll(onlineDir, 0755)

	return &Manager{
		cfg:        cfg,
		log:        log,
		db:         db,
		rulesMgr:   rulesMgr,
		httpClient: client,
		onlineDir:  onlineDir,
	}
}

// DownloadAll 下载所有启用的在线源，返回成功下载的文件路径
func (m *Manager) DownloadAll() []string {
	// 从数据库获取启用的订阅源
	rows, err := m.db.Query(`SELECT id, location FROM live_sources 
		WHERE enable = 1 AND location_type = 'url'`)
	if err != nil {
		m.log.Error("查询订阅源失败: %v", err)
		return nil
	}
	defer rows.Close()

	type task struct {
		id       int64
		location string
	}
	var tasks []task
	for rows.Next() {
		var t task
		if err := rows.Scan(&t.id, &t.location); err == nil {
			tasks = append(tasks, t)
		}
	}

	m.log.Info("开始下载 %d 个在线源", len(tasks))

	var wg sync.WaitGroup
	sem := make(chan struct{}, 3)
	resultChan := make(chan string, len(tasks))

	for _, t := range tasks {
		wg.Add(1)
		go func(id int64, loc string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if path, err := m.downloadWithRetry(loc); err == nil {
				resultChan <- path
				m.updateDownloadStatus(id, "success", 200)
				m.log.Info("下载成功: %s", loc)
			} else {
				m.updateDownloadStatus(id, "failed", 0)
				m.log.Error("下载失败 %s: %v", loc, err)
			}
		}(t.id, t.location)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	var downloaded []string
	for p := range resultChan {
		downloaded = append(downloaded, p)
	}
	m.log.Info("成功下载 %d 个源文件", len(downloaded))
	return downloaded
}

func (m *Manager) updateDownloadStatus(id int64, status string, httpStatus int) {
	_, err := m.db.Exec(`UPDATE live_sources SET download_status = ?, http_status = ?, last_download = ? WHERE id = ?`,
		status, httpStatus, time.Now(), id)
	if err != nil {
		m.log.Error("更新下载状态失败: %v", err)
	}
}

func (m *Manager) downloadWithRetry(downloadURL string) (string, error) {
	strategies := []bool{false, true}
	for _, useProxy := range strategies {
		if useProxy && !m.cfg.Network.ProxyEnabled {
			continue
		}
		path, err := m.downloadFile(downloadURL, useProxy)
		if err == nil {
			return path, nil
		}
		m.log.Debug("下载失败 (proxy=%v): %s - %v", useProxy, downloadURL, err)
	}
	return "", fmt.Errorf("所有策略均失败")
}

func (m *Manager) downloadFile(downloadURL string, useProxy bool) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	filename := getFilenameFromURL(downloadURL)
	filepath := filepath.Join(m.onlineDir, filename)
	if err := os.WriteFile(filepath, body, 0644); err != nil {
		return "", err
	}
	return filepath, nil
}

func getFilenameFromURL(rawURL string) string {
	u, _ := url.Parse(rawURL)
	filename := filepath.Base(u.Path)
	if filename == "" || filename == "." || filename == "/" {
		hash := md5.Sum([]byte(rawURL))
		filename = fmt.Sprintf("source_%x.txt", hash[:4])
	}
	filename = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, filename)
	return filename
}

// ParseAll 解析所有源文件（在线下载的 + 本地源目录），应用别名，去重后存入 url_sources 表
func (m *Manager) ParseAll() ([]models.URLSource, error) {
	var allSources []models.URLSource

	// 1. 解析在线下载的源
	onlineSources := m.parseDirectory(m.onlineDir, "online")
	allSources = append(allSources, onlineSources...)

	// 2. 解析本地源目录
	for _, dir := range m.cfg.Sources.LocalDirs {
		if _, err := os.Stat(dir); err == nil {
			localSources := m.parseDirectory(dir, "local")
			allSources = append(allSources, localSources...)
		}
	}

	m.log.Info("共解析 %d 个直播源（应用别名前）", len(allSources))

	// 3. 应用正则别名
	for i := range allSources {
		newName := m.rulesMgr.ApplyAlias(allSources[i].Name)
		if newName != allSources[i].Name {
			allSources[i].Name = newName
		}
	}

	// 4. 去重并入库
	m.deduplicateAndInsert(allSources)

	return allSources, nil
}

func (m *Manager) parseDirectory(dir, sourceType string) []models.URLSource {
	var sources []models.URLSource
	entries, err := os.ReadDir(dir)
	if err != nil {
		m.log.Error("读取目录失败 %s: %v", dir, err)
		return sources
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".m3u") && !strings.HasSuffix(name, ".m3u8") && !strings.HasSuffix(name, ".txt") {
			continue
		}
		filePath := filepath.Join(dir, name)
		data, err := os.ReadFile(filePath)
		if err != nil {
			m.log.Error("读取文件失败 %s: %v", filePath, err)
			continue
		}
		content := string(data)

		var rawSources []models.URLSource
		if strings.HasSuffix(name, ".txt") {
			rawSources = ParseTXT(content, sourceType, filePath)
		} else {
			rawSources = ParseM3U(content, sourceType, filePath)
		}

		// 获取对应的 live_source_id（如果来自数据库订阅）
		var liveSourceID sql.NullInt64
		if sourceType == "online" {
			var id int64
			err := m.db.QueryRow(`SELECT id FROM live_sources WHERE location = ?`, filePath).Scan(&id)
			if err == nil {
				liveSourceID = sql.NullInt64{Int64: id, Valid: true}
			}
		}
		for _, src := range rawSources {
			src.LiveSourceID = liveSourceID
			// IP 版本过滤
			if !utils.CheckIPVersion(src.URL, m.cfg.Network.IPVersion) {
				continue
			}
			sources = append(sources, src)
		}
	}
	return sources
}

// deduplicateAndInsert 按 (url, name) 去重并插入 url_sources 表
func (m *Manager) deduplicateAndInsert(sources []models.URLSource) {
	// 使用 map 去重
	seen := make(map[string]bool)
	for _, src := range sources {
		key := src.URL + "|" + src.Name
		if seen[key] {
			continue
		}
		seen[key] = true

		// 插入数据库，忽略重复（利用 UNIQUE 约束）
		_, err := m.db.Exec(`INSERT OR IGNORE INTO url_sources 
			(live_source_id, url, name, tvg_id, tvg_logo, group_title, catchup, catchup_days, user_agent, raw_attributes, source_type, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			src.LiveSourceID, src.URL, src.Name, src.TvgID, src.TvgLogo, src.GroupTitle,
			src.Catchup, src.CatchupDays, src.UserAgent, src.RawAttributes, src.SourceType, time.Now())
		if err != nil {
			m.log.Error("插入 url_sources 失败: %v", err)
		}
	}
	m.log.Info("去重后共 %d 个唯一源", len(seen))
}
