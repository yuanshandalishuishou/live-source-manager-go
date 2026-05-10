// internal/scheduler/scheduler.go
// 定时任务调度，负责周期性拉取、测试、过滤、生成。
package scheduler

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/collector" // 新增引用
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/filter"
	"live-source-manager-go/internal/generator"
	"live-source-manager-go/internal/tester"
	"live-source-manager-go/pkg/logger"
)

// Scheduler 负责周期性执行更新流水线
type Scheduler struct {
	cron      *cron.Cron
	entryID   cron.EntryID
	db        *db.DB
	cfg       *config.Config
	mu        sync.Mutex
	running   bool
	generator *generator.Generator // 生成器依赖
	filter    *filter.Filter       // 过滤器依赖
}

// New 创建调度器实例，注入必要的依赖
func New(database *db.DB, cfg *config.Config, gen *generator.Generator, f *filter.Filter) *Scheduler {
	return &Scheduler{
		cron:      cron.New(cron.WithSeconds()),
		db:        database,
		cfg:       cfg,
		generator: gen,
		filter:    f,
	}
}

// Start 启动调度器，开始执行定时更新任务
func (s *Scheduler) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("调度器已在运行")
	}

	// 将配置中的 cron 表达式适配为 cron 库支持的格式
	expr := convertToCronExpr(s.cfg.Scheduler.CronExpr)
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

// Stop 停止调度器，等待当前任务执行完成
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

// runUpdatePipeline 执行完整的更新流水线：拉取 → 测试 → 过滤 → 生成
func (s *Scheduler) runUpdatePipeline() {
	logger.Info("========== 定时更新流程开始 ==========")
	ctx := context.Background()

	// 1. 拉取和解析直播源
	logger.Info("步骤 1/4: 拉取直播源...")
	collect := collector.NewCollector(s.cfg, s.db, &http.Client{Timeout: 30 * time.Second})
	if err := collect.FetchAll(ctx); err != nil {
		logger.Error("源拉取失败: %v", err)
	}
	logger.Info("源拉取完成")

	// 2. 测试所有待测源
	logger.Info("步骤 2/4: 开始流测试...")
	pm := tester.NewProgressManager()
	t := tester.NewTester(s.cfg, s.db, pm, &http.Client{Timeout: time.Duration(s.cfg.Tester.Timeout) * time.Second})
	t.TestAll(ctx)

	// 3. 应用过滤规则
	logger.Info("步骤 3/4: 应用过滤规则...")
	if err := s.filter.ReloadIfNeed(); err != nil {
		logger.Warn("重新加载过滤规则失败", "error", err)
	}
	// 注意：过滤器的 Apply 方法应在生成器 Generate 内部调用，此处仅确保规则最新

	// 4. 生成 M3U/TXT 播放列表
	logger.Info("步骤 4/4: 生成播放列表...")
	if err := s.generator.Generate(); err != nil {
		logger.Error("生成播放列表失败: %v", err)
	} else {
		logger.Info("播放列表生成成功")
	}

	logger.Info("========== 定时更新流程结束 ==========")
}

// convertToCronExpr 将用户友好的 cron 表达式或描述转为标准 6 段式 cron 表达式
func convertToCronExpr(expr string) string {
	if expr == "" {
		return "0 0 2 * * *" // 默认每天凌晨2点
	}
	// 如果只有 5 段，补齐秒段
	if countFields(expr) == 5 {
		return "0 " + expr
	}
	return expr
}

// countFields 计算字符串中的字段数（以空格分隔）
func countFields(s string) int {
	n := 0
	for _, ch := range s {
		if ch == ' ' {
			n++
		}
	}
	return n + 1
}
