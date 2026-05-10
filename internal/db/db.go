// internal/db/db.go
// 数据库连接管理、自动创建/下载、表迁移

package db

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动
)

// DB 封装数据库连接和常用操作
type DB struct {
	conn *sql.DB
	path string
}

// New 初始化数据库连接。
// 如果数据库文件不存在，会尝试从远程 URL 下载（若配置），否则本地创建。
func New(dbPath string) (*DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建数据库目录失败: %w", err)
	}

	// 检查数据库文件是否存在
	needInit := false
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		needInit = true
		// 尝试从远程 URL 下载数据库（优先级最高）
		if err := downloadDatabase(dbPath); err != nil {
			// 下载失败则本地创建，这是正常情况
			fmt.Printf("远程数据库未配置或下载失败，将本地创建: %v\n", err)
		}
	}

	conn, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	db := &DB{conn: conn, path: dbPath}

	if needInit {
		if err := db.migrate(); err != nil {
			return nil, fmt.Errorf("数据库迁移失败: %w", err)
		}
	} else {
		// 即使文件已存在，也运行迁移以添加可能缺失的表/列
		if err := db.migrate(); err != nil {
			return nil, fmt.Errorf("数据库迁移失败: %w", err)
		}
	}

	return db, nil
}

// Close 关闭数据库连接
func (db *DB) Close() error {
	return db.conn.Close()
}

// Conn 返回原始 sql.DB 连接，供其他模块直接使用
func (db *DB) Conn() *sql.DB {
	return db.conn
}

// downloadDatabase 尝试从环境变量或配置的 URL 下载数据库文件
func downloadDatabase(destPath string) error {
	remoteURL := os.Getenv("REMOTE_DB_URL") // 可通过环境变量配置
	if remoteURL == "" {
		return fmt.Errorf("未配置 REMOTE_DB_URL")
	}

	resp, err := http.Get(remoteURL)
	if err != nil {
		return fmt.Errorf("下载请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载返回状态码: %d", resp.StatusCode)
	}

	file, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("创建目标文件失败: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(file, resp.Body); err != nil {
		return fmt.Errorf("写入数据库文件失败: %w", err)
	}
	return nil
}

// migrate 执行数据库模式迁移（建表、添加索引等）
func (db *DB) migrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT NOT NULL,
			name TEXT,
			group_name TEXT,
			logo TEXT,
			latency INTEGER DEFAULT 0,
			status TEXT DEFAULT 'unknown',
			last_check DATETIME,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS filter_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL DEFAULT 'blacklist', -- blacklist / whitelist
			pattern TEXT NOT NULL,
			field TEXT NOT NULL DEFAULT 'name',     -- name / url / group
			is_regex INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS display_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			match_pattern TEXT NOT NULL,      -- 频道名匹配正则
			display_group TEXT NOT NULL,      -- 显示分组名
			priority INTEGER DEFAULT 0,       -- 优先级，数字越小越高
			enabled INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS epg_data (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_name TEXT NOT NULL,
			start_time DATETIME NOT NULL,
			end_time DATETIME NOT NULL,
			title TEXT NOT NULL,
			description TEXT,
			category TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sources_url ON sources(url)`,
		`CREATE INDEX IF NOT EXISTS idx_epg_channel_time ON epg_data(channel_name, start_time)`,
	}

	for _, q := range queries {
		if _, err := db.conn.Exec(q); err != nil {
			return fmt.Errorf("执行SQL失败: %q, 错误: %w", q, err)
		}
	}
	return nil
}
