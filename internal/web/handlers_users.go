package web

import (
	"net/http"
	"strconv"

	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/source"
	"live-source-manager-go/internal/types"
)

func isAdmin(r *http.Request) bool {
	u := currentUser(r)
	return u != nil && u.Role == "admin"
}

func (s *Server) hListUsers(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "需要管理员权限"})
		return
	}
	users, err := db.ListUsers(s.conn)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "users": users})
}

func (s *Server) hCreateUser(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "需要管理员权限"})
		return
	}
	m := decodeBody(r)
	username := strField(m, "username")
	password := strField(m, "password")
	role := strField(m, "role")
	if role == "" {
		role = "viewer"
	}
	display := strField(m, "display_name")
	if len(username) < 2 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "用户名至少2个字符"})
		return
	}
	if len(password) < 6 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "密码至少6个字符"})
		return
	}
	if role != "admin" && role != "viewer" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "角色无效"})
		return
	}
	id, err := db.CreateUser(s.conn, username, password, role, display)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "创建用户失败（可能已存在）: " + err.Error()})
		return
	}
	s.audit(r, "user_create", username, "创建用户")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "message": "用户已创建"})
}

func (s *Server) hUpdateUser(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "需要管理员权限"})
		return
	}
	id, err := strconv.Atoi(routeParam(r, "user_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "无效的用户 ID"})
		return
	}
	m := decodeBody(r)
	if cur := currentUser(r); cur != nil && cur.ID == id {
		if rv := strField(m, "role"); rv != "" && rv != cur.Role {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "不能修改自己的角色"})
			return
		}
	}
	if _, ok := m["role"]; ok {
		rv := strField(m, "role")
		if rv != "admin" && rv != "viewer" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "角色无效"})
			return
		}
		if _, err := s.conn.Exec("UPDATE users SET role=?, updated_at=datetime('now') WHERE id=?", rv, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	}
	if dv := strField(m, "display_name"); dv != "" {
		if _, err := s.conn.Exec("UPDATE users SET display_name=?, updated_at=datetime('now') WHERE id=?", dv, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	}
	if v, ok := m["is_active"]; ok {
		active := boolFromAny(v)
		av := 0
		if active {
			av = 1
		}
		if _, err := s.conn.Exec("UPDATE users SET is_active=?, updated_at=datetime('now') WHERE id=?", av, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	}
	if pw := strField(m, "password"); pw != "" {
		if len(pw) < 6 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "密码至少6个字符"})
			return
		}
		if err := db.UpdateUserPassword(s.conn, id, pw); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	}
	s.audit(r, "user_update", strconv.Itoa(id), "更新用户信息")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "用户已更新"})
}

func (s *Server) hDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "需要管理员权限"})
		return
	}
	id, err := strconv.Atoi(routeParam(r, "user_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "无效的用户 ID"})
		return
	}
	if cur := currentUser(r); cur != nil && cur.ID == id {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "不能删除自己"})
		return
	}
	if err := db.DeleteUser(s.conn, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.audit(r, "user_delete", strconv.Itoa(id), "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "用户已删除"})
}

func (s *Server) hResetUserPassword(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "需要管理员权限"})
		return
	}
	id, err := strconv.Atoi(routeParam(r, "user_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "无效的用户 ID"})
		return
	}
	if cur := currentUser(r); cur != nil && cur.ID == id {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "请使用密码修改接口修改自己的密码"})
		return
	}
	target, _ := db.GetUserByID(s.conn, id)
	if target == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "用户不存在"})
		return
	}
	m := decodeBody(r)
	pw := strField(m, "new_password")
	if len(pw) < 6 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "密码至少6个字符"})
		return
	}
	if err := db.UpdateUserPassword(s.conn, id, pw); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.audit(r, "user_password_reset", target.Username, "管理员重置用户密码")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "用户 " + target.Username + " 的密码已重置"})
}

// dashboard stats helper reused by pages.
func (s *Server) dashStats() map[string]any {
	channels, report := s.collectChannels()
	return s.dashStatsFrom(channels, report)
}

// dashStatsFrom computes the dashboard stat block from an already-collected
// channel set. Used by the dashboard page/handlers so they can render from the
// in-memory cache without triggering a fresh (slow) network collection.
func (s *Server) dashStatsFrom(channels []types.Channel, report *source.CollectReport) map[string]any {
	total := len(channels)
	invalid := 0
	if report != nil {
		invalid = len(report.Exclusions)
	}
	rate := "0%"
	if total > 0 {
		rate = strconv.Itoa((total-invalid)*100/total) + "%"
	}
	files := 0
	if report != nil {
		files = len(report.SourceFiles)
	}
	return map[string]any{
		"total_sources": total,
		"valid":         total - invalid,
		"invalid":       invalid,
		"rate":          rate,
		"files":         files,
	}
}
