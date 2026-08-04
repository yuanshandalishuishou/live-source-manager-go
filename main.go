// Command live-source-manager-go is the entry point for the IPTV live-source manager.
// It opens the SQLite database, seeds defaults, ensures an admin user, starts the
// management web server (manager_port) and the static file-share server (fileshare_port).
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/epg"
	"live-source-manager-go/internal/logger"
	"live-source-manager-go/internal/manager"
	"live-source-manager-go/internal/rules"
	"live-source-manager-go/internal/web"
)

func main() {
	var (
		configDir     = flag.String("config-dir", ".", "配置/数据根目录")
		dbPath        = flag.String("db", "", "SQLite 数据库路径（默认 <config-dir>/data/app.db）")
		host          = flag.String("host", "0.0.0.0", "监听地址")
		managerPort   = flag.Int("manager-port", 0, "管理端口（默认读配置 HTTPServer.manager_port）")
		filesharePort = flag.Int("fileshare-port", 0, "发布端口（默认读配置 HTTPServer.fileshare_port）")
		initDB        = flag.Bool("init-db", false, "仅初始化数据库与默认值后退出")
		logFile       = flag.String("log-file", "", "日志文件路径（默认读配置 Logging.file）")
	)
	flag.Parse()

	log := logger.L()

	// Resolve paths (normalize a Git-Bash / MSYS style POSIX path like
	// /e/foo into a Windows drive path E:\foo so config loading works).
	absDir, err := filepath.Abs(normalizePath(*configDir))
	if err != nil {
		absDir = normalizePath(*configDir)
	}
	dbFile := *dbPath
	if dbFile == "" {
		dbFile = filepath.Join(absDir, "data", "app.db")
	} else {
		dbFile = normalizePath(dbFile)
	}

	// Open database + migrate.
	conn, err := db.Open(dbFile)
	if err != nil {
		log.Error("打开数据库失败: %v", err)
		os.Exit(1)
	}
	defer conn.Close()

	cfg := config.New(conn)

	// Apply logging config (file + size-bounded rotation, mirroring Python).
	logPath := *logFile
	if logPath == "" {
		logPath = cfg.Get("Logging", "file", "./log/app.log")
	}
	if logPath != "" {
		if dir := filepath.Dir(logPath); dir != "" {
			_ = os.MkdirAll(dir, 0o755)
		}
		maxSize := cfg.GetInt("Logging", "max_size", 10)
		backupCount := cfg.GetInt("Logging", "backup_count", 5)
		logger.SetDefault(logger.NewRotating(levelFromConfig(cfg), logPath, maxSize, backupCount))
	}
	setLogLevel(cfg)

	// Seed default config values.
	n, err := db.SeedDefaults(conn, config.DefaultValues())
	if err != nil {
		log.Error("写入默认配置失败: %v", err)
		os.Exit(1)
	}
	log.Info("配置默认值已就绪（新增 %d 项）", n)

	// Seed category dictionary from YAML.
	yamlPath := filepath.Join(absDir, "config", "channel_rules.yml")
	if _, statErr := os.Stat(yamlPath); statErr == nil {
		eng := rules.NewEngine(conn)
		if sdErr := eng.SeedDictionary(yamlPath); sdErr != nil {
			log.Warning("分类词典种子导入失败: %v", sdErr)
		} else {
			log.Info("分类词典已导入: %s", yamlPath)
		}
	} else {
		log.Warning("未找到分类词典文件，跳过导入: %s", yamlPath)
	}

	// Ensure an admin user exists.
	ensureAdmin(conn, log)

	if *initDB {
		log.Info("初始化完成，按 --init-db 退出")
		return
	}

	// Resolve ports.
	mPort := *managerPort
	if mPort == 0 {
		mPort = cfg.GetInt("HTTPServer", "manager_port", 23456)
	}
	fPort := *filesharePort
	if fPort == 0 {
		fPort = cfg.GetInt("HTTPServer", "fileshare_port", 12345)
	}
	documentRoot := cfg.Get("HTTPServer", "document_root", "./www/output")
	bindHost := *host

	// Build pipeline manager + web router.
	mgr := manager.New(conn, cfg)
	mgr.StartScheduler(context.Background())

	// EPG 管理器（与 Python 版对齐：抓取外部 XMLTV → 频道对齐 → 注入 m3u + 生成 epg.xml.gz）。
	em := epg.New(conn, cfg)
	// NamesProvider 提供当前已采集到的本地频道名，用于 EPG 频道自动对齐；
	// 冷缓存（未采集）时返回 nil，由 epg.MatchChannels 回落到 channel_name_mapping 表。
	em.NamesProvider = func() []string {
		chs, _, ok := mgr.PeekChannels()
		if !ok || len(chs) == 0 {
			return nil
		}
		names := make([]string, 0, len(chs))
		for _, c := range chs {
			names = append(names, c.Name)
		}
		return names
	}

	router := web.NewRouter(conn, cfg, mgr, em)

	// EPG 常驻调度：每分钟巡检启用源的刷新计划，到点触发增量刷新。
	epgCtx, epgCancel := context.WithCancel(context.Background())
	go newEPGScheduler(em, conn, filepath.Join(absDir, "data", "status")).run(epgCtx)

	// File-share server (static M3U output).
	go func() {
		fsRoot := documentRoot
		_ = os.MkdirAll(fsRoot, 0o755)
		fs := http.FileServer(http.Dir(fsRoot))
		addr := fmt.Sprintf("%s:%d", bindHost, fPort)
		log.Info("发布服务已启动: http://%s", addr)
		srv := &http.Server{Addr: addr, Handler: fs, ReadHeaderTimeout: 30 * time.Second}
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("发布服务异常: %v", err)
		}
	}()

	// Management web server.
	addr := fmt.Sprintf("%s:%d", bindHost, mPort)
	log.Info("管理界面已启动: http://%s", addr)
	httpSrv := &http.Server{Addr: addr, Handler: router, ReadHeaderTimeout: 30 * time.Second}

	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("管理服务异常: %v", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Info("正在关闭服务…")
	epgCancel()
	mgr.StopScheduler()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	log.Info("已停止")
	logger.L().Close()
}

func ensureAdmin(conn *sql.DB, log *logger.Logger) {
	users, err := db.ListUsers(conn)
	if err != nil {
		log.Warning("查询用户失败: %v", err)
		return
	}
	if len(users) > 0 {
		return
	}
	// 与 Python 版本保持一致：优先读 WEB_ADMIN_PASSWORD，回退 LSM_ADMIN_PASSWORD。
	pw := os.Getenv("WEB_ADMIN_PASSWORD")
	if pw == "" {
		pw = os.Getenv("LSM_ADMIN_PASSWORD")
	}
	if pw == "" {
		// 两者皆空：使用默认初始密码 Admin@123（李总指定，与历史 Python 部署习惯一致）。
		// 环境变量可覆盖；首次登录后建议在「配置中心 → 密码管理」修改。
		pw = "Admin@123"
		log.Warning("未设置 WEB_ADMIN_PASSWORD / LSM_ADMIN_PASSWORD，使用默认初始密码 Admin@123（建议首次登录后修改）")
		fmt.Println("ADMIN_PASSWORD_INITIALIZED=" + pw)
	}
	// 校验密码强度（兜底：过短则重新生成）。
	if len(pw) < 6 {
		pw = generateAdminPassword()
	}
	id, err := db.CreateUser(conn, "admin", pw, "admin", "管理员")
	if err != nil {
		log.Error("创建管理员失败: %v", err)
		return
	}
	log.Info("已创建默认管理员账号 admin (id=%d)", id)
}

// generateAdminPassword returns a crypto-random 16-char password (no ambiguous
// chars), mirroring Python's auto-generated admin password on first deploy.
func generateAdminPassword() string {
	const chars = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// 极端兜底：不应发生
		return fmt.Sprintf("adm%x", time.Now().UnixNano())
	}
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}

func setLogLevel(cfg *config.Config) {
	logger.L().SetLevel(levelFromConfig(cfg))
}

func levelFromConfig(cfg *config.Config) logger.Level {
	switch cfg.Get("Logging", "level", "INFO") {
	case "DEBUG":
		return logger.LevelDebug
	case "WARNING", "WARN":
		return logger.LevelWarning
	case "ERROR":
		return logger.LevelError
	default:
		return logger.LevelInfo
	}
}

// normalizePath converts a POSIX-style path (e.g. /e/foo/bar produced by
// Git-Bash / MSYS shells) into a Windows drive path (E:\foo\bar) so that
// filepath.Abs on Windows resolves it correctly. Other paths pass through.
func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "."
	}
	if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == '/' {
		drive := p[1]
		if (drive >= 'a' && drive <= 'z') || (drive >= 'A' && drive <= 'Z') {
			return strings.ToUpper(string(drive)) + ":" + p[2:]
		}
	}
	return p
}
