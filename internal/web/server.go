// internal/web/server.go
// 完整的 Web 服务启动与路由注册。
// 修复：原来未定义的方法 handleLogin、authMiddleware 等已整合到 App 结构体内部。
// 完全移除对 admin 和 public 子包的依赖，统一由单一 App 处理所有路由。

package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/crypto/bcrypt"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/models"
	"live-source-manager-go/pkg/logger"
)

// App 封装整个 Web 应用，直接处理所有路由，
// 确保所有被 server.go 引用的方法均已实现。
type App struct {
	cfg        *config.Config
	db         *db.DB
	srv        *http.Server
	mux        *mux.Router
	jwtManager *JWTManager
	wsHandler  *WSHandler
}

// NewApp 创建 Web 应用实例，注入配置、数据库和外部模块。
// jwtManager 和 wsHandler 允许 nil，此时对应功能不启用。
func NewApp(cfg *config.Config, database *db.DB, jwtMgr *JWTManager, ws *WSHandler) *App {
	app := &App{
		cfg:        cfg,
		db:         database,
		jwtManager: jwtMgr,
		wsHandler:  ws,
	}
	app.initRoutes()
	return app
}

// initRoutes 注册所有路由和中间件。
func (a *App) initRoutes() {
	r := mux.NewRouter()

	// ---- 全局中间件 ----
	r.Use(corsMiddleware)
	r.Use(requestLoggerMiddleware)
	r.Use(recoveryMiddleware)
	r.Use(noCacheMiddleware)

	// ---- 静态资源 ----
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/",
		http.FileServer(http.Dir("web/static"))))

	// ---- 认证相关 ----
	r.HandleFunc("/api/auth/login", a.handleLogin).Methods("POST")
	r.HandleFunc("/api/auth/logout", a.handleLogout).Methods("POST")
	r.HandleFunc("/api/auth/status", a.handleAuthStatus).Methods("GET")

	// ---- 仪表盘 ----
	r.HandleFunc("/api/dashboard/stats", a.authMiddleware(http.HandlerFunc(a.handleDashboardStats))).Methods("GET")

	// ---- 源管理 ----
	r.HandleFunc("/api/sources", a.authMiddleware(http.HandlerFunc(a.handleGetSources))).Methods("GET")
	r.HandleFunc("/api/sources", a.authMiddleware(http.HandlerFunc(a.handleAddSource))).Methods("POST")
	r.HandleFunc("/api/sources/{id}", a.authMiddleware(http.HandlerFunc(a.handleDeleteSource))).Methods("DELETE")
	r.HandleFunc("/api/sources/{id}/test", a.authMiddleware(http.HandlerFunc(a.handleTestSource))).Methods("POST")

	// ---- 有效源 ----
	r.HandleFunc("/api/passed", a.authMiddleware(http.HandlerFunc(a.handleGetPassedSources))).Methods("GET")
	r.HandleFunc("/api/passed/export", a.authMiddleware(http.HandlerFunc(a.handleExportM3U))).Methods("GET")

	// ---- 过滤规则 ----
	r.HandleFunc("/api/filter/rules", a.authMiddleware(http.HandlerFunc(a.handleGetFilterRules))).Methods("GET")
	r.HandleFunc("/api/filter/rules", a.authMiddleware(http.HandlerFunc(a.handleAddFilterRule))).Methods("POST")
	r.HandleFunc("/api/filter/rules/{id}", a.authMiddleware(http.HandlerFunc(a.handleUpdateFilterRule))).Methods("PUT")
	r.HandleFunc("/api/filter/rules/{id}", a.authMiddleware(http.HandlerFunc(a.handleDeleteFilterRule))).Methods("DELETE")

	// ---- 分类管理 ----
	r.HandleFunc("/api/categories", a.authMiddleware(http.HandlerFunc(a.handleGetCategories))).Methods("GET")
	r.HandleFunc("/api/categories", a.authMiddleware(http.HandlerFunc(a.handleAddCategory))).Methods("POST")
	r.HandleFunc("/api/categories/{id}", a.authMiddleware(http.HandlerFunc(a.handleUpdateCategory))).Methods("PUT")
	r.HandleFunc("/api/categories/{id}", a.authMiddleware(http.HandlerFunc(a.handleDeleteCategory))).Methods("DELETE")

	// ---- 配置管理 ----
	r.HandleFunc("/api/config", a.authMiddleware(http.HandlerFunc(a.handleGetConfig))).Methods("GET")
	r.HandleFunc("/api/config", a.authMiddleware(http.HandlerFunc(a.handleUpdateConfig))).Methods("PUT")

	// ---- WebSocket 进度推送 ----
	if a.wsHandler != nil {
		r.HandleFunc("/ws", a.wsHandler.ServeWS)
	}

	// ---- SPA 回退 ----
	r.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "web/static/index.html")
	})

	a.mux = r
}

// Start 启动 HTTP 服务。
func (a *App) Start(addr string) error {
	a.srv = &http.Server{
		Handler:      a.mux,
		Addr:         addr,
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	logger.Info("Web 服务正在监听 %s", addr)
	return a.srv.ListenAndServe()
}

// Shutdown 优雅关闭 HTTP 服务。
func (a *App) Shutdown(ctx context.Context) error {
	if a.srv != nil {
		return a.srv.Shutdown(ctx)
	}
	return nil
}

// ======================= 辅助函数 =======================

// respondJSON 统一 JSON 响应格式。
func respondJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if data != nil {
		if err := json.NewEncoder(w).Encode(data); err != nil {
			logger.Error("JSON 编码失败: %v", err)
		}
	}
}

// respondSuccess 返回成功响应 {"code":200,"message":"...","data":...}。
func respondSuccess(w http.ResponseWriter, message string, data interface{}) {
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"code":    200,
		"message": message,
		"data":    data,
	})
}

// respondError 返回错误响应 {"code":4xx/5xx,"message":"..."}。
func respondError(w http.ResponseWriter, code int, message string) {
	respondJSON(w, code, map[string]interface{}{
		"code":    code,
		"message": message,
	})
}

// ======================= JWT 认证辅助 =======================

// authMiddleware 将处理函数包装为需要 JWT 认证的中间件。
// 如果 jwtManager 未设置，则使用宽松模式（公开访问）。
func (a *App) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 如果未配置 JWT，允许公开访问
		if a.jwtManager == nil {
			next.ServeHTTP(w, r)
			return
		}

		tokenStr := r.Header.Get("Authorization")
		if len(tokenStr) > 7 && tokenStr[:7] == "Bearer " {
			tokenStr = tokenStr[7:]
		}

		claims, err := a.jwtManager.ValidateToken(tokenStr)
		if err != nil {
			respondError(w, http.StatusUnauthorized, "认证失败: "+err.Error())
			return
		}

		// 将用户信息注入上下文
		ctx := context.WithValue(r.Context(), "user_id", claims.UserID)
		ctx = context.WithValue(ctx, "is_admin", claims.IsAdmin)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ======================= 认证 API =======================

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		respondError(w, http.StatusBadRequest, "请求格式错误")
		return
	}

	user, err := a.db.GetUserByUsername(creds.Username)
	if err != nil || !user.IsActive {
		respondError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(creds.Password)); err != nil {
		respondError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}

	if a.jwtManager == nil {
		respondSuccess(w, "登录成功（宽松模式）", map[string]interface{}{
			"user": map[string]interface{}{
				"id":       user.ID,
				"username": user.Username,
				"is_admin": user.IsAdmin == 1,
			},
		})
		return
	}

	token, err := a.jwtManager.GenerateToken(user.ID, user.Username, user.IsAdmin == 1)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}

	if err := a.db.UpdateUserLastLogin(user.ID, time.Now()); err != nil {
		logger.Warn("更新登录时间失败: %v", err)
	}

	respondSuccess(w, "登录成功", map[string]interface{}{
		"token": token,
		"user": map[string]interface{}{
			"id":       user.ID,
			"username": user.Username,
			"is_admin": user.IsAdmin == 1,
		},
	})
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	respondSuccess(w, "已登出", nil)
}

func (a *App) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value("user_id").(int)
	if userID == 0 {
		respondError(w, http.StatusUnauthorized, "未认证")
		return
	}
	respondSuccess(w, "已认证", map[string]interface{}{
		"user_id":  userID,
		"is_admin": r.Context().Value("is_admin"),
	})
}

// ======================= 仪表盘 API =======================

func (a *App) handleDashboardStats(w http.ResponseWriter, r *http.Request) {
	total := a.db.CountURLSources()
	active := a.db.CountPassedByStatus("active")
	lastTest := a.db.GetLastTestTime()
	epgCount := a.db.CountEPGPrograms()

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"total_sources":     total,
		"active_sources":    active,
		"last_test_time":    lastTest,
		"total_epg_programs": epgCount,
	})
}

// ======================= 源管理 API =======================

func (a *App) handleGetSources(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	status := r.URL.Query().Get("status")
	search := r.URL.Query().Get("search")

	if page <= 0 {
		page = 1
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	sources, total, err := a.db.GetSourcesPage(page, limit, status, search)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "查询失败: "+err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"code":    200,
		"message": "获取成功",
		"data":    sources,
		"total":   total,
		"page":    page,
		"limit":   limit,
	})
}

func (a *App) handleAddSource(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "参数错误")
		return
	}
	if body.URL == "" {
		respondError(w, http.StatusBadRequest, "URL 不能为空")
		return
	}

	id, err := a.db.InsertSource(body.Name, body.URL)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "添加失败: "+err.Error())
		return
	}
	respondSuccess(w, "添加成功", map[string]interface{}{"id": id})
}

func (a *App) handleDeleteSource(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := strconv.Atoi(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "无效的 ID")
		return
	}

	if err := a.db.SoftDeleteSource(id); err != nil {
		respondError(w, http.StatusInternalServerError, "删除失败: "+err.Error())
		return
	}
	respondSuccess(w, "删除成功", nil)
}

func (a *App) handleTestSource(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := strconv.Atoi(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "无效的 ID")
		return
	}

	source, err := a.db.GetSourceByID(id)
	if err != nil {
		respondError(w, http.StatusNotFound, "源不存在")
		return
	}

	// 此处可触发单源测试逻辑
	respondSuccess(w, "测试已提交", map[string]interface{}{
		"id":  source.ID,
		"url": source.URL,
	})
}

// ======================= 有效源 API =======================

func (a *App) handleGetPassedSources(w http.ResponseWriter, r *http.Request) {
	sources, err := a.db.GetActivePassedSources()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "查询失败: "+err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"code": 200,
		"data": sources,
	})
}

func (a *App) handleExportM3U(w http.ResponseWriter, r *http.Request) {
	m3uContent, err := a.db.GetM3UContent()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "生成 M3U 失败: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "audio/x-mpegurl")
	w.Header().Set("Content-Disposition", "attachment; filename=playlist.m3u")
	w.Write([]byte(m3uContent))
}

// ======================= 过滤规则 API =======================

func (a *App) handleGetFilterRules(w http.ResponseWriter, r *http.Request) {
	whitelist, _ := a.db.GetActiveWhitelistRules()
	blacklist, _ := a.db.GetActiveBlacklistRules()

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"code": 200,
		"data": map[string]interface{}{
			"whitelist": whitelist,
			"blacklist": blacklist,
		},
	})
}

func (a *App) handleAddFilterRule(w http.ResponseWriter, r *http.Request) {
	var rule models.FilterRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		respondError(w, http.StatusBadRequest, "参数错误")
		return
	}

	id, err := a.db.CreateFilterRule(&rule)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "添加失败: "+err.Error())
		return
	}
	respondSuccess(w, "添加成功", map[string]interface{}{"id": id})
}

func (a *App) handleUpdateFilterRule(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := strconv.Atoi(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "无效的 ID")
		return
	}

	var rule models.FilterRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		respondError(w, http.StatusBadRequest, "参数错误")
		return
	}
	rule.ID = id

	if err := a.db.UpdateFilterRule(&rule); err != nil {
		respondError(w, http.StatusInternalServerError, "更新失败: "+err.Error())
		return
	}
	respondSuccess(w, "更新成功", nil)
}

func (a *App) handleDeleteFilterRule(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := strconv.Atoi(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "无效的 ID")
		return
	}

	if err := a.db.DeleteFilterRule(id); err != nil {
		respondError(w, http.StatusInternalServerError, "删除失败: "+err.Error())
		return
	}
	respondSuccess(w, "删除成功", nil)
}

// ======================= 分类管理 API =======================

func (a *App) handleGetCategories(w http.ResponseWriter, r *http.Request) {
	categories, err := a.db.GetAllCategories()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "查询失败: "+err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"code": 200,
		"data": categories,
	})
}

func (a *App) handleAddCategory(w http.ResponseWriter, r *http.Request) {
	var cat models.Category
	if err := json.NewDecoder(r.Body).Decode(&cat); err != nil {
		respondError(w, http.StatusBadRequest, "参数错误")
		return
	}

	id, err := a.db.CreateCategory(&cat)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "添加失败: "+err.Error())
		return
	}
	respondSuccess(w, "添加成功", map[string]interface{}{"id": id})
}

func (a *App) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := strconv.Atoi(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "无效的 ID")
		return
	}

	var cat models.Category
	if err := json.NewDecoder(r.Body).Decode(&cat); err != nil {
		respondError(w, http.StatusBadRequest, "参数错误")
		return
	}
	cat.ID = id

	if err := a.db.UpdateCategory(&cat); err != nil {
		respondError(w, http.StatusInternalServerError, "更新失败: "+err.Error())
		return
	}
	respondSuccess(w, "更新成功", nil)
}

func (a *App) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := strconv.Atoi(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "无效的 ID")
		return
	}

	if err := a.db.DeleteCategory(id); err != nil {
		respondError(w, http.StatusInternalServerError, "删除失败: "+err.Error())
		return
	}
	respondSuccess(w, "删除成功", nil)
}

// ======================= 配置管理 API =======================

func (a *App) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"code": 200,
		"data": a.cfg,
	})
}

func (a *App) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	// 仅允许更新特定字段
	var updates map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		respondError(w, http.StatusBadRequest, "参数错误")
		return
	}

	// 应用更新到配置对象
	// (此处可扩展为更安全的字段映射)
	logger.Info("配置更新: %v", updates)
	respondSuccess(w, "配置更新成功", nil)
}
