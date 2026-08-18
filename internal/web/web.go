// Package web implements the IPTV live-source manager web UI: an http.Handler
// with session auth, CSRF protection, embedded templates/static assets, and all
// JSON + HTML routes mirroring the original Python web layer.
package web

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"io"
	"mime"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"live-source-manager-go/internal/auth"
	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/epg"
	"live-source-manager-go/internal/logger"
	"live-source-manager-go/internal/manager"
	"live-source-manager-go/internal/rules"
	"live-source-manager-go/internal/types"
)

const (
	cookieName    = "lsm_session"
	csrfHeader    = "X-CSRF-Token"
	ctxUserKey    = ctxKey(0)
	ctxSessionKey = ctxKey(1)
)

type ctxKey int

// Server holds shared state for the web layer.
type Server struct {
	conn   *sql.DB
	cfg    *config.Config
	mgr    *manager.Manager
	eng    *rules.Engine
	epgMgr *epg.Manager
	tmpl   map[string]*template.Template
	routes []routeEntry
	csrf   sync.Map // sessionID -> csrf token
	mu     sync.RWMutex
	encKey bool // simplified encryption-key flag
}

// routeEntry is one (method, pattern) -> handler binding with {param} support.
type routeEntry struct {
	method  string
	pattern string
	re      *regexp.Regexp
	params  []string
	auth    bool
	h       func(http.ResponseWriter, *http.Request)
}

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// NewRouter builds the full HTTP handler for the web UI.
func NewRouter(conn *sql.DB, cfg *config.Config, mgr *manager.Manager, epgMgr *epg.Manager) http.Handler {
	s := &Server{
		conn:   conn,
		cfg:    cfg,
		mgr:    mgr,
		eng:    rules.NewEngine(conn),
		epgMgr: epgMgr,
		tmpl:   map[string]*template.Template{},
	}

	pages := []string{"dashboard", "sources", "rules", "config", "system", "test", "epg", "epg_sources", "logs", "audit", "users", "login"}
	for _, p := range pages {
		t, err := template.New("base").ParseFS(templateFS, "templates/base.html", "templates/"+p+".html")
		if err != nil {
			logger.L().Error("解析模板失败 %s: %v", p, err)
			continue
		}
		s.tmpl[p] = t
	}

	// ── Page routes (HTML) ───────────────────────────────────────────────
	s.route("GET", "/", s.pageDashboard, true)
	s.route("GET", "/login", s.pageLogin, false)
	s.route("GET", "/sources", s.pageSources, true)
	s.route("GET", "/sources/add", s.pageSourceAdd, true)
	s.route("GET", "/sources/{source_id}/edit", s.pageSourceEdit, true)
	s.route("GET", "/rules", s.pageRules, true)
	s.route("GET", "/config", s.pageConfig, true)
	s.route("GET", "/system", s.pageSystem, true)
	s.route("GET", "/logs", s.pageLogs, true)
	s.route("GET", "/audit", s.pageAudit, true)
	s.route("GET", "/users", s.pageUsers, true)
	s.route("GET", "/test", s.pageTest, true)
	s.route("GET", "/epg", s.pageEpg, true)
	s.route("GET", "/epg/sources", s.pageEpgSources, true)

	// ── Health / auth ──────────────────────────────────────────────────────
	s.route("GET", "/api/health", s.hHealth, false)
	s.route("POST", "/api/auth/login", s.hLogin, false)
	s.route("POST", "/api/auth/logout", s.hLogout, false)
	s.route("GET", "/api/auth/me", s.hMe, true)
	s.route("GET", "/api/auth/csrf-token", s.hCSRFToken, true)
	s.route("GET", "/api/auth/encrypt-key-status", s.hEncryptKeyStatus, true)
	s.route("PUT", "/api/auth/encrypt-key", s.hEncryptKey, true)
	s.route("PUT", "/api/auth/password", s.hPassword, true)

	// ── Sources ────────────────────────────────────────────────────────────
	s.route("GET", "/api/sources", s.hListSources, true)
	s.route("POST", "/api/sources", s.hCreateSource, true)
	s.route("POST", "/api/sources/collect", s.hCollectSources, true)
	s.route("POST", "/api/sources/generate", s.hGenerateM3U, true)
	s.route("GET", "/api/sources/generate/status", s.hGetGenerateStatus, true)
	s.route("GET", "/api/sources/{source_id}", s.hGetSource, true)
	s.route("PUT", "/api/sources/{source_id}", s.hUpdateSource, true)
	s.route("DELETE", "/api/sources/{source_id}", s.hDeleteSource, true)
	s.route("GET", "/api/sources/{source_id}/categories", s.hGetSourceCategories, true)
	s.route("PUT", "/api/sources/{source_id}/categories/{dim_key}", s.hUpdateSourceCategory, true)

	s.route("GET", "/api/source-files", s.hListSourceFiles, true)
	s.route("POST", "/api/source-files", s.hCreateSourceFile, true)
	s.route("GET", "/api/source-files/{file_id}", s.hGetSourceFile, true)
	s.route("PUT", "/api/source-files/{file_id}", s.hUpdateSourceFile, true)
	s.route("DELETE", "/api/source-files/{file_id}", s.hDeleteSourceFile, true)
	s.route("GET", "/api/source-files/{file_id}/channels", s.hGetSourceFileChannels, true)
	s.route("PUT", "/api/source-files/{file_id}/ua", s.hSetSourceFileUA, true)
	s.route("DELETE", "/api/source-files/{file_id}/ua", s.hDeleteSourceFileUA, true)
	s.route("PUT", "/api/source-files/{file_id}/channel-ua", s.hSetChannelUA, true)
	s.route("DELETE", "/api/source-files/{file_id}/channel-ua", s.hDeleteChannelUA, true)

	// ── Rules / mappings / dictionary ─────────────────────────────────────
	s.route("GET", "/api/rules", s.hListRules, true)
	s.route("POST", "/api/rules", s.hCreateRule, true)
	s.route("PUT", "/api/rules/{rule_id}", s.hUpdateRule, true)
	s.route("DELETE", "/api/rules/{rule_id}", s.hDeleteRule, true)
	s.route("PUT", "/api/rules/batch-order", s.hBatchOrderRules, true)
	s.route("GET", "/api/rules/dimensions", s.hListDimensions, true)
	s.route("POST", "/api/rules/dimensions", s.hCreateDimension, true)
	s.route("DELETE", "/api/rules/dimensions/{dim_key}", s.hDeleteDimension, true)
	s.route("GET", "/api/rules/exclusions", s.hListExclusions, true)
	s.route("POST", "/api/rules/exclusions", s.hCreateExclusion, true)
	s.route("DELETE", "/api/rules/exclusions/{exclusion_id}", s.hDeleteExclusion, true)
	s.route("POST", "/api/rules/reimport", s.hReimportRules, true)
	s.route("POST", "/api/rules/reset-defaults", s.hResetRules, true)
	s.route("POST", "/api/rules/test-classification", s.hTestClassification, true)

	s.route("GET", "/api/channel-mappings", s.hListChannelMappings, true)
	s.route("GET", "/api/channel-mapping/{channel_name}", s.hGetChannelMapping, true)
	s.route("PUT", "/api/channel-mapping/{channel_name}", s.hSaveChannelMapping, true)
	s.route("DELETE", "/api/channel-mapping/{channel_name}", s.hDeleteChannelMapping, true)
	s.route("POST", "/api/channel-mappings/batch-import", s.hBatchImportMappings, true)

	s.route("GET", "/api/category-dictionary", s.hGetCategoryDictionary, true)
	s.route("POST", "/api/category-dictionary/reset-defaults", s.hResetCategoryDictionary, true)
	s.route("POST", "/api/category-dictionary/{dimension}", s.hAddCategoryValue, true)
	s.route("PUT", "/api/category-dictionary/{dimension}", s.hSetCategoryDimension, true)
	s.route("DELETE", "/api/category-dictionary/{dimension}/{value}", s.hDeleteCategoryValue, true)

	// ── Config ──────────────────────────────────────────────────────────────
	s.route("GET", "/api/config", s.hGetConfig, true)
	s.route("GET", "/api/config/fields", s.hGetConfigFields, true)
	s.route("GET", "/api/config/history", s.hGetConfigHistory, true)
	s.route("GET", "/api/config/{section}", s.hGetConfigSection, true)
	s.route("PUT", "/api/config", s.hPutConfigBulk, true)
	s.route("PUT", "/api/config/{section}", s.hPutConfigSection, true)
	s.route("POST", "/api/config/validate", s.hValidateConfig, true)
	s.route("POST", "/api/config/reload", s.hReloadConfig, true)

	// ── Dashboard ───────────────────────────────────────────────────────────
	s.route("GET", "/api/dashboard/stats", s.hDashStats, true)
	s.route("GET", "/api/dashboard/channel-stats", s.hDashChannelStats, true)
	s.route("GET", "/api/dashboard/status", s.hDashStatus, true)
	s.route("GET", "/api/dashboard/system", s.hDashSystem, true)
	s.route("GET", "/api/dashboard/test-info", s.hDashTestInfo, true)

	// ── System / logs / audit ──────────────────────────────────────────────
	s.route("GET", "/api/system/info", s.hSystemInfo, true)
	s.route("GET", "/api/system/network", s.hGetNetwork, true)
	s.route("POST", "/api/system/network", s.hUpdateNetwork, true)
	s.route("POST", "/api/github/test-token", s.hGitHubTestToken, true)
	s.route("GET", "/api/logs", s.hLogs, true)
	s.route("GET", "/api/logs/download", s.hLogsDownload, true)
	s.route("GET", "/api/audit", s.hAudit, true)
	s.route("GET", "/api/audit/actions", s.hAuditActions, true)

	// ── Users ────────────────────────────────────────────────────────────────
	s.route("GET", "/api/users", s.hListUsers, true)
	s.route("POST", "/api/users", s.hCreateUser, true)
	s.route("PUT", "/api/users/{user_id}", s.hUpdateUser, true)
	s.route("DELETE", "/api/users/{user_id}", s.hDeleteUser, true)
	s.route("PUT", "/api/users/{user_id}/password", s.hResetUserPassword, true)

	// ── Realtime test ───────────────────────────────────────────────────────
	s.route("POST", "/api/test/trigger", s.hTestTrigger, true)
	s.route("POST", "/api/test/pause", s.hTestPause, true)
	s.route("POST", "/api/test/resume", s.hTestResume, true)
	s.route("POST", "/api/test/cancel", s.hTestCancel, true)
	s.route("GET", "/api/test/status", s.hTestStatus, true)
	s.route("GET", "/api/test/stream", s.hTestStream, true)

	// ── EPG（电子节目单） ───────────────────────────────────────────────────
	s.route("GET", "/api/epg/sources", s.hListEPGSources, true)
	s.route("POST", "/api/epg/sources", s.hCreateEPGSource, true)
	s.route("PUT", "/api/epg/sources/{source_id}", s.hUpdateEPGSource, true)
	s.route("DELETE", "/api/epg/sources/{source_id}", s.hDeleteEPGSource, true)
	s.route("POST", "/api/epg/sources/{source_id}/refresh", s.hRefreshEPGSource, true)
	s.route("POST", "/api/epg/refresh-all", s.hRefreshAllEPG, true)
	s.route("POST", "/api/epg/generate", s.hGenerateEPG, true)
	s.route("GET", "/api/epg/grid", s.hEPGGrid, true)
	s.route("GET", "/api/epg/channels", s.hListEPGChannels, true)
	s.route("GET", "/api/epg/now", s.hEPGNowNext, true)
	s.route("POST", "/api/epg/channels/{channel_id}/match", s.hMatchEPGChannel, true)
	s.route("GET", "/api/epg/status", s.hEPGStatus, true)
	s.route("GET", "/api/epg/url", s.hEPGURL, true)

	return s
}

// route registers a handler (a method value bound to the Server).
func (s *Server) route(method, pattern string, h func(http.ResponseWriter, *http.Request), auth bool) {
	re, names := compilePattern(pattern)
	s.routes = append(s.routes, routeEntry{
		method:  method,
		pattern: pattern,
		re:      re,
		params:  names,
		auth:    auth,
		h:       h,
	})
}

// compilePattern turns "/a/{b}/c" into an anchored regex with named capture groups.
func compilePattern(pattern string) (*regexp.Regexp, []string) {
	parts := strings.Split(strings.Trim(pattern, "/"), "/")
	var sb strings.Builder
	sb.WriteString("^/")
	var names []string
	for i, p := range parts {
		if i > 0 {
			sb.WriteString("/")
		}
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			names = append(names, p[1:len(p)-1])
			sb.WriteString("([^/]+)")
		} else {
			sb.WriteString(regexp.QuoteMeta(p))
		}
	}
	sb.WriteString("$")
	return regexp.MustCompile(sb.String()), names
}

// staticETag caches content-based ETags so we don't hash on every request.
var staticETag sync.Map // relPath -> etag string

// serveStatic serves embedded static assets with a content-hash ETag and
// "no-cache", so browsers always revalidate. embed.FS files have a zero
// modtime; the default http.FileServer therefore emits no Last-Modified/ETag,
// and browsers heuristically cache stale JS forever. This fixes that.
func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	rel := strings.Trim(strings.TrimPrefix(r.URL.Path, "/static/"), "/")
	if rel == "" || strings.Contains(rel, "..") {
		http.NotFound(w, r)
		return
	}
	data, err := staticFS.ReadFile("static/" + rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var etag string
	if v, ok := staticETag.Load(rel); ok {
		etag = v.(string)
	} else {
		sum := sha256.Sum256(data)
		etag = `"` + hex.EncodeToString(sum[:8]) + `"`
		staticETag.Store(rel, etag)
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	ct := mime.TypeByExtension(filepath.Ext(rel))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// ServeHTTP is the master handler: recover + log + session + static + route + auth/CSRF.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer s.recoverPanic(w, r)

	if strings.HasPrefix(r.URL.Path, "/static/") {
		s.serveStatic(w, r)
		return
	}

	// Resolve session -> user in context.
	sid := cookieValue(r, cookieName)
	var user *types.User
	if sid != "" {
		idle := s.cfg.GetInt("Session", "idle_timeout", 1800)
		ttl := s.cfg.GetInt("Session", "session_ttl", 28800)
		user, _ = db.GetSession(s.conn, sid, idle, ttl)
	}
	ctx := context.WithValue(r.Context(), ctxUserKey, user)
	ctx = context.WithValue(ctx, ctxSessionKey, sid)
	r = r.WithContext(ctx)

	entry, params, ok := s.match(r.Method, r.URL.Path)
	if !ok {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "not found"})
		} else {
			http.NotFound(w, r)
		}
		return
	}

	// Auth gate.
	if entry.auth && user == nil {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauthorized"})
		} else {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
		}
		return
	}

	// CSRF gate for state-changing methods.
	// Only /api/auth/login is exempt: it has no session yet, so there is no
	// CSRF token to validate. Logout MUST be CSRF-protected to prevent
	// cross-site forced logout attacks.
	if (r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete) &&
		r.URL.Path != "/api/auth/login" {
		if !s.checkCSRF(r, sid) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "csrf token missing or invalid"})
			return
		}
	}

	r = r.WithContext(context.WithValue(r.Context(), ctxParamKey, params))

	entry.h(w, r)
}

// ctxParamKey carries route params.
const ctxParamKey = ctxKey(2)

func (s *Server) match(method, path string) (*routeEntry, map[string]string, bool) {
	for i := range s.routes {
		e := &s.routes[i]
		if e.method != "" && e.method != method {
			continue
		}
		m := e.re.FindStringSubmatch(path)
		if m == nil {
			continue
		}
		params := map[string]string{}
		for i, n := range e.params {
			params[n] = m[i+1]
		}
		return e, params, true
	}
	return nil, nil, false
}

func (s *Server) recoverPanic(w http.ResponseWriter, r *http.Request) {
	if rec := recover(); rec != nil {
		logger.L().Error("请求处理异常 %s %s: %v", r.Method, r.URL.Path, rec)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "internal error"})
	}
}

// checkCSRF validates the submitted token against the session token.
func (s *Server) checkCSRF(r *http.Request, sid string) bool {
	present := r.Header.Get(csrfHeader)
	if present == "" && r.Method == http.MethodPost {
		_ = r.ParseForm()
		present = r.FormValue("__csrf_token")
	}
	if present == "" {
		return false
	}
	expectedI, ok := s.csrf.Load(sid)
	if !ok {
		return false
	}
	expected, _ := expectedI.(string)
	return auth.ValidateCSRFToken(present, expected)
}

// ── helpers ────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// maxJSONBodyBytes limits request body size to prevent memory exhaustion DoS.
const maxJSONBodyBytes = 2 * 1024 * 1024 // 2 MB — sufficient for all config/API payloads

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, maxJSONBodyBytes)).Decode(v)
}

func currentUser(r *http.Request) *types.User {
	u, _ := r.Context().Value(ctxUserKey).(*types.User)
	return u
}

func sessionID(r *http.Request) string {
	sid, _ := r.Context().Value(ctxSessionKey).(string)
	return sid
}

func routeParam(r *http.Request, name string) string {
	params, _ := r.Context().Value(ctxParamKey).(map[string]string)
	if params == nil {
		return ""
	}
	return params[name]
}

func clientIP(r *http.Request) string {
	if x := r.Header.Get("X-Forwarded-For"); x != "" {
		if i := strings.Index(x, ","); i >= 0 {
			return strings.TrimSpace(x[:i])
		}
		return strings.TrimSpace(x)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) audit(r *http.Request, action, target, detail string) {
	u := currentUser(r)
	uid := 0
	uname := "system"
	if u != nil {
		uid = u.ID
		uname = u.Username
	}
	_ = db.AddAuditLog(s.conn, uid, uname, action, target, detail, clientIP(r))
}

func cookieValue(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

func (s *Server) renderPage(w http.ResponseWriter, name string, data map[string]any) {
	t, ok := s.tmpl[name]
	if !ok {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "base", data); err != nil {
		logger.L().Error("渲染模板 %s 失败: %v", name, err)
	}
}
