// internal/scheduler/scheduler.go
// 补充 LockFile 和 UnlockFile 的实现。
// 这两个函数在原始代码中被调用但未定义，导致编译失败。

package scheduler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/pkg/logger"

	"github.com/robfig/cron/v3"
)

// TaskFunc 定义调度任务的函数签名
type TaskFunc func(ctx context.Context) error

// Manager 管理定时任务的生命周期
type Manager struct {
	cfg      *config.Config
	cron     *cron.Cron
	lockPath string
	taskFn   TaskFunc
	mu       sync.Mutex
	entryID  cron.EntryID
	running  bool
}

// NewManager 创建调度器实例
func NewManager(cfg *config.Config, taskFn TaskFunc) *Manager {
	lockDir := cfg.Output.Directory
	if lockDir == "" {
		lockDir = os.TempDir()
	}
	return &Manager{
		cfg:      cfg,
		cron:     cron.New(cron.WithSeconds()),
		lockPath: filepath.Join(lockDir, ".scheduler.lock"),
		taskFn:   taskFn,
	}
}

// Start 启动调度器
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("调度器已在运行")
	}

	expr := m.cfg.Scheduler.Cron
	if expr == "" {
		expr = "0 0 */2 * * *" // 默认每 2 小时
		logger.Info("未配置 cron 表达式，使用默认值: %s", expr)
	}

	entryID, err := m.cron.AddFunc(expr, func() {
		m.executeTask()
	})
	if err != nil {
		return fmt.Errorf("添加定时任务失败: %w", err)
	}

	m.entryID = entryID
	m.cron.Start()
	m.running = true
	logger.Info("调度器已启动，cron 表达式: %s", expr)
	return nil
}

// Stop 停止调度器
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return
	}

	m.cron.Remove(m.entryID)
	ctx := m.cron.Stop()
	<-ctx.Done()
	m.running = false
	logger.Info("调度器已停止")
}

// executeTask 执行实际任务，带有文件锁保护以避免重复执行。
func (m *Manager) executeTask() {
	// 确保锁文件所在目录存在
	lockDir := filepath.Dir(m.lockPath)
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		logger.Error("无法创建锁文件目录 %s: %v", lockDir, err)
		return
	}

	lockFile, err := os.OpenFile(m.lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		logger.Error("无法打开锁文件 %s: %v", m.lockPath, err)
		return
	}
	defer lockFile.Close()

	if err := LockFile(lockFile); err != nil {
		logger.Warn("未获取到分布式锁，任务可能正在由其他实例执行")
		return
	}
	defer UnlockFile(lockFile)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	logger.Info("开始执行调度任务...")
	if err := m.taskFn(ctx); err != nil {
		logger.Error("调度任务执行失败: %v", err)
	} else {
		logger.Info("调度任务执行成功")
	}
}
