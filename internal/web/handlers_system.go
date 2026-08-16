package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"live-source-manager-go/internal/db"
)

func (s *Server) systemInfoMap() map[string]any {
	host, _ := os.Hostname()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	net := s.cfg.GetNetworkConfig()
	tokenSet := strings.TrimSpace(s.cfg.Get("GitHub", "api_token", "")) != ""

	// 频道统计：非阻塞取缓存；冷缓存时为 0（与 Python dashboard 系统信息一致）。
	total, valid, invalid := 0, 0, 0
	if ch, rep, ok := s.mgr.PeekChannels(); ok {
		st := s.dashStatsFrom(ch, rep)
		if v, ok2 := st["total_sources"].(int); ok2 {
			total = v
		}
		if v, ok2 := st["valid"].(int); ok2 {
			valid = v
		}
		if v, ok2 := st["invalid"].(int); ok2 {
			invalid = v
		}
	}

	// CPU / 内存占用（跨平台，零依赖）。
	cpu := cpuUsagePercent()
	mem := memoryUsagePercent()

	// ffprobe 可用性（与 Python _get_system_info 对齐）；使用 Tester 实际定位到的二进制，
	// 而非仅 exec.LookPath，从而与实时测试的探测能力保持一致（例如 tools/ffmpeg 下的 ffprobe）。
	ffprobePath, _ := s.mgr.ProbeBinaries()
	ffprobeAvailable := ffprobePath != ""

	return map[string]any{
		"hostname":          host,
		"go_version":        runtime.Version(),
		"platform":          runtime.GOOS + "/" + runtime.GOARCH,
		"num_cpu":           runtime.NumCPU(),
		"goroutines":        runtime.NumGoroutine(),
		"mem_alloc_mb":      m.Alloc / 1024 / 1024,
		"mem_sys_mb":        m.Sys / 1024 / 1024,
		"timestamp":         time.Now().Format("2006-01-02 15:04:05"),
		"total_sources":     total,
		"valid":             valid,
		"invalid":           invalid,
		"cpu":               fmt.Sprintf("%.1f%%", cpu),
		"memory":            fmt.Sprintf("%.1f%%", mem),
		"ffprobe_available": ffprobeAvailable,
		"ffprobe_path":      ffprobePath,
		"github_token_set":  tokenSet,
		"version":           "1.0.0",
		"net": map[string]any{
			"proxy_enabled": net["proxy_enabled"],
			"proxy_type":    net["proxy_type"],
			"proxy_host":    net["proxy_host"],
			"proxy_port":    net["proxy_port"],
			"github_mirror": net["github_mirror"],
			"ipv6_enabled":  net["ipv6_enabled"],
		},
	}
}

func (s *Server) hSystemInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "info": s.systemInfoMap()})
}

func (s *Server) hGetNetwork(w http.ResponseWriter, r *http.Request) {
	net := s.cfg.GetNetworkConfig()
	// Secrets: never return proxy credentials or the GitHub token in API
	// responses. The UI shows set/unset via github_token_set from
	// /api/system/info and only posts a new value when the operator types
	// one (empty = keep existing). Proxy credentials follow the same pattern.
	net["github_token"] = ""
	net["proxy_password"] = ""
	net["proxy_username"] = ""
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "network": net})
}

func (s *Server) hUpdateNetwork(w http.ResponseWriter, r *http.Request) {
	m := decodeBody(r)
	if v, ok := m["proxy_enabled"]; ok {
		s.cfg.Set("Network", "proxy_enabled", boolToStr(v))
	}
	if v, ok := m["proxy_type"]; ok {
		s.cfg.Set("Network", "proxy_type", toStrVal(v))
	}
	if v, ok := m["proxy_host"]; ok {
		s.cfg.Set("Network", "proxy_host", toStrVal(v))
	}
	if v, ok := m["proxy_port"]; ok {
		s.cfg.Set("Network", "proxy_port", toStrVal(v))
	}
	if v, ok := m["github_mirror"]; ok {
		s.cfg.Set("Network", "github_mirror", toStrVal(v))
	}
	if v, ok := m["ipv6_enabled"]; ok {
		s.cfg.Set("Network", "ipv6_enabled", boolToStr(v))
	}
	if v, ok := m["github_token"]; ok {
		if sv := toStrVal(v); sv != "" {
			s.cfg.Set("GitHub", "api_token", sv)
		}
	}
	s.audit(r, "network_update", "Network", "更新网络配置")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "网络配置已更新"})
}

func boolToStr(v any) string {
	switch t := v.(type) {
	case bool:
		if t {
			return "True"
		}
		return "False"
	case string:
		return t
	}
	return "False"
}

func (s *Server) hGitHubTestToken(w http.ResponseWriter, r *http.Request) {
	m := decodeBody(r)
	token := strField(m, "token")
	if token == "" {
		token = s.cfg.Get("GitHub", "api_token", "")
	}
	if token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "未提供 GitHub Token"})
		return
	}
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/rate_limit", nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "valid": false, "error": err.Error()})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "valid": false, "status": resp.StatusCode, "error": string(body)})
		return
	}
	var rate struct {
		Resources struct {
			Core struct {
				Limit     int `json:"limit"`
				Remaining int `json:"remaining"`
				Reset     int `json:"reset"`
			} `json:"core"`
		} `json:"resources"`
	}
	_ = json.Unmarshal(body, &rate)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"valid":     true,
		"limit":     rate.Resources.Core.Limit,
		"remaining": rate.Resources.Core.Remaining,
	})
}

func (s *Server) hLogs(w http.ResponseWriter, r *http.Request) {
	logPath := s.cfg.Get("Logging", "file", "./log/app.log")
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	if lines <= 0 || lines > 5000 {
		lines = 500
	}
	entries := tailFile(logPath, lines)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": logPath, "lines": entries, "count": len(entries)})
}

// maxLogFileSize caps how much of the log file we read into memory. For
// files larger than this, only the tail is read (L16 fix).
const maxLogFileSize = 5 * 1024 * 1024 // 5 MB

func tailFile(path string, n int) []string {
	info, err := os.Stat(path)
	if err != nil {
		return []string{}
	}
	// For large files, read only the last maxLogFileSize bytes.
	var data []byte
	if info.Size() > maxLogFileSize {
		f, err := os.Open(path)
		if err != nil {
			return []string{}
		}
		defer f.Close()
		seek := info.Size() - maxLogFileSize
		if _, err := f.Seek(seek, io.SeekStart); err != nil {
			return []string{}
		}
		data, err = io.ReadAll(f)
		if err != nil {
			return []string{}
		}
	} else {
		data, err = os.ReadFile(path)
		if err != nil {
			return []string{}
		}
	}
	all := strings.Split(string(data), "\n")
	var nonEmpty []string
	for _, l := range all {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, l)
		}
	}
	if len(nonEmpty) > n {
		nonEmpty = nonEmpty[len(nonEmpty)-n:]
	}
	return nonEmpty
}

func (s *Server) hLogsDownload(w http.ResponseWriter, r *http.Request) {
	logPath := s.cfg.Get("Logging", "file", "./log/app.log")
	if _, err := os.Stat(logPath); err != nil {
		http.Error(w, "日志文件不存在", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\"app.log\"")
	http.ServeFile(w, r, logPath)
}

func (s *Server) hAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	action := q.Get("action")
	username := q.Get("username")
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit <= 0 {
		limit = 50
	}
	entries, err := db.GetAuditLogs(s.conn, limit, offset, action, username)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	total := s.countAudit(action, username)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "entries": entries, "total": total, "limit": limit, "offset": offset,
	})
}

func (s *Server) countAudit(action, username string) int {
	qq := "SELECT COUNT(*) FROM audit_logs WHERE 1=1"
	args := []any{}
	if action != "" {
		qq += " AND action = ?"
		args = append(args, action)
	}
	if username != "" {
		qq += " AND username = ?"
		args = append(args, username)
	}
	var cnt int
	_ = s.conn.QueryRow(qq, args...).Scan(&cnt)
	return cnt
}

func (s *Server) hAuditActions(w http.ResponseWriter, r *http.Request) {
	acts, err := db.GetAuditActions(s.conn)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "actions": acts})
}
