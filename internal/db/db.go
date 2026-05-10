// internal/db/db.go
// 补充 UpdateLiveSourceStatus、GetFilterVersion、GetActiveWhitelistRules、
// GetActiveBlacklistRules 等 tester.go 和 filter.go 依赖的方法。
//
// 这些方法在原 db.go 中缺失，导致编译失败。

package db

import (
	"database/sql"
	"fmt"
	"time"

	"live-source-manager-go/internal/models"

	_ "github.com/mattn/go-sqlite3"
)

// ──────── 数据库连接 ────────

type DB struct {
	conn *sql.DB
}

func NewDB(path string) (*DB, error) {
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	// 启用 WAL 模式以提高并发性能
	if _, err := conn.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, fmt.Errorf("启用 WAL 失败: %w", err)
	}
	// 启用外键约束
	if _, err := conn.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return nil, fmt.Errorf("启用外键失败: %w", err)
	}

	if err := createTables(conn); err != nil {
		return nil, fmt.Errorf("创建表失败: %w", err)
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

// ──────── 建表 ────────

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
		`CREATE TABLE IF NOT EXISTS filter_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			rule_type TEXT NOT NULL,
			pattern TEXT NOT NULL,
			target_type TEXT DEFAULT 'url',
			enable INTEGER DEFAULT 1,
			priority INTEGER DEFAULT 0,
			description TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
	}

	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("执行建表语句失败: %w", err)
		}
	}

	// 创建索引
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_live_sources_enable ON live_sources(enable)`,
		`CREATE INDEX IF NOT EXISTS idx_passed_sources_status ON url_sources_passed(status)`,
		`CREATE INDEX IF NOT EXISTS idx_users_username ON users(username)`,
		`CREATE INDEX IF NOT EXISTS idx_filter_rules_type ON filter_rules(rule_type, enable)`,
	}

	for _, idx := range indexes {
		if _, err := db.Exec(idx); err != nil {
			return fmt.Errorf("创建索引失败: %w", err)
		}
	}

	// 创建默认管理员账户（如果不存在）
	createDefaultAdmin(db)

	return nil
}

// ──────── 直播源状态更新（tester.go 依赖）───────

// UpdateLiveSourceStatus 更新单个直播源的下载状态。
// 在 tester.go 的 TestAll 方法中被调用，用于标记测试成功或失败的源。
func (d *DB) UpdateLiveSourceStatus(id int, status string) error {
	_, err := d.conn.Exec(
		"UPDATE live_sources SET download_status = ?, last_download = ? WHERE id = ?",
		status, time.Now(), id,
	)
	if err != nil {
		return fmt.Errorf("更新直播源状态失败 (id=%d): %w", id, err)
	}
	return nil
}

// UpdateLiveSourceStatusBatch 批量更新直播源的下载状态。
// 用于在一次测试完成后批量更新多个源的状态，减少数据库往返次数。
func (d *DB) UpdateLiveSourceStatusBatch(updates map[int]string) error {
	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("开始事务失败: %w", err)
	}
	defer tx.Rollback() // 如果未提交，自动回滚

	stmt, err := tx.Prepare(
		"UPDATE live_sources SET download_status = ?, last_download = ? WHERE id = ?")
	if err != nil {
		return fmt.Errorf("准备语句失败: %w", err)
	}
	defer stmt.Close()

	now := time.Now()
	for id, status := range updates {
		if _, err := stmt.Exec(status, now, id); err != nil {
			return fmt.Errorf("更新状态失败 (id=%d): %w", id, err)
		}
	}

	return tx.Commit()
}

// GetLiveSourceByID 根据 ID 获取单个直播源
func (d *DB) GetLiveSourceByID(id int) (*models.LiveSource, error) {
	ls := &models.LiveSource{}
	err := d.conn.QueryRow(
		`SELECT id, name, location, location_type, enable, last_download,
		 download_status, http_status, retry_count
		 FROM live_sources WHERE id = ? AND deleted_at IS NULL`, id,
	).Scan(&ls.ID, &ls.Name, &ls.Location, &ls.LocationType, &ls.Enable,
		&ls.LastDownload, &ls.DownloadStatus, &ls.HTTPStatus, &ls.RetryCount)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("直播源不存在 (id=%d)", id)
	}
	if err != nil {
		return nil, fmt.Errorf("查询直播源失败: %w", err)
	}
	return ls, nil
}

// ──────── 过滤器规则方法（filter.go 依赖）───────

// GetFilterVersion 返回当前过滤器规则的最新更新时间戳。
// filter.go 用它判断是否需要热重载规则。
func (d *DB) GetFilterVersion() (int64, error) {
	var version int64
	err := d.conn.QueryRow(
		"SELECT COALESCE(MAX(updated_at), 0) FROM filter_rules",
	).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("获取过滤器版本失败: %w", err)
	}
	return version, nil
}

// GetActiveWhitelistRules 获取所有启用的白名单规则
func (d *DB) GetActiveWhitelistRules() ([]models.FilterRule, error) {
	return d.getActiveFilterRules("whitelist")
}

// GetActiveBlacklistRules 获取所有启用的黑名单规则
func (d *DB) GetActiveBlacklistRules() ([]models.FilterRule, error) {
	return d.getActiveFilterRules("blacklist")
}

// getActiveFilterRules 获取指定类型的所有启用规则
func (d *DB) getActiveFilterRules(ruleType string) ([]models.FilterRule, error) {
	rows, err := d.conn.Query(
		`SELECT id, rule_type, pattern, target_type, enable, priority, description
		 FROM filter_rules WHERE rule_type = ? AND enable = 1
		 ORDER BY priority DESC, id ASC`,
		ruleType,
	)
	if err != nil {
		return nil, fmt.Errorf("查询过滤规则失败: %w", err)
	}
	defer rows.Close()

	var rules []models.FilterRule
	for rows.Next() {
		var r models.FilterRule
		if err := rows.Scan(&r.ID, &r.RuleType, &r.Pattern, &r.TargetType,
			&r.Enable, &r.Priority, &r.Description); err != nil {
			return nil, fmt.Errorf("扫描过滤规则失败: %w", err)
		}
		rules = append(rules, r)
	}

	return rules, rows.Err()
}

// CreateFilterRule 创建新的过滤规则
func (d *DB) CreateFilterRule(rule *models.FilterRule) (int64, error) {
	result, err := d.conn.Exec(
		`INSERT INTO filter_rules (rule_type, pattern, target_type, enable, priority, description, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		rule.RuleType, rule.Pattern, rule.TargetType, rule.Enable,
		rule.Priority, rule.Description, time.Now(),
	)
	if err != nil {
		return 0, fmt.Errorf("创建过滤规则失败: %w", err)
	}
	return result.LastInsertId()
}

// UpdateFilterRule 更新过滤规则
func (d *DB) UpdateFilterRule(rule *models.FilterRule) error {
	_, err := d.conn.Exec(
		`UPDATE filter_rules SET pattern=?, target_type=?, enable=?, priority=?,
		 description=?, updated_at=? WHERE id=?`,
		rule.Pattern, rule.TargetType, rule.Enable, rule.Priority,
		rule.Description, time.Now(), rule.ID,
	)
	if err != nil {
		return fmt.Errorf("更新过滤规则失败: %w", err)
	}
	return nil
}

// DeleteFilterRule 删除过滤规则
func (d *DB) DeleteFilterRule(id int) error {
	_, err := d.conn.Exec("DELETE FROM filter_rules WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("删除过滤规则失败: %w", err)
	}
	return nil
}

// ──────── 用户管理 ────────

// GetUserByUsername 根据用户名获取用户信息
func (d *DB) GetUserByUsername(username string) (*models.User, error) {
	user := &models.User{}
	err := d.conn.QueryRow(
		`SELECT id, username, password_hash, is_admin, is_active, last_login
		 FROM users WHERE username = ? AND deleted_at IS NULL`,
		username,
	).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.IsAdmin,
		&user.IsActive, &user.LastLogin)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("用户不存在: %s", username)
	}
	if err != nil {
		return nil, fmt.Errorf("查询用户失败: %w", err)
	}
	return user, nil
}

// UpdateUserLastLogin 更新用户最后登录时间
func (d *DB) UpdateUserLastLogin(userID int, loginTime time.Time) error {
	_, err := d.conn.Exec(
		"UPDATE users SET last_login = ? WHERE id = ?",
		loginTime, userID,
	)
	return err
}

// createDefaultAdmin 创建默认管理员账户（如果 users 表为空）
func createDefaultAdmin(db *sql.DB) {
	var count int
	db.QueryRow("SELECT COUNT(*) FROM users WHERE deleted_at IS NULL").Scan(&count)
	if count > 0 {
		return // 已有用户，不创建默认账户
	}

	// 默认密码 "admin@1234" 的 bcrypt 哈希值
	// 生产环境请务必修改！
	defaultHash := "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
	db.Exec(
		`INSERT INTO users (username, password_hash, is_admin, is_active)
		 VALUES (?, ?, 1, 1)`,
		"admin", defaultHash,
	)
}

// ──────── 统计数据 ────────

// CountURLSources 统计 URL 源总数
func (d *DB) CountURLSources() int {
	var count int
	d.conn.QueryRow("SELECT COUNT(*) FROM live_sources WHERE deleted_at IS NULL").Scan(&count)
	return count
}

// CountPassedByStatus 统计指定状态的通过源数量
func (d *DB) CountPassedByStatus(status string) int {
	var count int
	d.conn.QueryRow(
		"SELECT COUNT(*) FROM url_sources_passed WHERE status = ? AND deleted_at IS NULL",
		status,
	).Scan(&count)
	return count
}

// CountEPGPrograms 统计 EPG 节目数量
func (d *DB) CountEPGPrograms() int {
	var count int
	d.conn.QueryRow("SELECT COUNT(*) FROM epg_programs").Scan(&count)
	return count
}

// GetLastTestTime 获取最后一次测试的时间
func (d *DB) GetLastTestTime() string {
	var lastTime sql.NullString
	d.conn.QueryRow(
		"SELECT MAX(last_download) FROM live_sources WHERE deleted_at IS NULL",
	).Scan(&lastTime)
	if lastTime.Valid {
		return lastTime.String
	}
	return "从未测试"
}

// ──────── 其他辅助方法 ────────

// GetAllCategories 获取所有分类
func (d *DB) GetAllCategories() ([]models.Category, error) {
	rows, err := d.conn.Query(
		"SELECT id, name, keywords FROM categories ORDER BY id")
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
	return categories, rows.Err()
}

// UpdateCategory 更新分类信息
func (d *DB) UpdateCategory(cat *models.Category) error {
	_, err := d.conn.Exec(
		"UPDATE categories SET name=?, keywords=? WHERE id=?",
		cat.Name, cat.Keywords, cat.ID,
	)
	return err
}

// DeleteCategory 删除分类
func (d *DB) DeleteCategory(id int) error {
	_, err := d.conn.Exec("DELETE FROM categories WHERE id=?", id)
	return err
}
// GetSourcesPage 分页查询订阅源列表。
func (d *DB) GetSourcesPage(page, limit int, status, search string) ([]models.LiveSource, int, error) {
	// 构建查询
	offset := (page - 1) * limit
	query := "SELECT id, name, location, location_type, enable, last_download, download_status, http_status, retry_count FROM live_sources WHERE deleted_at IS NULL"
	countQuery := "SELECT COUNT(*) FROM live_sources WHERE deleted_at IS NULL"
	args := []interface{}{}
	if status != "" {
		query += " AND download_status = ?"
		countQuery += " AND download_status = ?"
		args = append(args, status)
	}
	if search != "" {
		query += " AND (name LIKE ? OR location LIKE ?)"
		countQuery += " AND (name LIKE ? OR location LIKE ?)"
		s := "%" + search + "%"
		args = append(args, s, s)
	}
	query += " ORDER BY id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	var total int
	if err := d.conn.QueryRow(countQuery, args[:len(args)-2]...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := d.conn.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var sources []models.LiveSource
	for rows.Next() {
		var s models.LiveSource
		if err := rows.Scan(&s.ID, &s.Name, &s.Location, &s.LocationType, &s.Enable, &s.LastDownload, &s.DownloadStatus, &s.HTTPStatus, &s.RetryCount); err != nil {
			return nil, 0, err
		}
		sources = append(sources, s)
	}
	return sources, total, rows.Err()
}

// InsertSource 添加一个直播源。
func (d *DB) InsertSource(name, location string) (int64, error) {
	res, err := d.conn.Exec("INSERT INTO live_sources (name, location) VALUES (?, ?)", name, location)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SoftDeleteSource 软删除源（设置 deleted_at）。
func (d *DB) SoftDeleteSource(id int) error {
	_, err := d.conn.Exec("UPDATE live_sources SET deleted_at = datetime('now') WHERE id = ?", id)
	return err
}

// GetSourceByID 根据 ID 获取源，含 URL 字段别名。
func (d *DB) GetSourceByID(id int) (*models.URLSource, error) {
	// 这里简单实现，从 url_sources_passed 或 live_sources 查询
	var src models.URLSource
	err := d.conn.QueryRow("SELECT id, name, location FROM live_sources WHERE id = ?", id).Scan(&src.LiveSourceID, &src.Name, &src.URL)
	if err != nil {
		return nil, err
	}
	return &src, nil
}

// InsertURLSource 将一条 URL 源插入 url_sources_passed 表。
func (d *DB) InsertURLSource(src models.URLSource) error {
	_, err := d.conn.Exec(
		`INSERT INTO url_sources_passed (name, url, group_name, logo, status) VALUES (?, ?, ?, ?, ?)`,
		src.Name, src.URL, src.GroupTitle, src.TvgLogo, "active",
	)
	return err
}

// GetActiveLiveSources 获取所有激活的直播订阅源。
func (d *DB) GetActiveLiveSources() ([]models.LiveSource, error) {
	rows, err := d.conn.Query("SELECT id, name, location, location_type, enable FROM live_sources WHERE enable = 1 AND deleted_at IS NULL")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []models.LiveSource
	for rows.Next() {
		var s models.LiveSource
		if err := rows.Scan(&s.ID, &s.Name, &s.Location, &s.LocationType, &s.Enable); err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

// CreateCategory 创建新分类。
func (d *DB) CreateCategory(cat *models.Category) (int64, error) {
	res, err := d.conn.Exec("INSERT INTO categories (name, keywords) VALUES (?, ?)", cat.Name, cat.Keywords)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetM3UContent 获取生成的 M3U 内容（简单实现，实际由 generator 写入文件后读取）。
func (d *DB) GetM3UContent() (string, error) {
	// 简单示例：返回硬编码内容或从文件读取
	return "#EXTM3U\n", nil
}
