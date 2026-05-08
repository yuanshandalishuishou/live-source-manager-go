package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/config"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/db"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/epg"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/filter"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/generator"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/geo"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/logger"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/progress"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/rtmp"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/rules"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/scheduler"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/source"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/tester"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/web"
)

func main() {
	// 解析命令行参数
	configPath := "/config/config.ini"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	// 加载配置
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("配置加载失败: %v", err)
	}
	// 初始化日志
	logger.Init(cfg)

	// 数据库初始化
	database, err := db.Init()
	if err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}
	defer database.Close()

	// 初始化归属地解析器
	geoResolver, err := geo.NewResolver()
	if err != nil {
		logger.Warn("归属地解析器初始化失败，将跳过归属地识别", "error", err)
	}

	// 进度管理器
	progMgr := progress.NewManager()

	// 测试器
	t := tester.NewTester(cfg, database, geoResolver, progMgr)

	// 过滤器
	filterInstance, err := filter.NewFilter(database)
	if err != nil {
		logger.Fatal("过滤器初始化失败", err)
	}

	// 生成器
	gen := generator.NewGenerator(cfg, database, filterInstance)

	// 源解析器
	parser := source.NewParser()
	sourceMgr := source.NewManager(cfg, database, parser)

	// 别名匹配器
	aliasMatcher, err := rules.NewAliasMatcher(database)
	if err != nil {
		logger.Fatal("别名匹配器初始化失败", err)
	}

	// EPG 管理器
	epgMgr := epg.NewManager(cfg, database)

	// RTMP 管理器
	rtmpCfg := rtmp.RTMPConfig{
		MaxStreams:     cfg.RTMP.MaxStreams,
		IdleTimeout:    time.Duration(cfg.RTMP.IdleTimeout) * time.Second,
		RetryMax:       cfg.RTMP.RetryMax,
		RetryBaseDelay: time.Duration(cfg.RTMP.RetryBaseDelay) * time.Second,
		FfmpegPath:     cfg.RTMP.FfmpegPath,
		TranscodeMode:  cfg.RTMP.TranscodeMode,
	}
	rtmpMgr := rtmp.NewManager(database, rtmpCfg)

	// 调度器（用于定期下载、测试、生成）
	sched := scheduler.NewScheduler(cfg, database)

	// 注册定时任务
	sched.AddTask("0 2 * * *", func(ctx context.Context) error {
		logger.Info("开始定时更新流程")
		_, err := sourceMgr.DownloadAll(ctx)
		if err != nil {
			return err
		}
		// 应用别名
		unprocessed, _ := database.GetUnprocessedSources()
		for i, src := range unprocessed {
			unprocessed[i].Name = aliasMatcher.Apply(src.Name)
		}
		database.BatchUpdateNames(unprocessed) // 更新名称
		// 测试
		if err := t.Start(ctx); err != nil {
			logger.Error("测试任务失败", err)
		}
		// 生成播放列表
		if err := gen.Generate(); err != nil {
			logger.Error("生成播放列表失败", err)
		}
		return nil
	})
	sched.Start()

	// 启动 EPG 自动更新
	epgMgr.Start()

	// 启动 RTMP 推流管理器
	if cfg.RTMP.OpenRTMP {
		rtmpMgr.Start()
	}

	// 启动 Web 服务
	jwtMgr := web.NewJWTManager(cfg)
	handler := web.NewHandler(cfg, database, t, filterInstance, gen, sourceMgr, epgMgr, rtmpMgr, jwtMgr)
	wsHandler := web.NewWSHandler(progMgr)

	router := mux.NewRouter()
	// WebSocket 路由
	router.HandleFunc("/ws/progress", wsHandler.ServeWS)
	// API 路由
	handler.RegisterRoutes(router)

	server := web.NewServer(cfg, router)

	// 优雅退出
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("服务已启动")
		if err := server.Serve(); err != nil {
			logger.Fatal("Web 服务异常退出", err)
		}
	}()

	<-quit
	logger.Info("收到退出信号，开始关闭服务...")
	sched.Stop()
	epgMgr.Stop()
	rtmpMgr.Shutdown()
	// 其他清理...
	logger.Info("服务已安全退出")
}
