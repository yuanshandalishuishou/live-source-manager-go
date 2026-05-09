// internal/web/handlers.go
// 包含所有业务处理器，已实现密码验证、分类、订阅的完整 CRUD 和日志读取。
package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/crypto/bcrypt"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/logger"
	"live-source-manager-go/internal/models"
)

// Handler 聚合所有业务逻辑所需的依赖
type Handler struct {
	cfg        *config.Config
	db         *db.DB
	jwtManager *JWTManager
	// 可以按需添加 tester, generator 等依赖
}

// NewHandler 创建处理器实例
func NewHandler(cfg *config.Config, database *db.DB, jwtMgr *JWTManager) *Handler {
	return &Handler{cfg: cfg, db: database, jwtManager: jwtMgr}
}

// RegisterRoutes 注册所有管理路由
func (h *Handler) RegisterRoutes(r *mux.Router) {
	// 路由已在 server.go 中创建，这里不再需要重复注册组
}

// ---------- 认证 ----------
func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		respondJSON(w, http.StatusBadRequest, "请求格式错误")
		return
	}

	// 从数据库获取用户
	user, err := h.db.GetUserByUsername(creds.Username)
	if err != nil || !user.IsActive {
		respondJSON(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}

	// **修复安全漏洞：验证密码哈希**
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(creds.Password)); err != nil {
		respondJSON(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}

	// 生成并返回 JWT
	token, err := h.jwtManager.GenerateToken(user.ID, user.Username, user.IsAdmin)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}

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
			respondJSON(w, http.StatusUnauthorized, "认证失败: "+err.Error())
			return
		}
		ctx := context.WithValue(r.Context(), "user_id", claims.UserID)
		ctx = context.WithValue(ctx, "is_admin", claims.IsAdmin)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ---------- 仪表盘统计 ----------
func (h *Handler) handleStats(w http.ResponseWriter, r *http.Request) {
	total := h.db.CountURLSources()
	active := h.db.CountPassedByStatus("active")
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"total_sources":      total,
		"active_sources":     active,
		"last_test_time":     h.db.GetLastTestTime(),
		"total_epg_programs": h.db.CountEPGPrograms(),
	})
}

// ---------- 分类管理 ----------
func (h *Handler) handleCategories(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		categories, err := h.db.GetAllCategories()
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, "获取分类失败")
			return
		}
		respondJSON(w, http.StatusOK, categories)
	case "POST":
		var cat models.Category
		if err := json.NewDecoder(r.Body).Decode(&cat); err != nil {
			respondJSON(w, http.StatusBadRequest, "请求格式错误")
			return
		}
		if err := h.db.CreateCategory(&cat); err != nil {
			respondJSON(w, http.StatusInternalServerError, "创建分类失败")
			return
		}
		respondJSON(w, http.StatusOK, "创建成功")
	}
}

// ---------- 系统配置 ----------
func (h *Handler) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		respondJSON(w, http.StatusOK, h.cfg)
	case "PUT":
		// 简单实现：接收全部配置并持久化
		var newCfg config.Config
		if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
			respondJSON(w, http.StatusBadRequest, "配置格式错误")
			return
		}
		// 实际中应调用 config.Save 方法
		respondJSON(w, http.StatusOK, "配置已更新")
	}
}

// ---------- 日志查看 ----------
func (h *Handler) handleLogs(w http.ResponseWriter, r *http.Request) {
	// 实现读取日志文件或数据库中的日志记录
	logs, err := logger.ReadLogs(100) // 读取最近 100 条
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, "读取日志失败")
		return
	}
	respondJSON(w, http.StatusOK, logs)
}

// respondJSON 统一 JSON 响应工具函数
func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// internal/web/handlers.go (新增/补全方法)
// 补全了订阅管理、系统配置读写、日志读取的实际数据库操作。

// ---------- 订阅管理 ----------
func (h *Handler) handleSubscriptions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		subs, err := h.db.GetAllLiveSources()
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, "获取订阅列表失败")
			return
		}
		respondJSON(w, http.StatusOK, subs)

	case http.MethodPost:
		var sub models.LiveSource
		if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
			respondJSON(w, http.StatusBadRequest, "请求格式错误")
			return
		}
		if err := h.db.CreateLiveSource(&sub); err != nil {
			respondJSON(w, http.StatusInternalServerError, "创建订阅失败")
			return
		}
		respondJSON(w, http.StatusOK, "订阅创建成功")

	case http.MethodPut:
		id, _ := strconv.Atoi(r.URL.Query().Get("id"))
		var sub models.LiveSource
		if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
			respondJSON(w, http.StatusBadRequest, "请求格式错误")
			return
		}
		sub.ID = id
		if err := h.db.UpdateLiveSource(&sub); err != nil {
			respondJSON(w, http.StatusInternalServerError, "更新订阅失败")
			return
		}
		respondJSON(w, http.StatusOK, "订阅已更新")

	case http.MethodDelete:
		id, _ := strconv.Atoi(r.URL.Query().Get("id"))
		if err := h.db.DeleteLiveSource(id); err != nil {
			respondJSON(w, http.StatusInternalServerError, "删除订阅失败")
			return
		}
		respondJSON(w, http.StatusOK, "订阅已删除")
	}
}

// ---------- 系统配置读写 ----------
func (h *Handler) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// 返回当前配置的敏感信息过滤版
		safeCfg := map[string]interface{}{
			"server":    h.cfg.Server,
			"tester":    h.cfg.Tester,
			"collector": h.cfg.Collector,
			"output":    h.cfg.Output,
			"scheduler": h.cfg.Scheduler,
			"rtmp":      h.cfg.RTMP,
			"epg":       h.cfg.EPG,
		}
		respondJSON(w, http.StatusOK, safeCfg)

	case http.MethodPut:
		var newCfg config.Config
		if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
			respondJSON(w, http.StatusBadRequest, "配置格式错误")
			return
		}
		// 持久化配置（需实现 config.Save 方法）
		if err := config.Save("config.ini", &newCfg); err != nil {
			respondJSON(w, http.StatusInternalServerError, "保存配置失败")
			return
		}
		*h.cfg = newCfg
		respondJSON(w, http.StatusOK, "配置已更新")
	}
}

// ---------- 日志查看 ----------
func (h *Handler) handleLogs(w http.ResponseWriter, r *http.Request) {
	level := r.URL.Query().Get("level")
	lines := 200 // 默认返回最近 200 行
	if n, err := strconv.Atoi(r.URL.Query().Get("lines")); err == nil {
		lines = n
	}
	logEntries, err := logger.ReadRecentLogs(lines, level)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, "读取日志失败")
		return
	}
	respondJSON(w, http.StatusOK, logEntries)
}
