// Package epg 提供 EPG（电子节目指南）数据的管理功能，包括从配置的 XMLTV 源下载、
// 解析节目信息，存入数据库，并生成标准的 XMLTV 格式文件供播放器使用。
package epg

import (
	"context"
	"database/sql"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/models"
	"live-source-manager-go/pkg/logger"
	"live-source-manager-go/pkg/utils"
)

// Manager EPG 管理器
type Manager struct {
	cfg         *config.Config
	log         *logger.Logger
	db          *sql.DB
	httpClient  *http.Client
	mu          sync.RWMutex
	lastUpdate  time.Time
	updateTimer *time.Timer
}

// NewManager 创建 EPG 管理器实例
func NewManager(cfg *config.Config, log *logger.Logger, db *sql.DB) *Manager {
	return &Manager{
		cfg:        cfg,
		log:        log,
		db:         db,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// Start 启动 EPG 管理器，立即执行一次更新，并启动定时任务
func (m *Manager) Start() {
	m.log.Info("EPG 管理器启动，更新间隔 %d 小时", m.cfg.EPG.UpdateInterval)

	// 立即执行一次
	go m.Update()

	// 启动定时器
	m.scheduleNext()
}

// Stop 停止 EPG 管理器
func (m *Manager) Stop() {
	if m.updateTimer != nil {
		m.updateTimer.Stop()
	}
}

// scheduleNext 安排下一次更新
func (m *Manager) scheduleNext() {
	interval := time.Duration(m.cfg.EPG.UpdateInterval) * time.Hour
	m.updateTimer = time.AfterFunc(interval, func() {
		m.Update()
		m.scheduleNext()
	})
}

// Update 执行一次完整的 EPG 更新流程
func (m *Manager) Update() {
	m.log.Info("开始 EPG 数据更新...")
	start := time.Now()

	// 1. 获取 EPG 源列表
	sources := m.cfg.EPG.EPGSources
	if len(sources) == 0 {
		m.log.Warn("未配置 EPG 数据源，跳过更新")
		return
	}

	// 2. 并发下载并解析所有源
	allPrograms := make(map[string][]models.EPGProgram) // epg_id -> programs
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 3) // 最多并发 3 个

	for _, url := range sources {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			programs, err := m.downloadAndParse(u)
			if err != nil {
				m.log.Error("下载 EPG 失败 %s: %v", u, err)
				return
			}
			mu.Lock()
			for epgID, progs := range programs {
				allPrograms[epgID] = append(allPrograms[epgID], progs...)
			}
			mu.Unlock()
			m.log.Info("EPG 下载成功: %s (%d 个频道)", u, len(programs))
		}(url)
	}
	wg.Wait()

	if len(allPrograms) == 0 {
		m.log.Warn("没有获取到任何 EPG 数据")
		return
	}

	// 3. 存入数据库（事务中先删后插，或使用 REPLACE）
	if err := m.saveToDatabase(allPrograms); err != nil {
		m.log.Error("保存 EPG 数据失败: %v", err)
		return
	}

	// 4. 生成 XML 文件
	if err := m.generateXMLFile(allPrograms); err != nil {
		m.log.Error("生成 EPG XML 文件失败: %v", err)
	}

	// 5. 更新频道映射（将 epg_id 关联到 url_sources_passed）
	m.updateChannelMapping(allPrograms)

	m.mu.Lock()
	m.lastUpdate = time.Now()
	m.mu.Unlock()

	m.log.Info("EPG 更新完成，共 %d 个频道，耗时 %v", len(allPrograms), time.Since(start))
}

// downloadAndParse 下载并解析单个 XMLTV 源
func (m *Manager) downloadAndParse(url string) (map[string][]models.EPGProgram, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return m.parseXMLTV(body)
}

// parseXMLTV 解析 XMLTV 格式数据
func (m *Manager) parseXMLTV(data []byte) (map[string][]models.EPGProgram, error) {
	var tv XMLTVRoot
	if err := xml.Unmarshal(data, &tv); err != nil {
		return nil, fmt.Errorf("XML 解析失败: %w", err)
	}

	programs := make(map[string][]models.EPGProgram)
	for _, p := range tv.Programmes {
		epgID := p.Channel
		startTime, err := parseXMLTVTime(p.Start)
		if err != nil {
			continue
		}
		endTime, err := parseXMLTVTime(p.Stop)
		if err != nil {
			continue
		}

		prog := models.EPGProgram{
			EPGID:       epgID,
			StartTime:   startTime,
			EndTime:     endTime,
			Title:       p.Title,
			Description: toNullString(p.Desc),
			Category:    toNullString(p.Category),
			Language:    toNullString(p.Language),
			Icon:        toNullString(p.Icon),
			CreatedAt:   time.Now(),
		}
		programs[epgID] = append(programs[epgID], prog)
	}
	return programs, nil
}

// XMLTVRoot XMLTV 根结构
type XMLTVRoot struct {
	XMLName    xml.Name       `xml:"tv"`
	Programmes []XMLTVProgram `xml:"programme"`
}

// XMLTVProgram 单个节目
type XMLTVProgram struct {
	Channel  string `xml:"channel,attr"`
	Start    string `xml:"start,attr"`
	Stop     string `xml:"stop,attr"`
	Title    string `xml:"title"`
	Desc     string `xml:"desc"`
	Category string `xml:"category"`
	Language string `xml:"language"`
	Icon     string `xml:"icon"`
}

// parseXMLTVTime 解析 XMLTV 时间格式（如 20240101120000 +0800）
func parseXMLTVTime(s string) (time.Time, error) {
	// 格式：YYYYMMDDHHMMSS +ZZZZ 或 YYYYMMDDHHMMSS
	if len(s) >= 14 {
		layout := "20060102150405"
		t, err := time.Parse(layout, s[:14])
		if err != nil {
			return time.Time{}, err
		}
		// 处理时区
		if len(s) > 15 && (s[15] == '+' || s[15] == '-') {
			// 简化处理，忽略时区偏移，实际可进一步完善
		}
		return t, nil
	}
	return time.Time{}, fmt.Errorf("无效的时间格式: %s", s)
}

// saveToDatabase 将节目数据存入数据库（使用 REPLACE 或先删后插）
func (m *Manager) saveToDatabase(programs map[string][]models.EPGProgram) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 删除旧数据（可配置保留天数，这里简单全部删除）
	_, err = tx.Exec(`DELETE FROM epg_program`)
	if err != nil {
		return err
	}

	// 批量插入
	stmt, err := tx.Prepare(`INSERT INTO epg_program 
		(epg_id, start_time, end_time, title, description, category, language, icon, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, progs := range programs {
		for _, p := range progs {
			_, err = stmt.Exec(p.EPGID, p.StartTime, p.EndTime, p.Title,
				p.Description, p.Category, p.Language, p.Icon, p.CreatedAt)
			if err != nil {
				m.log.Warn("插入 EPG 节目失败: %v", err)
			}
		}
	}
	return tx.Commit()
}

// generateXMLFile 生成标准 XMLTV 文件到输出目录
func (m *Manager) generateXMLFile(programs map[string][]models.EPGProgram) error {
	outputPath := filepath.Join(m.cfg.Output.OutputDir, "epg.xml")

	var allProgrammes []XMLTVProgram
	for epgID, progs := range programs {
		for _, p := range progs {
			allProgrammes = append(allProgrammes, XMLTVProgram{
				Channel:  epgID,
				Start:    p.StartTime.Format("20060102150405") + " +0800",
				Stop:     p.EndTime.Format("20060102150405") + " +0800",
				Title:    p.Title,
				Desc:     p.Description.String,
				Category: p.Category.String,
				Language: p.Language.String,
				Icon:     p.Icon.String,
			})
		}
	}

	root := XMLTVRoot{Programmes: allProgrammes}
	data, err := xml.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	// 添加 XML 头
	content := []byte(xml.Header + string(data))
	return utils.AtomicWriteFile(outputPath, content)
}

// updateChannelMapping 根据 EPG 数据中的频道名称，尝试自动更新 url_sources_passed 的 epg_id
func (m *Manager) updateChannelMapping(programs map[string][]models.EPGProgram) {
	// 获取所有活跃频道
	rows, err := m.db.Query(`SELECT id, name FROM url_sources_passed WHERE status = 'active' AND epg_id IS NULL`)
	if err != nil {
		m.log.Error("查询待更新 EPG ID 的频道失败: %v", err)
		return
	}
	defer rows.Close()

	// 构建 epg_id -> 是否存在的映射
	epgIDSet := make(map[string]bool)
	for epgID := range programs {
		epgIDSet[epgID] = true
	}

	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			continue
		}
		// 尝试模糊匹配：频道名是否包含在某个 epg_id 中，或反之
		var matchedID string
		for epgID := range epgIDSet {
			if strings.Contains(strings.ToLower(epgID), strings.ToLower(name)) ||
				strings.Contains(strings.ToLower(name), strings.ToLower(epgID)) {
				matchedID = epgID
				break
			}
		}
		if matchedID != "" {
			m.db.Exec(`UPDATE url_sources_passed SET epg_id = ? WHERE id = ?`, matchedID, id)
		}
	}
}

// toNullString 辅助函数
func toNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
