// cmd/manager/main.go

package main

import (
	"context"
	"log"
	"net/http"
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
	// 1. 加载配置
	configPath := "/config/config.ini"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("配置加载失败: %v", err)
	}
	logger.Init(cfg)

	// 2. 初始化数据库
	database, err := db.Init()
	if err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}
	defer database.Close()

	// 3. 创建公共 HTTP 客户端池（统一配置，长期复用）
	sharedHTTPClient := &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        50,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  false,
		},
	}

	// 4. 初始化核心组件
	geoResolver, err := geo.NewResolver()
	if err != nil {
		logger.Warn("归属地解析器初始化失败，将跳过归属地识别", "error", err)
	}

	progMgr := progress.NewManager()
	t := tester.NewTester(cfg, database, geoResolver, progMgr, sharedHTTPClient)

	filterInstance, err := filter.NewFilter(database)
	if err != nil {
		logger.Fatal("过滤器初始化失败", err)
	}

	gen := generator.NewGenerator(cfg, database, filterInstance)
	parser := source.NewParser()
	sourceMgr := source.NewManager(cfg, database, parser, sharedHTTPClient)

	aliasMatcher, err := rules.NewAliasMatcher(database)
	if err != nil {
		logger.Fatal("别名匹配器初始化失败", err)
	}

	epgMgr := epg.NewManager(cfg, database, sharedHTTPClient)

	// 5. 启动初始测试任务
	// 定时调度器虽已注册，但程序首次启动时应立即运行一次测试
	ctx := context.Background()
	logger.Info("程序启动，开始执行初始测试任务")
	_, err = sourceMgr.DownloadAll(ctx)
	if err != nil {
		logger.Error("初始下载失败", "error", err)
	}
	// 应用别名替换
	unprocessed, _ := database.GetUnprocessedSources()
	for i, src := range unprocessed {
		unprocessed[i].Name = aliasMatcher.Apply(src.Name)
	}
	database.BatchUpdateNames(unprocessed)
	// 执行测试
	err = t.Start(ctx)
	if err != nil {
		logger.Error("初始测试任务失败", "error", err)
	}
	// 生成播放列表
	err = gen.Generate()
	if err != nil {
		logger.Error("初始生成播放列表失败", "error", err)
	}

	// 6. 启动后台服务
	sched := scheduler.NewScheduler(cfg, database)
	sched.AddTask("0 2 * * *", func(ctx context.Context) error {
		// 定时更新逻辑
		_, err := sourceMgr.DownloadAll(ctx)
		if err != nil {
			return err
		}
		unprocessed, _ := database.GetUnprocessedSources()
		for i, src := range unprocessed {
			unprocessed[i].Name = aliasMatcher.Apply(src.Name)
		}
		database.BatchUpdateNames(unprocessed)
		if err := t.Start(ctx); err != nil {
			logger.Error("定时测试任务失败", "error", err)
		}
		return gen.Generate()
	})
	sched.Start()
	epgMgr.Start()

	rtmpCfg := rtmp.RTMPConfig{
		MaxStreams:     cfg.RTMP.MaxStreams,
		IdleTimeout:    time.Duration(cfg.RTMP.IdleTimeout) * time.Second,
		RetryMax:       cfg.RTMP.RetryMax,
		RetryBaseDelay: time.Duration(cfg.RTMP.RetryBaseDelay) * time.Second,
		FfmpegPath:     cfg.RTMP.FfmpegPath,
		TranscodeMode:  cfg.RTMP.TranscodeMode,
	}
	if cfg.RTMP.OpenRTMP {
		rtmpMgr := rtmp.NewManager(database, rtmpCfg)
		rtmpMgr.Start()
	}

	// 7. 启动 Web 服务
	jwtMgr := web.NewJWTManager(cfg)
	handler := web.NewHandler(cfg, database, t, filterInstance, gen, sourceMgr, epgMgr, nil)
	wsHandler := web.NewWSHandler(progMgr)

	router := mux.NewRouter()
	router.HandleFunc("/ws/progress", wsHandler.ServeWS)
	handler.RegisterRoutes(router)

	server := web.NewServer(cfg, router)

	// 8. 优雅退出
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("Web 服务已启动")
		if err := server.Serve(); err != nil {
			logger.Fatal("Web 服务异常退出", "error", err)
		}
	}()

	<-quit
	logger.Info("收到退出信号，开始关闭服务...")
	sched.Stop()
	epgMgr.Stop()
	logger.Info("服务已安全退出")
}
