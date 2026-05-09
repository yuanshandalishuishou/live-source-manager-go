// internal/db/db.go
// 重塑 InsertPassedSourceBatch 的事务处理逻辑：错误发生时立即回滚并返回错误，
// 确保数据一致性。同时补齐了 tester 和 web 层所需的 UpdateLiveSourceStatus 等方法。
package db

import (
	"database/sql"
	"fmt"
	"time"

	"live-source-manager-go/internal/models"

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

func (d *DB) Close() error {
	return d.conn.Close()
}

// Conn 返回原始 sql.DB 连接，供 Web 层直接查询使用
func (d *DB) Conn() *sql.DB {
	return d.conn
}

func createTables(db *sql.DB) error {
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
		`CREATE TABLE IF NOT EXISTS categories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			keywords TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS display_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			category_id INTEGER,
			category_name TEXT,
			group_name_override TEXT,
			sort_order INTEGER DEFAULT 0,
			item_sort_order TEXT DEFAULT '0',
			hide_empty_groups INTEGER DEFAULT 0,
			max_items_per_category INTEGER DEFAULT 0,
			enable INTEGER DEFAULT 1
		)`,
	}
	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("exec query: %w", err)
		}
	}

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

// ==================== 直播源管理 ====================

func (d *DB) CreateLiveSource(ls *models.LiveSource) error {
	_, err := d.conn.Exec(
		`INSERT INTO live_sources (name, location, location_type, enable, download_status, http_status, retry_count)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ls.Name, ls.Location, ls.LocationType, ls.Enable, ls.DownloadStatus, ls.HTTPStatus, ls.RetryCount,
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
		ls.LastDownload, ls.DownloadStatus, ls.HTTPStatus, ls.RetryCount,
		ls.ID,
	)
	return err
}

func (d *DB) GetAllLiveSources() ([]models.LiveSource, error) {
	rows, err := d.conn.Query(
		"SELECT id, name, location, location_type, enable, last_download, download_status, http_status, retry_count FROM live_sources WHERE deleted_at IS NULL ORDER BY id",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []models.LiveSource
	for rows.Next() {
		var ls models.LiveSource
		if err := rows.Scan(&ls.ID, &ls.Name, &ls.Location, &ls.LocationType, &ls.Enable, &ls.LastDownload, &ls.DownloadStatus, &ls.HTTPStatus, &ls.RetryCount); err != nil {
			return nil, err
		}
		sources = append(sources, ls)
	}
	return sources, nil
}

func (d *DB) DeleteLiveSource(id int) error {
	return d.softDelete("live_sources", id)
}

// ==================== 通过测试的有效源管理 ====================

func (d *DB) GetActivePassedSources() ([]models.PassedSource, error) {
	return d.GetPassedSourcesByStatus("active")
}

func (d *DB) GetPassedSourcesByStatus(status string) ([]models.PassedSource, error) {
	rows, err := d.conn.Query(
		"SELECT id, name, url, group_name, logo, category_id, epg_id, status FROM url_sources_passed WHERE status = ? AND deleted_at IS NULL ORDER BY id",
		status,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []models.PassedSource
	for rows.Next() {
		var ps models.PassedSource
		var logo, groupName sql.NullString
		if err := rows.Scan(&ps.ID, &ps.Name, &ps.URL, &groupName, &logo, &ps.CategoryID, &ps.EPGID, &ps.Status); err != nil {
			return nil, err
		}
		if logo.Valid {
			ps.Logo = logo.String
		}
		if groupName.Valid {
			ps.GroupName = groupName.String
		}
		sources = append(sources, ps)
	}
	return sources, nil
}

// CountURLSources 统计 URL 源总数
func (d *DB) CountURLSources() int {
	var count int
	d.conn.QueryRow("SELECT COUNT(*) FROM url_sources WHERE deleted_at IS NULL").Scan(&count)
	return count
}

// CountPassedByStatus 统计指定状态的源数量
func (d *DB) CountPassedByStatus(status string) int {
	var count int
	d.conn.QueryRow("SELECT COUNT(*) FROM url_sources_passed WHERE status = ? AND deleted_at IS NULL", status).Scan(&count)
	return count
}

// GetLastTestTime 获取最后一次测试时间
func (d *DB) GetLastTestTime() *time.Time {
	var t time.Time
	err := d.conn.QueryRow("SELECT MAX(created_at) FROM url_sources_passed").Scan(&t)
	if err != nil {
		return nil
	}
	return &t
}

// CountEPGPrograms 统计 EPG 节目总数
func (d *DB) CountEPGPrograms() int {
	var count int
	d.conn.QueryRow("SELECT COUNT(*) FROM epg_programs").Scan(&count)
	return count
}

// internal/db/db.go

// ... 文件开头的 package、import 等保持不变 ...

// InsertPassedSourceBatch 带事务的批量插入，确保数据一致性
func (d *DB) InsertPassedSourceBatch(sources []models.PassedSource) error {
	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	// 使用 defer 确保事务最终被提交或回滚
	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p) // 重新抛出，让上层感知
		} else if err != nil {
			tx.Rollback() // 如果 err 不是 nil，回滚
		} else {
			err = tx.Commit() // 提交事务，并将提交错误赋值给 err
		}
	}()

	stmt, err := tx.Prepare(`
        INSERT INTO url_sources_passed 
        (name, url, group_name, logo, category_id, epg_id, status) 
        VALUES (?, ?, ?, ?, ?, ?, ?)
    `)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, s := range sources {
		if _, execErr := stmt.Exec(s.Name, s.URL, s.GroupName, s.Logo, s.CategoryID, s.EPGID, s.Status); execErr != nil {
			err = fmt.Errorf("insert source %s failed: %w", s.URL, execErr) // 赋值给命名返回值 err
			return err
		}
	}
	return nil
}

// ==================== 分类管理 ====================

func (d *DB) GetAllCategories() ([]models.Category, error) {
	rows, err := d.conn.Query("SELECT id, name, keywords FROM categories ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var categories []models.Category
	for rows.Next() {
		var c models.Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Keywords); err != nil {
			return nil, err
		}
		categories = append(categories, c)
	}
	return categories, nil
}

func (d *DB) CreateCategory(cat *models.Category) error {
	_, err := d.conn.Exec("INSERT INTO categories (name, keywords) VALUES (?, ?)", cat.Name, cat.Keywords)
	return err
}

// ==================== 用户管理 ====================

func (d *DB) GetUserByUsername(username string) (*models.User, error) {
	row := d.conn.QueryRow("SELECT id, username, password_hash, is_admin, is_active, last_login FROM users WHERE username = ? AND deleted_at IS NULL", username)
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
	rows, err := d.conn.Query("SELECT id, username, password_hash, is_admin, is_active, last_login FROM users WHERE deleted_at IS NULL ORDER BY id")
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
	_, err := d.conn.Exec("INSERT INTO users (username, password_hash, is_admin, is_active) VALUES (?, ?, ?, ?)", u.Username, u.PasswordHash, u.IsAdmin, u.IsActive)
	return err
}

func (d *DB) UpdateUser(u *models.User) error {
	_, err := d.conn.Exec("UPDATE users SET username=?, password_hash=?, is_admin=?, is_active=?, last_login=? WHERE id=?", u.Username, u.PasswordHash, u.IsAdmin, u.IsActive, u.LastLogin, u.ID)
	return err
}

func (d *DB) DeleteUser(id int) error {
	return d.softDelete("users", id)
}

func (d *DB) UpdateUserLastLogin(userID int, t time.Time) error {
	_, err := d.conn.Exec("UPDATE users SET last_login = ? WHERE id = ?", t, userID)
	return err
}

// ==================== 测试器辅助方法 ====================

func (d *DB) UpdateLiveSourceStatus(id int, status string) error {
	_, err := d.conn.Exec("UPDATE live_sources SET download_status = ? WHERE id = ?", status, id)
	return err
}

func (d *DB) UpdateLiveSourceMeta(id int, meta *models.StreamMeta) error {
	resolution := fmt.Sprintf("%dx%d", meta.Width, meta.Height)
	_, err := d.conn.Exec(`INSERT INTO url_sources_passed (name, url, status, resolution, bitrate)
		SELECT name, location, 'active', ?, ? FROM live_sources WHERE id = ?`,
		resolution, meta.BitRate, id)
	return err
}
