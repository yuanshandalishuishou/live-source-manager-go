package web

import (
	"net/http"
	"strings"

	"live-source-manager-go/internal/auth"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/logger"
)

// decodeBody reads a request body that may be JSON or form-encoded into a generic map.
func decodeBody(r *http.Request) map[string]any {
	out := map[string]any{}
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		_ = readJSON(r, &out)
		return out
	}
	_ = r.ParseForm()
	for k := range r.PostForm {
		out[k] = r.PostFormValue(k)
	}
	return out
}

func strField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// boolField 读取一个布尔字段，兼容 JSON 原生 bool 与字符串表示
// （"true"/"1"/"on"/"yes"，大小写不敏感；"false"/"0"/"off"/"no" 视为 false）。
// 第二个返回值 reported 表示请求体中是否真正提供了该字段，
// 使调用方能够区分「未提供（保持原值）」与「显式设为 false」。
// 注意：strField 对非 string 直接返回 ""，无法解析布尔，故此处单独处理。
func boolField(m map[string]any, key string) (val bool, reported bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return false, false
	}
	switch b := v.(type) {
	case bool:
		return b, true
	case string:
		switch strings.ToLower(strings.TrimSpace(b)) {
		case "true", "1", "on", "yes":
			return true, true
		case "false", "0", "off", "no", "":
			return false, true
		}
	}
	return false, false
}

func (s *Server) hHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) hLogin(w http.ResponseWriter, r *http.Request) {
	m := decodeBody(r)
	username := strField(m, "username")
	password := strField(m, "password")
	if username == "" || password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "用户名和密码不能为空"})
		return
	}
	user, err := db.VerifyPassword(s.conn, username, password)
	if err != nil {
		logger.L().Error("登录校验失败: %v", err)
	}
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "用户名或密码错误"})
		return
	}
	sid := auth.GenerateSessionID()
	if err := db.CreateSession(s.conn, sid, user.ID, user.Username, user.Role, 0); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "会话创建失败"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil, // Set Secure flag when served over HTTPS
		SameSite: http.SameSiteLaxMode,
	})
	s.auditRaw(user.ID, user.Username, "login", "", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"user": map[string]any{"username": user.Username, "role": user.Role, "display_name": user.DisplayName},
	})
}

func (s *Server) hLogout(w http.ResponseWriter, r *http.Request) {
	sid := cookieValue(r, cookieName)
	if sid != "" {
		idle := s.cfg.GetInt("Session", "idle_timeout", 1800)
		ttl := s.cfg.GetInt("Session", "session_ttl", 28800)
		if u, err := db.GetSession(s.conn, sid, idle, ttl); err == nil && u != nil {
			s.auditRaw(u.ID, u.Username, "logout", "", clientIP(r))
		}
		_ = db.DeleteSession(s.conn, sid)
		// Clean up CSRF token for this session to prevent token buildup.
		s.csrf.Delete(sid)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) hMe(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauthorized"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"username": u.Username, "role": u.Role, "display_name": u.DisplayName, "is_active": u.IsActive,
	})
}

func (s *Server) hCSRFToken(w http.ResponseWriter, r *http.Request) {
	sid := sessionID(r)
	if sid == "" {
		sid = cookieValue(r, cookieName)
	}
	if sid == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "no session"})
		return
	}
	v, ok := s.csrf.Load(sid)
	var tok string
	if !ok {
		tok = auth.GenerateCSRFToken()
		s.csrf.Store(sid, tok)
	} else {
		tok, _ = v.(string)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "token": tok})
}

func (s *Server) hEncryptKeyStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	v := s.encKey
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "has_custom_key": v})
}

func (s *Server) hEncryptKey(w http.ResponseWriter, r *http.Request) {
	m := decodeBody(r)
	newKey := strField(m, "new_key")
	if len(newKey) < 16 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "密钥长度至少16位"})
		return
	}
	s.mu.Lock()
	s.encKey = true
	s.mu.Unlock()
	s.audit(r, "encrypt_key_update", "app_config", "密钥已更新")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "密钥已更新"})
}

func (s *Server) hPassword(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauthorized"})
		return
	}
	m := decodeBody(r)
	oldP := strField(m, "old_password")
	newP := strField(m, "new_password")
	if oldP == "" || newP == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "旧密码和新密码不能为空"})
		return
	}
	if len(newP) < 6 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "新密码长度至少6个字符"})
		return
	}
	if oldP == newP {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "新密码不能与旧密码相同"})
		return
	}
	verified, err := db.VerifyPassword(s.conn, u.Username, oldP)
	if err != nil || verified == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "旧密码错误"})
		return
	}
	if err := db.UpdateUserPassword(s.conn, u.ID, newP); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "密码修改失败"})
		return
	}
	s.audit(r, "password_change", "self", "用户修改自身密码")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "密码已修改"})
}

// auditRaw records an audit entry with explicit user identity (login/logout paths,
// when the request context user may not yet be resolved).
func (s *Server) auditRaw(uid int, uname, action, target, ip string) {
	_ = db.AddAuditLog(s.conn, uid, uname, action, target, "", ip)
}
