package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/logger"
	"live-source-manager-go/internal/types"
)

// ── 页面 ────────────────────────────────────────────────────────────────

func (s *Server) pageEpg(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, "epg", s.pageData(r, "节目单", true))
}

func (s *Server) pageEpgSources(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, "epg_sources", s.pageData(r, "EPG 源管理", true))
}

// ── 工具 ────────────────────────────────────────────────────────────────

func (s *Server) isAdmin(r *http.Request) bool {
	u := currentUser(r)
	return u != nil && u.Role == "admin"
}

// triggerEPGRefresh 在后台并发刷新指定来源（ids 为空表示全部启用来源）。
// 与 Python 版一致：若已有刷新在进行，直接返回 running 状态，不重复触发。
func (s *Server) triggerEPGRefresh(ids []int) map[string]any {
	if s.epgMgr.State().Running {
		return map[string]any{"ok": true, "running": true, "message": "刷新任务正在进行中"}
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if _, err := s.epgMgr.RefreshAll(ctx, ids); err != nil {
			logger.L().Warning("EPG 后台刷新失败: %v", err)
		}
	}()
	return map[string]any{"ok": true, "running": true, "message": "已触发刷新"}
}

func mapStr(m map[string]any, k string) (string, bool) {
	v, ok := m[k]
	if !ok {
		return "", false
	}
	if s, ok2 := v.(string); ok2 {
		return s, true
	}
	return fmt.Sprintf("%v", v), true
}

func mapBool(m map[string]any, k string) (bool, bool) {
	v, ok := m[k]
	if !ok {
		return false, false
	}
	switch t := v.(type) {
	case bool:
		return t, true
	case float64:
		return t != 0, true
	case string:
		return t == "true" || t == "1", true
	}
	return false, false
}

func mapInt(m map[string]any, k string) (int, bool) {
	v, ok := m[k]
	if !ok {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return int(t), true
	case int:
		return t, true
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n, true
		}
	}
	return 0, false
}

// ── 源管理 API ──────────────────────────────────────────────────────────

func (s *Server) hListEPGSources(w http.ResponseWriter, r *http.Request) {
	enabledOnly := false
	if v := r.URL.Query().Get("enabled_only"); v == "true" || v == "1" {
		enabledOnly = true
	}
	srcs, err := db.ListEPGSources(s.conn, enabledOnly)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sources": srcs, "count": len(srcs)})
}

func (s *Server) hCreateEPGSource(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "需要管理员权限"})
		return
	}
	var body struct {
		Name           string `json:"name"`
		URL            string `json:"url"`
		Enabled        bool   `json:"enabled"`
		Priority       int    `json:"priority"`
		RefreshMode    string `json:"refresh_mode"`
		RefreshAt      string `json:"refresh_at"`
		RefreshMinutes int    `json:"refresh_minutes"`
		Remark         string `json:"remark"`
	}
	if err := readJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "请求体必须为 JSON"})
		return
	}
	url := strings.TrimSpace(body.URL)
	if url == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "EPG 源地址（url）不能为空"})
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = url
	}
	src := types.EPGSource{
		Name:           name,
		URL:            url,
		Enabled:        body.Enabled,
		Priority:       body.Priority,
		RefreshMode:    strings.TrimSpace(body.RefreshMode),
		RefreshAt:      strings.TrimSpace(body.RefreshAt),
		RefreshMinutes: body.RefreshMinutes,
		Remark:         strings.TrimSpace(body.Remark),
	}
	if src.Priority == 0 {
		src.Priority = 100
	}
	id, err := db.CreateEPGSource(s.conn, src)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "该地址已存在或写入失败"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (s *Server) hUpdateEPGSource(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "需要管理员权限"})
		return
	}
	id, err := strconv.Atoi(routeParam(r, "source_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "无效的 source_id"})
		return
	}
	existing, err := db.GetEPGSource(s.conn, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if existing == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "EPG 源不存在"})
		return
	}
	var body map[string]any
	if err := readJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "请求体必须为 JSON"})
		return
	}
	if v, ok := mapStr(body, "name"); ok {
		existing.Name = v
	}
	if v, ok := mapStr(body, "url"); ok {
		existing.URL = v
	}
	if v, ok := mapBool(body, "enabled"); ok {
		existing.Enabled = v
	}
	if v, ok := mapInt(body, "priority"); ok {
		existing.Priority = v
	}
	if v, ok := mapStr(body, "refresh_mode"); ok {
		existing.RefreshMode = v
	}
	if v, ok := mapStr(body, "refresh_at"); ok {
		existing.RefreshAt = v
	}
	if v, ok := mapInt(body, "refresh_minutes"); ok {
		existing.RefreshMinutes = v
	}
	if v, ok := mapStr(body, "remark"); ok {
		existing.Remark = v
	}
	if err := db.UpdateEPGSource(s.conn, *existing); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "更新失败（地址冲突或无有效变更）"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) hDeleteEPGSource(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "需要管理员权限"})
		return
	}
	id, err := strconv.Atoi(routeParam(r, "source_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "无效的 source_id"})
		return
	}
	existing, err := db.GetEPGSource(s.conn, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if existing == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "EPG 源不存在"})
		return
	}
	if err := db.DeleteEPGSource(s.conn, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ── 抓取 / 生成 API ─────────────────────────────────────────────────────

func (s *Server) hRefreshEPGSource(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "需要管理员权限"})
		return
	}
	id, err := strconv.Atoi(routeParam(r, "source_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "无效的 source_id"})
		return
	}
	src, err := db.GetEPGSource(s.conn, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if src == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "EPG 源不存在"})
		return
	}
	writeJSON(w, http.StatusOK, s.triggerEPGRefresh([]int{id}))
}

func (s *Server) hRefreshAllEPG(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "需要管理员权限"})
		return
	}
	writeJSON(w, http.StatusOK, s.triggerEPGRefresh(nil))
}

func (s *Server) hGenerateEPG(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "需要管理员权限"})
		return
	}
	path, err := s.epgMgr.GenerateXMLTV()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "生成 EPG 文件失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": path})
}

// ── 查询 API（网格 / 频道 / NowNext / 状态） ─────────────────────────────

func (s *Server) hEPGGrid(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	day, _ := strconv.Atoi(q.Get("day"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 80
	}
	rows, err := s.epgMgr.GetGrid(q.Get("keyword"), day, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "读取节目单失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"rows":  rows,
		"count": len(rows),
		"day":   day,
	})
}

func (s *Server) hListEPGChannels(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page <= 0 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if pageSize <= 0 {
		pageSize = 50
	}
	sourceID, _ := strconv.Atoi(q.Get("source_id"))
	offset := (page - 1) * pageSize
	chans, err := db.ListEPGChannels(s.conn, sourceID, q.Get("keyword"), pageSize, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	total, err := db.CountEPGChannels(s.conn, sourceID, q.Get("keyword"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"channels":  chans,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func (s *Server) hEPGNowNext(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.epgMgr.GetNowNext(q.Get("keyword"), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "读取节目单失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "rows": rows})
}

func (s *Server) hMatchEPGChannel(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "需要管理员权限"})
		return
	}
	id, err := strconv.Atoi(routeParam(r, "channel_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "无效的 channel_id"})
		return
	}
	var body struct {
		MatchedChannel string `json:"matched_channel"`
		TVGID          string `json:"tvg_id"`
	}
	if err := readJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "请求体必须为 JSON"})
		return
	}
	matched := strings.TrimSpace(body.MatchedChannel)
	if err := db.SetEPGChannelMatch(s.conn, id, matched); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	// 对齐成功后回写 channel_name_mapping，使 M3U 的 tvg-id / tvg-logo 注入生效。
	if matched != "" {
		ch, gErr := db.GetEPGChannel(s.conn, id)
		tvgID := strings.TrimSpace(body.TVGID)
		icon := ""
		if gErr == nil && ch != nil {
			if tvgID == "" {
				tvgID = ch.TVGID
			}
			icon = ch.Icon
		}
		if err := db.SetChannelTVGInfo(s.conn, matched, tvgID, icon); err != nil {
			logger.L().Warning("回写频道 tvg 信息失败[%s]: %v", matched, err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) hEPGStatus(w http.ResponseWriter, r *http.Request) {
	cfg := map[string]any{
		"enabled":         s.epgMgr.Enabled(),
		"inject_into_m3u": s.epgMgr.InjectEnabled(),
		"output_filename": strings.TrimSpace(s.cfg.Get("EPG", "output_filename", "epg.xml.gz")),
		"timezone":        s.cfg.Get("EPG", "timezone", "Asia/Shanghai"),
		"refresh_mode":    s.cfg.Get("EPG", "refresh_mode", "daily"),
		"refresh_at":      s.cfg.Get("EPG", "refresh_at", "03:30"),
		"refresh_minutes": s.cfg.GetInt("EPG", "refresh_minutes", 360),
	}
	stats, err := db.GetEPGStats(s.conn)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	state := s.epgMgr.State()
	total, okCount, failed := 0, 0, 0
	for _, res := range state.Results {
		total++
		if res.Success {
			okCount++
		} else {
			failed++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"config":       cfg,
		"url":          s.epgMgr.GetEPGURL(),
		"stats":        stats,
		"running":      state.Running,
		"message":      state.Message,
		"last_refresh": state.FinishedAt,
		"last_result":  map[string]any{"total": total, "ok": okCount, "failed": failed},
	})
}

func (s *Server) hEPGURL(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "url": s.epgMgr.GetEPGURL()})
}
