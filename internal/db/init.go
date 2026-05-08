package db

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/logger"
)

const (
	dbDir      = "./db"
	dbFileName = "live-source.db"
	dbFullPath = dbDir + "/" + dbFileName
	// 预置数据库下载地址
	presetDBURL = "https://raw.githubusercontent.com/yuanshandalishuishou/live-source-manager-go/main/db/live-source.db"
)

// Init 完成数据库初始化流程
func Init() (*sql.DB, error) {
	// 确保 db 目录存在
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return nil, fmt.Errorf("创建数据库目录失败: %w", err)
	}

	// 如果数据库文件不存在，尝试下载
	if _, err := os.Stat(dbFullPath); os.IsNotExist(err) {
		logger.Info("本地数据库不存在，尝试从 GitHub 下载")
		if err := downloadPresetDB(); err != nil {
			logger.Warn("下载预置数据库失败，将执行建表", "error", err)
			// 下载失败则创建新库并执行全部 DDL
			if err := createFromScratch(); err != nil {
				return nil, err
			}
		}
	} else {
		logger.Info("已存在数据库文件")
	}

	// 打开数据库连接（启用 WAL 和超时设置）
	db, err := sql.Open("sqlite3", dbFullPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite 建议单连接
	db.SetConnMaxLifetime(time.Minute * 5)

	// 验证关键表是否存在
	var count int
	err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='sys_config'").Scan(&count)
	if err != nil || count == 0 {
		logger.Warn("关键表缺失，重新建表")
		db.Close()
		if err := createFromScratch(); err != nil {
			return nil, err
		}
		db, err = sql.Open("sqlite3", dbFullPath+"?_journal_mode=WAL")
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(1)
	}

	// 检查是否需要迁移或新增字段（此处略）
	return db, nil
}

// downloadPresetDB 从 GitHub 下载预置数据库文件
func downloadPresetDB() error {
	resp, err := http.Get(presetDBURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	out, err := os.Create(dbFullPath)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

// createFromScratch 执行全部建表 SQL 并插入默认数据
func createFromScratch() error {
	db, err := sql.Open("sqlite3", dbFullPath)
	if err != nil {
		return err
	}
	defer db.Close()

	// 启用外键
	db.Exec("PRAGMA foreign_keys = ON;")
	// 执行建表脚本（此处简化，实际应读取内嵌 SQL 文件或使用事先定义的语句）
	for _, stmt := range allCreateTablesSQL() {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("执行建表语句失败: %v\n%s", err, stmt)
		}
	}
	// 插入默认配置
	for _, stmt := range defaultInsertsSQL() {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("执行默认数据插入失败: %v\n%s", err, stmt)
		}
	}
	logger.Info("数据库建表并初始化完成")
	return nil
}

// allCreateTablesSQL 返回所有建表语句的切片（从原 SQL 定义中截取，尽量不变）
func allCreateTablesSQL() []string {
	return []string{
		`CREATE TABLE sys_config ( id INTEGER PRIMARY KEY AUTOINCREMENT, group_name TEXT NOT NULL DEFAULT 'general', key TEXT NOT NULL UNIQUE, value TEXT, value_type TEXT DEFAULT 'string', description TEXT, version INTEGER DEFAULT 1, created_at DATETIME DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME DEFAULT CURRENT_TIMESTAMP );`,
		`CREATE TABLE users ( id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL, is_admin BOOLEAN DEFAULT 0, created_at DATETIME DEFAULT CURRENT_TIMESTAMP, last_login DATETIME, is_active BOOLEAN DEFAULT 1 );`,
		`CREATE TABLE live_sources ( id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, location TEXT NOT NULL, location_type TEXT NOT NULL DEFAULT 'url', enable BOOLEAN DEFAULT 1, last_download DATETIME, download_status TEXT DEFAULT 'pending', http_status INTEGER, retry_count INTEGER DEFAULT 0, created_at DATETIME DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME DEFAULT CURRENT_TIMESTAMP );`,
		// 其他表类似，此处省略完整列表，实际应全部包含
		`CREATE TABLE url_sources ( id INTEGER PRIMARY KEY AUTOINCREMENT, live_source_id INTEGER NOT NULL, url TEXT NOT NULL, name TEXT, tvg_id TEXT, tvg_logo TEXT, group_title TEXT, catchup TEXT, catchup_days INTEGER, user_agent TEXT, raw_attributes TEXT, source_type TEXT DEFAULT 'video', created_at DATETIME DEFAULT CURRENT_TIMESTAMP, UNIQUE(url, name), FOREIGN KEY(live_source_id) REFERENCES live_sources(id) ON DELETE CASCADE );`,
		// ... 剩余表参照 Excel 中 SQL 语句
	}
}

// defaultInsertsSQL 返回所有默认数据插入语句
func defaultInsertsSQL() []string {
	return []string{
		`INSERT INTO users (username, password_hash, is_admin) VALUES ('admin', '$2a$10$...', 1);`, // 实际哈希
		`INSERT INTO sys_config (group_name, key, value, value_type, description) VALUES ('Network','proxy_enabled','false','bool','是否启用代理');`,
		// ...
	}
}
