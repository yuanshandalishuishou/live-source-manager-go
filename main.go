package main

import (
    "database/sql"
    "fmt"
    "log"
    "net/http"
    "os"
    "path/filepath"
    "time"

    "video-source-manager/internal/config"
    "video-source-manager/internal/db"
    "video-source-manager/internal/web/admin"
    "video-source-manager/internal/web/public"

    _ "github.com/mattn/go-sqlite3"
)

const (
    dbDir      = "db"
    outDir     = "outfile"
    dbFileName = "live-source.db"
)

func main() {
    // 1. 检查并创建目录
    ensureDir(dbDir)
    ensureDir(outDir)

    // 2. 初始化数据库
    database := initDB()

    // 3. 加载配置（从 sys_config 表）
    cfg := loadConfig(database)

    // 4. 检查是否有有效源，生成初始 m3u
    initialGenerateM3U(database, cfg)

    // 5. 检查默认密码
    checkDefaultPassword(database, cfg)

    // 6. 启动 Web 服务
    startWebServers(database, cfg)

    // 7. 启动定时任务（每隔指定时间执行收集和测试）
    startScheduler(database, cfg)

    // 阻塞主线程
    select {}
}

// 确保目录存在
func ensureDir(dir string) {
    if _, err := os.Stat(dir); os.IsNotExist(err) {
        if err := os.MkdirAll(dir, 0755); err != nil {
            log.Fatalf("创建目录 %s 失败: %v", dir, err)
        }
    }
}

// 初始化数据库：如果不存在则尝试下载，否则创建新库
func initDB() *sql.DB {
    dbPath := filepath.Join(dbDir, dbFileName)
    if _, err := os.Stat(dbPath); os.IsNotExist(err) {
        log.Println("数据库文件不存在，尝试从 GitHub 下载...")
        err := downloader.DownloadDB(dbPath)
        if err != nil {
            log.Printf("下载失败: %v，将创建新数据库", err)
            // 创建新数据库和表
            if err := db.CreateSchema(dbPath); err != nil {
                log.Fatalf("创建数据库失败: %v", err)
            }
        } else {
            log.Println("数据库下载成功")
        }
    }

    // 打开数据库连接
    database, err := sql.Open("sqlite3", dbPath)
    if err != nil {
        log.Fatalf("打开数据库失败: %v", err)
    }
    // 设置连接池等
    database.SetMaxOpenConns(1)
    return database
}

// 从 sys_config 加载配置
func loadConfig(database *sql.DB) *config.Config {
    cfg := config.New()
    rows, err := database.Query("SELECT group_name, key, value FROM sys_config")
    if err != nil {
        log.Printf("读取配置失败，使用默认配置: %v", err)
        return cfg
    }
    defer rows.Close()
    for rows.Next() {
        var group, key, value string
        if err := rows.Scan(&group, &key, &value); err != nil {
            continue
        }
        // 简单映射，实际应解析为结构体
        switch key {
        case "test_interval":
            cfg.TestInterval, _ = time.ParseDuration(value)
        case "test_concurrency":
            fmt.Sscanf(value, "%d", &cfg.TestConcurrency)
        case "github_proxy":
            cfg.GitHubProxy = value
        }
    }
    return cfg
}

// 初始生成 M3U（如果已有有效源）
func initialGenerateM3U(database *sql.DB, cfg *config.Config) {
    var count int
    err := database.QueryRow("SELECT COUNT(*) FROM sources WHERE status='active' AND enabled=1").Scan(&count)
    if err != nil {
        log.Printf("查询有效源失败: %v", err)
    }
    outPath := filepath.Join(outDir, "live.m3u")
    if count > 0 {
        if err := generator.GenerateM3U(database, outPath); err != nil {
            log.Printf("生成 M3U 失败: %v", err)
        } else {
            log.Printf("已生成 %s", outPath)
        }
    } else {
        // 生成空文件
        if err := os.WriteFile(outPath, []byte("#EXTM3U\n"), 0644); err != nil {
            log.Printf("创建空 M3U 失败: %v", err)
        }
        // 同时在日志和管理界面提示（界面通过 Web 提示，这里只写日志）
        log.Println("警告：没有有效视频源，请添加源并测试")
    }
}

// 检查默认密码（假设 sys_config 中有 admin_password 项）
func checkDefaultPassword(database *sql.DB, cfg *config.Config) {
    var pwd string
    err := database.QueryRow("SELECT value FROM sys_config WHERE key='admin_password'").Scan(&pwd)
    if err == nil && pwd == "admin" { // 假设默认密码是 "admin"
        log.Println("警告：管理员密码为默认密码，请立即修改！")
        // 可在 admin 界面显示提示
    }
}

// 启动 HTTP 服务
func startWebServers(database *sql.DB, cfg *config.Config) {
    // 公共端口 12345
    publicMux := http.NewServeMux()
    publicHandler := public.NewHandler(database)
    publicMux.HandleFunc("/live.m3u", publicHandler.ServeM3U)
    go func() {
        log.Printf("公共 HTTP 服务启动在 :%d", cfg.PublicPort)
        if err := http.ListenAndServe(fmt.Sprintf(":%d", cfg.PublicPort), publicMux); err != nil {
            log.Fatalf("公共服务失败: %v", err)
        }
    }()

    // 管理端口 23456
    adminMux := http.NewServeMux()
    adminHandler := admin.NewHandler(database)
    adminMux.HandleFunc("/", adminHandler.Index)          // 管理首页
    adminMux.HandleFunc("/collect", adminHandler.Collect) // 手动触发收集
    adminMux.HandleFunc("/test", adminHandler.Test)       // 手动触发测试
    go func() {
        log.Printf("管理 HTTP 服务启动在 :%d", cfg.AdminPort)
        if err := http.ListenAndServe(fmt.Sprintf(":%d", cfg.AdminPort), adminMux); err != nil {
            log.Fatalf("管理服务失败: %v", err)
        }
    }()
}

// 启动定时任务
func startScheduler(database *sql.DB, cfg *config.Config) {
    ticker := time.NewTicker(cfg.TestInterval)
    go func() {
        // 立即执行一次
        runCollectAndTest(database, cfg)
        for range ticker.C {
            runCollectAndTest(database, cfg)
        }
    }()
}

// 执行收集、去重、测试、生成
func runCollectAndTest(database *sql.DB, cfg *config.Config) {
    // 检查是否已有任务在运行（可用文件锁或原子变量，这里简化）
    log.Println("开始执行收集和测试任务...")

    // 1. 从 live_sources 获取所有启用的源文件地址
    rows, err := database.Query("SELECT id, source_path, source_type FROM live_sources WHERE enabled=1")
    if err != nil {
        log.Printf("查询 live_sources 失败: %v", err)
        return
    }
    defer rows.Close()

    for rows.Next() {
        var id int64
        var path, typ string
        if err := rows.Scan(&id, &path, &typ); err != nil {
            continue
        }
        // 根据类型处理
        if typ == "online" {
            // 下载并解析
            collector.CollectFromURL(database, id, path, cfg)
        } else if typ == "local" {
            collector.CollectFromFile(database, id, path)
        }
        // custom 类型可能通过管理界面录入，这里暂不处理
    }

    // 2. 去重：删除 url_sources 中重复的 url（保留一条）
    deduplicateURLSources(database)

    // 3. 测试所有未测试或过期的源
    tester.TestAllPending(database, cfg)

    // 4. 生成新的 M3U
    outPath := filepath.Join(outDir, "live.m3u")
    if err := generator.GenerateM3U(database, outPath); err != nil {
        log.Printf("生成 M3U 失败: %v", err)
    }
}

// 简单去重：保留每个 url 最早的一条记录，删除其余
func deduplicateURLSources(database *sql.DB) {
    // 具体 SQL 略，可使用 ROW_NUMBER() 或子查询
    // 这里仅示意
    _, err := database.Exec(`
        DELETE FROM url_sources 
        WHERE id NOT IN (
            SELECT MIN(id) FROM url_sources GROUP BY url
        )
    `)
    if err != nil {
        log.Printf("去重失败: %v", err)
    }
}
