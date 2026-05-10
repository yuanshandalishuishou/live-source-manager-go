// internal/db/db.go
// SQLite 数据库初始化、迁移、CRUD。

package db

import (
	"database/sql"
	"fmt"
	"sync/atomic"

	_ "github.com/mattn/go-sqlite3"
	"live-source-manager-go/internal/models"
	"live-source-manager-go/pkg/logger"
)

// DB 数据库操作封装。
type DB struct {
	conn *sql.DB
}

// New 创建数据库连接并执行迁移。
func New(path string) (*DB, error) {
	conn, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_sync=1")
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}
	return db, nil
}

// SQLDB 暴露底层 *sql.DB，供 ProgressManager 等组件使用。
func (db *DB) SQLDB() *sql.DB {
	return db.conn
}

func (db *DB) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS live_sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT NOT NULL UNIQUE,
			name TEXT,
			group_title TEXT DEFAULT '',
			status TEXT DEFAULT 'pending',
			source_id INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS url_sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			live_source_id INTEGER,
			url TEXT NOT NULL,
			name TEXT,
			group_title TEXT,
			FOREIGN KEY(live_source_id) REFERENCES live_sources(id)
		)`,
		`CREATE TABLE IF NOT EXISTS hotel_scan_configs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ip_range TEXT NOT NULL,
			port INTEGER DEFAULT 80,
			path TEXT DEFAULT '/iptv.m3u',
			enabled INTEGER DEFAULT 1
		)`,
		`CREATE TABLE IF NOT EXISTS test_progress (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT UNIQUE,
			total_sources INTEGER DEFAULT 0,
			tested_sources INTEGER DEFAULT 0,
			success_count INTEGER DEFAULT 0,
			failed_count INTEGER DEFAULT 0,
			status TEXT DEFAULT 'running',
			started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS filter_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pattern TEXT NOT NULL,
			target_type TEXT DEFAULT 'name',
			enable INTEGER DEFAULT 1,
			priority INTEGER DEFAULT 0,
			rule_type TEXT DEFAULT 'blacklist'
		)`,
		`CREATE TABLE IF NOT EXISTS filter_version (
			id INTEGER PRIMARY KEY,
			version INTEGER NOT NULL DEFAULT 1
		)`,
		`INSERT OR IGNORE INTO filter_version(id, version) VALUES(1, 1)`,
	}
	for _, st := range stmts {
		if _, err := db.conn.Exec(st); err != nil {
			return fmt.Errorf("执行DDL失败: %w", err)
		}
	}
	return nil
}

// Close 关闭数据库连接。
func (db *DB) Close() error {
	return db.conn.Close()
}

// ─────── 酒店扫描相关 ───────

func (db *DB) GetHotelScanConfigs() ([]models.HotelScanConfig, error) {
	rows, err := db.conn.Query("SELECT id, ip_range, port, path FROM hotel_scan_configs WHERE enabled=1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []models.HotelScanConfig
	for rows.Next() {
		var c models.HotelScanConfig
		if err := rows.Scan(&c.ID, &c.IPRange, &c.Port, &c.Path); err != nil {
			return nil, err
		}
		c.Enabled = true
		list = append(list, c)
	}
	return list, rows.Err()
}

func (db *DB) BatchInsertURLSources(liveSourceID int, entries []models.URLSource) (int, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare("INSERT OR IGNORE INTO url_sources(live_source_id, url, name, group_title) VALUES(?,?,?,?)")
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	count := 0
	for _, e := range entries {
		if _, err := stmt.Exec(liveSourceID, e.URL, e.Name, e.GroupTitle); err == nil {
			count++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

func (db *DB) UpdateHotelScanStats(configID int, found int) {
	db.conn.Exec("UPDATE hotel_scan_configs SET last_scan_time=CURRENT_TIMESTAMP, last_found=? WHERE id=?", found, configID)
}

// ─────── 直播源相关 ───────

func (db *DB) InsertLiveSource(ls *models.LiveSource) (int64, error) {
	res, err := db.conn.Exec("INSERT INTO live_sources(url, name, group_title, status, source_id) VALUES(?,?,?,?,?)",
		ls.URL, ls.Name, ls.GroupTitle, ls.Status, ls.SourceID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) GetAllLiveSources() ([]models.LiveSource, error) {
	rows, err := db.conn.Query("SELECT id, url, name, group_title, status, source_id, created_at, updated_at FROM live_sources")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []models.LiveSource
	for rows.Next() {
		var s models.LiveSource
		if err := rows.Scan(&s.ID, &s.URL, &s.Name, &s.GroupTitle, &s.Status, &s.SourceID, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

func (db *DB) UpdateLiveSourceStatus(id int, status string) {
	db.conn.Exec("UPDATE live_sources SET status=?, updated_at=CURRENT_TIMESTAMP WHERE id=?", status, id)
}

// ─────── 过滤规则相关 ───────

func (db *DB) GetFilterVersion() (int64, error) {
	var v int64
	err := db.conn.QueryRow("SELECT version FROM filter_version WHERE id=1").Scan(&v)
	return v, err
}

func (db *DB) GetActiveWhitelistRules() ([]models.FilterRule, error) {
	rows, err := db.conn.Query("SELECT id, pattern, target_type, enable, priority FROM filter_rules WHERE rule_type='whitelist' AND enable=1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []models.FilterRule
	for rows.Next() {
		var r models.FilterRule
		if err := rows.Scan(&r.ID, &r.Pattern, &r.TargetType, &r.Enable, &r.Priority); err != nil {
			return nil, err
		}
		r.RuleType = "whitelist"
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

func (db *DB) GetActiveBlacklistRules() ([]models.FilterRule, error) {
	rows, err := db.conn.Query("SELECT id, pattern, target_type, enable, priority FROM filter_rules WHERE rule_type='blacklist' AND enable=1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []models.FilterRule
	for rows.Next() {
		var r models.FilterRule
		if err := rows.Scan(&r.ID, &r.Pattern, &r.TargetType, &r.Enable, &r.Priority); err != nil {
			return nil, err
		}
		r.RuleType = "blacklist"
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// IncrementFilterVersion 规则变更后递增版本号。
func (db *DB) IncrementFilterVersion() error {
	_, err := db.conn.Exec("UPDATE filter_version SET version = version + 1 WHERE id=1")
	if err != nil {
		return err
	}
	v, _ := db.GetFilterVersion()
	globalFilterVersion.Store(v)
	return nil
}

// 全局过滤器版本，供 filter 包热加载。
var globalFilterVersion atomic.Int64

// GetGlobalFilterVersion 返回当前过滤规则版本。
func GetGlobalFilterVersion() int64 {
	return globalFilterVersion.Load()
}
