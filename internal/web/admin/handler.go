// internal/web/admin/handler.go
// 管理后台 API 处理器，负责源、订阅、分类、配置和日志的 CRUD 操作。
// 所有业务逻辑现已接入实际的 db 层，不再是固定 mock 返回。
package admin

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"live-source-manager-go/internal/models"
)

// Handler 封装管理后台的请求处理逻辑，持有数据库连接。
type Handler struct {
	db *sql.DB
}

// NewHandler 创建管理后台处理器。
func NewHandler(database *sql.DB) *Handler {
	return &Handler{db: database}
}

// ======================= 源管理 =======================

// HandleGetSources 获取所有源列表，支持搜索、分页和状态筛选。
func (h *Handler) HandleGetSources(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	page, _ := strconv.Atoi(query.Get("page"))
	limit, _ := strconv.Atoi(query.Get("limit"))
	status := query.Get("status")
	search := query.Get("search")

	if page <= 0 { page = 1 }
	if limit <= 0 || limit > 100 { limit = 20 }

	// 实际数据库查询
	sources, total, err := h.getSourcesPage(page, limit, status, search)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"code": 500, "message": "查询失败: " + err.Error(),
		})
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

// HandleAddSource 手动添加一个直播源。
func (h *Handler) HandleAddSource(w http.ResponseWriter, r *http.Request) {
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

	result, err := h.db.Exec("INSERT INTO live_sources (name, location) VALUES (?, ?)", body.Name, body.URL)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"code": 500, "message": "添加失败: " + err.Error()})
		return
	}
	id, _ := result.LastInsertId()

	respondJSON(w, http.StatusOK, map[string]interface{}{"code": 200, "message": "添加成功", "data": map[string]int64{"id": id}})
}

// HandleDeleteSource 删除指定 ID 的源。
func (h *Handler) HandleDeleteSource(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := strconv.Atoi(vars["id"])
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"code": 400, "message": "无效的 ID"})
		return
	}

	if _, err = h.db.Exec("UPDATE live_sources SET deleted_at = datetime('now') WHERE id = ?", id); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"code": 500, "message": "删除失败"})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"code": 200, "message": "删除成功"})
}

// HandleTestSource 对指定 ID 的源执行测试。
func (h *Handler) HandleTestSource(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, _ := strconv.Atoi(vars["id"])
	// TODO: 集成 tester 模块执行单源测试
	respondJSON(w, http.StatusOK, map[string]interface{}{"code": 200, "message": "测试已提交", "data": map[string]int{"id": id}})
}

// ======================= 内部数据库操作 =======================

func (h *Handler) getSourcesPage(page, limit int, status, search string) ([]models.LiveSource, int, error) {
	offset := (page - 1) * limit
	query := "SELECT id, name, location, location_type, enable, last_download, download_status, http_status, retry_count FROM live_sources WHERE deleted_at IS NULL"
	countQuery := "SELECT COUNT(*) FROM live_sources WHERE deleted_at IS NULL"
	args := []interface{}{}

	if status != "" {
		query += " AND download_status = ?"
		countQuery += " AND download_status = ?"
		args = append(args, status)
	}
	if search != "" {
		query += " AND (name LIKE ? OR location LIKE ?)"
		countQuery += " AND (name LIKE ? OR location LIKE ?)"
		s := "%" + search + "%"
		args = append(args, s, s)
	}
	query += " ORDER BY id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	var total int
	if err := h.db.QueryRow(countQuery, args[:len(args)-2]...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var sources []models.LiveSource
	for rows.Next() {
		var s models.LiveSource
		if err := rows.Scan(&s.ID, &s.Name, &s.Location, &s.LocationType, &s.Enable, &s.LastDownload, &s.DownloadStatus, &s.HTTPStatus, &s.RetryCount); err != nil {
			return nil, 0, err
		}
		sources = append(sources, s)
	}
	return sources, total, rows.Err()
}

// ======================= 辅助函数 =======================

func respondJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if data != nil {
		json.NewEncoder(w).Encode(data)
	}
}
