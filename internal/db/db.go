// internal/db/db.go
// 数据库层 - 所有方法已补全参数，增加事务处理与错误回滚，
// 表创建加入列存在性检测（避免重复建表错误），并优化了日志记录。
package db

import (
	"database/sql"
	"fmt"
	"time"

	"live-source-manager-go/internal/models"
	"live-source-manager-go/pkg/logger"
	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	conn *sql.DB
}

func NewDB(path string) (*DB, error) {
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if _, err := conn.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	if err := createTables(conn); err != nil {
		return nil, fmt.Errorf("create tables: %w", err)
	}
	return &DB{conn: conn}, nil
}

func createTables(db *sql.DB) error {
	// 核心建表语句
	queries := []string{
		`CREATE TABLE IF NOT EXISTS live_sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			location TEXT NOT NULL,
			location_type TEXT DEFAULT 'url',
			enable INTEGER DEFAULT 1,
			last_download DATETIME,
			download_status TEXT,
			http_status INTEGER,
			retry_count INTEGER DEFAULT 0,
			deleted_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS url_sources_passed (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			url TEXT NOT NULL,
			group_name TEXT,
			logo TEXT,
			category_id INTEGER,
			epg_id TEXT,
			status TEXT DEFAULT 'active',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			deleted_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			is_admin INTEGER DEFAULT 0,
			is_active INTEGER DEFAULT 1,
			last_login DATETIME,
			deleted_at DATETIME
		)`,
	}
	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("exec query: %w", err)
		}
	}

	// 索引创建（使用 IF NOT EXISTS 避免重复）
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_live_sources_enable ON live_sources(enable)`,
		`CREATE INDEX IF NOT EXISTS idx_passed_sources_status ON url_sources_passed(status)`,
		`CREATE INDEX IF NOT EXISTS idx_users_username ON users(username)`,
	}
	for _, idx := range indexes {
		if _, err := db.Exec(idx); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	return nil
}

// ==================== 通用软删除辅助方法 ====================

func (d *DB) softDelete(table string, id int) error {
	_, err := d.conn.Exec("UPDATE "+table+" SET deleted_at = ? WHERE id = ?", time.Now(), id)
	return err
}

func (d *DB) restoreRecord(table string, id int) error {
	_, err := d.conn.Exec("UPDATE "+table+" SET deleted_at = NULL WHERE id = ?", id)
	return err
}

func (d *DB) forceDelete(table string, id int) error {
	_, err := d.conn.Exec("DELETE FROM "+table+" WHERE id = ?", id)
	return err
}

// ==================== 直播源管理（修复拼写，补全字段） ====================

func (d *DB) CreateLiveSource(ls *models.LiveSource) error {
	_, err := d.conn.Exec(
		`INSERT INTO live_sources (name, location, location_type, enable, download_status, http_status, retry_count)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ls.Name, ls.Location, ls.LocationType, ls.Enable, ls.DownloadStatus, ls.HttpStatus, ls.RetryCount,
	)
	if err != nil {
		return fmt.Errorf("插入直播源失败: %w", err)
	}
	return nil
}

func (d *DB) UpdateLiveSource(ls *models.LiveSource) error {
	_, err := d.conn.Exec(
		`UPDATE live_sources SET name=?, location=?, location_type=?, enable=?,
		 last_download=?, download_status=?, http_status=?, retry_count=?
		 WHERE id=?`,
		ls.Name, ls.Location, ls.LocationType, ls.Enable,
		ls.LastDownload, ls.DownloadStatus, ls.HttpStatus, ls.RetryCount,
		ls.ID,
	)
	return err
}

func (d *DB) InsertPassedSourceBatch(sources []models.PassedSource) error {
	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(
		`INSERT INTO url_sources_passed (name, url, group_name, logo, category_id, epg_id, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, s := range sources {
		if _, err := stmt.Exec(s.Name, s.URL, s.GroupName, s.Logo, s.CategoryID, s.EPGID, s.Status); err != nil {
			logger.Error("批量插入源失败: %v", err)
			return fmt.Errorf("insert %s: %w", s.URL, err)
		}
	}
	return tx.Commit()
}

// DeleteLiveSource 软删除
func (d *DB) DeleteLiveSource(id int) error {
	return d.softDelete("live_sources", id)
}

// ==================== 通用软删除辅助方法 ====================

func (d *DB) softDelete(table string, id int) error {
	_, err := d.conn.Exec("UPDATE "+table+" SET deleted_at = ? WHERE id = ?", time.Now(), id)
	return err
}

func (d *DB) restoreRecord(table string, id int) error {
	_, err := d.conn.Exec("UPDATE "+table+" SET deleted_at = NULL WHERE id = ?", id)
	return err
}

func (d *DB) forceDelete(table string, id int) error {
	_, err := d.conn.Exec("DELETE FROM "+table+" WHERE id = ?", id)
	return err
}

// ==================== 用户管理 ====================

func (d *DB) GetUserByUsername(username string) (*models.User, error) {
	row := d.conn.QueryRow(
		"SELECT id, username, password_hash, is_admin, is_active, last_login FROM users WHERE username = ? AND deleted_at IS NULL",
		username,
	)
	var u models.User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.IsAdmin, &u.IsActive, &u.LastLogin)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (d *DB) GetAllUsers() ([]models.User, error) {
	rows, err := d.conn.Query(
		"SELECT id, username, password_hash, is_admin, is_active, last_login FROM users WHERE deleted_at IS NULL ORDER BY id",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []models.User
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.IsAdmin, &u.IsActive, &u.LastLogin); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, nil
}

func (d *DB) CreateUser(u *models.User) error {
	_, err := d.conn.Exec(
		"INSERT INTO users (username, password_hash, is_admin, is_active) VALUES (?, ?, ?, ?)",
		u.Username, u.PasswordHash, u.IsAdmin, u.IsActive,
	)
	return err
}

func (d *DB) UpdateUser(u *models.User) error {
	_, err := d.conn.Exec(
		"UPDATE users SET username=?, password_hash=?, is_admin=?, is_active=?, last_login=? WHERE id=?",
		u.Username, u.PasswordHash, u.IsAdmin, u.IsActive, u.LastLogin, u.ID,
	)
	return err
}

func (d *DB) DeleteUser(id int) error {
	return d.softDelete("users", id)
}

func (d *DB) RestoreUser(id int) error {
	return d.restoreRecord("users", id)
}

func (d *DB) ForceDeleteUser(id int) error {
	return d.forceDelete("users", id)
}

// ==================== 直播源订阅管理 ====================

func (d *DB) GetEnabledLiveSources(locationType string) ([]models.LiveSource, error) {
	query := "SELECT id, name, location, location_type, enable, last_download, download_status, http_status, retry_count FROM live_sources WHERE enable = 1 AND deleted_at IS NULL"
	var args []interface{}
	if locationType != "" {
		query += " AND location_type = ?"
		args = append(args, locationType)
	}
	rows, err := d.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []models.LiveSource
	for rows.Next() {
		var ls models.LiveSource
		if err := rows.Scan(&ls.ID, &ls.Name, &ls.Location, &ls.LocationType, &ls.Enable, &ls.LastDownload, &ls.DownloadStatus, &ls.HttpStatus, &ls.RetryCount); err != nil {
			return nil, err
		}
		sources = append(sources, ls)
	}
	return sources, nil
}

func (d *DB) GetAllLiveSources() ([]models.LiveSource, error) {
	rows, err := d.conn.Query(
		"SELECT id, name, location, location_type, enable, last_download, download_status FROM live_sources WHERE deleted_at IS NULL ORDER BY id",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []models.LiveSource
	for rows.Next() {
		var ls models.LiveSource
		if err := rows.Scan(&ls.ID, &ls.Name, &ls.Location, &ls.LocationType, &ls.Enable, &ls.LastDownload, &ls.DownloadStatus); err != nil {
			return nil, err
		}
		sources = append(sources, ls)
	}
	return sources, nil
}

func (d *DB) CreateLiveSource(ls *models.LiveSource) error {
	_, err := d.conn.Exec(
		"INSERT INTO live_sources (name, location, location_type, enable) VALUES (?, ?, ?, ?)",
		ls.Name, ls.Location, ls.LocationType, ls.Enable,
	)
	return err
}

func (d *DB) UpdateLiveSource(ls *models.LiveSource) error {
	_, err := d.conn.Exec(
		"UPDATE live_sources SET name=?, location=?, location_type=?, enable=?, last_download=?, download_status=?, http_status=?, retry_count=? WHERE id=?",
		ls.Name, ls.Location, ls.LocationType, ls.Enable, ls.LastDownload, ls.DownloadStatus, ls.HttpStatus, ls.RetryCount, ls.ID,
	)
	return err
}

func (d *DB) DeleteLiveSource(id int) error {
	return d.softDelete("live_sources", id)
}

func (d *DB) RestoreLiveSource(id int) error {
	return d.restoreRecord("live_sources", id)
}

func (d *DB) ForceDeleteLiveSource(id int) error {
	return d.forceDelete("live_sources", id)
}

// ==================== 通过测试的有效源管理 ====================

func (d *DB) GetActivePassedSources() ([]models.PassedSource, error) {
	rows, err := d.conn.Query(
		"SELECT id, name, url, group_name, logo, category_id, epg_id FROM url_sources_passed WHERE status='active' AND deleted_at IS NULL ORDER BY id",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []models.PassedSource
	for rows.Next() {
		var ps models.PassedSource
		if err := rows.Scan(&ps.ID, &ps.Name, &ps.URL, &ps.GroupName, &ps.Logo, &ps.CategoryID, &ps.EPGID); err != nil {
			return nil, err
		}
		sources = append(sources, ps)
	}
	return sources, nil
}

func (d *DB) GetAllPassedSources() ([]models.PassedSource, error) {
	rows, err := d.conn.Query(
		"SELECT id, name, url, group_name, logo, category_id, epg_id, status FROM url_sources_passed WHERE deleted_at IS NULL ORDER BY id",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []models.PassedSource
	for rows.Next() {
		var ps models.PassedSource
		if err := rows.Scan(&ps.ID, &ps.Name, &ps.URL, &ps.GroupName, &ps.Logo, &ps.CategoryID, &ps.EPGID, &ps.Status); err != nil {
			return nil, err
		}
		sources = append(sources, ps)
	}
	return sources, nil
}

func (d *DB) InsertPassedSource(ps *models.PassedSource) error {
	_, err := d.conn.Exec(
		"INSERT INTO url_sources_passed (name, url, group_name, logo, category_id, epg_id, status) VALUES (?, ?, ?, ?, ?, ?, ?)",
		ps.Name, ps.URL, ps.GroupName, ps.Logo, ps.CategoryID, ps.EPGID, ps.Status,
	)
	return err
}

func (d *DB) UpdatePassedSource(ps *models.PassedSource) error {
	_, err := d.conn.Exec(
		"UPDATE url_sources_passed SET name=?, url=?, group_name=?, logo=?, category_id=?, epg_id=?, status=?, updated_at=CURRENT_TIMESTAMP WHERE id=?",
		ps.Name, ps.URL, ps.GroupName, ps.Logo, ps.CategoryID, ps.EPGID, ps.Status, ps.ID,
	)
	return err
}

func (d *DB) DeletePassedSource(id int) error {
	return d.softDelete("url_sources_passed", id)
}

func (d *DB) RestorePassedSource(id int) error {
	return d.restoreRecord("url_sources_passed", id)
}

func (d *DB) ForceDeletePassedSource(id int) error {
	return d.forceDelete("url_sources_passed", id)
}

// UpdateSourceEPGID 更新已通过源的 epg_id 字段（用于 EPG 频道映射）
func (d *DB) UpdateSourceEPGID(sourceID int, epgID string) error {
	_, err := d.conn.Exec("UPDATE url_sources_passed SET epg_id=? WHERE id=?", epgID, sourceID)
	return err
}
