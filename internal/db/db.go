// internal/db/db.go
// 数据库操作层 —— 使用软删除机制保护数据，并提供恢复与强制删除接口。
// 注意：使用前需在数据库中为 live_sources、url_sources_passed、users 表增加 deleted_at 字段（DATETIME，默认 NULL）。
package db

import (
	"database/sql"
	"time"

	"github.com/yuanshandalishuishou/live-source-manager-go/internal/models"
	"github.com/yuanshandalishuishou/live-source-manager-go/pkg/logger"

	_ "github.com/mattn/go-sqlite3" // 使用 SQLite 作为本地存储
)

// DB 封装数据库连接和通用操作方法
type DB struct {
	conn *sql.DB
}

// NewDB 打开或创建数据库文件，并确保表结构存在
func NewDB(path string) (*DB, error) {
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	// 启用 WAL 模式提升并发性能
	if _, err := conn.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, err
	}
	// 创建表（如果不存在）
	if err := createTables(conn); err != nil {
		return nil, err
	}
	return &DB{conn: conn}, nil
}

// createTables 执行建表语句（仅当表不存在时）
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
		`CREATE INDEX IF NOT EXISTS idx_live_sources_enable ON live_sources(enable)`,
		`CREATE INDEX IF NOT EXISTS idx_passed_sources_status ON url_sources_passed(status)`,
		`CREATE INDEX IF NOT EXISTS idx_users_username ON users(username)`,
	}
	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// Close 关闭数据库连接
func (d *DB) Close() error {
	return d.conn.Close()
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
