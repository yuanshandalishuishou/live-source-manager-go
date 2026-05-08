package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/config"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/db"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/epg"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/filter"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/generator"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/logger"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/models"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/rtmp"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/source"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/tester"
)

// Handler 聚合所有依赖的处理器
type Handler struct {
	cfg        *config.Config
	db         *db.DB
	tester     *tester.Tester
	filter     *filter.Filter
	generator  *generator.Generator
	sourceMgr  *source.Manager
	epgMgr     *epg.Manager
	rtmpMgr    *rtmp.Manager
	jwtManager *JWTManager
}

// NewHandler 创建处理器实例
func NewHandler(cfg *config.Config, database *db.DB, t *tester.Tester, f *filter.Filter,
	gen *generator.Generator, sm *source.Manager, em *epg.Manager, rm *rtmp.Manager, jwtMgr *JWTManager) *Handler {
	return &Handler{
		cfg:        cfg,
		db:         database,
		tester:     t,
		filter:     f,
		generator:  gen,
		sourceMgr:  sm,
		epgMgr:     em,
		rtmpMgr:    rm,
		jwtManager: jwtMgr,
	}
}

// RegisterRoutes 注册所有 API 路由到给定的 router
func (h *Handler) RegisterRoutes(r *mux.Router) {
	// 公开接口
	r.HandleFunc("/api/login", h.handleLogin).Methods("POST")

	// 需要认证的接口
	protected := r.PathPrefix("/api").Subrouter()
	protected.Use(h.authMiddleware)

	protected.HandleFunc("/stats", h.handleStats).Methods("GET")
	protected.HandleFunc("/sources", h.handleSources).Methods("GET", "POST")
	protected.HandleFunc("/sources/{id:[0-9]+}", h.handleSourceDetail).Methods("GET", "PUT", "DELETE")
	protected.HandleFunc("/subscriptions", h.handleSubscriptions).Methods("GET", "POST")
	protected.HandleFunc("/subscriptions/{id:[0-9]+}", h.handleSubscriptionDetail).Methods("PUT", "DELETE")
	protected.HandleFunc("/categories", h.handleCategories).Methods("GET", "POST")
	protected.HandleFunc("/display-rules", h.handleDisplayRules).Methods("GET", "POST")
	protected.HandleFunc("/config", h.handleConfig).Methods("GET", "POST")
	protected.HandleFunc("/logs", h.handleLogs).Methods("GET")
	protected.HandleFunc("/scan/hotel", h.handleHotelScan).Methods("POST")
	protected.HandleFunc("/scan/multicast", h.handleMulticastScan).Methods("POST")
	protected.HandleFunc("/preview", h.handlePreview).Methods("GET")
	protected.HandleFunc("/filter/reload", h.handleFilterReload).Methods("POST")
	protected.HandleFunc("/epg/update", h.handleEpgUpdate).Methods("POST")
	protected.HandleFunc("/rtmp/status", h.handleRtmpStatus).Methods("GET")
	protected.HandleFunc("/users", h.handleUsers).Methods("GET", "POST")          // 管理员功能
	protected.HandleFunc("/users/{id:[0-9]+}", h.handleUserDetail).Methods("PUT", "DELETE")
}

// ---------- 认证 ----------

// handleLogin 处理登录请求，返回 JWT
func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		http.Error(w, "请求格式错误", http.StatusBadRequest)
		return
	}
	// 从数据库验证用户
	user, err := h.db.GetUserByUsername(creds.Username)
	if err != nil || !user.IsActive {
		http.Error(w, "用户名或密码错误", http.StatusUnauthorized)
		return
	}
	// 检查密码哈希（bcrypt）
	if !checkPasswordHash(creds.Password, user.PasswordHash) {
		http.Error(w, "用户名或密码错误", http.StatusUnauthorized)
		return
	}
	// 签发 token
	token, err := h.jwtManager.GenerateToken(user.ID, user.Username, user.IsAdmin)
	if err != nil {
		http.Error(w, "服务器内部错误", http.StatusInternalServerError)
		return
	}
	// 更新最后登录时间
	h.db.UpdateUserLastLogin(user.ID, time.Now())
	respondJSON(w, http.StatusOK, map[string]string{"token": token})
}

// authMiddleware JWT 认证中间件
func (h *Handler) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenStr := r.Header.Get("Authorization")
		if len(tokenStr) > 7 && tokenStr[:7] == "Bearer " {
			tokenStr = tokenStr[7:]
		}
		claims, err := h.jwtManager.ValidateToken(tokenStr)
		if err != nil {
			http.Error(w, "认证失败: "+err.Error(), http.StatusUnauthorized)
			return
		}
		// 将用户信息存入 context
		ctx := context.WithValue(r.Context(), "user_id", claims.UserID)
		ctx = context.WithValue(ctx, "is_admin", claims.IsAdmin)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ---------- 仪表盘 ----------

// handleStats 返回仪表盘统计信息
func (h *Handler) handleStats(w http.ResponseWriter, r *http.Request) {
	stats := struct {
		TotalSources     int `json:"total_sources"`
		ActiveSources    int `json:"active_sources"`
		InactiveSources  int `json:"inactive_sources"`
		UnknownSources   int `json:"unknown_sources"`
		LastTestTime     string `json:"last_test_time"`
		TotalEPGPrograms int `json:"total_epg_programs"`
		RTMPStreams      int `json:"rtmp_streams"`
	}{
		TotalSources:     h.db.CountURLSources(),
		ActiveSources:    h.db.CountPassedByStatus("active"),
		InactiveSources:  h.db.CountPassedByStatus("inactive"),
		UnknownSources:   h.db.CountPassedByStatus("unknown"),
		LastTestTime:     h.db.GetLastTestTime(),
		TotalEPGPrograms: h.db.CountEPGPrograms(),
		RTMPStreams:      h.db.CountRTMPStreams("running"),
	}
	respondJSON(w, http.StatusOK, stats)
}

// ---------- 源管理 ----------

// handleSources 获取通过测试的源列表（分页、搜索、过滤）
func (h *Handler) handleSources(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		size, _ := strconv.Atoi(r.URL.Query().Get("size"))
		search := r.URL.Query().Get("search")
		status := r.URL.Query().Get("status")
		if page <= 0 {
			page = 1
		}
		if size <= 0 || size > 100 {
			size = 20
		}
		sources, total, err := h.db.GetPassedSources(page, size, search, status)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"data":  sources,
			"total": total,
			"page":  page,
			"size":  size,
		})
		return
	}
	// POST 请求用于手动添加或更新源（略）
}

// handleSourceDetail 获取/更新/删除单个源
func (h *Handler) handleSourceDetail(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(mux.Vars(r)["id"])
	switch r.Method {
	case "GET":
		src, err := h.db.GetPassedSourceByID(id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		respondJSON(w, http.StatusOK, src)
	case "PUT":
		var updates map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			http.Error(w, "数据格式错误", http.StatusBadRequest)
			return
		}
		err := h.db.UpdatePassedSource(id, updates)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case "DELETE":
		err := h.db.DeletePassedSource(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

// ---------- 订阅管理 ----------

func (h *Handler) handleSubscriptions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		subs, err := h.db.GetAllLiveSources()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		respondJSON(w, http.StatusOK, subs)
	case "POST":
		var sub models.LiveSource
		if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
			http.Error(w, "数据格式错误", http.StatusBadRequest)
			return
		}
		id, err := h.db.InsertLiveSource(&sub)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		respondJSON(w, http.StatusCreated, map[string]int{"id": id})
	}
}

func (h *Handler) handleSubscriptionDetail(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(mux.Vars(r)["id"])
	switch r.Method {
	case "PUT":
		var sub models.LiveSource
		if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
			http.Error(w, "数据格式错误", http.StatusBadRequest)
			return
		}
		sub.ID = id
		if err := h.db.UpdateLiveSource(&sub); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case "DELETE":
		if err := h.db.DeleteLiveSource(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

// ---------- 分类与显示规则 ----------

func (h *Handler) handleCategories(w http.ResponseWriter, r *http.Request) {
	// 实现省略，返回分类列表、新增分类等
}

func (h *Handler) handleDisplayRules(w http.ResponseWriter, r *http.Request) {
	// 实现省略
}

// ---------- 系统配置 ----------

func (h *Handler) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		configs, err := h.db.GetAllConfigs()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		respondJSON(w, http.StatusOK, configs)
	case "POST":
		var updates map[string]string
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			http.Error(w, "数据格式错误", http.StatusBadRequest)
			return
		}
		for key, value := range updates {
			// 记录历史
			oldVal, _ := h.db.GetConfigValue("general", key) // 简化 group
			h.db.UpdateConfigValue("general", key, value)
			h.db.InsertConfigHistory(key, oldVal, value)
		}
		// 通知配置热重载（部分模块需要重启或手动重载）
		respondJSON(w, http.StatusOK, map[string]string{"status": "updated"})
	}
}

// ---------- 日志 ----------

func (h *Handler) handleLogs(w http.ResponseWriter, r *http.Request) {
	level := r.URL.Query().Get("level")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	logs, err := h.db.GetSystemLogs(level, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusOK, logs)
}

// ---------- 扫描触发 ----------

func (h *Handler) handleHotelScan(w http.ResponseWriter, r *http.Request) {
	// 调用酒店源扫描器，此处为占位，实际应调用 collector
	respondJSON(w, http.StatusOK, map[string]string{"status": "hotel scan started"})
}

func (h *Handler) handleMulticastScan(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{"status": "multicast scan started"})
}

// ---------- 过滤器热重载 ----------

func (h *Handler) handleFilterReload(w http.ResponseWriter, r *http.Request) {
	if err := h.filter.Reload(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "filter reloaded"})
}

// ---------- EPG 手动更新 ----------

func (h *Handler) handleEpgUpdate(w http.ResponseWriter, r *http.Request) {
	go func() {
		if err := h.epgMgr.UpdateNow(); err != nil {
			logger.Error("EPG 手动更新失败", err)
		}
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"status": "epg update started"})
}

// ---------- RTMP 状态 ----------

func (h *Handler) handleRtmpStatus(w http.ResponseWriter, r *http.Request) {
	streams, err := h.db.GetRTMPStreams()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusOK, streams)
}

// ---------- 用户管理（管理员） ----------

func (h *Handler) handleUsers(w http.ResponseWriter, r *http.Request) {
	// 检查管理员权限
	if !isAdmin(r.Context()) {
		http.Error(w, "需要管理员权限", http.StatusForbidden)
		return
	}
	if r.Method == "GET" {
		users, err := h.db.GetAllUsers()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		respondJSON(w, http.StatusOK, users)
		return
	}
	// POST 新增用户
	var newUser models.User
	if err := json.NewDecoder(r.Body).Decode(&newUser); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// 密码哈希处理
	hashed, _ := hashPassword(newUser.PasswordHash) // 实际需从请求体读取明文
	newUser.PasswordHash = hashed
	id, err := h.db.InsertUser(&newUser)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusCreated, map[string]int{"id": id})
}

func (h *Handler) handleUserDetail(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r.Context()) {
		http.Error(w, "需要管理员权限", http.StatusForbidden)
		return
	}
	// 更新/删除用户
}

// ---------- 预览生成 ----------

func (h *Handler) handlePreview(w http.ResponseWriter, r *http.Request) {
	content, err := h.db.GetPreviewM3UContent() // 或调用 generator 生成临时内容
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "audio/x-mpegurl")
	w.Write([]byte(content))
}

// ---------- 工具函数 ----------

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func isAdmin(ctx context.Context) bool {
	val, ok := ctx.Value("is_admin").(bool)
	return ok && val
}

// 密码处理函数（需引入 golang.org/x/crypto/bcrypt）
func hashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

func checkPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}
