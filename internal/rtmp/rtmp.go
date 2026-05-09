// internal/rtmp/manager.go
package rtmp

import (
    "context"
    "fmt"
    "os/exec"
    "sync"
    "syscall"

    "live-source-manager-go/internal/config"
    "live-source-manager-go/internal/models"
    "live-source-manager-go/pkg/logger"
)

// Manager 管理 RTMP 推流任务
type Manager struct {
    ctx     context.Context
    cfg     *config.Config
    mu      sync.Mutex
    streams map[string]*streamTask // key: 推流名称
}

type streamTask struct {
    cmd    *exec.Cmd
    cancel context.CancelFunc
}

// NewManager 创建 RTMP 管理器
func NewManager(ctx context.Context, cfg *config.Config) *Manager {
    return &Manager{
        ctx:     ctx,
        cfg:     cfg,
        streams: make(map[string]*streamTask),
    }
}

// Reload 根据新的源列表重新加载推流任务
func (m *Manager) Reload(sources []models.PassedSource) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    // 停止所有旧流
    m.stopAllLocked()

    if !m.cfg.RTMP.Enable {
        return nil
    }

    // 启动新流
    for _, src := range sources {
        if src.URL == "" {
            continue
        }
        key := fmt.Sprintf("%s-%s", src.Name, src.URL)
        if err := m.startStreamLocked(src.Name, src.URL); err != nil {
            logger.Error("启动 RTMP 推流失败 [%s]: %v", key, err)
        }
    }

    logger.Info("RTMP 推流已更新，当前推流数: %d", len(m.streams))
    return nil
}

func (m *Manager) stopAllLocked() {
    for name, t := range m.streams {
        t.cancel()
        if t.cmd != nil && t.cmd.Process != nil {
            t.cmd.Process.Signal(syscall.SIGTERM)
        }
        delete(m.streams, name)
    }
}

// startStreamLocked 启动一个推流任务，调用 ffmpeg
func (m *Manager) startStreamLocked(name, sourceURL string) error {
    streamCtx, cancel := context.WithCancel(m.ctx)

    args := []string{
        "-re",
        "-i", sourceURL,
        "-c", "copy",
        "-f", "flv",
        fmt.Sprintf("%s/%s", m.cfg.RTMP.ServerURL, name),
    }
    cmd := exec.CommandContext(streamCtx, "ffmpeg", args...)
    cmd.Stdout = nil
    cmd.Stderr = nil

    if err := cmd.Start(); err != nil {
        cancel()
        return fmt.Errorf("执行 ffmpeg 失败: %w", err)
    }

    task := &streamTask{
        cmd:    cmd,
        cancel: cancel,
    }
    m.streams[name] = task

    go func() {
        err := cmd.Wait()
        if err != nil && err.Error() != "signal: killed" {
            logger.Error("RTMP 推流 [%s] 异常退出: %v", name, err)
        }
        // 清理
        m.mu.Lock()
        if t, ok := m.streams[name]; ok && t == task {
            delete(m.streams, name)
        }
        m.mu.Unlock()
    }()

    logger.Info("已启动 RTMP 推流: %s -> %s/%s", sourceURL, m.cfg.RTMP.ServerURL, name)
    return nil
}

// Stop 停止所有推流并释放资源
func (m *Manager) Stop() {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.stopAllLocked()
    logger.Info("所有 RTMP 推流已停止")
}
