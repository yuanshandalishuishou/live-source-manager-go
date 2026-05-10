// internal/source/manager.go
// 统一管理订阅源的下载和解析，输出 URLSource 列表供后续测试和存储。

package source

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/models"
	"live-source-manager-go/pkg/logger"
)

// Manager 管理源文件下载和解析。
type Manager struct {
	cfg        *config.Config
	db         *db.DB
	parser     *Parser
	httpClient *http.Client
}

// NewManager 创建源管理器。
func NewManager(cfg *config.Config, database *db.DB, parser *Parser, client *http.Client) *Manager {
	return &Manager{
		cfg:        cfg,
		db:         database,
		parser:     parser,
		httpClient: client,
	}
}

// DownloadAll 下载所有订阅源并解析为 URLSource 列表。
// 返回所有解析出的条目，同时将错误记录到日志但不阻断流程。
func (m *Manager) DownloadAll(ctx context.Context) (int, error) {
	// 从数据库获取所有激活的订阅源
	sources, err := m.db.GetActiveLiveSources()
	if err != nil {
		return 0, fmt.Errorf("获取订阅源失败: %w", err)
	}

	totalEntries := 0
	for _, src := range sources {
		logger.Info("正在下载订阅源: %s (%s)", src.Name, src.Location)
		entries, err := m.downloadAndParse(ctx, src)
		if err != nil {
			logger.Error("下载解析失败 [%s]: %v", src.Name, err)
			// 可以考虑更新状态为失败
			continue
		}
		// 将解析出的条目存入 url_sources_passed 或中间表
		for _, e := range entries {
			if err := m.db.InsertURLSource(e); err != nil {
				logger.Warn("插入 URL 源失败: %v", err)
			}
		}
		totalEntries += len(entries)
	}
	return totalEntries, nil
}

// downloadAndParse 下载单个订阅源并解析。
func (m *Manager) downloadAndParse(ctx context.Context, src models.LiveSource) ([]models.URLSource, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.Location, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	entries, err := m.parser.Parse(resp.Body)
	if err != nil {
		return nil, err
	}
	return entries, nil
}
