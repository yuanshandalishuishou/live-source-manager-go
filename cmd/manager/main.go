// cmd/manager/main.go
//
// 系统主入口：负责加载配置、初始化各核心模块（数据库、采集器、测试器、调度器等），
// 最后启动 Web 管理服务并等待退出信号。
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
	"live-source-manager-go/internal/downloader"
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
	// 1. 加载配置文件
	cfg, err := config.Load("config.ini")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 2. 初始化日志系统
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

	// 4. 共享 HTTP 客户端（用于下载器、采集器等）
	httpClient := &http.Client{
		Timeout: 60 * time.Second,
	}

	// 5. 进度管理器（WebSocket 广播）
	progMgr := progress.NewManager()

	// 6. 黑白名单过滤器
	blFilter := filter.NewFilter(cfg.Filter.BlacklistFile, cfg.Filter.WhitelistFile)
	if err := blFilter.Load(); err != nil {
		logger.Warn("过滤规则加载失败（将使用空规则）: %v", err)
	}

	// 7. 分类器（基于名称规则匹配）
	clsf := classifier.NewClassifier(cfg.Classifier.RulesFile)

	// 8. 流测试器（ffprobe 探针）
	t := tester.NewTester(cfg, database, progMgr, httpClient)

	// 9. 采集器（下载订阅源）
	collect := collector.NewCollector(cfg, database, httpClient, progMgr)

	// 10. M3U 生成器、EPG、RTMP 管理器
	gen := generator.NewGenerator(cfg, database)
	epgMgr := epg.NewManager(cfg, database)
	rtmpMgr := rtmp.NewManager(context.Background())

	// 11. 数据库下载器（Web 端一键分发）
	dl := downloader.NewDownloader(cfg, database)

	// 12. 完整的一次性同步任务（采集 -> 测试 -> 分类 -> 生成 -> EPG）
	taskFunc := func(ctx context.Context) error {
		logger.Info("开始执行计划任务...")

		// 12.1 下载订阅源
		if err := collect.Collect(ctx); err != nil {
			logger.Error("采集失败: %v", err)
			return err
		}

		// 12.2 测试所有源
		t.TestAll(ctx)

		// 12.3 应用过滤器与分类
		sources, err := database.GetActivePassedSources()
		if err != nil {
			logger.Error("获取有效源失败: %v", err)
			return err
		}
		filtered := blFilter.Apply(sources)
		classified := clsf.Apply(filtered)

		// 12.4 生成 M3U 播放列表
		if err := gen.Generate(classified); err != nil {
			logger.Error("生成 M3U 失败: %v", err)
		}

		// 12.5 更新 EPG 数据
		if err := epgMgr.Update(); err != nil {
			logger.Error("EPG 更新失败: %v", err)
		}

		// 12.6 RTMP 推流（根据需要）
		if cfg.RTMP.Enable {
			rtmpMgr.PushStreams(classified)
		}

		logger.Info("计划任务执行完毕")
		return nil
	}

	// 13. 调度器
	sched := scheduler.NewManager(cfg, taskFunc)
	if err := sched.Start(); err != nil {
		logger.Fatal("启动调度器失败: %v", err)
	}
	defer sched.Stop()

	// 14. 启动 Web 管理服务
	webApp := web.NewApp(cfg, database)
	go func() {
		addr := ":" + cfg.Server.Port
		logger.Info("Web 管理后台启动于 %s", addr)
		if err := webApp.Start(addr); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Web 服务启动失败: %v", err)
		}
	}()

	// 15. 监听系统信号，优雅退出
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	logger.Info("收到退出信号 %v，开始优雅关闭...", sig)

	// 给 Web 服务一定时间完成现有请求
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := webApp.Shutdown(shutdownCtx); err != nil {
		logger.Error("Web 服务关闭出错: %v", err)
	}
	rtmpMgr.Shutdown(5 * time.Second)
	logger.Info("系统已安全退出")
}
