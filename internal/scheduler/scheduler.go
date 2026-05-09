// internal/scheduler/scheduler.go
// 调度器：基于 cron 的定时任务管理器，通过文件锁保证跨实例单次执行。
// 任务执行失败时会记录日志，不会影响下一次调度。
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

// TaskFunc 定义调度任务的函数类型，返回可能的错误。
type TaskFunc func(ctx context.Context) error

// Manager 管理定时任务的启动、停止和状态。
type Manager struct {
	cfg      *config.Config
	cron     *cron.Cron
	lockPath string // 分布式锁文件路径
	taskFn   TaskFunc
	mu       sync.Mutex
	entryID  cron.EntryID
	running  bool
}

// NewManager 创建一个新的调度器实例。
// 参数 cfg 提供配置（如 cron 表达式），taskFn 为需要定时执行的任务。
func NewManager(cfg *config.Config, taskFn TaskFunc) *Manager {
	// 锁文件放在输出目录（通常是持久化目录），文件名为 .scheduler.lock
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

// Start 启动调度器。根据配置中的 cron 表达式添加定时任务，并启动 cron 引擎。
// 如果配置中没有 cron 表达式，则每小时执行一次。
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("调度器已在运行")
	}

	// 确定 cron 表达式：优先使用配置文件中的值，否则每两小时执行一次
	expr := m.cfg.Scheduler.Cron
	if expr == "" {
		expr = "0 0 */2 * * *" // 每小时的第0分钟执行
		logger.Info("未配置 cron 表达式，使用默认值: %s", expr)
	}

	// 添加定时任务
	entryID, err := m.cron.AddFunc(expr, func() {
		m.executeTask()
	})
	if err != nil {
		return fmt.Errorf("添加定时任务失败: %w", err)
	}
	m.entryID = entryID

	// 启动 cron
	m.cron.Start()
	m.running = true
	logger.Info("调度器已启动，cron 表达式: %s", expr)
	return nil
}

// Stop 停止调度器，移除定时任务并关闭 cron 引擎。
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return
	}

	// 移除注册的任务
	m.cron.Remove(m.entryID)
	// 停止 cron（会等待正在执行的任务完成）
	ctx := m.cron.Stop()
	<-ctx.Done()
	m.running = false
	logger.Info("调度器已停止")
}

// executeTask 是真正执行任务的方法，包含文件锁的获取与释放。
// 只有获取到文件锁的实例才会执行任务，从而避免多实例重复执行。
func (m *Manager) executeTask() {
	lockFile, err := os.OpenFile(m.lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		logger.Error("无法打开锁文件: %v", err)
		return
	}
	defer lockFile.Close()

	if err := LockFile(lockFile); err != nil {
		logger.Warn("未获取到分布式锁，跳过本次调度")
		return
	}
	// 确保无论何种退出路径都会解锁
	defer UnlockFile(lockFile)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if err := m.taskFn(ctx); err != nil {
		logger.Error("调度任务执行失败: %v", err)
	} else {
		logger.Info("调度任务执行成功")
	}
}
