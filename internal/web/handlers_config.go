package web

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/types"
)

var configActions = []string{"config_update", "config_section_update", "config_change", "config_reload"}

func sectionKeySet() map[string]map[string]bool {
	dv := config.DefaultValues()
	out := map[string]map[string]bool{}
	for k := range dv {
		idx := strings.Index(k, ".")
		if idx < 0 {
			continue
		}
		sec, key := k[:idx], k[idx+1:]
		if out[sec] == nil {
			out[sec] = map[string]bool{}
		}
		out[sec][key] = true
	}
	return out
}

func inferType(def string) string {
	if def == "True" || def == "False" {
		return "bool"
	}
	if _, err := strconv.Atoi(def); err == nil {
		return "int"
	}
	if _, err := strconv.ParseFloat(def, 64); err == nil {
		return "float"
	}
	return "string"
}

func (s *Server) hGetConfig(w http.ResponseWriter, r *http.Request) {
	all := s.cfg.GetAll()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": all})
}

func (s *Server) hGetConfigFields(w http.ResponseWriter, r *http.Request) {
	dv := config.DefaultValues()
	ks := sectionKeySet()
	optsMap := config.FieldOptions()
	secMap := config.SecretKeys()
	fields := map[string]map[string]map[string]any{}
	secs := []string{}
	for sec, keys := range ks {
		secs = append(secs, sec)
		fields[sec] = map[string]map[string]any{}
		for key := range keys {
			dk := sec + "." + key
			def := dv[dk]
			val := ""
			set := false
			if secMap[dk] {
				// Secret fields must never return their real value. If a
				// value is stored, hand back a fixed sentinel "********" plus
				// set:true so the UI can show "已保存" without leaking it.
				if raw := s.cfg.GetRaw(sec, key); raw != "" {
					val = "********"
					set = true
				}
			} else {
				val = s.cfg.Get(sec, key, "")
			}
			fields[sec][key] = map[string]any{
				"type":    inferType(def),
				"default": def,
				"value":   val,
				"options": optsMap[dk],
				"secret":  secMap[dk],
				"set":     set,
			}
		}
	}
	sort.Strings(secs)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sections": secs, "fields": fields})
}

func (s *Server) hGetConfigHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("size"))
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 50
	}
	offset := (page - 1) * size

	ph := strings.Repeat("?,", len(configActions))
	ph = ph[:len(ph)-1]
	args := make([]any, 0, len(configActions)+2)
	for _, a := range configActions {
		args = append(args, a)
	}
	totalRow := s.conn.QueryRow("SELECT COUNT(*) FROM audit_logs WHERE action IN ("+ph+")", args...)
	var total int
	_ = totalRow.Scan(&total)

	args = append(args, size, offset)
	rows, err := s.conn.Query("SELECT id, user_id, username, action, target, detail, ip_address, created_at FROM audit_logs WHERE action IN ("+ph+") ORDER BY id DESC LIMIT ? OFFSET ?", args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	defer rows.Close()
	var history []types.AuditLogEntry
	for rows.Next() {
		var e types.AuditLogEntry
		if err := rows.Scan(&e.ID, &e.UserID, &e.Username, &e.Action, &e.Target, &e.Detail, &e.IPAddress, &e.CreatedAt); err != nil {
			break
		}
		history = append(history, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "total": total, "page": page, "size": size, "history": history,
	})
}

func (s *Server) hGetConfigSection(w http.ResponseWriter, r *http.Request) {
	section := routeParam(r, "section")
	all := s.cfg.GetAll()
	sec, ok := all[section]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "配置段落 [" + section + "] 不存在"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "section": section, "data": sec})
}

func (s *Server) hPutConfigBulk(w http.ResponseWriter, r *http.Request) {
	m := decodeBody(r)
	ks := sectionKeySet()
	secretSet := config.SecretKeys()
	written := 0
	for sec, v := range m {
		secMap, ok := v.(map[string]any)
		if !ok {
			continue
		}
		validKeys, known := ks[sec]
		if !known {
			continue
		}
		for k, val := range secMap {
			if !validKeys[k] {
				continue
			}
			// Secret fields: an empty value or the sentinel "********" means
			// "keep the existing stored secret" — never overwrite it.
			if secretSet[sec+"."+k] {
				sv := toStrVal(val)
				if sv == "" || sv == "********" {
					continue
				}
			}
			s.cfg.Set(sec, k, toStrVal(val))
			written++
		}
	}
	s.audit(r, "config_update", "SQLite", "批量更新配置，共 "+strconv.Itoa(written)+" 项")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "配置已保存", "written": written})
}

func (s *Server) hPutConfigSection(w http.ResponseWriter, r *http.Request) {
	section := routeParam(r, "section")
	m := decodeBody(r)
	ks := sectionKeySet()
	secretSet := config.SecretKeys()
	validKeys, known := ks[section]
	if !known {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "未知配置段落 [" + section + "]"})
		return
	}
	written := 0
	for k, val := range m {
		if !validKeys[k] {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "[" + section + "] 不存在字段 \"" + k + "\""})
			return
		}
		// Secret fields: an empty value or the sentinel "********" means
		// "keep the existing stored secret" — never overwrite it.
		if secretSet[section+"."+k] {
			sv := toStrVal(val)
			if sv == "" || sv == "********" {
				continue
			}
		}
		s.cfg.Set(section, k, toStrVal(val))
		written++
	}
	s.audit(r, "config_section_update", "["+section+"]", "保存配置段，共 "+strconv.Itoa(written)+" 项")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "配置段 [" + section + "] 已保存", "written": written})
}

func (s *Server) hValidateConfig(w http.ResponseWriter, r *http.Request) {
	m := decodeBody(r)
	section := strField(m, "section")
	key := strField(m, "key")
	value := strField(m, "value")
	ks := sectionKeySet()
	validKeys, known := ks[section]
	if !known {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": "未知配置段落 [" + section + "]", "coerced_value": ""})
		return
	}
	if !validKeys[key] {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": "[" + section + "] 不存在字段 \"" + key + "\"", "coerced_value": ""})
		return
	}
	dv := config.DefaultValues()
	typ := inferType(dv[section+"."+key])
	switch typ {
	case "int":
		if _, err := strconv.Atoi(value); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": "必须是整数", "coerced_value": ""})
			return
		}
	case "float":
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": "必须是数字", "coerced_value": ""})
			return
		}
	case "bool":
		if !(value == "True" || value == "true" || value == "False" || value == "false" || value == "1" || value == "0") {
			writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": "必须是布尔值", "coerced_value": ""})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true, "error": "", "coerced_value": value})
}

func (s *Server) hReloadConfig(w http.ResponseWriter, r *http.Request) {
	// Config is read live from SQLite on every Get() call — there is no
	// in-memory cache to invalidate. This endpoint exists for UI parity with
	// the Python version and confirms the DB is readable (L14 fix: previously
	// called db.GetAllConfig but discarded the result, implying a reload
	// happened when nothing actually changed).
	all := db.GetAllConfig(s.conn)
	sections := len(all)
	s.audit(r, "config_reload", "SQLite", "配置重载确认（直读模式，无缓存）")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "配置重载完成", "sections": sections})
}

func toStrVal(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "True"
		}
		return "False"
	case nil:
		return ""
	default:
		return ""
	}
}
