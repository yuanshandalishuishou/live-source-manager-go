// cmd/manager/main.go
package main

import (
    "context"
    "fmt"
    "net/http"
    "os"
    "os/signal"
    "runtime/debug"
    "syscall"
    "time"

    "live-source-manager-go/internal/classifier"
    "live-source-manager-go/internal/collector"
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
    cfg, err := config.Load("config.ini")
    if err != nil {
        fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
        os.Exit(1)
    }
    if err := cfg.Validate(); err != nil {
        fmt.Fprintf(os.Stderr, "Invalid config: %v\n", err)
        os.Exit(1)
    }

    // 2. 初始化日志系统（必须在其他组件前）
    if err := logger.Init(cfg.Output.Directory); err != nil {
        fmt.Fprintf(os.Stderr, "Failed to init logger: %v\n", err)
        os.Exit(1)
    }
    defer logger.Info("系统已安全退出")
    logger.Info("live-source-manager-go 正在启动...")

    // 3. 初始化数据库
    database, err := db.NewDB(cfg.Database.Path)
    if err != nil {
        logger.Fatal("初始化数据库失败: %v", err)
    }
    defer database.Close()
    logger.Info("数据库连接成功: %s", cfg.Database.Path)

    // 4. 共享 HTTP 客户端
    httpClient := &http.Client{Timeout: 60 * time.Second}

    // 5. 滤波器（黑白名单）
    blFilter := filter.NewFilter(cfg)
    if err := blFilter.Load(); err != nil {
        logger.Warn("过滤规则加载失败: %v", err)
    }

    // 6. 分类器
    clsf := classifier.NewClassifier(cfg.Classifier.RulesFile)

    // 7. 核心功能模块初始化
    t := tester.NewTester(cfg, database, httpClient)
    collect := collector.NewCollector(cfg, database, httpClient)
    gen := generator.NewGenerator(cfg, database)

    // 8. EPG 管理器（如有需要可在此初始化）
    // epgMgr := epg.NewManager(cfg, database)

    // 9. RTMP 管理器
    rtmpCtx, rtmpCancel := context.WithCancel(context.Background())
    defer rtmpCancel()
    rtmpMgr := rtmp.NewManager(rtmpCtx, cfg)

    // 10. 完整任务函数
    taskFunc := func(ctx context.Context) error {
        logger.Info("开始执行计划任务...")

        // 采集订阅源
        if err := collect.Collect(ctx); err != nil {
            logger.Error("采集失败: %v", err)
        }

        // 测试所有源
        t.TestAll(ctx)

        // 获取有效源并过滤
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

        // 启动/重启 RTMP 推流（如果启用）
        if cfg.RTMP.Enable {
            if err := rtmpMgr.Reload(classified); err != nil {
                logger.Error("RTMP 推流更新失败: %v", err)
            }
        }

        logger.Info("计划任务执行完毕")
        return nil
    }

    // 11. 启动调度器
    sched := scheduler.NewManager(cfg, taskFunc)
    if err := sched.Start(); err != nil {
        logger.Fatal("启动调度器失败: %v", err)
    }
    defer sched.Stop()

    // 12. 首次任务：带 panic 恢复
    go func() {
        defer func() {
            if r := recover(); r != nil {
                logger.Error("首次任务发生 panic 并已恢复: %v\n堆栈: %s",
                    r, string(debug.Stack()))
            }
        }()
        ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
        defer cancel()
        if err := taskFunc(ctx); err != nil {
            logger.Error("首次任务执行失败: %v", err)
        }
    }()

    // 13. Web 管理后台
    webApp := web.NewApp(cfg, database)
    go func() {
        addr := fmt.Sprintf(":%d", cfg.Server.Port)
        logger.Info("Web 管理后台启动于 http://localhost%s", addr)
        if err := webApp.Start(addr); err != nil && err != http.ErrServerClosed {
            logger.Fatal("Web 服务启动失败: %v", err)
        }
    }()

    // 14. 优雅退出
    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit
    logger.Info("收到退出信号，开始关闭服务...")

    shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    if err := webApp.Shutdown(shutdownCtx); err != nil {
        logger.Error("Web 服务关闭出错: %v", err)
    }
    rtmpMgr.Stop()
}
