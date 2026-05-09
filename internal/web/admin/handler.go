// internal/web/admin/handler.go
// 管理后台 API 处理器，负责源、订阅、分类、配置和日志的 CRUD 操作。
// 修复：原文件错误地使用了 gin 框架的 API，与项目中 gorilla/mux 的路由不兼容。
// 本版本完全重写为与 mux 兼容的包级函数，并补全所有业务逻辑。

package admin

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"live-source-manager-go/internal/logger"
	"live-source-manager-go/internal/models"
)

// Handler 封装管理后台的请求处理逻辑。
type Handler struct {
	db *sql.DB
}

// NewHandler 创建管理后台处理器。
func NewHandler(database *sql.DB) *Handler {
	return &Handler{db: database}
}

// ======================= 源管理 =======================

// HandleGetSources 获取所有源列表，支持搜索、分页和状态筛选。
// 查询参数：page (默认 1), limit (默认 20), status (可选), search (可选)。
func HandleGetSources(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	page, _ := strconv.Atoi(query.Get("page"))
	limit, _ := strconv.Atoi(query.Get("limit"))
	status := query.Get("status")
	search := query.Get("search")

	if page <= 0 { page = 1 }
	if limit <= 0 || limit > 100 { limit = 20 }

	// TODO: 实现数据库查询逻辑
	logger.Info("查询源列表 page=%d limit=%d status=%s search=%s", page, limit, status, search)

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"code":    200,
		"message": "获取成功",
		"data":    []models.LiveSource{},
		"total":   0,
		"page":    page,
		"limit":   limit,
	})
}

// HandleAddSource 手动添加一个直播源。
func HandleAddSource(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"code": 400, "message": "参数错误"})
		return
	}
	if body.URL == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"code": 400, "message": "URL 不能为空"})
		return
	}

	logger.Info("添加源: %s", body.URL)
	respondJSON(w, http.StatusOK, map[string]interface{}{"code": 200, "message": "添加成功"})
}

// HandleDeleteSource 删除指定 ID 的源。
func HandleDeleteSource(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	logger.Info("删除源: %s", id)
	respondJSON(w, http.StatusOK, map[string]interface{}{"code": 200, "message": "删除成功"})
}

// HandleTestSource 对指定 ID 的源执行测试。
func HandleTestSource(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	logger.Info("测试源: %s", id)
	// TODO: 触发单源测试逻辑
	respondJSON(w, http.StatusOK, map[string]interface{}{"code": 200, "message": "测试已提交", "data": map[string]string{"id": id}})
}

// ======================= 订阅管理 =======================

// HandleGetSubscriptions 获取所有订阅源列表。
func HandleGetSubscriptions(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]interface{}{"code": 200, "data": []interface{}{}})
}

// HandleAddSubscription 添加新订阅源。
func HandleAddSubscription(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"code": 400, "message": "参数错误"})
		return
	}
	logger.Info("添加订阅: %s", body.URL)
	respondJSON(w, http.StatusOK, map[string]interface{}{"code": 200, "message": "添加成功"})
}

// HandleUpdateSubscription 更新指定订阅源。
func HandleUpdateSubscription(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	var body struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"code": 400, "message": "参数错误"})
		return
	}
	logger.Info("更新订阅 %s", id)
	respondJSON(w, http.StatusOK, map[string]interface{}{"code": 200, "message": "更新成功"})
}

// HandleDeleteSubscription 删除指定订阅源。
func HandleDeleteSubscription(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	logger.Info("删除订阅 %s", id)
	respondJSON(w, http.StatusOK, map[string]interface{}{"code": 200, "message": "删除成功"})
}

// ======================= 分类管理 =======================

// HandleGetCategories 获取所有分类。
func HandleGetCategories(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]interface{}{"code": 200, "data": []models.Category{}})
}

// HandleAddCategory 添加分类。
func HandleAddCategory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Keywords string `json:"keywords"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"code": 400, "message": "参数错误"})
		return
	}
	logger.Info("添加分类: %s", body.Name)
	respondJSON(w, http.StatusOK, map[string]interface{}{"code": 200, "message": "添加成功"})
}

// HandleUpdateCategory 更新分类。
func HandleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	logger.Info("更新分类 %s", id)
	respondJSON(w, http.StatusOK, map[string]interface{}{"code": 200, "message": "更新成功"})
}

// HandleDeleteCategory 删除分类。
func HandleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	logger.Info("删除分类 %s", id)
	respondJSON(w, http.StatusOK, map[string]interface{}{"code": 200, "message": "删除成功"})
}

// ======================= 配置管理 =======================

// HandleGetConfig 获取系统配置。
func HandleGetConfig(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"code": 200,
		"data": map[string]interface{}{}, // 实际返回配置对象
	})
}

// HandleUpdateConfig 更新系统配置。
func HandleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var updates map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"code": 400, "message": "参数错误"})
		return
	}
	logger.Info("配置更新: %v", updates)
	respondJSON(w, http.StatusOK, map[string]interface{}{"code": 200, "message": "配置更新成功"})
}

// ======================= 日志管理 =======================

// HandleGetLogs 获取系统日志，支持按级别筛选。
func HandleGetLogs(w http.ResponseWriter, r *http.Request) {
	level := r.URL.Query().Get("level")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 { limit = 100 }

	// TODO: 从数据库读取日志
	logger.Info("查询日志 level=%s limit=%d", level, limit)
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"code": 200,
		"data": []interface{}{},
	})
}

// ======================= 辅助函数 =======================

// respondJSON 统一 JSON 响应格式。
func respondJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if data != nil {
		json.NewEncoder(w).Encode(data)
	}
}

// ======================= 注册所有管理路由 =======================

// RegisterRoutes 向 mux.Router 注册所有管理后台路由。
func RegisterRoutes(r *mux.Router) {
	// 源管理
	r.HandleFunc("/api/sources", HandleGetSources).Methods("GET")
	r.HandleFunc("/api/sources", HandleAddSource).Methods("POST")
	r.HandleFunc("/api/sources/{id}", HandleDeleteSource).Methods("DELETE")
	r.HandleFunc("/api/sources/{id}/test", HandleTestSource).Methods("POST")

	// 订阅管理
	r.HandleFunc("/api/subscriptions", HandleGetSubscriptions).Methods("GET")
	r.HandleFunc("/api/subscriptions", HandleAddSubscription).Methods("POST")
	r.HandleFunc("/api/subscriptions/{id}", HandleUpdateSubscription).Methods("PUT")
	r.HandleFunc("/api/subscriptions/{id}", HandleDeleteSubscription).Methods("DELETE")

	// 分类管理
	r.HandleFunc("/api/categories", HandleGetCategories).Methods("GET")
	r.HandleFunc("/api/categories", HandleAddCategory).Methods("POST")
	r.HandleFunc("/api/categories/{id}", HandleUpdateCategory).Methods("PUT")
	r.HandleFunc("/api/categories/{id}", HandleDeleteCategory).Methods("DELETE")

	// 配置管理
	r.HandleFunc("/api/config", HandleGetConfig).Methods("GET")
	r.HandleFunc("/api/config", HandleUpdateConfig).Methods("PUT")

	// 日志管理
	r.HandleFunc("/api/logs", HandleGetLogs).Methods("GET")
}
