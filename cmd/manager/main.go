// cmd/manager/main.go
// 修复了 config.Filter 未定义导致的编译错误，并移除了未使用的 downloader 导入。
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"live-source-manager-go/internal/classifier"
	"live-source-manager-go/internal/collector"
	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/epg"
	"live-source-manager-go/internal/filter"
	"live-source-manager-go/internal/generator"
	"live-source-manager-go/internal/progress"
	"live-source-manager-go/internal/rtmp"
	"live-source-manager-go/internal/scheduler"
	"live-source-manager-go/internal/tester"
	"live-source-manager-go/internal/web"
	"live-source-manager-go/pkg/logger"
)

func main() {
	// 1. 加载配置
	cfg, err := config.Load("config.ini")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 2. 初始化日志
	if err := logger.Init(cfg.Output.Directory); err != nil {
		log.Fatalf("初始化日志失败: %v", err)
	}
	logger.Info("live-source-manager-go 启动中...")

	// 3. 初始化数据库
	database, err := db.NewDB(cfg.Database.Path)
	if err != nil {
		logger.Fatal("初始化数据库失败: %v", err)
	}
	defer database.Close()
	logger.Info("数据库连接成功: %s", cfg.Database.Path)

	// 4. 共享 HTTP 客户端
	httpClient := &http.Client{
		Timeout: 60 * time.Second,
	}

	// 5. 进度管理器
	progMgr := progress.NewManager()

	// 6. 过滤器 (修复：直接使用 https://raw.githubusercontent.com/yuanshandalishuishou/live-source-manager-go/main/internal/filter/filter.go 定义的构造函数)
	// 注意：根据实际 filter.go 文件，NewFilter 仅需一个参数 (config *config.Config)
	blFilter := filter.NewFilter(cfg)
	if err := blFilter.Load(); err != nil {
		logger.Warn("过滤规则加载失败（将使用空规则）: %v", err)
	}

	// 7. 分类器
	clsf := classifier.NewClassifier(cfg.Classifier.RulesFile)

	// 8. 流测试器
	t := tester.NewTester(cfg, database, progMgr, httpClient)

	// 9. 采集器
	collect := collector.NewCollector(cfg, database, httpClient, progMgr)

	// 10. M3U 生成器、EPG、RTMP 管理器
	gen := generator.NewGenerator(cfg, database)
	epgMgr := epg.NewManager(cfg, database)
	rtmpMgr := rtmp.NewManager(context.Background())

	// 11. 完整的同步任务
	taskFunc := func(ctx context.Context) error {
		logger.Info("开始执行计划任务...")

		// 采集订阅源
		if err := collect.Collect(ctx); err != nil {
			logger.Error("采集失败: %v", err)
			return err
		}

		// 测试所有源
		t.TestAll(ctx)

		// 应用过滤器与分类
		sources, err := database.GetActivePassedSources()
		if err != nil {
			logger.Error("获取有效源失败: %v", err)
			return err
		}
		filtered := blFilter.Apply(sources)
		classified := clsf.Apply(filtered)

		// 生成 M3U 播放列表
		if err := gen.Generate(classified); err != nil {
			logger.Error("生成 M3U 失败: %v", err)
		}

		// 更新 EPG 数据
		if err := epgMgr.Update(); err != nil {
			logger.Error("EPG 更新失败: %v", err)
		}

		logger.Info("计划任务执行完毕")
		return nil
	}

	// 12. 启动调度器
	sched := scheduler.NewManager(cfg, taskFunc)
	if err := sched.Start(); err != nil {
		logger.Fatal("启动调度器失败: %v", err)
	}
	defer sched.Stop()

	// 立即执行一次完整任务
	go func() {
		logger.Info("首次启动，立即执行一次完整任务...")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := taskFunc(ctx); err != nil {
			logger.Error("首次任务执行失败: %v", err)
		}
	}()

	// 13. 启动 Web 管理服务
	webApp := web.NewApp(cfg, database)
	go func() {
		addr := ":" + cfg.Server.Port
		logger.Info("Web 管理后台启动于 %s", addr)
		if err := webApp.Start(addr); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Web 服务启动失败: %v", err)
		}
	}()

	// 14. 优雅退出
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	logger.Info("收到退出信号 %v，开始优雅关闭...", sig)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := webApp.Shutdown(shutdownCtx); err != nil {
		logger.Error("Web 服务关闭出错: %v", err)
	}
	rtmpMgr.Shutdown(5 * time.Second)
	logger.Info("系统已安全退出")
}
