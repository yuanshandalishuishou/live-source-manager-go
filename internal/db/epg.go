package db

// EPG（电子节目单）数据层：三张表 + 频道映射 tvg 扩列 + 预置源种子 + 全部读写函数。
//
// 设计对齐 Python 版 web/models.py 的 EPG 部分，并规避其两处坑：
//  1. 预置源幂等写入按 URL 判重，避免用户改名后重复灌入；
//  2. 整源替换（ReplaceEPGData）走单事务，中途失败不会留下半截数据。

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"live-source-manager-go/internal/logger"
	"live-source-manager-go/internal/types"
)

const epgSchema = `
CREATE TABLE IF NOT EXISTS epg_sources (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    url TEXT NOT NULL UNIQUE,
    enabled INTEGER NOT NULL DEFAULT 1,
    priority INTEGER NOT NULL DEFAULT 100,
    refresh_mode TEXT NOT NULL DEFAULT 'daily',
    refresh_at TEXT NOT NULL DEFAULT '03:30',
    refresh_minutes INTEGER NOT NULL DEFAULT 360,
    remark TEXT NOT NULL DEFAULT '',
    last_fetch_at TEXT NOT NULL DEFAULT '',
    last_status TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    last_channel_count INTEGER NOT NULL DEFAULT 0,
    last_programme_count INTEGER NOT NULL DEFAULT 0,
    last_duration_ms INTEGER NOT NULL DEFAULT 0,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_epg_sources_enabled ON epg_sources(enabled, priority);

CREATE TABLE IF NOT EXISTS epg_channels (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id INTEGER NOT NULL,
    tvg_id TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    icon TEXT NOT NULL DEFAULT '',
    matched_channel TEXT NOT NULL DEFAULT '',
    updated_at TEXT DEFAULT (datetime('now')),
    UNIQUE(source_id, tvg_id)
);
CREATE INDEX IF NOT EXISTS idx_epg_channels_tvg ON epg_channels(tvg_id);
CREATE INDEX IF NOT EXISTS idx_epg_channels_matched ON epg_channels(matched_channel);

CREATE TABLE IF NOT EXISTS epg_programmes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id INTEGER NOT NULL,
    tvg_id TEXT NOT NULL,
    start_utc TEXT NOT NULL,
    stop_utc TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    sub_title TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    episode TEXT NOT NULL DEFAULT '',
    icon TEXT NOT NULL DEFAULT '',
    UNIQUE(source_id, tvg_id, start_utc)
);
CREATE INDEX IF NOT EXISTS idx_epg_prog_lookup ON epg_programmes(tvg_id, start_utc);
CREATE INDEX IF NOT EXISTS idx_epg_prog_source ON epg_programmes(source_id);
CREATE INDEX IF NOT EXISTS idx_epg_prog_stop ON epg_programmes(stop_utc);
`

// defaultEPGSource 是一条预置节目单源。
type defaultEPGSource struct {
	Name     string
	URL      string
	Priority int
	Remark   string
}

// DefaultEPGSources 与 Python 版 DEFAULT_EPG_SOURCES 保持一致（7 条公开源）。
var DefaultEPGSources = []defaultEPGSource{
	{"51zmt 大陆+港澳台", "http://epg.51zmt.top:8000/e.xml.gz", 10, "老牌稳定，覆盖大陆及港澳台频道"},
	{"Meroser 稳定版", "https://epg.pw/xmltv/epg_CN.xml.gz", 20, "epg.pw 中国区节目单"},
	{"Meroser 详情版", "https://e.erw.cc/all.xml.gz", 30, "含节目简介，体积较大"},
	{"zbds 直播myTV", "http://epg.51zmt.top:8000/difang.xml.gz", 40, "地方台补充"},
	{"BurningC4 jsDelivr", "https://cdn.jsdelivr.net/gh/BurningC4/Chinese-IPTV@master/TV-EPG.xml", 50, "GitHub CDN 镜像，国内可达性较好"},
	{"BurningC4 GitHub", "https://raw.githubusercontent.com/BurningC4/Chinese-IPTV/master/TV-EPG.xml", 60, "GitHub 源站，需代理"},
	{"epg.pw 全量", "https://epg.pw/xmltv/epg.xml.gz", 70, "全球频道，体积大，按需启用"},
}

// migrateEPG 建 EPG 三表、给 channel_name_mapping 自愈补列、灌预置源。
func migrateEPG(conn *sql.DB) error {
	if _, err := conn.Exec(epgSchema); err != nil {
		return fmt.Errorf("migrate epg schema: %w", err)
	}
	// channel_name_mapping 扩列（老库自愈；SQLite 无 IF NOT EXISTS，靠查列名判断）。
	if err := ensureColumns(conn, "channel_name_mapping", map[string]string{
		"tvg_id":   "TEXT NOT NULL DEFAULT ''",
		"tvg_logo": "TEXT NOT NULL DEFAULT ''",
	}); err != nil {
		return err
	}
	return seedEPGSources(conn)
}

// ensureColumns 为表补齐缺失的列（幂等）。
func ensureColumns(conn *sql.DB, table string, cols map[string]string) error {
	rows, err := conn.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return fmt.Errorf("table_info(%s): %w", table, err)
	}
	existing := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		existing[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for col, decl := range cols {
		if existing[col] {
			continue
		}
		if _, err := conn.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, col, decl)); err != nil {
			return fmt.Errorf("add column %s.%s: %w", table, col, err)
		}
		logger.L().Info("表 %s 自愈补列: %s", table, col)
	}
	return nil
}

// seedEPGSources 幂等写入预置节目单源（按 URL 判重，用户改名/改优先级不会被覆盖）。
func seedEPGSources(conn *sql.DB) error {
	inserted := 0
	for _, s := range DefaultEPGSources {
		var cnt int
		if err := conn.QueryRow("SELECT COUNT(*) FROM epg_sources WHERE url = ?", s.URL).Scan(&cnt); err != nil {
			return err
		}
		if cnt > 0 {
			continue
		}
		// 只有 51zmt 与 epg.pw 中国区默认启用，其余留给用户按需打开，避免首启抓取过久。
		enabled := 0
		if s.Priority <= 20 {
			enabled = 1
		}
		if _, err := conn.Exec(
			`INSERT INTO epg_sources (name, url, enabled, priority, refresh_mode, refresh_at, refresh_minutes, remark)
			 VALUES (?,?,?,?,'daily','03:30',360,?)`,
			s.Name, s.URL, enabled, s.Priority, s.Remark); err != nil {
			return fmt.Errorf("seed epg source %s: %w", s.Name, err)
		}
		inserted++
	}
	if inserted > 0 {
		logger.L().Info("预置 EPG 节目单源已初始化: %d 条", inserted)
	}
	return nil
}

// ── epg_sources CRUD ──────────────────────────────────────────────────────

func scanEPGSource(sc interface{ Scan(...any) error }) (types.EPGSource, error) {
	var s types.EPGSource
	var enabled int
	err := sc.Scan(&s.ID, &s.Name, &s.URL, &enabled, &s.Priority, &s.RefreshMode, &s.RefreshAt,
		&s.RefreshMinutes, &s.Remark, &s.LastFetchAt, &s.LastStatus, &s.LastError,
		&s.LastChannelCount, &s.LastProgrammeCount, &s.LastDurationMs, &s.CreatedAt, &s.UpdatedAt)
	s.Enabled = enabled != 0
	return s, err
}

const epgSourceCols = `id, name, url, enabled, priority, refresh_mode, refresh_at, refresh_minutes,
	remark, last_fetch_at, last_status, last_error, last_channel_count, last_programme_count,
	last_duration_ms, created_at, updated_at`

// ListEPGSources 返回全部节目单源（按 priority, id 排序）。enabledOnly 为 true 时仅返回启用的。
func ListEPGSources(conn *sql.DB, enabledOnly bool) ([]types.EPGSource, error) {
	q := "SELECT " + epgSourceCols + " FROM epg_sources"
	if enabledOnly {
		q += " WHERE enabled = 1"
	}
	q += " ORDER BY priority, id"
	rows, err := conn.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []types.EPGSource{}
	for rows.Next() {
		s, err := scanEPGSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetEPGSource 按 id 返回一个节目单源，不存在返回 (nil, nil)。
func GetEPGSource(conn *sql.DB, id int) (*types.EPGSource, error) {
	row := conn.QueryRow("SELECT "+epgSourceCols+" FROM epg_sources WHERE id = ?", id)
	s, err := scanEPGSource(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// CreateEPGSource 新增一个节目单源，返回自增 id。
func CreateEPGSource(conn *sql.DB, s types.EPGSource) (int, error) {
	enabled := 0
	if s.Enabled {
		enabled = 1
	}
	res, err := conn.Exec(
		`INSERT INTO epg_sources (name, url, enabled, priority, refresh_mode, refresh_at, refresh_minutes, remark)
		 VALUES (?,?,?,?,?,?,?,?)`,
		s.Name, s.URL, enabled, s.Priority, s.RefreshMode, s.RefreshAt, s.RefreshMinutes, s.Remark)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return int(id), nil
}

// UpdateEPGSource 更新一个节目单源的可编辑字段。
func UpdateEPGSource(conn *sql.DB, s types.EPGSource) error {
	enabled := 0
	if s.Enabled {
		enabled = 1
	}
	_, err := conn.Exec(
		`UPDATE epg_sources SET name=?, url=?, enabled=?, priority=?, refresh_mode=?, refresh_at=?,
		   refresh_minutes=?, remark=?, updated_at=datetime('now') WHERE id=?`,
		s.Name, s.URL, enabled, s.Priority, s.RefreshMode, s.RefreshAt, s.RefreshMinutes, s.Remark, s.ID)
	return err
}

// DeleteEPGSource 删除节目单源及其全部频道/节目数据。
func DeleteEPGSource(conn *sql.DB, id int) error {
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		"DELETE FROM epg_programmes WHERE source_id = ?",
		"DELETE FROM epg_channels WHERE source_id = ?",
		"DELETE FROM epg_sources WHERE id = ?",
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpdateEPGSourceStatus 写回一次抓取的结果状态。
func UpdateEPGSourceStatus(conn *sql.DB, r types.EPGRefreshResult) error {
	status := "failed"
	if r.Success {
		status = "success"
	}
	errMsg := r.Error
	if len(errMsg) > 500 {
		errMsg = errMsg[:500]
	}
	_, err := conn.Exec(
		`UPDATE epg_sources SET last_fetch_at=?, last_status=?, last_error=?,
		   last_channel_count=?, last_programme_count=?, last_duration_ms=?, updated_at=datetime('now')
		 WHERE id=?`,
		time.Now().Format("2006-01-02 15:04:05"), status, errMsg,
		r.ChannelCount, r.ProgrammeCount, r.DurationMs, r.SourceID)
	return err
}

// ── 数据写入 ──────────────────────────────────────────────────────────────

// ReplaceEPGData 用新抓取的数据整源替换该来源的频道与节目（单事务，失败不留半截）。
func ReplaceEPGData(conn *sql.DB, sourceID int, channels []types.EPGChannel, programmes []types.EPGProgramme) error {
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// 先记住已有的人工对齐结果，替换后恢复，避免刷新清空用户的手工匹配。
	matched := map[string]string{}
	rows, err := tx.Query("SELECT tvg_id, matched_channel FROM epg_channels WHERE source_id = ? AND matched_channel != ''", sourceID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var tvgID, mc string
		if err := rows.Scan(&tvgID, &mc); err != nil {
			rows.Close()
			return err
		}
		matched[tvgID] = mc
	}
	rows.Close()

	if _, err := tx.Exec("DELETE FROM epg_programmes WHERE source_id = ?", sourceID); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM epg_channels WHERE source_id = ?", sourceID); err != nil {
		return err
	}

	chStmt, err := tx.Prepare(
		`INSERT INTO epg_channels (source_id, tvg_id, display_name, icon, matched_channel, updated_at)
		 VALUES (?,?,?,?,?,datetime('now'))
		 ON CONFLICT(source_id, tvg_id) DO UPDATE SET
		   display_name=excluded.display_name, icon=excluded.icon, updated_at=excluded.updated_at`)
	if err != nil {
		return err
	}
	defer chStmt.Close()
	for _, c := range channels {
		if strings.TrimSpace(c.TVGID) == "" {
			continue
		}
		if _, err := chStmt.Exec(sourceID, c.TVGID, c.DisplayName, c.Icon, matched[c.TVGID]); err != nil {
			return err
		}
	}

	pStmt, err := tx.Prepare(
		`INSERT INTO epg_programmes (source_id, tvg_id, start_utc, stop_utc, title, sub_title, description, category, episode, icon)
		 VALUES (?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(source_id, tvg_id, start_utc) DO UPDATE SET
		   stop_utc=excluded.stop_utc, title=excluded.title, sub_title=excluded.sub_title,
		   description=excluded.description, category=excluded.category,
		   episode=excluded.episode, icon=excluded.icon`)
	if err != nil {
		return err
	}
	defer pStmt.Close()
	for _, p := range programmes {
		if strings.TrimSpace(p.TVGID) == "" || strings.TrimSpace(p.StartUTC) == "" {
			continue
		}
		if _, err := pStmt.Exec(sourceID, p.TVGID, p.StartUTC, p.StopUTC, p.Title,
			p.SubTitle, p.Description, p.Category, p.Episode, p.Icon); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ── 查询 ──────────────────────────────────────────────────────────────────

// ListEPGChannels 返回频道列表（可按来源过滤 / 关键词搜索）。sourceID <= 0 表示全部来源。
func ListEPGChannels(conn *sql.DB, sourceID int, query string, limit, offset int) ([]types.EPGChannel, error) {
	q := `SELECT c.id, c.source_id, COALESCE(s.name,''), c.tvg_id, c.display_name, c.icon, c.matched_channel, c.updated_at
	      FROM epg_channels c LEFT JOIN epg_sources s ON s.id = c.source_id`
	conds := []string{}
	args := []any{}
	if sourceID > 0 {
		conds = append(conds, "c.source_id = ?")
		args = append(args, sourceID)
	}
	if strings.TrimSpace(query) != "" {
		conds = append(conds, "(c.tvg_id LIKE ? OR c.display_name LIKE ?)")
		like := "%" + strings.TrimSpace(query) + "%"
		args = append(args, like, like)
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY c.display_name, c.tvg_id LIMIT ? OFFSET ?"
	if limit <= 0 {
		limit = 200
	}
	args = append(args, limit, offset)
	rows, err := conn.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []types.EPGChannel{}
	for rows.Next() {
		var c types.EPGChannel
		if err := rows.Scan(&c.ID, &c.SourceID, &c.SourceName, &c.TVGID, &c.DisplayName,
			&c.Icon, &c.MatchedChannel, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountEPGChannels 返回符合条件的频道总数。
func CountEPGChannels(conn *sql.DB, sourceID int, query string) (int, error) {
	q := "SELECT COUNT(*) FROM epg_channels c"
	conds := []string{}
	args := []any{}
	if sourceID > 0 {
		conds = append(conds, "c.source_id = ?")
		args = append(args, sourceID)
	}
	if strings.TrimSpace(query) != "" {
		conds = append(conds, "(c.tvg_id LIKE ? OR c.display_name LIKE ?)")
		like := "%" + strings.TrimSpace(query) + "%"
		args = append(args, like, like)
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	var n int
	err := conn.QueryRow(q, args...).Scan(&n)
	return n, err
}

// GetEPGChannel 按 id 返回一个 EPG 频道，不存在返回 (nil, nil)。
func GetEPGChannel(conn *sql.DB, id int) (*types.EPGChannel, error) {
	row := conn.QueryRow(
		`SELECT c.id, c.source_id, COALESCE(s.name,''), c.tvg_id, c.display_name, c.icon, c.matched_channel, c.updated_at
		 FROM epg_channels c LEFT JOIN epg_sources s ON s.id = c.source_id WHERE c.id = ?`, id)
	var c types.EPGChannel
	if err := row.Scan(&c.ID, &c.SourceID, &c.SourceName, &c.TVGID, &c.DisplayName,
		&c.Icon, &c.MatchedChannel, &c.UpdatedAt); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &c, nil
}

// SetEPGChannelMatch 记录某个 EPG 频道人工对齐到的本地频道名。
func SetEPGChannelMatch(conn *sql.DB, id int, matchedChannel string) error {
	_, err := conn.Exec(
		"UPDATE epg_channels SET matched_channel = ?, updated_at = datetime('now') WHERE id = ?",
		matchedChannel, id)
	return err
}

// ListAllEPGChannels 返回全部频道（用于导出 XMLTV / 频道对齐，无分页）。
func ListAllEPGChannels(conn *sql.DB) ([]types.EPGChannel, error) {
	rows, err := conn.Query(
		`SELECT c.id, c.source_id, c.tvg_id, c.display_name, c.icon, c.matched_channel
		 FROM epg_channels c JOIN epg_sources s ON s.id = c.source_id
		 WHERE s.enabled = 1 ORDER BY s.priority, c.tvg_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []types.EPGChannel{}
	for rows.Next() {
		var c types.EPGChannel
		if err := rows.Scan(&c.ID, &c.SourceID, &c.TVGID, &c.DisplayName, &c.Icon, &c.MatchedChannel); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListAllEPGProgrammes 返回启用来源的全部节目（用于导出 XMLTV）。
func ListAllEPGProgrammes(conn *sql.DB) ([]types.EPGProgramme, error) {
	rows, err := conn.Query(
		`SELECT p.source_id, p.tvg_id, p.start_utc, p.stop_utc, p.title, p.sub_title,
		        p.description, p.category, p.episode, p.icon
		 FROM epg_programmes p JOIN epg_sources s ON s.id = p.source_id
		 WHERE s.enabled = 1 ORDER BY p.tvg_id, p.start_utc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []types.EPGProgramme{}
	for rows.Next() {
		var p types.EPGProgramme
		if err := rows.Scan(&p.SourceID, &p.TVGID, &p.StartUTC, &p.StopUTC, &p.Title,
			&p.SubTitle, &p.Description, &p.Category, &p.Episode, &p.Icon); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// QueryEPGProgrammes 查询一批 tvg_id 在 [startUTC, stopUTC] 窗口内有重叠的节目。
// 为规避 SQLite 999 变量上限，按 400 一批分片查询（与 Python 版一致）。
func QueryEPGProgrammes(conn *sql.DB, tvgIDs []string, startUTC, stopUTC string) ([]types.EPGProgramme, error) {
	out := []types.EPGProgramme{}
	const batch = 400
	for i := 0; i < len(tvgIDs); i += batch {
		end := i + batch
		if end > len(tvgIDs) {
			end = len(tvgIDs)
		}
		chunk := tvgIDs[i:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, 0, len(chunk)+2)
		for _, id := range chunk {
			args = append(args, id)
		}
		// 重叠判定：节目开始 < 窗口结束 且 节目结束 > 窗口开始。
		args = append(args, stopUTC, startUTC)
		q := fmt.Sprintf(
			`SELECT source_id, tvg_id, start_utc, stop_utc, title, sub_title, description, category, episode, icon
			 FROM epg_programmes WHERE tvg_id IN (%s) AND start_utc < ? AND stop_utc > ?
			 ORDER BY tvg_id, start_utc`, placeholders)
		rows, err := conn.Query(q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var p types.EPGProgramme
			if err := rows.Scan(&p.SourceID, &p.TVGID, &p.StartUTC, &p.StopUTC, &p.Title,
				&p.SubTitle, &p.Description, &p.Category, &p.Episode, &p.Icon); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, p)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// CleanupEPGProgrammes 删除 stop_utc 早于 beforeUTC 的过期节目，返回删除条数。
func CleanupEPGProgrammes(conn *sql.DB, beforeUTC string) (int64, error) {
	res, err := conn.Exec("DELETE FROM epg_programmes WHERE stop_utc != '' AND stop_utc < ?", beforeUTC)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// GetEPGStats 返回 EPG 概览统计。
func GetEPGStats(conn *sql.DB) (types.EPGStats, error) {
	var st types.EPGStats
	_ = conn.QueryRow("SELECT COUNT(*), COALESCE(SUM(enabled),0) FROM epg_sources").
		Scan(&st.SourceCount, &st.EnabledSources)
	_ = conn.QueryRow("SELECT COUNT(*) FROM epg_channels").Scan(&st.ChannelCount)
	_ = conn.QueryRow("SELECT COUNT(*) FROM epg_programmes").Scan(&st.ProgrammeCount)
	_ = conn.QueryRow("SELECT COUNT(*) FROM epg_channels WHERE matched_channel != ''").Scan(&st.MatchedChannels)
	var earliest, latest, lastFetch sql.NullString
	_ = conn.QueryRow("SELECT MIN(start_utc), MAX(stop_utc) FROM epg_programmes").Scan(&earliest, &latest)
	_ = conn.QueryRow("SELECT MAX(last_fetch_at) FROM epg_sources WHERE last_status = 'success'").Scan(&lastFetch)
	st.EarliestStart = earliest.String
	st.LatestStop = latest.String
	st.LastRefreshAt = lastFetch.String
	return st, nil
}

// ── channel_name_mapping tvg 扩展 ──────────────────────────────────────────

// SetChannelTVGInfo 把 tvg_id / tvg_logo 写回频道映射表（不存在则建行，保留已有分类）。
func SetChannelTVGInfo(conn *sql.DB, channelName, tvgID, tvgLogo string) error {
	if strings.TrimSpace(channelName) == "" {
		return nil
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	_, err := conn.Exec(
		`INSERT INTO channel_name_mapping (channel_name, tvg_id, tvg_logo, is_manual, created_at, updated_at)
		 VALUES (?,?,?,0,?,?)
		 ON CONFLICT(channel_name) DO UPDATE SET
		   tvg_id = CASE WHEN excluded.tvg_id != '' THEN excluded.tvg_id ELSE channel_name_mapping.tvg_id END,
		   tvg_logo = CASE WHEN excluded.tvg_logo != '' THEN excluded.tvg_logo ELSE channel_name_mapping.tvg_logo END,
		   updated_at = excluded.updated_at`,
		channelName, tvgID, tvgLogo, now, now)
	return err
}

// ListAllChannelNames 返回频道映射表里已知的全部频道名（EPG 对齐的兜底数据源）。
func ListAllChannelNames(conn *sql.DB) ([]string, error) {
	rows, err := conn.Query("SELECT channel_name FROM channel_name_mapping")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		if strings.TrimSpace(n) != "" {
			out = append(out, n)
		}
	}
	return out, rows.Err()
}

// GetAllChannelTVGInfo 返回 channel_name -> [tvg_id, tvg_logo]，供 m3u 生成注入。
func GetAllChannelTVGInfo(conn *sql.DB) (map[string][2]string, error) {
	out := map[string][2]string{}
	rows, err := conn.Query(
		"SELECT channel_name, tvg_id, tvg_logo FROM channel_name_mapping WHERE tvg_id != '' OR tvg_logo != ''")
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, tvgID, tvgLogo string
		if err := rows.Scan(&name, &tvgID, &tvgLogo); err != nil {
			return out, err
		}
		out[name] = [2]string{tvgID, tvgLogo}
	}
	return out, rows.Err()
}
