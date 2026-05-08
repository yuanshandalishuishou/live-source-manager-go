package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/yuanshandalishuishou/live-source-manager-go/internal/models"
)

// DB 封装 sql.DB，提供业务查询方法
type DB struct {
	*sql.DB
}

// NewDB 返回包装后的数据库连接
func NewDB(database *sql.DB) *DB {
	return &DB{DB: database}
}

// ---------- 用户相关 ----------

// GetUserByUsername 根据用户名获取用户
func (d *DB) GetUserByUsername(username string) (*models.User, error) {
	row := d.QueryRow("SELECT id, username, password_hash, is_admin, is_active, last_login FROM users WHERE username = ?", username)
	var u models.User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.IsAdmin, &u.IsActive, &u.LastLogin)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// UpdateUserLastLogin 更新最后登录时间
func (d *DB) UpdateUserLastLogin(userID int, t time.Time) error {
	_, err := d.Exec("UPDATE users SET last_login = ? WHERE id = ?", t, userID)
	return err
}

// GetAllUsers 返回所有用户
func (d *DB) GetAllUsers() ([]models.User, error) {
	rows, err := d.Query("SELECT id, username, password_hash, is_admin, is_active, last_login FROM users ORDER BY id")
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

// InsertUser 插入新用户，返回 ID
func (d *DB) InsertUser(u *models.User) (int, error) {
	res, err := d.Exec("INSERT INTO users (username, password_hash, is_admin, is_active) VALUES (?, ?, ?, ?)",
		u.Username, u.PasswordHash, u.IsAdmin, u.IsActive)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return int(id), nil
}

// UpdateUser 更新用户信息
func (d *DB) UpdateUser(u *models.User) error {
	_, err := d.Exec("UPDATE users SET username=?, password_hash=?, is_admin=?, is_active=? WHERE id=?",
		u.Username, u.PasswordHash, u.IsAdmin, u.IsActive, u.ID)
	return err
}

// DeleteUser 删除用户
func (d *DB) DeleteUser(id int) error {
	_, err := d.Exec("DELETE FROM users WHERE id = ?", id)
	return err
}

// ---------- 系统配置 ----------

// GetConfigValue 获取单个配置值
func (d *DB) GetConfigValue(group, key string) (string, error) {
	var value string
	err := d.QueryRow("SELECT value FROM sys_config WHERE group_name = ? AND key = ?", group, key).Scan(&value)
	return value, err
}

// GetAllConfigs 获取所有配置项
func (d *DB) GetAllConfigs() ([]models.SysConfig, error) {
	rows, err := d.Query("SELECT id, group_name, key, value, value_type, description, version FROM sys_config ORDER BY group_name, key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var configs []models.SysConfig
	for rows.Next() {
		var c models.SysConfig
		if err := rows.Scan(&c.ID, &c.GroupName, &c.Key, &c.Value, &c.ValueType, &c.Description, &c.Version); err != nil {
			return nil, err
		}
		configs = append(configs, c)
	}
	return configs, nil
}

// UpdateConfigValue 更新配置值，同时递增版本号
func (d *DB) UpdateConfigValue(group, key, value string) error {
	_, err := d.Exec(`UPDATE sys_config SET value = ?, updated_at = CURRENT_TIMESTAMP, version = version + 1 WHERE group_name = ? AND key = ?`,
		value, group, key)
	return err
}

// InsertConfigHistory 记录配置变更历史
func (d *DB) InsertConfigHistory(key, oldValue, newValue string) error {
	_, err := d.Exec("INSERT INTO sys_config_history (config_key, old_value, new_value) VALUES (?, ?, ?)",
		key, oldValue, newValue)
	return err
}

// GetFilterVersion 获取过滤器规则版本号（用于热重载判断）
func (d *DB) GetFilterVersion() (int64, error) {
	var version int64
	err := d.QueryRow("SELECT COALESCE(MAX(version), 0) FROM (SELECT MAX(updated_at) as version FROM whitelist UNION SELECT MAX(updated_at) FROM blacklist)").Scan(&version)
	// 简化实现：可用触发器或每次更新规则时递增全局版本，此处用记录数模拟
	var count int
	d.QueryRow("SELECT COUNT(*) FROM whitelist").Scan(&count)
	version = int64(count)
	d.QueryRow("SELECT COUNT(*) FROM blacklist").Scan(&count)
	version += int64(count)
	return version, nil
}

// ---------- 直播源订阅 ----------

// GetEnabledLiveSources 获取启用的订阅源
func (d *DB) GetEnabledLiveSources(locationType string) ([]models.LiveSource, error) {
	query := "SELECT id, name, location, location_type, enable, last_download, download_status, http_status, retry_count FROM live_sources WHERE enable = 1"
	if locationType != "" {
		query += " AND location_type = ?"
	}
	rows, err := d.Query(query, locationType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []models.LiveSource
	for rows.Next() {
		var s models.LiveSource
		if err := rows.Scan(&s.ID, &s.Name, &s.Location, &s.LocationType, &s.Enable, &s.LastDownload, &s.DownloadStatus, &s.HTTPStatus, &s.RetryCount); err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, nil
}

// GetAllLiveSources 返回所有订阅源
func (d *DB) GetAllLiveSources() ([]models.LiveSource, error) {
	rows, err := d.Query("SELECT id, name, location, location_type, enable, last_download, download_status FROM live_sources ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []models.LiveSource
	for rows.Next() {
		var s models.LiveSource
		if err := rows.Scan(&s.ID, &s.Name, &s.Location, &s.LocationType, &s.Enable, &s.LastDownload, &s.DownloadStatus); err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, nil
}

// InsertLiveSource 插入新的订阅源
func (d *DB) InsertLiveSource(s *models.LiveSource) (int, error) {
	res, err := d.Exec("INSERT INTO live_sources (name, location, location_type, enable) VALUES (?, ?, ?, ?)",
		s.Name, s.Location, s.LocationType, s.Enable)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return int(id), nil
}

// UpdateLiveSource 更新订阅源
func (d *DB) UpdateLiveSource(s *models.LiveSource) error {
	_, err := d.Exec("UPDATE live_sources SET name=?, location=?, location_type=?, enable=?, updated_at=CURRENT_TIMESTAMP WHERE id=?",
		s.Name, s.Location, s.LocationType, s.Enable, s.ID)
	return err
}

// DeleteLiveSource 删除订阅源
func (d *DB) DeleteLiveSource(id int) error {
	_, err := d.Exec("DELETE FROM live_sources WHERE id = ?", id)
	return err
}

// UpdateDownloadStatus 更新下载状态
func (d *DB) UpdateDownloadStatus(sourceID int, status string, httpStatus int) error {
	_, err := d.Exec(`UPDATE live_sources SET download_status=?, http_status=?, last_download=CURRENT_TIMESTAMP, retry_count=0 WHERE id=?`,
		status, httpStatus, sourceID)
	return err
}

// ---------- 源条目 (url_sources) ----------

// BatchInsertURLSources 批量插入 url_sources，去重，返回实际插入数量
func (d *DB) BatchInsertURLSources(liveSourceID int, entries []models.URLSource) (int, error) {
	tx, err := d.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO url_sources (live_source_id, url, name, tvg_id, tvg_logo, group_title, catchup, catchup_days, user_agent, raw_attributes, source_type) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	inserted := 0
	for _, e := range entries {
		res, err := stmt.Exec(liveSourceID, e.URL, e.Name, e.TvgID, e.TvgLogo, e.GroupTitle, e.Catchup, e.CatchupDays, e.UserAgent, e.RawAttributes, e.SourceType)
		if err != nil {
			continue
		}
		aff, _ := res.RowsAffected()
		if aff > 0 {
			inserted++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return inserted, nil
}

// GetUnprocessedSources 获取未处理的源（用于别名匹配）
func (d *DB) GetUnprocessedSources() ([]models.URLSource, error) {
	rows, err := d.Query("SELECT id, name FROM url_sources WHERE name IS NOT NULL")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []models.URLSource
	for rows.Next() {
		var s models.URLSource
		if err := rows.Scan(&s.ID, &s.Name); err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, nil
}

// BatchUpdateNames 批量更新频道名称（别名替换后）
func (d *DB) BatchUpdateNames(sources []models.URLSource) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare("UPDATE url_sources SET name = ? WHERE id = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, s := range sources {
		if _, err := stmt.Exec(s.Name, s.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CountURLSources 统计 url_sources 总数
func (d *DB) CountURLSources() int {
	var count int
	d.QueryRow("SELECT COUNT(*) FROM url_sources").Scan(&count)
	return count
}

// GetPassedSources 分页获取通过测试的源
func (d *DB) GetPassedSources(page, size int, search, status string) ([]models.PassedSource, int, error) {
	// 构建查询
	baseQuery := `FROM url_sources_passed pass 
		JOIN url_sources us ON pass.source_id = us.id
		LEFT JOIN source_categories sc ON sc.source_id = pass.id
		LEFT JOIN categories c ON c.id = sc.category_id`
	var conditions []string
	var args []interface{}

	if status != "" {
		conditions = append(conditions, "pass.status = ?")
		args = append(args, status)
	}
	if search != "" {
		conditions = append(conditions, "(us.name LIKE ? OR us.url LIKE ?)")
		args = append(args, "%"+search+"%", "%"+search+"%")
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = " WHERE " + strings.Join(conditions, " AND ")
	}

	var total int
	countQuery := "SELECT COUNT(DISTINCT pass.id) " + baseQuery + whereClause
	err := d.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * size
	dataQuery := fmt.Sprintf(`SELECT pass.id, pass.source_id, us.url, us.name, us.tvg_id, us.tvg_logo, us.group_title, 
		pass.status, pass.response_time_ms, pass.resolution, pass.bitrate, pass.last_checked, pass.location, pass.isp
		%s %s ORDER BY pass.last_checked DESC LIMIT ? OFFSET ?`, baseQuery, whereClause)
	args = append(args, size, offset)
	rows, err := d.Query(dataQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var sources []models.PassedSource
	for rows.Next() {
		var s models.PassedSource
		if err := rows.Scan(&s.ID, &s.SourceID, &s.URL, &s.Name, &s.TvgID, &s.TvgLogo, &s.GroupTitle,
			&s.Status, &s.ResponseTimeMs, &s.Resolution, &s.Bitrate, &s.LastChecked, &s.Location, &s.ISP); err != nil {
			return nil, 0, err
		}
		sources = append(sources, s)
	}
	return sources, total, nil
}

// GetPassedSourceByID 根据 ID 获取单个通过源
func (d *DB) GetPassedSourceByID(id int) (*models.PassedSource, error) {
	var s models.PassedSource
	err := d.QueryRow(`SELECT pass.id, pass.source_id, us.url, us.name, pass.status, pass.response_time_ms, pass.resolution, pass.bitrate, pass.last_checked 
		FROM url_sources_passed pass JOIN url_sources us ON pass.source_id = us.id WHERE pass.id = ?`, id).Scan(
		&s.ID, &s.SourceID, &s.URL, &s.Name, &s.Status, &s.ResponseTimeMs, &s.Resolution, &s.Bitrate, &s.LastChecked)
	return &s, err
}

// UpdatePassedSource 更新通过源记录
func (d *DB) UpdatePassedSource(id int, updates map[string]interface{}) error {
	setClauses := []string{}
	args := []interface{}{}
	for k, v := range updates {
		setClauses = append(setClauses, k+" = ?")
		args = append(args, v)
	}
	if len(setClauses) == 0 {
		return nil
	}
	setClauses = append(setClauses, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, id)
	query := fmt.Sprintf("UPDATE url_sources_passed SET %s WHERE id = ?", strings.Join(setClauses, ", "))
	_, err := d.Exec(query, args...)
	return err
}

// DeletePassedSource 删除通过源
func (d *DB) DeletePassedSource(id int) error {
	_, err := d.Exec("DELETE FROM url_sources_passed WHERE id = ?", id)
	return err
}

// CountPassedByStatus 按状态统计
func (d *DB) CountPassedByStatus(status string) int {
	var count int
	d.QueryRow("SELECT COUNT(*) FROM url_sources_passed WHERE status = ?", status).Scan(&count)
	return count
}

// GetActivePassedSources 获取所有活跃源用于生成播放列表
func (d *DB) GetActivePassedSources() ([]models.PassedSource, error) {
	rows, err := d.Query(`SELECT pass.id, pass.source_id, us.url, us.name, us.tvg_id, us.tvg_logo, us.group_title, pass.status, 
		pass.response_time_ms, pass.resolution, pass.bitrate, pass.last_checked, pass.location, pass.isp
		FROM url_sources_passed pass JOIN url_sources us ON pass.source_id = us.id
		WHERE pass.status = 'active' ORDER BY us.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []models.PassedSource
	for rows.Next() {
		var s models.PassedSource
		if err := rows.Scan(&s.ID, &s.SourceID, &s.URL, &s.Name, &s.TvgID, &s.TvgLogo, &s.GroupTitle,
			&s.Status, &s.ResponseTimeMs, &s.Resolution, &s.Bitrate, &s.LastChecked, &s.Location, &s.ISP); err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, nil
}

// GetLastTestTime 返回最后一次测试时间
func (d *DB) GetLastTestTime() string {
	var t sql.NullString
	d.QueryRow("SELECT MAX(last_checked) FROM url_sources_passed").Scan(&t)
	if t.Valid {
		return t.String
	}
	return "从未测试"
}

// ---------- 分类与显示规则 ----------

// GetActiveWhitelistRules 获取所有启用的白名单规则
func (d *DB) GetActiveWhitelistRules() ([]models.WhitelistRule, error) {
	rows, err := d.Query("SELECT id, pattern, target_type, priority, enable FROM whitelist WHERE enable = 1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []models.WhitelistRule
	for rows.Next() {
		var r models.WhitelistRule
		if err := rows.Scan(&r.ID, &r.Pattern, &r.TargetType, &r.Priority, &r.Enable); err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// GetActiveBlacklistRules 获取所有启用的黑名单规则
func (d *DB) GetActiveBlacklistRules() ([]models.BlacklistRule, error) {
	rows, err := d.Query("SELECT id, pattern, target_type, enable FROM blacklist WHERE enable = 1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []models.BlacklistRule
	for rows.Next() {
		var r models.BlacklistRule
		if err := rows.Scan(&r.ID, &r.Pattern, &r.TargetType, &r.Enable); err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// GetDisplayRules 获取显示规则
func (d *DB) GetDisplayRules() ([]models.DisplayRule, error) {
	rows, err := d.Query(`SELECT dr.id, dr.category_id, c.name, dr.group_name_override, dr.sort_order, dr.item_sort_order, 
		dr.hide_empty_groups, dr.max_items_per_category, dr.enable
		FROM display_rule dr LEFT JOIN categories c ON dr.category_id = c.id ORDER BY dr.sort_order`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []models.DisplayRule
	for rows.Next() {
		var r models.DisplayRule
		var catName sql.NullString
		err := rows.Scan(&r.ID, &r.CategoryID, &catName, &r.GroupNameOverride, &r.SortOrder, &r.ItemSortOrder,
			&r.HideEmptyGroups, &r.MaxItemsPerCategory, &r.Enable)
		if err != nil {
			return nil, err
		}
		r.CategoryName = catName.String
		rules = append(rules, r)
	}
	return rules, nil
}

// ---------- 别名规则 ----------

// GetChannelAliases 获取所有启用的频道别名规则
func (d *DB) GetChannelAliases() ([]models.ChannelAlias, error) {
	rows, err := d.Query("SELECT id, pattern, target_name, priority, enable, description FROM channel_alias WHERE enable=1 ORDER BY priority")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var aliases []models.ChannelAlias
	for rows.Next() {
		var a models.ChannelAlias
		if err := rows.Scan(&a.ID, &a.Pattern, &a.TargetName, &a.Priority, &a.Enable, &a.Description); err != nil {
			return nil, err
		}
		aliases = append(aliases, a)
	}
	return aliases, nil
}

// ---------- RTMP 相关 ----------

// UpsertRTMPStream 插入或更新 RTMP 推流记录
func (d *DB) UpsertRTMPStream(sourceID int, pushURL, hlsURL string) error {
	_, err := d.Exec(`INSERT INTO rtmp_streams (source_id, push_url, hls_url, stream_status) VALUES (?, ?, ?, 'running')
		ON CONFLICT(source_id) DO UPDATE SET push_url=excluded.push_url, hls_url=excluded.hls_url, stream_status='running', last_push_time=CURRENT_TIMESTAMP`,
		sourceID, pushURL, hlsURL)
	return err
}

// SetStreamStatus 设置推流状态
func (d *DB) SetStreamStatus(sourceID int, status string) error {
	_, err := d.Exec("UPDATE rtmp_streams SET stream_status = ? WHERE source_id = ?", status, sourceID)
	return err
}

// GetStreamIdleSeconds 获取推流的空闲秒数（此处简化，未实现实际播放统计）
func (d *DB) GetStreamIdleSeconds(sourceID int) (int, error) {
	var seconds int
	err := d.QueryRow("SELECT COALESCE(idle_seconds, 0) FROM rtmp_streams WHERE source_id = ?", sourceID).Scan(&seconds)
	return seconds, err
}

// GetRTMPStreams 获取所有推流状态
func (d *DB) GetRTMPStreams() ([]models.RTMPStream, error) {
	rows, err := d.Query("SELECT id, source_id, stream_status, push_url, hls_url, last_push_time, idle_seconds FROM rtmp_streams")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var streams []models.RTMPStream
	for rows.Next() {
		var s models.RTMPStream
		if err := rows.Scan(&s.ID, &s.SourceID, &s.Status, &s.PushURL, &s.HLSURL, &s.LastPush, &s.IdleSec); err != nil {
			return nil, err
		}
		streams = append(streams, s)
	}
	return streams, nil
}

// CountRTMPStreams 统计推流数量
func (d *DB) CountRTMPStreams(status string) int {
	var count int
	d.QueryRow("SELECT COUNT(*) FROM rtmp_streams WHERE stream_status = ?", status).Scan(&count)
	return count
}

// ---------- EPG ----------

// CountEPGPrograms 统计 EPG 节目数
func (d *DB) CountEPGPrograms() int {
	var count int
	d.QueryRow("SELECT COUNT(*) FROM epg_program").Scan(&count)
	return count
}

// ---------- 系统日志 ----------

// GetSystemLogs 获取系统日志（按级别筛选）
func (d *DB) GetSystemLogs(level string, limit int) ([]map[string]interface{}, error) {
	query := "SELECT id, level, module, message, details, created_at FROM system_log"
	var args []interface{}
	if level != "" {
		query += " WHERE level = ?"
		args = append(args, level)
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var logs []map[string]interface{}
	for rows.Next() {
		var id int
		var lvl, module, message, details string
		var createdAt time.Time
		if err := rows.Scan(&id, &lvl, &module, &message, &details, &createdAt); err != nil {
			return nil, err
		}
		logs = append(logs, map[string]interface{}{
			"id": id, "level": lvl, "module": module, "message": message, "details": details, "created_at": createdAt,
		})
	}
	return logs, nil
}

// InsertSystemLog 插入一条系统日志
func (d *DB) InsertSystemLog(level, module, message, details string) error {
	_, err := d.Exec("INSERT INTO system_log (level, module, message, details) VALUES (?, ?, ?, ?)",
		level, module, message, details)
	return err
}

// ---------- 酒店扫描 ----------

// GetHotelScanConfigs 获取启用的酒店扫描配置
func (d *DB) GetHotelScanConfigs() ([]models.HotelScanConfig, error) {
	rows, err := d.Query("SELECT id, ip_range, port, path, enable FROM hotel_scan_config WHERE enable = 1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var configs []models.HotelScanConfig
	for rows.Next() {
		var c models.HotelScanConfig
		if err := rows.Scan(&c.ID, &c.IPRange, &c.Port, &c.Path, &c.Enable); err != nil {
			return nil, err
		}
		configs = append(configs, c)
	}
	return configs, nil
}

// UpdateHotelScanStats 更新酒店源扫描统计
func (d *DB) UpdateHotelScanStats(id int, foundCount int) error {
	_, err := d.Exec("UPDATE hotel_scan_config SET last_scan = CURRENT_TIMESTAMP, found_count = ? WHERE id = ?", foundCount, id)
	return err
}

// ---------- 组播扫描 ----------

// GetMulticastConfigs 获取启用的组播扫描配置
func (d *DB) GetMulticastConfigs() ([]models.MulticastConfig, error) {
	rows, err := d.Query("SELECT id, interface, address, enable FROM multicast_config WHERE enable = 1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var configs []models.MulticastConfig
	for rows.Next() {
		var c models.MulticastConfig
		if err := rows.Scan(&c.ID, &c.Interface, &c.Address, &c.Enable); err != nil {
			return nil, err
		}
		configs = append(configs, c)
	}
	return configs, nil
}

// UpdateMulticastScanStats 更新组播扫描统计
func (d *DB) UpdateMulticastScanStats(id int) error {
	_, err := d.Exec("UPDATE multicast_config SET last_scan = CURRENT_TIMESTAMP WHERE id = ?", id)
	return err
}

// 需补充 models 中缺失的类型定义
