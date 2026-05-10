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
