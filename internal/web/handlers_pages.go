package web

import (
	"net/http"
	"sort"

	"live-source-manager-go/internal/auth"
	"live-source-manager-go/internal/db"
)

func (s *Server) pageData(r *http.Request, title string, showNav bool) map[string]any {
	data := map[string]any{"Title": title, "ShowNav": showNav}
	if u := currentUser(r); u != nil {
		data["User"] = map[string]any{"username": u.Username, "role": u.Role, "display_name": u.DisplayName}
		data["IsAdmin"] = u.Role == "admin"
	} else {
		data["IsAdmin"] = false
	}
	sid := sessionID(r)
	if sid != "" {
		if v, ok := s.csrf.Load(sid); ok {
			data["CSRFToken"] = v
		} else {
			// 会话已有但尚无 CSRF token：首屏自动生成并存储，
			// 使 window.__csrf_token 立即可用，避免前端 POST 报 "csrf token missing or invalid"。
			tok := auth.GenerateCSRFToken()
			s.csrf.Store(sid, tok)
			data["CSRFToken"] = tok
		}
	}
	return data
}

func (s *Server) pageDashboard(w http.ResponseWriter, r *http.Request) {
	d := s.pageData(r, "仪表盘", true)
	ch, rep, ok := s.mgr.PeekChannels()
	if !ok {
		// 冷缓存：后台预热，首屏立即返回占位（不再阻塞 20s 实时采集）
		s.mgr.WarmChannels()
		d["collected"] = false
		d["total_sources"], d["valid"], d["invalid"], d["rate"], d["files"] = 0, 0, 0, "0%", 0
	} else {
		for k, v := range s.dashStatsFrom(ch, rep) {
			d[k] = v
		}
		d["collected"] = true
	}
	s.renderPage(w, "dashboard", d)
}

func (s *Server) pageLogin(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.renderPage(w, "login", s.pageData(r, "登录", false))
}

func (s *Server) pageSources(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, "sources", s.pageData(r, "源管理", true))
}

func (s *Server) pageSourceAdd(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, "sources", s.pageData(r, "添加源", true))
}

func (s *Server) pageSourceEdit(w http.ResponseWriter, r *http.Request) {
	d := s.pageData(r, "编辑源", true)
	id := routeParam(r, "source_id")
	if ch, _ := s.findChannel(id); ch != nil {
		d["source"] = ch
		cats, _ := db.GetSourceCategories(s.conn, id)
		d["categories"] = cats
		d["source_id"] = id
	}
	s.renderPage(w, "sources", d)
}

func (s *Server) pageRules(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, "rules", s.pageData(r, "分类规则", true))
}

func (s *Server) pageConfig(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, "config", s.pageData(r, "配置中心", true))
}

func (s *Server) pageSystem(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, "system", s.pageData(r, "系统信息", true))
}

func (s *Server) pageLogs(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, "logs", s.pageData(r, "日志与审计", true))
}

func (s *Server) pageAudit(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, "logs", s.pageData(r, "日志与审计", true))
}

func (s *Server) pageUsers(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, "users", s.pageData(r, "用户管理", true))
}

func (s *Server) pageTest(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, "test", s.pageData(r, "实时测试", true))
}

// ── dashboard API ──────────────────────────────────────────────

func (s *Server) hDashStats(w http.ResponseWriter, r *http.Request) {
	ch, rep, ok := s.mgr.PeekChannels()
	if !ok {
		// 冷缓存：立即返回占位并后台预热，前端轮询填充
		s.mgr.WarmChannels()
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "collected": false,
			"total_sources": 0, "valid": 0, "invalid": 0, "rate": "0%", "files": 0,
			"message": "正在采集源数据，请稍候自动刷新…",
		})
		return
	}
	st := s.dashStatsFrom(ch, rep)
	st["ok"] = true
	st["collected"] = true
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) hDashChannelStats(w http.ResponseWriter, r *http.Request) {
	channels, _, ok := s.mgr.PeekChannels()
	if !ok {
		s.mgr.WarmChannels()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "collected": false, "channels": []any{}, "total": 0})
		return
	}
	groups := map[string]int{}
	for _, ch := range channels {
		g := ch.Group
		if g == "" {
			g = "未分类"
		}
		groups[g]++
	}
	type grp struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	var list []grp
	for k, v := range groups {
		list = append(list, grp{Name: k, Count: v})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Count > list[j].Count })
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "collected": true, "channels": list, "total": len(list)})
}

func (s *Server) hDashStatus(w http.ResponseWriter, r *http.Request) {
	st := s.dashStats()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"sources": map[string]any{
			"total":   st["total_sources"],
			"valid":   st["valid"],
			"invalid": st["invalid"],
			"rate":    st["rate"],
		},
		"system":  s.systemInfoMap(),
		"test":    map[string]any{"status": "idle", "last_run": nil},
		"version": "1.0.0",
	})
}

func (s *Server) hDashSystem(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"system":  s.systemInfoMap(),
		"network": s.cfg.GetNetworkConfig(),
	})
}

func (s *Server) hDashTestInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"status":  "idle",
		"message": "暂无测试记录",
	})
}
