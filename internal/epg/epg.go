// internal/epg/epg.go
// EPG 管理器：拉取、解析、存储电子节目单数据

package epg

import (
	"context"
	"database/sql"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"live-source-manager-go/internal/db"
	"live-source-manager-go/pkg/logger"
)

// Manager 负责 EPG 数据的定时更新和查询
type Manager struct {
	db          *db.DB
	epgURLs     []string          // EPG 数据源地址列表
	interval    time.Duration     // 更新间隔
	client      *http.Client
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

// NewManager 创建 EPG 管理器
func NewManager(database *db.DB, urls []string, interval time.Duration) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		db:       database,
		epgURLs:  urls,
		interval: interval,
		client:   &http.Client{Timeout: 30 * time.Second},
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Start 启动定时更新循环
func (m *Manager) Start() {
	m.wg.Add(1)
	go m.loop()
	logger.Info("EPG 管理器已启动，更新间隔 %v", m.interval)
}

// Stop 停止 EPG 管理器，等待当前更新完成
func (m *Manager) Stop() {
	m.cancel()
	m.wg.Wait()
	logger.Info("EPG 管理器已停止")
}

// loop 定时执行 EPG 更新
func (m *Manager) loop() {
	defer m.wg.Done()

	// 启动时立即执行一次
	m.update()

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.update()
		}
	}
}

// update 拉取所有 EPG 源并存入数据库
func (m *Manager) update() {
	for _, url := range m.epgURLs {
		logger.Info("开始更新 EPG 源: %s", url)
		if err := m.fetchAndStore(url); err != nil {
			logger.Error("更新 EPG 失败 [%s]: %v", url, err)
		}
	}
	// 清理过期数据
	if err := m.cleanupOldEntries(); err != nil {
		logger.Error("EPG 清理失败: %v", err)
	}
}

// fetchAndStore 下载并解析 XMLTV 格式的 EPG 数据，存入数据库
func (m *Manager) fetchAndStore(url string) error {
	resp, err := m.client.Get(url)
	if err != nil {
		return fmt.Errorf("HTTP请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP状态码: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取响应体失败: %w", err)
	}

	var tv xmltv
	if err := xml.Unmarshal(body, &tv); err != nil {
		return fmt.Errorf("XML解析失败: %w", err)
	}

	// 批量插入数据库（使用事务提高性能）
	tx, err := m.db.Conn().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO epg_data 
		(channel_name, start_time, end_time, title, description, category, updated_at) 
		VALUES (?, ?, ?, ?, ?, ?, datetime('now'))`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, prog := range tv.Programme {
		// 解析时间，XMLTV 格式通常为 "20060102150405 +0800"
		start, err := parseTime(prog.Start)
		if err != nil {
			logger.Warn("跳过无效节目开始时间 %s: %v", prog.Start, err)
			continue
		}
		end, err := parseTime(prog.Stop)
		if err != nil {
			logger.Warn("跳过无效节目结束时间 %s: %v", prog.Stop, err)
			continue
		}
		title := prog.Title
		desc := prog.Desc
		category := ""
		if len(prog.Category) > 0 {
			category = prog.Category[0]
		}

		_, err = stmt.Exec(prog.Channel, start, end, title, desc, category)
		if err != nil {
			return fmt.Errorf("插入节目数据失败: %w", err)
		}
	}

	return tx.Commit()
}

// cleanupOldEntries 删除 48 小时前的 EPG 数据
func (m *Manager) cleanupOldEntries() error {
	_, err := m.db.Conn().Exec(`DELETE FROM epg_data WHERE end_time < datetime('now', '-48 hours')`)
	return err
}

// 以下为 XMLTV 解析所需的结构体和辅助函数

type xmltv struct {
	XMLName  xml.Name    `xml:"tv"`
	Programme []programme `xml:"programme"`
}

type programme struct {
	Channel  string   `xml:"channel,attr"`
	Start    string   `xml:"start,attr"`
	Stop     string   `xml:"stop,attr"`
	Title    string   `xml:"title"`
	Desc     string   `xml:"desc"`
	Category []string `xml:"category"`
}

func parseTime(s string) (time.Time, error) {
	// 常见格式: "20230515103000 +0800"
	// 去除空格，统一处理
	s = strings.ReplaceAll(s, " ", "")
	formats := []string{
		"20060102150405-0700",
		"20060102150405+0700",
		"200601021504",
	}
	for _, f := range formats {
		t, err := time.Parse(f, s)
		if err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间: %s", s)
}
