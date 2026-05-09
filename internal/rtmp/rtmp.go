// internal/rtmp/rtmp.go
// 优化后的 RTMP 推流管理，通过 context 实现优雅关闭和资源清理。
package rtmp

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"live-source-manager-go/pkg/logger"
)

// Stream 表示一个推流实例
type Stream struct {
	ID       string
	InputURL string
	OutputID string
}

// Manager 管理所有推流进程
type Manager struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	streams map[string]*exec.Cmd
	mu     sync.RWMutex
}

// NewManager 创建 RTMP 管理器
func NewManager(parent context.Context) *Manager {
	ctx, cancel := context.WithCancel(parent)
	return &Manager{
		ctx:    ctx,
		cancel: cancel,
		streams: make(map[string]*exec.Cmd),
	}
}

// StartStream 启动 ffmpeg 推流
func (m *Manager) StartStream(streamID, inputURL, outputURL string) error {
	select {
	case <-m.ctx.Done():
		return fmt.Errorf("rtmp manager is shutting down")
	default:
	}

	args := []string{
		"-i", inputURL,
		"-c", "copy",
		"-f", "flv",
		"-flvflags", "no_duration_filesize",
		outputURL,
	}
	cmd := exec.CommandContext(m.ctx, "ffmpeg", args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	m.mu.Lock()
	m.streams[streamID] = cmd
	m.mu.Unlock()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		err := cmd.Wait()
		// 进程退出时通知 manager 清理
		m.mu.Lock()
		delete(m.streams, streamID)
		m.mu.Unlock()
		if err != nil {
			logger.Warn("rtmp stream %s exited with error: %v", streamID, err)
		} else {
			logger.Info("rtmp stream %s finished", streamID)
		}
	}()

	return nil
}

// StopStream 停止指定推流
func (m *Manager) StopStream(streamID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cmd, ok := m.streams[streamID]
	if !ok {
		return fmt.Errorf("stream %s not found", streamID)
	}
	if err := cmd.Process.Kill(); err != nil {
		return fmt.Errorf("kill stream %s: %w", streamID, err)
	}
	// 清理将由 goroutine 中的 Wait 完成，此处仅负责发送信号
	return nil
}

// Shutdown 优雅关闭所有推流
func (m *Manager) Shutdown(timeout time.Duration) error {
	m.cancel()
	// 发送 SIGTERM 信号给所有子进程
	m.mu.RLock()
	for id, cmd := range m.streams {
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			logger.Warn("failed to interrupt stream %s: %v", id, err)
		}
	}
	m.mu.RUnlock()

	// 等待 goroutine 全部退出或超时
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("rtmp manager shut down gracefully")
	case <-time.After(timeout):
		logger.Warn("rtmp manager shutdown timed out, force killing")
		m.mu.RLock()
		for _, cmd := range m.streams {
			_ = cmd.Process.Kill()
		}
		m.mu.RUnlock()
	}

	return nil
}
