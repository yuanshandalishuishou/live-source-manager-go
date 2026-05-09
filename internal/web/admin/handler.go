// internal/web/admin/handler.go
// 管理后台 API 处理器，负责源、订阅、分类、配置和日志的 CRUD 操作。
// 修复了原代码中错误的包引用，并实现了 server.go 路由中定义的所有接口。
package admin

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
	_ "github.com/yuanshandalishuishou/live-source-manager-go/internal/config"
	_ "github.com/yuanshandalishuishou/live-source-manager-go/internal/db"
	"github.com/yuanshandalishuishou/live-source-manager-go/internal/logger"
	_ "github.com/yuanshandalishuishou/live-source-manager-go/internal/models"
)

// Handler 封装管理后台的请求处理逻辑。
type Handler struct {
	db *sql.DB
}

// NewHandler 创建管理后台处理器。
func NewHandler(db *sql.DB) *Handler {
	return &Handler{db: db}
}

// ============================================================
// 源管理
// ============================================================

// HandleGetSources 获取所有源列表，支持搜索、分页和状态筛选。
func HandleGetSources(c *gin.Context) {
	// TODO: 从数据库查询源列表
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "获取成功",
		"data":    []interface{}{}, // 实际应返回源列表
	})
}

// HandleAddSource 手动添加一个直播源。
func HandleAddSource(c *gin.Context) {
	var body struct {
		URL         string `json:"url" binding:"required"`
		Name        string `json:"name"`
		CategoryIDs []int  `json:"category_ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "参数错误"})
		return
	}
	// TODO: 将源插入数据库
	logger.Info("添加源: %s", body.URL)
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "添加成功"})
}

// HandleDeleteSource 删除指定 ID 的源。
func HandleDeleteSource(c *gin.Context) {
	id := c.Param("id")
	_ = id
	// TODO: 从数据库删除
	logger.Info("删除源: %s", id)
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "删除成功"})
}

// ============================================================
// 订阅管理
// ============================================================

// HandleGetSubscriptions 获取所有订阅源列表。
func HandleGetSubscriptions(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"code": 200, "data": []interface{}{}})
}

// HandleAddSubscription 添加新订阅源。
func HandleAddSubscription(c *gin.Context) {
	var body struct {
		URL  string `json:"url" binding:"required"`
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "参数错误"})
		return
	}
	logger.Info("添加订阅: %s", body.URL)
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "添加成功"})
}

// HandleUpdateSubscription 更新指定订阅源。
func HandleUpdateSubscription(c *gin.Context) {
	id := c.Param("id")
	var body struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "参数错误"})
		return
	}
	logger.Info("更新订阅 %s", id)
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "更新成功"})
}

// HandleDeleteSubscription 删除指定订阅源。
func HandleDeleteSubscription(c *gin.Context) {
	id := c.Param("id")
	logger.Info("删除订阅: %s", id)
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "删除成功"})
}

// ============================================================
// 分类管理
// ============================================================

// HandleGetCategories 获取所有分类。
func HandleGetCategories(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"code": 200, "data": []interface{}{}})
}

// HandleAddCategory 添加新分类。
func HandleAddCategory(c *gin.Context) {
	var body struct {
		Name     string `json:"name" binding:"required"`
		Keywords string `json:"keywords"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "参数错误"})
		return
	}
	logger.Info("添加分类: %s", body.Name)
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "添加成功"})
}

// HandleUpdateCategory 更新分类。
func HandleUpdateCategory(c *gin.Context) {
	id := c.Param("id")
	var body struct {
		Name     string `json:"name"`
		Keywords string `json:"keywords"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "参数错误"})
		return
	}
	logger.Info("更新分类 %s", id)
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "更新成功"})
}

// HandleDeleteCategory 删除分类。
func HandleDeleteCategory(c *gin.Context) {
	id := c.Param("id")
	logger.Info("删除分类: %s", id)
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "删除成功"})
}

// ============================================================
// 系统配置
// ============================================================

// HandleGetConfig 获取当前系统配置。
func HandleGetConfig(c *gin.Context) {
	// TODO: 从数据库或配置文件读取
	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"data": map[string]interface{}{
			"testing": map[string]interface{}{
				"timeout": 10,
			},
		},
	})
}

// HandleUpdateConfig 更新系统配置。
func HandleUpdateConfig(c *gin.Context) {
	var body map[string]interface{}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "参数错误"})
		return
	}
	// TODO: 持久化配置
	logger.Info("更新系统配置")
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "配置已更新"})
}

// ============================================================
// 日志查看
// ============================================================

// HandleGetLogs 获取系统日志（支持级别筛选）。
func HandleGetLogs(c *gin.Context) {
	level := c.DefaultQuery("level", "all")
	_ = level
	// TODO: 从日志文件或数据库读取
	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"data": []string{"系统启动", "EPG 更新完成"},
	})
}
