// Package db provides the SQLite data layer (schema migration + all data access).
//
// The database is the single source of truth for: app configuration, web users, sessions,
// audit logs, classification rules / dimensions / exclusions, channel-name mappings,
// category dictionary, per-source multi-dimension categories, and github download cache.
package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "embed"
	_ "modernc.org/sqlite"

	"live-source-manager-go/internal/auth"
	"live-source-manager-go/internal/logger"
	"live-source-manager-go/internal/types"
)

//go:embed seed_classification_rules.sql
var seedClassificationRulesSQL string

// Open opens (and migrates) the SQLite database at the given path.
func Open(dbPath string) (*sql.DB, error) {
	if dir := filepath.Dir(dbPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", dbPath)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1) // SQLite + WAL: single writer avoids lock contention
	if err := conn.Ping(); err != nil {
		return nil, err
	}
	if err := Migrate(conn); err != nil {
		return nil, err
	}
	return conn, nil
}

// Migrate creates all tables if missing.
func Migrate(conn *sql.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'viewer',
    display_name TEXT DEFAULT '',
    is_active INTEGER DEFAULT 1,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS audit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER,
    username TEXT NOT NULL,
    action TEXT NOT NULL,
    target TEXT DEFAULT '',
    detail TEXT DEFAULT '',
    ip_address TEXT DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_audit_created_at ON audit_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_logs_action ON audit_logs(action);
CREATE INDEX IF NOT EXISTS idx_audit_username ON audit_logs(username);
CREATE INDEX IF NOT EXISTS idx_audit_action_created ON audit_logs(action, created_at);

CREATE TABLE IF NOT EXISTS app_config (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    username TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'viewer',
    created_at REAL NOT NULL,
    last_active REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);

CREATE TABLE IF NOT EXISTS classification_dimensions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    dim_key TEXT UNIQUE NOT NULL,
    dim_name TEXT NOT NULL,
    sort_order INTEGER DEFAULT 0,
    is_active INTEGER DEFAULT 1,
    created_at TEXT DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS classification_rules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    rule_type TEXT NOT NULL,
    name TEXT NOT NULL,
    keywords TEXT NOT NULL DEFAULT '[]',
    priority INTEGER DEFAULT 100,
    sort_order INTEGER DEFAULT 0,
    is_active INTEGER DEFAULT 1,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS province_exclusion_map (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    province_keyword TEXT NOT NULL,
    excluded_keyword TEXT NOT NULL,
    note TEXT DEFAULT '',
    created_at TEXT DEFAULT (datetime('now')),
    UNIQUE(province_keyword, excluded_keyword)
);
CREATE TABLE IF NOT EXISTS stream_source_categories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id TEXT NOT NULL,
    dim_key TEXT NOT NULL,
    dim_value TEXT NOT NULL DEFAULT '未知',
    is_manual INTEGER DEFAULT 0,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    UNIQUE(source_id, dim_key)
);
CREATE INDEX IF NOT EXISTS idx_source_categories_source ON stream_source_categories(source_id);
CREATE TABLE IF NOT EXISTS channel_name_mapping (
    channel_name TEXT PRIMARY KEY,
    content TEXT NOT NULL DEFAULT '其他频道',
    region TEXT NOT NULL DEFAULT '未知',
    language TEXT NOT NULL DEFAULT '未知',
    quality TEXT NOT NULL DEFAULT '高清',
    media_type TEXT NOT NULL DEFAULT '电视节目',
    genre TEXT NOT NULL DEFAULT '综合',
    is_manual INTEGER DEFAULT 1,
    created_at TEXT,
    updated_at TEXT
);
CREATE TABLE IF NOT EXISTS category_dictionary (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    dimension TEXT NOT NULL,
    value TEXT NOT NULL,
    label TEXT NOT NULL DEFAULT '',
    sort_order INTEGER DEFAULT 0,
    UNIQUE(dimension, value)
);
CREATE INDEX IF NOT EXISTS idx_category_dict_dim ON category_dictionary(dimension);
CREATE TABLE IF NOT EXISTS github_download_cache (
    repo_key TEXT NOT NULL,
    filename TEXT NOT NULL,
    file_size INTEGER DEFAULT 0,
    downloaded_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (repo_key, filename)
);
CREATE INDEX IF NOT EXISTS idx_github_dl_repo ON github_download_cache(repo_key);
`
	if _, err := conn.Exec(schema); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	// Default dimensions (only when empty).
	var cnt int
	_ = conn.QueryRow("SELECT COUNT(*) FROM classification_dimensions").Scan(&cnt)
	if cnt == 0 {
		defaultDims := [][3]any{
			{"content", "内容分类", 1}, {"region", "地域", 2}, {"language", "语言", 3},
			{"quality", "清晰度", 4}, {"media_type", "媒体类型", 5}, {"genre", "节目类型", 6},
		}
		for _, d := range defaultDims {
			if _, err := conn.Exec(
				"INSERT INTO classification_dimensions (dim_key, dim_name, sort_order) VALUES (?,?,?)",
				d[0], d[1], d[2]); err != nil {
				return err
			}
		}
		logger.L().Info("默认分类维度已初始化")
	}
	// 分类规则 + 省份排除映射种子（仅在表为空时执行，幂等安全）。
	var ruleCnt int
	_ = conn.QueryRow("SELECT COUNT(*) FROM classification_rules").Scan(&ruleCnt)
	if ruleCnt == 0 && strings.TrimSpace(seedClassificationRulesSQL) != "" {
		if err := execSeedSQL(conn, seedClassificationRulesSQL); err != nil {
			logger.L().Warning("分类规则种子执行失败: %v", err)
		} else {
			logger.L().Info("默认分类规则已初始化")
		}
	}
	// EPG（电子节目单）三表 + 频道映射扩列 + 预置源种子。
	if err := migrateEPG(conn); err != nil {
		return err
	}
	return nil
}

// stripSQLComments 移除 SQL 行注释（-- 到行尾），但保留单引号字符串内的 '--'。
// 种子文件每个 INSERT 块前都有 '--' 注释头，因此不能简单地用 HasPrefix("--") 整体跳过。
func stripSQLComments(s string) string {
	var b strings.Builder
	runes := []rune(s)
	inStr := false
	i := 0
	for i < len(runes) {
		c := runes[i]
		if inStr {
			b.WriteRune(c)
			if c == '\'' {
				// 处理转义引号 ''
				if i+1 < len(runes) && runes[i+1] == '\'' {
					b.WriteRune('\'')
					i += 2
					continue
				}
				inStr = false
			}
			i++
			continue
		}
		if c == '\'' {
			inStr = true
			b.WriteRune(c)
			i++
			continue
		}
		if c == '-' && i+1 < len(runes) && runes[i+1] == '-' {
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
			continue
		}
		b.WriteRune(c)
		i++
	}
	return b.String()
}

// execSeedSQL 先剥离 SQL 注释，再按分号拆分并执行（每条语句独立执行）。
// 种子文件中 JSON 关键字不含分号，可安全按 ';' 拆分；每个块前的 '--' 注释已被剥离。
func execSeedSQL(conn *sql.DB, sqlText string) error {
	clean := stripSQLComments(sqlText)
	for _, raw := range strings.Split(clean, ";") {
		stmt := strings.TrimSpace(raw)
		if stmt == "" {
			continue
		}
		if _, err := conn.Exec(stmt); err != nil {
			return fmt.Errorf("seed statement failed: %w", err)
		}
	}
	return nil
}

// ── app_config ────────────────────────────────────────────────────────────

// GetAppConfig returns the raw stored value for key, or "" if absent.
func GetAppConfig(conn *sql.DB, key string) (string, error) {
	var v string
	err := conn.QueryRow("SELECT value FROM app_config WHERE key = ?", key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// SetAppConfig upserts key = value.
func SetAppConfig(conn *sql.DB, key, value string) error {
	_, err := conn.Exec(
		"INSERT INTO app_config (key, value, updated_at) VALUES (?, ?, datetime('now')) "+
			"ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=datetime('now')",
		key, value)
	return err
}

// GetAllConfig returns section -> key -> value (parsed from "Section.key" keys).
func GetAllConfig(conn *sql.DB) map[string]map[string]string {
	out := map[string]map[string]string{}
	rows, err := conn.Query("SELECT key, value FROM app_config")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue
		}
		idx := strings.Index(k, ".")
		if idx < 0 {
			continue
		}
		section, key := k[:idx], k[idx+1:]
		if out[section] == nil {
			out[section] = map[string]string{}
		}
		out[section][key] = v
	}
	return out
}

// SeedDefaults inserts every default key that is not already present. Returns count inserted.
func SeedDefaults(conn *sql.DB, defaults map[string]string) (int, error) {
	count := 0
	for k, v := range defaults {
		var exists int
		_ = conn.QueryRow("SELECT COUNT(*) FROM app_config WHERE key = ?", k).Scan(&exists)
		if exists > 0 {
			continue
		}
		if err := SetAppConfig(conn, k, v); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// FillMissingDefaults ensures all default keys exist (upserts missing only).
func FillMissingDefaults(conn *sql.DB, defaults map[string]string) error {
	_, err := SeedDefaults(conn, defaults)
	return err
}

// ── users ─────────────────────────────────────────────────────────────────

// GetUserByUsername returns the user or nil.
func GetUserByUsername(conn *sql.DB, username string) (*types.User, error) {
	u := &types.User{}
	var role string
	var isActive int
	err := conn.QueryRow(
		"SELECT id, username, password_hash, role, display_name, is_active, created_at, updated_at "+
			"FROM users WHERE username = ?", username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &role, &u.DisplayName, &isActive, &u.CreatedAt, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.Role = role
	u.IsActive = isActive != 0
	return u, nil
}

// GetUserByID returns the user or nil.
func GetUserByID(conn *sql.DB, id int) (*types.User, error) {
	u := &types.User{}
	var role string
	var isActive int
	err := conn.QueryRow(
		"SELECT id, username, password_hash, role, display_name, is_active, created_at, updated_at "+
			"FROM users WHERE id = ?", id).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &role, &u.DisplayName, &isActive, &u.CreatedAt, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.Role = role
	u.IsActive = isActive != 0
	return u, nil
}

// VerifyPassword returns the user if username+password match, else nil.
func VerifyPassword(conn *sql.DB, username, password string) (*types.User, error) {
	u, err := GetUserByUsername(conn, username)
	if err != nil {
		return nil, err
	}
	if u == nil || !u.IsActive {
		return nil, nil
	}
	if !auth.CheckPasswordHash(password, u.PasswordHash) {
		return nil, nil
	}
	return u, nil
}

// CreateUser inserts a new user. Returns the new user id.
func CreateUser(conn *sql.DB, username, password, role, displayName string) (int, error) {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return 0, err
	}
	if role == "" {
		role = "viewer"
	}
	res, err := conn.Exec(
		"INSERT INTO users (username, password_hash, role, display_name) VALUES (?, ?, ?, ?)",
		username, hash, role, displayName)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return int(id), nil
}

// UpdateUserPassword changes a user's password.
func UpdateUserPassword(conn *sql.DB, userID int, newPassword string) error {
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}
	_, err = conn.Exec("UPDATE users SET password_hash=?, updated_at=datetime('now') WHERE id=?", hash, userID)
	return err
}

// ListUsers returns all users.
func ListUsers(conn *sql.DB) ([]types.User, error) {
	rows, err := conn.Query("SELECT id, username, role, display_name, is_active, created_at, updated_at FROM users ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.User
	for rows.Next() {
		var u types.User
		var role string
		var isActive int
		if err := rows.Scan(&u.ID, &u.Username, &role, &u.DisplayName, &isActive, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		u.Role = role
		u.IsActive = isActive != 0
		out = append(out, u)
	}
	return out, nil
}

// DeleteUser removes a user by id (sessions cascade).
func DeleteUser(conn *sql.DB, userID int) error {
	_, err := conn.Exec("DELETE FROM users WHERE id = ?", userID)
	return err
}

// SetUserActive toggles a user's active flag.
func SetUserActive(conn *sql.DB, userID int, active bool) error {
	v := 0
	if active {
		v = 1
	}
	_, err := conn.Exec("UPDATE users SET is_active=?, updated_at=datetime('now') WHERE id=?", v, userID)
	return err
}

// ── sessions ──────────────────────────────────────────────────────────────

// CreateSession inserts a new session row.
func CreateSession(conn *sql.DB, id string, userID int, username, role string, ttl float64) error {
	now := float64(time.Now().Unix())
	_, err := conn.Exec(
		"INSERT INTO sessions (id, user_id, username, role, created_at, last_active) VALUES (?,?,?,?,?,?)",
		id, userID, username, role, now, now)
	return err
}

// GetSession returns the session if valid (within idle + absolute TTL), else nil.
func GetSession(conn *sql.DB, id string, idleTimeout, sessionTTL int) (*types.User, error) {
	var userID int
	var username, role string
	var createdAt, lastActive float64
	err := conn.QueryRow(
		"SELECT user_id, username, role, created_at, last_active FROM sessions WHERE id = ?", id).
		Scan(&userID, &username, &role, &createdAt, &lastActive)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	now := float64(time.Now().Unix())
	if now-lastActive > float64(idleTimeout) || now-createdAt > float64(sessionTTL) {
		_ = DeleteSession(conn, id)
		return nil, nil
	}
	// Refresh last_active.
	_, _ = conn.Exec("UPDATE sessions SET last_active = ? WHERE id = ?", now, id)
	return &types.User{ID: userID, Username: username, Role: role}, nil
}

// DeleteSession removes a session.
func DeleteSession(conn *sql.DB, id string) error {
	_, err := conn.Exec("DELETE FROM sessions WHERE id = ?", id)
	return err
}

// DeleteExpiredSessions purges sessions past their TTL.
func DeleteExpiredSessions(conn *sql.DB, idleTimeout, sessionTTL int) (int64, error) {
	now := float64(time.Now().Unix())
	res, err := conn.Exec(
		"DELETE FROM sessions WHERE (? - last_active) > ? OR (? - created_at) > ?",
		now, idleTimeout, now, sessionTTL)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ── audit ─────────────────────────────────────────────────────────────────

// AddAuditLog records an audit entry.
func AddAuditLog(conn *sql.DB, userID int, username, action, target, detail, ip string) error {
	_, err := conn.Exec(
		"INSERT INTO audit_logs (user_id, username, action, target, detail, ip_address) VALUES (?,?,?,?,?,?)",
		userID, username, action, target, detail, ip)
	return err
}

// GetAuditLogs returns audit entries (paginated, newest first).
func GetAuditLogs(conn *sql.DB, limit, offset int, action, username string) ([]types.AuditLogEntry, error) {
	q := "SELECT id, user_id, username, action, target, detail, ip_address, created_at FROM audit_logs WHERE 1=1"
	args := []any{}
	if action != "" {
		q += " AND action = ?"
		args = append(args, action)
	}
	if username != "" {
		q += " AND username = ?"
		args = append(args, username)
	}
	q += " ORDER BY id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := conn.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.AuditLogEntry
	for rows.Next() {
		var e types.AuditLogEntry
		if err := rows.Scan(&e.ID, &e.UserID, &e.Username, &e.Action, &e.Target, &e.Detail, &e.IPAddress, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// GetAuditActions returns the distinct action names (for filter dropdowns).
func GetAuditActions(conn *sql.DB) ([]string, error) {
	rows, err := conn.Query("SELECT DISTINCT action FROM audit_logs ORDER BY action")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// ── classification rules / dimensions / exclusions ─────────────────────────

// ListRules returns all classification rules ordered by sort_order.
func ListRules(conn *sql.DB) ([]types.ClassificationRule, error) {
	rows, err := conn.Query(
		"SELECT id, rule_type, name, keywords, priority, sort_order, is_active, created_at, updated_at " +
			"FROM classification_rules ORDER BY sort_order, priority, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.ClassificationRule
	for rows.Next() {
		var r types.ClassificationRule
		var keywords string
		var isActive int
		if err := rows.Scan(&r.ID, &r.RuleType, &r.Name, &keywords, &r.Priority, &r.SortOrder, &isActive, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(keywords), &r.Keywords)
		r.IsActive = isActive != 0
		out = append(out, r)
	}
	return out, nil
}

// GetActiveRules returns only active rules ordered by priority.
func GetActiveRules(conn *sql.DB) ([]types.ClassificationRule, error) {
	rows, err := conn.Query(
		"SELECT id, rule_type, name, keywords, priority, sort_order, is_active, created_at, updated_at " +
			"FROM classification_rules WHERE is_active=1 ORDER BY priority, sort_order, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.ClassificationRule
	for rows.Next() {
		var r types.ClassificationRule
		var keywords string
		var isActive int
		if err := rows.Scan(&r.ID, &r.RuleType, &r.Name, &keywords, &r.Priority, &r.SortOrder, &isActive, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(keywords), &r.Keywords)
		r.IsActive = isActive != 0
		out = append(out, r)
	}
	return out, nil
}

// CreateRule inserts a rule.
func CreateRule(conn *sql.DB, ruleType, name string, keywords []string, priority, sortOrder int, isActive bool) (int, error) {
	kb, _ := json.Marshal(keywords)
	active := 0
	if isActive {
		active = 1
	}
	res, err := conn.Exec(
		"INSERT INTO classification_rules (rule_type, name, keywords, priority, sort_order, is_active) VALUES (?,?,?,?,?,?)",
		ruleType, name, string(kb), priority, sortOrder, active)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return int(id), nil
}

// UpdateRule updates a rule by id.
func UpdateRule(conn *sql.DB, id int, name string, keywords []string, priority, sortOrder int, isActive bool) error {
	kb, _ := json.Marshal(keywords)
	active := 0
	if isActive {
		active = 1
	}
	_, err := conn.Exec(
		"UPDATE classification_rules SET name=?, keywords=?, priority=?, sort_order=?, is_active=?, updated_at=datetime('now') WHERE id=?",
		name, string(kb), priority, sortOrder, active, id)
	return err
}

// DeleteRule deletes a rule by id.
func DeleteRule(conn *sql.DB, id int) error {
	_, err := conn.Exec("DELETE FROM classification_rules WHERE id = ?", id)
	return err
}

// BatchOrder reorders rules by id list (sets sort_order = index).
func BatchOrder(conn *sql.DB, ids []int) error {
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := tx.Exec("UPDATE classification_rules SET sort_order=? WHERE id=?", i, id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// ListDimensions returns classification dimensions ordered by sort_order.
func ListDimensions(conn *sql.DB) ([]types.ClassificationDimension, error) {
	rows, err := conn.Query("SELECT dim_key, dim_name, sort_order, is_active FROM classification_dimensions ORDER BY sort_order, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.ClassificationDimension
	for rows.Next() {
		var d types.ClassificationDimension
		var isActive int
		if err := rows.Scan(&d.DimKey, &d.DimName, &d.SortOrder, &isActive); err != nil {
			return nil, err
		}
		d.IsActive = isActive != 0
		out = append(out, d)
	}
	return out, nil
}

// CreateDimension inserts a dimension.
func CreateDimension(conn *sql.DB, dimKey, dimName string, sortOrder int) error {
	_, err := conn.Exec(
		"INSERT INTO classification_dimensions (dim_key, dim_name, sort_order) VALUES (?,?,?) "+
			"ON CONFLICT(dim_key) DO UPDATE SET dim_name=excluded.dim_name, sort_order=excluded.sort_order",
		dimKey, dimName, sortOrder)
	return err
}

// DeleteDimension removes a dimension (and its dictionary values).
func DeleteDimension(conn *sql.DB, dimKey string) error {
	if _, err := conn.Exec("DELETE FROM category_dictionary WHERE dimension = ?", dimKey); err != nil {
		return err
	}
	_, err := conn.Exec("DELETE FROM classification_dimensions WHERE dim_key = ?", dimKey)
	return err
}

// ListExclusions returns province exclusion mappings.
func ListExclusions(conn *sql.DB) ([]types.ProvinceExclusion, error) {
	rows, err := conn.Query("SELECT id, province_keyword, excluded_keyword, note, created_at FROM province_exclusion_map ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.ProvinceExclusion
	for rows.Next() {
		var e types.ProvinceExclusion
		if err := rows.Scan(&e.ID, &e.ProvinceKeyword, &e.ExcludedKeyword, &e.Note, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// CreateExclusion inserts a province exclusion (ignores duplicates).
func CreateExclusion(conn *sql.DB, province, excluded, note string) (int, error) {
	res, err := conn.Exec(
		"INSERT INTO province_exclusion_map (province_keyword, excluded_keyword, note) VALUES (?,?,?) "+
			"ON CONFLICT(province_keyword, excluded_keyword) DO UPDATE SET note=excluded.note",
		province, excluded, note)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return int(id), nil
}

// DeleteExclusion removes an exclusion by id.
func DeleteExclusion(conn *sql.DB, id int) error {
	_, err := conn.Exec("DELETE FROM province_exclusion_map WHERE id = ?", id)
	return err
}

// ── channel name mapping ───────────────────────────────────────────────────

// GetChannelMapping returns the mapping for a channel name, or nil.
func GetChannelMapping(conn *sql.DB, channelName string) (*types.ChannelMapping, error) {
	var m types.ChannelMapping
	var isManual int
	err := conn.QueryRow(
		"SELECT channel_name, content, region, language, quality, media_type, genre, is_manual, created_at, updated_at "+
			"FROM channel_name_mapping WHERE channel_name = ?", channelName).
		Scan(&m.ChannelName, &m.Content, &m.Region, &m.Language, &m.Quality, &m.MediaType, &m.Genre, &isManual, &m.CreatedAt, &m.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m.IsManual = isManual != 0
	m.Categories = map[string]string{
		"content": m.Content, "region": m.Region, "language": m.Language,
		"quality": m.Quality, "media_type": m.MediaType, "genre": m.Genre,
	}
	return &m, nil
}

// SaveChannelMapping upserts a channel mapping.
func SaveChannelMapping(conn *sql.DB, channelName string, cats map[string]string) error {
	now := time.Now().Format("2006-01-02 15:04:05")
	content := cats["content"]
	region := cats["region"]
	language := cats["language"]
	quality := cats["quality"]
	mediaType := cats["media_type"]
	genre := cats["genre"]
	_, err := conn.Exec(
		`INSERT INTO channel_name_mapping
         (channel_name, content, region, language, quality, media_type, genre, is_manual, created_at, updated_at)
         VALUES (?,?,?,?,?,?,?,1,?,?)
         ON CONFLICT(channel_name) DO UPDATE SET
           content=excluded.content, region=excluded.region, language=excluded.language,
           quality=excluded.quality, media_type=excluded.media_type, genre=excluded.genre,
           is_manual=1, updated_at=excluded.updated_at`,
		channelName, content, region, language, quality, mediaType, genre, now, now)
	return err
}

// DeleteChannelMapping removes a channel mapping.
func DeleteChannelMapping(conn *sql.DB, channelName string) error {
	_, err := conn.Exec("DELETE FROM channel_name_mapping WHERE channel_name = ?", channelName)
	return err
}

// ListChannelMappings returns all channel mappings.
func ListChannelMappings(conn *sql.DB, limit, offset int, query string) ([]types.ChannelMapping, error) {
	q := "SELECT channel_name, content, region, language, quality, media_type, genre FROM channel_name_mapping"
	args := []any{}
	if query != "" {
		q += " WHERE channel_name LIKE ?"
		args = append(args, "%"+query+"%")
	}
	q += " ORDER BY channel_name LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := conn.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.ChannelMapping
	for rows.Next() {
		var m types.ChannelMapping
		if err := rows.Scan(&m.ChannelName, &m.Content, &m.Region, &m.Language, &m.Quality, &m.MediaType, &m.Genre); err != nil {
			return nil, err
		}
		m.Categories = map[string]string{
			"content": m.Content, "region": m.Region, "language": m.Language,
			"quality": m.Quality, "media_type": m.MediaType, "genre": m.Genre,
		}
		out = append(out, m)
	}
	return out, nil
}

// ── category dictionary ────────────────────────────────────────────────────

// GetCategoryDictionary returns dimension -> list of values.
func GetCategoryDictionary(conn *sql.DB) (map[string][]types.CategoryDictValue, error) {
	rows, err := conn.Query("SELECT id, dimension, value, label, sort_order FROM category_dictionary ORDER BY dimension, sort_order, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]types.CategoryDictValue{}
	for rows.Next() {
		var v types.CategoryDictValue
		if err := rows.Scan(&v.ID, &v.Dimension, &v.Value, &v.Label, &v.SortOrder); err != nil {
			return nil, err
		}
		out[v.Dimension] = append(out[v.Dimension], v)
	}
	return out, nil
}

// AddCategoryValue inserts a category dictionary value (ignores duplicates).
func AddCategoryValue(conn *sql.DB, dimension, value, label string, sortOrder int) error {
	_, err := conn.Exec(
		"INSERT INTO category_dictionary (dimension, value, label, sort_order) VALUES (?,?,?,?) "+
			"ON CONFLICT(dimension, value) DO UPDATE SET label=excluded.label, sort_order=excluded.sort_order",
		dimension, value, label, sortOrder)
	return err
}

// DeleteCategoryValue removes a category dictionary value.
func DeleteCategoryValue(conn *sql.DB, dimension, value string) error {
	_, err := conn.Exec("DELETE FROM category_dictionary WHERE dimension = ? AND value = ?", dimension, value)
	return err
}

// ── source categories (per-channel, multi-dimension) ──────────────────────

// GetSourceCategories returns dimension -> value for a source id (text hash).
func GetSourceCategories(conn *sql.DB, sourceID string) (map[string]string, error) {
	rows, err := conn.Query("SELECT dim_key, dim_value FROM stream_source_categories WHERE source_id = ?", sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}

// SaveSourceCategories replaces all categories for a source id.
func SaveSourceCategories(conn *sql.DB, sourceID string, cats map[string]string) error {
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM stream_source_categories WHERE source_id = ?", sourceID); err != nil {
		_ = tx.Rollback()
		return err
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	for k, v := range cats {
		if _, err := tx.Exec(
			"INSERT INTO stream_source_categories (source_id, dim_key, dim_value, is_manual, created_at, updated_at) VALUES (?,?,?,1,?,?)",
			sourceID, k, v, now, now); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// UpdateSourceCategory sets one dimension value for a source id.
func UpdateSourceCategory(conn *sql.DB, sourceID, dimKey, dimValue string) error {
	now := time.Now().Format("2006-01-02 15:04:05")
	_, err := conn.Exec(
		`INSERT INTO stream_source_categories (source_id, dim_key, dim_value, is_manual, created_at, updated_at)
         VALUES (?,?,?,1,?,?)
         ON CONFLICT(source_id, dim_key) DO UPDATE SET dim_value=excluded.dim_value, is_manual=1, updated_at=excluded.updated_at`,
		sourceID, dimKey, dimValue, now, now)
	return err
}

// DeleteSourceCategories removes all categories for a source id.
func DeleteSourceCategories(conn *sql.DB, sourceID string) error {
	_, err := conn.Exec("DELETE FROM stream_source_categories WHERE source_id = ?", sourceID)
	return err
}

// ── github download cache ──────────────────────────────────────────────────

// GetGitHubDownloadCache returns cached files for a repo.
func GetGitHubDownloadCache(conn *sql.DB, repoKey string) ([]types.GitHubCacheEntry, error) {
	rows, err := conn.Query("SELECT repo_key, filename, file_size, downloaded_at FROM github_download_cache WHERE repo_key = ?", repoKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.GitHubCacheEntry
	for rows.Next() {
		var e types.GitHubCacheEntry
		if err := rows.Scan(&e.RepoKey, &e.Filename, &e.FileSize, &e.DownloadedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// SaveGitHubDownloadCache records a downloaded github file.
func SaveGitHubDownloadCache(conn *sql.DB, repoKey, filename string, size int64) error {
	_, err := conn.Exec(
		"INSERT INTO github_download_cache (repo_key, filename, file_size) VALUES (?,?,?) "+
			"ON CONFLICT(repo_key, filename) DO UPDATE SET file_size=excluded.file_size, downloaded_at=CURRENT_TIMESTAMP",
		repoKey, filename, size)
	return err
}
