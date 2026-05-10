// cmd/manager/main.go
// 应用主入口，完整初始化所有模块并启动 HTTP 服务。

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/filter"
	"live-source-manager-go/internal/generator"
	"live-source-manager-go/internal/rtmp"
	"live-source-manager-go/internal/scheduler"
	"live-source-manager-go/internal/tester"
	"live-source-manager-go/internal/web"
	"live-source-manager-go/pkg/logger"
)

func main() {
	// 1. 加载配置
	cfgPath := "configs/config.ini"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	// 2. 初始化日志
	log, err := logger.New(cfg.Logging.File, logger.INFO)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化日志失败: %v\n", err)
		os.Exit(1)
	}
	logger.SetDefault(log)

	// 3. 初始化数据库
	database, err := db.New(cfg.Database.Path)
	if err != nil {
		logger.Fatal("数据库初始化失败: %v", err)
	}
	defer database.Close()

	// 4. 初始化核心模块
	// 过滤器
	f := filter.NewFilter(database)
	if err != nil {
		logger.Fatal("初始化过滤器失败: %v", err)
	}

	// 生成器
	gen := generator.NewGenerator(cfg, database, f)

	// RTMP 管理器
	rmtpMgr := rtmp.NewManager(context.Background(), cfg)

	// 5. 初始化并启动调度器
	sched := scheduler.New(database, cfg, gen, f) // 传入生成器和过滤器
	if cfg.Scheduler.Enabled {
		if err := sched.Start(); err != nil {
			logger.Error("调度器启动失败: %v", err)
		}
	}
	defer sched.Stop()

	// 6. 设置 HTTP 路由
	router := mux.NewRouter()

	// JWT 管理器
	jwtMgr := web.NewJWTManager(cfg)

	// WebSocket 进度管理器
	progressMgr := tester.NewProgressManager()
	wsHandler := web.NewWSHandler(progressMgr)

	// 注入所有依赖到 Web 应用
	app := web.NewApp(cfg, database, jwtMgr, wsHandler, gen, f, progressMgr)

	// 注册所有路由
	app.RegisterRoutes(router) // 改用显式的方法调用

	// 7. 启动服务器
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 优雅关闭
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		logger.Info("收到关闭信号，正在退出...")
		// 停止 RTMP 推流
		rmtpMgr.Stop()

		// 停止调度器
		sched.Stop()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	logger.Info("服务器启动在 %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatal("服务器启动失败: %v", err)
	}
}
