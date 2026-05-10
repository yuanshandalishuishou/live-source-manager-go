// cmd/manager/main.go
// 应用主入口，初始化所有模块并启动 HTTP 服务。

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
	"live-source-manager-go/internal/scheduler"
	"live-source-manager-go/internal/tester"
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

	// 4. 初始化调度器
	sched := scheduler.New(database, cfg)
	if cfg.Scheduler.Enabled {
		if err := sched.Start(); err != nil {
			logger.Error("调度器启动失败: %v", err)
		}
	}
	defer sched.Stop()

	// 5. 设置 HTTP 路由
	router := mux.NewRouter()

	// 静态文件
	router.PathPrefix("/static/").Handler(
		http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))),
	)

	// API
	api := router.PathPrefix("/api/v1").Subrouter()
	api.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	}).Methods("GET")

	api.HandleFunc("/update", func(w http.ResponseWriter, r *http.Request) {
		go func() {
			// 实例化进度管理器（可选，若要 WebSocket 推送请注入）
			pm := tester.NewProgressManager(database.SQLDB())
			t := tester.NewTester(cfg, database, pm, nil)
			t.TestAll(context.Background())
		}()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":200,"message":"update started"}`))
	}).Methods("POST")

	// 前端入口
	router.PathPrefix("/").Handler(http.FileServer(http.Dir("web/static")))

	// 6. 启动服务器
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
