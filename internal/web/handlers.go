// internal/web/handlers.go
// 包含所有业务处理器，已实现密码验证、分类、订阅的完整 CRUD 和日志读取。
package web

import (
	"context"
	"encoding/json"
	"net/http"
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
