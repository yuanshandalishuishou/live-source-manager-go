package epg

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/yuanshandalishuishou/live-source-manager-go/internal/config"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/db"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/logger"
)

// Manager 负责 EPG 数据的下载、解析、合并和数据库更新
type Manager struct {
	cfg        *config.Config
	db         *db.DB
	httpClient *http.Client
	mu         sync.Mutex
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

// NewManager 创建 EPG 管理器
func NewManager(cfg *config.Config, database *db.DB) *Manager {
	return &Manager{
		cfg:        cfg,
		db:         database,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		stopCh:     make(chan struct{}),
	}
}

// Start 启动自动更新循环（使用带 context 的 ticker 代替 time.AfterFunc）
func (m *Manager) Start() {
	m.wg.Add(1)
	go m.loop()
	logger.Info("EPG 自动更新已启动")
}

// Stop 停止自动更新
func (m *Manager) Stop() {
	close(m.stopCh)
	m.wg.Wait()
	logger.Info("EPG 自动更新已停止")
}

// loop 周期运行更新
func (m *Manager) loop() {
	defer m.wg.Done()
	interval := time.Duration(m.cfg.EPG.UpdateInterval) * time.Hour
	if interval <= 0 {
		interval = 12 * time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// 启动时立即执行一次
	m.update()

	for {
		select {
		case <-ticker.C:
			m.update()
		case <-m.stopCh:
			return
		}
	}
}

// UpdateNow 手动触发一次更新（供 API 调用）
func (m *Manager) UpdateNow() error {
	return m.update()
}

// update 执行完整的更新流程：下载 -> 解析 -> 合并 -> 写入数据库 -> 导出 XML
func (m *Manager) update() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	logger.Info("开始更新 EPG 数据")

	// 获取 EPG 源列表
	sources, err := m.getSources()
	if err != nil {
		return fmt.Errorf("获取 EPG 源失败: %w", err)
	}
	if len(sources) == 0 {
		return nil
	}

	// 并发下载并解析
	type epgResult struct {
		Programs []Program
		Err      error
	}
	resultCh := make(chan epgResult, len(sources))
	var wg sync.WaitGroup
	for _, url := range sources {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			progs, err := m.downloadAndParse(u)
			resultCh <- epgResult{Programs: progs, Err: err}
		}(url)
	}
	wg.Wait()
	close(resultCh)

	// 合并节目
	var allPrograms []Program
	for res := range resultCh {
		if res.Err != nil {
			logger.Warn("下载 EPG 源失败，跳过", "error", res.Err)
			continue
		}
		allPrograms = append(allPrograms, res.Programs...)
	}

	// 去重
	uniqueProgs := m.deduplicate(allPrograms)

	// 写入数据库
	if err := m.saveToDatabase(uniqueProgs); err != nil {
		return fmt.Errorf("保存 EPG 到数据库失败: %w", err)
	}

	// 导出 XML 文件
	if err := m.exportXML(uniqueProgs); err != nil {
		logger.Warn("导出 EPG XML 失败", "error", err)
	}

	// 更新频道映射
	m.updateChannelMapping(uniqueProgs)

	logger.Info("EPG 更新完成", "program_count", len(uniqueProgs))
	return nil
}

// getSources 从数据库或配置读取 EPG 源 URL 列表
func (m *Manager) getSources() ([]string, error) {
	// 从 sys_config 读取 epg_sources JSON 字段
	raw, err := m.db.GetConfigValue("EPG", "epg_sources")
	if err != nil || raw == "" {
		return nil, nil
	}
	var sources []string
	json.Unmarshal([]byte(raw), &sources)
	return sources, nil
}

// downloadAndParse 下载并解析 XMLTV 格式
func (m *Manager) downloadAndParse(url string) ([]Program, error) {
	resp, err := m.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 50*1024*1024)) // 50MB
	if err != nil {
		return nil, err
	}
	return parseXMLTV(data)
}

// deduplicate 按 (epg_id, start_time) 去重
func (m *Manager) deduplicate(progs []Program) []Program {
	seen := make(map[string]bool)
	var unique []Program
	for _, p := range progs {
		key := fmt.Sprintf("%s|%d", p.EpgID, p.StartTime.Unix())
		if !seen[key] {
			seen[key] = true
			unique = append(unique, p)
		}
	}
	return unique
}

// saveToDatabase 先删旧节目（保留天数），后批量插入
func (m *Manager) saveToDatabase(progs []Program) error {
	retentionDays := m.cfg.EPG.RetentionDays
	if retentionDays <= 0 {
		retentionDays = 7
	}
	// 删除过期节目
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	_, err := m.db.Exec("DELETE FROM epg_program WHERE start_time < ?", cutoff)
	if err != nil {
		return err
	}

	// 批量插入
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO epg_program (epg_id, start_time, end_time, title, description, category)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, p := range progs {
		_, err = stmt.Exec(p.EpgID, p.StartTime, p.EndTime, p.Title, p.Description, p.Category)
		if err != nil {
			logger.Warn("插入 EPG 节目失败", "error", err)
		}
	}
	return tx.Commit()
}

// exportXML 导出标准 XMLTV 文件
func (m *Manager) exportXML(progs []Program) error {
	// 省略 XML 序列化细节，使用 encoding/xml 生成
	return nil
}

// updateChannelMapping 将 EPG 的频道名称与 url_sources_passed 模糊匹配
func (m *Manager) updateChannelMapping(progs []Program) {
	// 简化实现：提取唯一 epg_id，然后通过名称模糊匹配更新
}

// Program EPG 节目
type Program struct {
	EpgID       string
	StartTime   time.Time
	EndTime     time.Time
	Title       string
	Description string
	Category    string
}

// parseXMLTV 解析 XMLTV 格式
func parseXMLTV(data []byte) ([]Program, error) {
	// 实现省略，可使用 github.com/nicholasgasior/goxmltv 等库
	return nil, nil
}
