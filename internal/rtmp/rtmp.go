package rtmp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/yuanshandalishuishou/live-source-manager-go/internal/db"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/logger"
)

// Stream 代表一个推流任务
type Stream struct {
	ID        int
	SourceID  int
	InputURL  string
	PushURL   string
	HLSURL    string
	cmd       *exec.Cmd
	ctx       context.Context
	cancel    context.CancelFunc
	status    string // "running", "stopped", "error", "idle"
	mu        sync.Mutex
	startedAt time.Time
}

// Manager 推流总管
type Manager struct {
	mu      sync.Mutex
	streams map[int]*Stream // key: source_id
	db      *db.DB
	cfg     RTMPConfig
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// RTMPConfig 推流配置
type RTMPConfig struct {
	MaxStreams     int
	IdleTimeout    time.Duration
	RetryMax       int           // 最大重试次数
	RetryBaseDelay time.Duration // 基础退避延迟（秒）
	FfmpegPath     string
	TranscodeMode  string
}

// NewManager 创建推流管理器，并创建全局 context 用于退出
func NewManager(database *db.DB, cfg RTMPConfig) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		db:      database,
		cfg:     cfg,
		streams: make(map[int]*Stream),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Start 启动后台监控（空闲检测与故障恢复）
func (m *Manager) Start() {
	m.wg.Add(2)
	go m.idleMonitor()
	go m.recoverer()
	logger.Info("RTMP 推流管理器已启动")
}

// Shutdown 优雅关闭所有推流
func (m *Manager) Shutdown() {
	logger.Info("正在关闭 RTMP 推流管理器...")
	m.cancel()
	m.mu.Lock()
	for _, s := range m.streams {
		s.stop()
	}
	m.mu.Unlock()
	m.wg.Wait()
	logger.Info("RTMP 推流管理器已完全退出")
}

// AddStream 请求启动一个推流，返回是否成功以及错误
func (m *Manager) AddStream(sourceID int, inputURL string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查是否已存在或达到上限
	if _, exists := m.streams[sourceID]; exists {
		return nil // 已存在，忽略
	}
	if len(m.streams) >= m.cfg.MaxStreams {
		return fmt.Errorf("已达到最大推流数量 %d", m.cfg.MaxStreams)
	}

	// 构造推流与 HLS 地址
	pushDomain := "rtmp://127.0.0.1:1935/live"
	streamName := fmt.Sprintf("live_%d", sourceID)
	pushURL := fmt.Sprintf("%s/%s", pushDomain, streamName)
	hlsURL := fmt.Sprintf("http://127.0.0.1:8080/hls/%s.m3u8", streamName)

	s := &Stream{
		SourceID: sourceID,
		InputURL: inputURL,
		PushURL:  pushURL,
		HLSURL:   hlsURL,
		status:   "pending",
	}
	s.ctx, s.cancel = context.WithCancel(m.ctx)
	m.streams[sourceID] = s

	// 异步启动 ffmpeg
	go s.run(m.cfg)

	// 记录数据库
	m.db.UpsertRTMPStream(sourceID, pushURL, hlsURL)
	return nil
}

// idleMonitor 定期检查所有推流是否长时间无播放，若是则停止
func (m *Manager) idleMonitor() {
	defer m.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.mu.Lock()
			for id, s := range m.streams {
				if s.getStatus() != "running" {
					continue
				}
				// 检查是否超过空闲时间
				idle, err := m.db.GetStreamIdleSeconds(id) // 通过查询统计最近有播放的事件
				if err != nil {
					continue
				}
				if idle > int(m.cfg.IdleTimeout.Seconds()) {
					logger.Info("空闲超时，停止推流", "source_id", id, "idle_seconds", idle)
					s.stop()
					delete(m.streams, id)
					m.db.SetStreamStatus(id, "idle")
				}
			}
			m.mu.Unlock()
		case <-m.ctx.Done():
			return
		}
	}
}

// recoverer 协程：周期性检查崩溃/错误状态的流，按退避策略重启
func (m *Manager) recoverer() {
	defer m.wg.Done()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	// 记录每个流的重试次数和下次重试时间
	type retryState struct {
		retries  int
		nextTime time.Time
	}
	retryMap := make(map[int]*retryState)

	for {
		select {
		case <-ticker.C:
			m.mu.Lock()
			for _, s := range m.streams {
				if s.getStatus() != "error" && s.getStatus() != "stopped" {
					continue
				}
				st, exists := retryMap[s.SourceID]
				if !exists {
					st = &retryState{retries: 0, nextTime: time.Now()}
					retryMap[s.SourceID] = st
				}
				if st.retries >= m.cfg.RetryMax {
					continue // 超出最大重试次数
				}
				if time.Now().After(st.nextTime) {
					logger.Info("尝试重启推流", "source_id", s.SourceID, "retry", st.retries+1)
					s.ctx, s.cancel = context.WithCancel(m.ctx) // 生成新 ctx
					go s.run(m.cfg)
					st.retries++
					// 指数退避: 基础延迟 * 2^(retries-1)
					delay := m.cfg.RetryBaseDelay * time.Duration(1<<(st.retries-1))
					st.nextTime = time.Now().Add(delay)
				}
			}
			m.mu.Unlock()
			// 清理已不在 streaming 列表中的条目的重试记录
			m.mu.Lock()
			for id := range retryMap {
				if _, ok := m.streams[id]; !ok {
					delete(retryMap, id)
				}
			}
			m.mu.Unlock()
		case <-m.ctx.Done():
			return
		}
	}
}

// ---------- Stream 方法 ----------

// run 执行 ffmpeg 推流，参数数组防注入，使用绝对路径
func (s *Stream) run(cfg RTMPConfig) {
	ffmpegPath := cfg.FfmpegPath
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	args := []string{
		"-re",
		"-i", s.InputURL,
		"-c", "copy",
		"-f", "flv",
		s.PushURL,
	}
	if cfg.TranscodeMode != "copy" {
		// 示例：低码率转码
		args = []string{
			"-re",
			"-i", s.InputURL,
			"-c:v", "libx264", "-preset", "veryfast", "-b:v", "800k",
			"-c:a", "aac", "-b:a", "64k",
			"-f", "flv",
			s.PushURL,
		}
	}
	cmd := exec.CommandContext(s.ctx, ffmpegPath, args...)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	s.mu.Lock()
	s.cmd = cmd
	s.status = "running"
	s.startedAt = time.Now()
	s.mu.Unlock()

	go s.streamOutput(stdout, "stdout")
	go s.streamOutput(stderr, "stderr")

	err := cmd.Run()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		logger.Error("ffmpeg 推流异常退出", "source_id", s.SourceID, "error", err)
		s.status = "error"
	} else {
		s.status = "stopped"
	}
}

// stop 停止当前推流进程
func (s *Stream) stop() {
	if s.cancel != nil {
		s.cancel()
	}
	// 等待进程退出（可加超时）
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

// getStatus 线程安全获取状态
func (s *Stream) getStatus() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// streamOutput 简单记录 ffmpeg 的输出到日志（避免占用过多内存）
func (s *Stream) streamOutput(reader io.Reader, stream string) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		// 只记录错误和警告类，正常输出丢弃
		if strings.Contains(line, "error") || strings.Contains(line, "Warning") {
			logger.Debug("ffmpeg", "source", s.SourceID, "stream", stream, "msg", line)
		}
	}
}
