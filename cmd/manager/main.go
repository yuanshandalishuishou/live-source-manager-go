// cmd/manager/main.go
// 主入口，负责初始化所有模块并启动 Web 服务与调度器。
// 修复：先前版本内容被错误地替换为 admin/handler.go，现已恢复核心启动逻辑。
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/scheduler"
	"live-source-manager-go/internal/web"
)

func main() {
	// 1. 加载配置
	cfg, err := config.Load("config.ini")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("配置校验失败: %v", err)
	}

	// 2. 初始化数据库
	database, err := db.NewDB(cfg.Database.Path)
	if err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}
	defer database.Close()

	// 3. 初始化 Web 应用（不启用 JWT 和 WebSocket）
	webApp := web.NewApp(cfg, database)

	// 4. 启动 Web 服务
	go func() {
		addr := fmt.Sprintf(":%d", cfg.Server.Port)
		log.Printf("Web 管理后台启动于 http://localhost%s", addr)
		if err := webApp.Start(addr); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Web 服务启动失败: %v", err)
		}
	}()

	// 5. 定时任务函数（核心业务逻辑待集成）
	taskFunc := func(ctx context.Context) error {
		log.Println("计划任务执行中...")
		// TODO: 集成 collector、tester、filter、generator 等模块
		return nil
	}

	// 6. 启动调度器
	sched := scheduler.NewManager(cfg, taskFunc)
	if err := sched.Start(); err != nil {
		log.Fatalf("启动调度器失败: %v", err)
	}
	defer sched.Stop()

	// 7. 首次执行任务（异步，带 panic 恢复）
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("首次任务发生 panic 并已恢复: %v\n堆栈: %s", r, string(debug.Stack()))
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := taskFunc(ctx); err != nil {
			log.Printf("首次任务执行失败: %v", err)
		}
	}()

	// 8. 等待退出信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("收到退出信号，正在关闭服务...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := webApp.Shutdown(shutdownCtx); err != nil {
		log.Printf("Web 服务关闭出错: %v", err)
	}
}
