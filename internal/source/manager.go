// internal/source/manager.go

package source

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yuanshandalishuishou/live-source-manager-go/internal/config"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/db"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/logger"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/models"
)

// Manager 负责从 live_sources 下载并解析源文件
type Manager struct {
	cfg        *config.Config
	db         *db.DB
	httpClient *http.Client
	parser     *Parser
}

// NewManager 创建下载管理器，接受外部注入的 HTTP 客户端
func NewManager(cfg *config.Config, database *db.DB, parser *Parser, client *http.Client) *Manager {
	return &Manager{
		cfg:        cfg,
		db:         database,
		httpClient: client,
		parser:     parser,
	}
}

// DownloadAll 遍历所有启用的网络源，下载并更新
// 返回处理的源文件和发现的条目总数
func (m *Manager) DownloadAll(ctx context.Context) (int, error) {
	sources, err := m.db.GetEnabledLiveSources("url")
	if err != nil {
		return 0, fmt.Errorf("查询 live_sources 失败: %w", err)
	}
	if len(sources) == 0 {
		logger.Info("没有启用的网络源需要下载")
		return 0, nil
	}

	type result struct {
		sourceID int
		entries  []models.URLSource
		err      error
	}
	resultCh := make(chan result, len(sources))

	var wg sync.WaitGroup
	for _, src := range sources {
		wg.Add(1)
		go func(s models.LiveSource) {
			defer wg.Done()
			entries, err := m.downloadAndParse(ctx, s)
			resultCh <- result{sourceID: s.ID, entries: entries, err: err}
		}(src)
	}
	wg.Wait()
	close(resultCh)

	totalEntries := 0
	for res := range resultCh {
		if res.err != nil {
			logger.Error("下载或解析失败", "source_id", res.sourceID, "error", res.err)
			m.db.UpdateDownloadStatus(res.sourceID, "failed", 0)
			continue
		}
		// 更新下载状态
		m.db.UpdateDownloadStatus(res.sourceID, "success", http.StatusOK)
		// 将解析出的条目存入 url_sources（带 live_source_id 和去重）
		inserted, err := m.db.BatchInsertURLSources(res.sourceID, res.entries)
		if err != nil {
			logger.Error("批量插入 url_sources 失败", "source_id", res.sourceID, "error", err)
			continue
		}
		totalEntries += inserted
	}
	return totalEntries, nil
}

// downloadAndParse 下载网络文件或读取本地文件，调用解析器
func (m *Manager) downloadAndParse(ctx context.Context, src models.LiveSource) ([]models.URLSource, error) {
	var content []byte
	var err error

	switch src.LocationType {
	case "url":
		content, err = m.downloadWithRetry(ctx, src.Location)
		if err != nil {
			return nil, fmt.Errorf("下载失败 (%s): %w", src.Location, err)
		}
	case "local_file":
		content, err = os.ReadFile(src.Location)
		if err != nil {
			return nil, fmt.Errorf("读取本地文件失败: %w", err)
		}
	default:
		return nil, fmt.Errorf("不支持的 location_type: %s", src.LocationType)
	}

	// 解析内容
	entries, err := m.parser.Parse(content)
	if err != nil {
		return nil, fmt.Errorf("解析失败: %w", err)
	}
	return entries, nil
}

// downloadWithRetry 下载网络文件，对 GitHub 等源尝试多重回退策略
func (m *Manager) downloadWithRetry(ctx context.Context, url string) ([]byte, error) {
	strategies := m.resolveStrategies(url)

	var lastErr error
	for _, candidate := range strategies {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		data, err := m.downloadSingle(ctx, candidate)
		if err == nil {
			return data, nil
		}
		lastErr = err
		logger.Warn("下载失败，尝试下一策略", "url", candidate, "error", err)
	}
	return nil, fmt.Errorf("所有下载策略均失败: %w", lastErr)
}

// resolveStrategies 对 GitHub 链接生成多个回退 URL
func (m *Manager) resolveStrategies(url string) []string {
	var out []string
	out = append(out, url)

	if strings.Contains(url, "github.com") {
		// 替换为 raw.githubusercontent.com
		raw := strings.Replace(url, "github.com", "raw.githubusercontent.com", 1)
		raw = strings.Replace(raw, "/blob/", "/", 1)
		out = append(out, raw)

		// 添加 ghproxy 前缀
		out = append(out, "https://ghproxy.com/"+url)

		// 如果有配置的代理地址
		if m.cfg.Network.ProxyURL != "" {
			out = append(out, m.cfg.Network.ProxyURL+url)
		}
	}
	return out
}

// downloadSingle 执行单次 HTTP GET 下载
func (m *Manager) downloadSingle(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Live-Source-Manager)")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	// 限制读取大小，防止内存溢出
	limitReader := io.LimitReader(resp.Body, 10*1024*1024) // 10 MB
	data, err := io.ReadAll(limitReader)
	if err != nil {
		return nil, err
	}
	return data, nil
}
