package scheduler

import (
	"context"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/config"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/db"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/logger"
)

// TaskFunc 定义调度任务
type TaskFunc func(context.Context) error

// Scheduler 基于 cron 的调度器，支持分布式锁
type Scheduler struct {
	cfg         *config.Config
	db          *db.DB
	cron        *cron.Cron
	lockFile    string // 文件锁路径，默认为 /tmp/live-source-manager.lock
	mu          sync.Mutex
	taskRunning bool
}

// NewScheduler 创建调度器
func NewScheduler(cfg *config.Config, database *db.DB) *Scheduler {
	lockPath := cfg.System.LockFile
	if lockPath == "" {
		lockPath = "/tmp/live-source-manager.lock"
	}
	return &Scheduler{
		cfg:      cfg,
		db:       database,
		lockFile: lockPath,
	}
}

// AddTask 注册定时任务
func (s *Scheduler) AddTask(spec string, fn TaskFunc) (cron.EntryID, error) {
	if s.cron == nil {
		s.cron = cron.New(cron.WithSeconds())
	}
	return s.cron.AddFunc(spec, func() {
		s.runWithLock(fn)
	})
}

// Start 启动调度器
func (s *Scheduler) Start() {
	if s.cron == nil {
		logger.Warn("没有注册任何定时任务，调度器未启动")
		return
	}
	logger.Info("调度器启动")
	s.cron.Start()
}

// Stop 优雅停止（等待当前任务完成）
func (s *Scheduler) Stop() context.Context {
	ctx := s.cron.Stop()
	logger.Info("调度器已停止")
	return ctx
}

// runWithLock 获取分布式文件锁后执行任务，避免多实例重复运行
func (s *Scheduler) runWithLock(fn TaskFunc) {
	s.mu.Lock()
	// 防止同一进程内并发执行
	if s.taskRunning {
		s.mu.Unlock()
		logger.Warn("上一任务尚未完成，跳过本次执行")
		return
	}
	s.taskRunning = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.taskRunning = false
		s.mu.Unlock()
	}()

	// 获取跨进程的文件锁
	lockFile, err := os.OpenFile(s.lockFile, os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		logger.Error("无法打开锁文件", "error", err)
		return
	}
	defer lockFile.Close()

	// 尝试获取排他锁（非阻塞，不能获取则说明其他实例正在执行）
	if err := flock(lockFile, false); err != nil {
		logger.Warn("未获取到分布式锁，跳过本次调度", "error", err)
		return
	}
	defer funlock(lockFile)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute) // 全局超时
	defer cancel()
	logger.Info("开始执行调度任务")
	if err := fn(ctx); err != nil {
		logger.Error("调度任务执行失败", "error", err)
	} else {
		logger.Info("调度任务执行成功")
	}
}

// 以下为简化的文件锁实现（Linux/Unix 环境），使用 syscall
// 在 windows 下可用 LockFileEx 等替代

func flock(f *os.File, nonBlocking bool) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|int(syscall.LOCK_NB))
}

func funlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
