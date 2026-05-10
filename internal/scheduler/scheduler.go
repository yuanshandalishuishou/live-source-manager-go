// internal/scheduler/scheduler.go
// 定时任务调度，负责周期性拉取、测试、过滤、生成。

package scheduler

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/robfig/cron/v3"
	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/tester"
	"live-source-manager-go/pkg/logger"
)

type Scheduler struct {
	cron    *cron.Cron
	entryID cron.EntryID
	db      *db.DB
	cfg     *config.Config
	mu      sync.Mutex
	running bool
}

func New(database *db.DB, config *config.Config) *Scheduler {
	return &Scheduler{
		cron: cron.New(cron.WithSeconds()),
		db:   database,
		cfg:  config,
	}
}

func (s *Scheduler) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return fmt.Errorf("调度器已在运行")
	}

	expr := convertToCronExpr(s.cfg.Scheduler.Cron)
	id, err := s.cron.AddFunc(expr, s.runUpdatePipeline)
	if err != nil {
		return fmt.Errorf("注册cron任务失败: %w", err)
	}
	s.entryID = id
	s.cron.Start()
	s.running = true
	logger.Info("调度器已启动，cron=%s", expr)
	return nil
}

func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		ctx := s.cron.Stop()
		<-ctx.Done()
		s.running = false
		logger.Info("调度器已停止")
	}
}

func (s *Scheduler) runUpdatePipeline() {
	logger.Info("定时更新流程开始 =====")
	ctx := context.Background()

	// 1. 拉取和解析（省略，假设 collector 已填充 live_sources）
	// 2. 测试
	t := tester.NewTester(s.cfg, s.db, nil, &http.Client{})
	t.TestAll(ctx)

	// 3. 过滤和分类（略）
	// 4. 生成 M3U/TXT
	logger.Info("定时更新流程结束 =====")
}
// internal/scheduler/scheduler.go

// ... 导入部分保持不变

// Scheduler 结构体新增必要的依赖。
type Scheduler struct {
	cron      *cron.Cron
	entryID   cron.EntryID
	db        *db.DB
	cfg       *config.Config
	mu        sync.Mutex
	running   bool
	generator *generator.Generator  // 新增生成器依赖
	filter    *filter.Filter        // 新增过滤器依赖
}

// New 构造函数需要注入生成器和过滤器。
func New(database *db.DB, cfg *config.Config, gen *generator.Generator, f *filter.Filter) *Scheduler {
	return &Scheduler{
		cron:      cron.New(cron.WithSeconds()),
		db:        database,
		cfg:       cfg,
		generator: gen,
		filter:    f,
	}
}

// runUpdatePipeline 执行完整的更新流水线：测试 -> 过滤 -> 生成。
func (s *Scheduler) runUpdatePipeline() {
	logger.Info("========== 定时更新流程开始 ==========")
	ctx := context.Background()

	// 1. 拉取和解析直播源
	// TODO: 集成 Collector 模块进行源拉取
	logger.Info("步骤 1/4: 拉取直播源... (待实现 Collector)")

	// 2. 测试所有待测源
	logger.Info("步骤 2/4: 开始流测试...")
	pm := tester.NewProgressManager()
	t := tester.NewTester(s.cfg, s.db, pm, &http.Client{Timeout: 8 * time.Second})
	t.TestAll(ctx)

	// 3. 应用过滤规则
	logger.Info("步骤 3/4: 应用过滤规则...")
	if err := s.filter.ReloadIfNeed(); err != nil {
		logger.Warn("重新加载过滤规则失败", "error", err)
	}

	// 4. 生成 M3U/TXT 播放列表
	logger.Info("步骤 4/4: 生成播放列表...")
	if err := s.generator.Generate(); err != nil {
		logger.Error("生成播放列表失败: %v", err)
	} else {
		logger.Info("播放列表生成成功")
	}
	logger.Info("========== 定时更新流程结束 ==========")
}
func convertToCronExpr(expr string) string {
	if expr == "" {
		return "0 0 2 * * *" // 默认每天2点
	}
	if countFields(expr) == 5 {
		return "0 " + expr
	}
	return expr
}

func countFields(s string) int {
	n := 0
	for _, ch := range s {
		if ch == ' ' {
			n++
		}
	}
	return n + 1
}
