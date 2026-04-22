// Package rtmp 提供 RTMP 推流管理功能，包括启动/停止 FFmpeg 推流进程、
// 与 Nginx-RTMP 服务器集成、HLS 地址生成、空闲检测与资源回收。
package rtmp

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/pkg/logger"
)

// Manager RTMP 推流管理器
type Manager struct {
	cfg        *config.Config
	log        *logger.Logger
	db         *sql.DB
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	streamsMu  sync.RWMutex
	streams    map[int64]*StreamController
	maxStreams int
}

// StreamController 单个推流控制器
type StreamController struct {
	sourceID   int64
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	status     string
	hlsURL     string
	pushURL    string
	lastAccess time.Time
	mu         sync.RWMutex
}

// NewManager 创建 RTMP 管理器实例
func NewManager(cfg *config.Config, log *logger.Logger, db *sql.DB) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		cfg:        cfg,
		log:        log,
		db:         db,
		ctx:        ctx,
		cancel:     cancel,
		streams:    make(map[int64]*StreamController),
		maxStreams: cfg.RTMP.RTMPMaxStreams,
	}
}

// Start 启动管理器
func (m *Manager) Start() error {
	if !m.cfg.RTMP.OpenRTMP {
		m.log.Info("RTMP 推流功能未启用")
		return nil
	}

	hlsDir := "/var/www/hls"
	if err := os.MkdirAll(hlsDir, 0755); err != nil {
		return fmt.Errorf("创建 HLS 目录失败: %w", err)
	}

	m.recoverStreams()

	m.wg.Add(1)
	go m.idleMonitor()

	m.wg.Add(1)
	go m.scheduler()

	m.log.Info("RTMP 推流管理器已启动，最大并发 %d 个流", m.maxStreams)
	return nil
}

// Stop 停止管理器
func (m *Manager) Stop() {
	m.cancel()
	m.stopAllStreams()
	m.wg.Wait()
	m.log.Info("RTMP 推流管理器已停止")
}

func (m *Manager) recoverStreams() {
	rows, err := m.db.Query(`SELECT id, source_id, push_url, hls_url FROM rtmp_streams WHERE stream_status = 'pushing'`)
	if err != nil {
		m.log.Error("查询待恢复推流失败: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var rec struct {
			ID       int64
			SourceID int64
			PushURL  sql.NullString
			HLSURL   sql.NullString
		}
		if err := rows.Scan(&rec.ID, &rec.SourceID, &rec.PushURL, &rec.HLSURL); err != nil {
			continue
		}
		m.log.Info("尝试恢复推流: source_id=%d", rec.SourceID)
		if err := m.StartStream(rec.SourceID); err != nil {
			m.log.Error("恢复推流失败 source_id=%d: %v", rec.SourceID, err)
			m.db.Exec(`UPDATE rtmp_streams SET stream_status = 'error', error_message = ? WHERE id = ?`, err.Error(), rec.ID)
		}
	}
}

// StartStream 启动指定源的推流
func (m *Manager) StartStream(sourceID int64) error {
	if !m.cfg.RTMP.OpenRTMP {
		return fmt.Errorf("RTMP 功能未启用")
	}

	m.streamsMu.Lock()
	defer m.streamsMu.Unlock()

	if _, exists := m.streams[sourceID]; exists {
		return nil
	}

	if len(m.streams) >= m.maxStreams {
		return fmt.Errorf("已达到最大并发推流数 %d", m.maxStreams)
	}

	var src struct {
		URL  string
		Name string
	}
	err := m.db.QueryRow(`SELECT url, name FROM url_sources_passed WHERE id = ? AND status = 'active'`, sourceID).Scan(&src.URL, &src.Name)
	if err != nil {
		return fmt.Errorf("获取源信息失败: %w", err)
	}

	pushURL := fmt.Sprintf("rtmp://127.0.0.1:%d/live/%s", m.cfg.RTMP.NginxRTMPPort, sanitizeStreamKey(src.Name))
	hlsURL := fmt.Sprintf("http://127.0.0.1:%d/hls/%s.m3u8", m.cfg.RTMP.NginxHTTPPort, sanitizeStreamKey(src.Name))

	args := m.buildFFmpegArgs(src.URL, pushURL)
	cmd := exec.CommandContext(m.ctx, "ffmpeg", args...)

	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 FFmpeg 失败: %w", err)
	}

	ctx, cancel := context.WithCancel(m.ctx)
	controller := &StreamController{
		sourceID:   sourceID,
		cmd:        cmd,
		cancel:     cancel,
		status:     "pushing",
		hlsURL:     hlsURL,
		pushURL:    pushURL,
		lastAccess: time.Now(),
	}
	m.streams[sourceID] = controller

	var streamID int64
	err = m.db.QueryRow(`INSERT INTO rtmp_streams (source_id, stream_status, push_url, hls_url, last_push_time) 
		VALUES (?, 'pushing', ?, ?, ?) RETURNING id`,
		sourceID, pushURL, hlsURL, time.Now()).Scan(&streamID)
	if err != nil {
		m.log.Error("记录推流状态失败: %v", err)
	}

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		go func() {
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				m.log.Debug("[FFmpeg stdout] %s", scanner.Text())
			}
		}()
		go func() {
			scanner := bufio.NewScanner(stderr)
			for scanner.Scan() {
				m.log.Debug("[FFmpeg stderr] %s", scanner.Text())
			}
		}()

		err := cmd.Wait()
		m.streamsMu.Lock()
		delete(m.streams, sourceID)
		m.streamsMu.Unlock()

		status := "stopped"
		errMsg := ""
		if err != nil {
			status = "error"
			errMsg = err.Error()
		}
		m.db.Exec(`UPDATE rtmp_streams SET stream_status = ?, error_message = ? WHERE source_id = ?`, status, errMsg, sourceID)
		m.log.Info("推流结束 source_id=%d, status=%s", sourceID, status)
	}()

	m.log.Info("启动推流: source_id=%d, name=%s, hls=%s", sourceID, src.Name, hlsURL)
	return nil
}

// StopStream 停止指定源的推流
func (m *Manager) StopStream(sourceID int64) error {
	m.streamsMu.Lock()
	ctrl, exists := m.streams[sourceID]
	if exists {
		ctrl.cancel()
		delete(m.streams, sourceID)
	}
	m.streamsMu.Unlock()

	if !exists {
		m.db.Exec(`UPDATE rtmp_streams SET stream_status = 'stopped' WHERE source_id = ?`, sourceID)
		return nil
	}

	time.Sleep(100 * time.Millisecond)
	if ctrl.cmd.Process != nil {
		ctrl.cmd.Process.Kill()
	}
	m.db.Exec(`UPDATE rtmp_streams SET stream_status = 'stopped' WHERE source_id = ?`, sourceID)
	m.log.Info("停止推流: source_id=%d", sourceID)
	return nil
}

func (m *Manager) stopAllStreams() {
	m.streamsMu.Lock()
	defer m.streamsMu.Unlock()
	for id, ctrl := range m.streams {
		ctrl.cancel()
		if ctrl.cmd.Process != nil {
			ctrl.cmd.Process.Kill()
		}
		m.db.Exec(`UPDATE rtmp_streams SET stream_status = 'stopped' WHERE source_id = ?`, id)
	}
	m.streams = make(map[int64]*StreamController)
}

func (m *Manager) buildFFmpegArgs(inputURL, outputURL string) []string {
	return []string{
		"-re", "-i", inputURL, "-c", "copy", "-f", "flv",
		"-flvflags", "no_duration_filesize", outputURL,
	}
}

func (m *Manager) idleMonitor() {
	defer m.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	idleTimeout := time.Duration(m.cfg.RTMP.RTMPIdleTimeout) * time.Second

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.checkIdleStreams(idleTimeout)
		}
	}
}

func (m *Manager) checkIdleStreams(timeout time.Duration) {
	m.streamsMu.RLock()
	streams := make(map[int64]*StreamController)
	for id, ctrl := range m.streams {
		streams[id] = ctrl
	}
	m.streamsMu.RUnlock()

	for id, ctrl := range streams {
		hlsPath := filepath.Join("/var/www/hls", sanitizeStreamKey(fmt.Sprintf("%d", id))+".m3u8")
		info, err := os.Stat(hlsPath)
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) > timeout {
			m.log.Info("推流空闲超时，停止推流 source_id=%d", id)
			m.StopStream(id)
		}
	}
}

func (m *Manager) scheduler() {
	defer m.wg.Done()
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.scheduleStreams()
		}
	}
}

func (m *Manager) scheduleStreams() {
	m.streamsMu.RLock()
	currentCount := len(m.streams)
	m.streamsMu.RUnlock()

	if currentCount >= m.maxStreams {
		return
	}

	rows, err := m.db.Query(`SELECT id FROM url_sources_passed 
		WHERE status = 'active' 
		AND id NOT IN (SELECT source_id FROM rtmp_streams WHERE stream_status = 'pushing')
		LIMIT ?`, m.maxStreams-currentCount)
	if err != nil {
		m.log.Error("查询待推流源失败: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var sourceID int64
		if err := rows.Scan(&sourceID); err != nil {
			continue
		}
		if err := m.StartStream(sourceID); err != nil {
			m.log.Error("启动推流失败 source_id=%d: %v", sourceID, err)
		}
		time.Sleep(2 * time.Second)
	}
}

// GetHLSURL 获取源的 HLS 播放地址
func (m *Manager) GetHLSURL(sourceID int64) string {
	m.streamsMu.RLock()
	defer m.streamsMu.RUnlock()
	if ctrl, ok := m.streams[sourceID]; ok {
		ctrl.mu.RLock()
		defer ctrl.mu.RUnlock()
		return ctrl.hlsURL
	}
	var hlsURL sql.NullString
	m.db.QueryRow(`SELECT hls_url FROM rtmp_streams WHERE source_id = ? AND stream_status = 'pushing'`, sourceID).Scan(&hlsURL)
	return hlsURL.String
}

func sanitizeStreamKey(name string) string {
	key := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, name)
	if key == "" {
		key = "stream"
	}
	return key
}
