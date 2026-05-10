// internal/scheduler/scheduler.go
// 定时任务调度，负责周期性拉取、测试、过滤、生成、归类、RTMP 推流
package scheduler

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/collector"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/filter"
	"live-source-manager-go/internal/generator"
	"live-source-manager-go/internal/rtmp"
	"live-source-manager-go/internal/tester"
	"live-source-manager-go/pkg/logger"
)

// Scheduler 负责周期性执行更新流水线
type Scheduler struct {
	cron     *cron.Cron
	entryID  cron.EntryID
	db       *db.DB
	cfg      *config.Config
	mu       sync.Mutex
	running  bool
	generator *generator.Generator // 生成器依赖
	filter    *filter.Filter       // 过滤器依赖
	rtmpMgr   *rtmp.Manager        // RTMP 管理器
}

// New 创建调度器实例，注入必要的依赖
func New(database *db.DB, cfg *config.Config, gen *generator.Generator, f *filter.Filter) *Scheduler {
	return &Scheduler{
		cron:     cron.New(cron.WithSeconds()),
		db:       database,
		cfg:      cfg,
		generator: gen,
		filter:   f,
	}
}

// SetRTMPManager 注入 RTMP 管理器（可选）
func (s *Scheduler) SetRTMPManager(mgr *rtmp.Manager) {
	s.rtmpMgr = mgr
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

// runUpdatePipeline 执行完整的更新流水线：拉取 → 测试 → 过滤 → 生成 → RTMP 更新
func (s *Scheduler) runUpdatePipeline() {
	logger.Info("========== 定时更新流程开始 ==========")
	ctx := context.Background()

	// 1. 拉取和解析直播源
	logger.Info("步骤 1/5: 拉取直播源...")
	collect := collector.NewCollector(s.cfg, s.db, &http.Client{Timeout: 30 * time.Second})
	if err := collect.FetchAll(ctx); err != nil {
		logger.Error("源拉取失败: %v", err)
	}
	logger.Info("源拉取完成")

	// 2. 测试所有待测源
	logger.Info("步骤 2/5: 开始流测试...")
	pm := tester.NewProgressManager()
	t := tester.NewTester(s.cfg, s.db, pm,
		&http.Client{Timeout: time.Duration(s.cfg.Tester.Timeout) * time.Second})
	t.TestAll(ctx)

	// 3. 应用过滤规则
	logger.Info("步骤 3/5: 应用过滤规则...")
	if err := s.filter.ReloadIfNeed(); err != nil {
		logger.Warn("重新加载过滤规则失败", "error", err)
	}
	// 注意：过滤器的 Apply 方法已在生成器 Generate 内部调用

	// 4. 生成 M3U/TXT 播放列表
	logger.Info("步骤 4/5: 生成播放列表...")
	if err := s.generator.Generate(); err != nil {
		logger.Error("生成播放列表失败: %v", err)
	} else {
		logger.Info("播放列表生成成功")
	}

	// 5. 更新 RTMP 推流（如果启用）
	if s.rtmpMgr != nil && s.cfg.RTMP.Enable {
		logger.Info("步骤 5/5: 更新 RTMP 推流...")
		activeSources, err := s.db.GetActiveSources()
		if err != nil {
			logger.Error("获取活跃源失败: %v", err)
		} else {
			if err := s.rtmpMgr.Reload(activeSources); err != nil {
				logger.Error("RTMP 推流更新失败: %v", err)
			}
		}
	}

	logger.Info("========== 定时更新流程结束 ==========")
}

// convertToCronExpr 将标准 cron 表达式转换为 cron 库支持的格式（6位，包含秒）
func convertToCronExpr(expr string) string {
	// 如果已经是 6 位，直接返回
	parts := len(strings.Fields(expr))
	if parts == 6 {
		return expr
	}
	// 如果是 5 位，添加秒字段
	if parts == 5 {
		return "0 " + expr
	}
	// 默认每天凌晨 2 点
	return "0 0 2 * * *"
}
