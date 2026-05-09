// internal/web/server.go
// Web 服务层：负责 HTTP 路由注册、中间件配置、请求分发和优雅关闭。
// 所有业务逻辑（如生成播放列表、解析 EPG、JWT 验证）均通过接口委托给对应的服务模块，
// 确保 Web 层只关注请求处理而不包含具体实现细节。
package web

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/web/admin"
	"live-source-manager-go/pkg/logger"

	"github.com/gin-gonic/gin"
)

// ============================================================
// 服务接口定义
// ============================================================

// PlaylistGenerator 定义生成 M3U 播放列表的接口。
// 具体实现由 playlist 包提供，传入期望格式和源列表即可获得完整内容。
type PlaylistGenerator interface {
	Generate(format string) ([]byte, error)
}

// EPGGenerator 定义生成电子节目单（XMLTV 格式）的接口。
type EPGGenerator interface {
	Generate() ([]byte, error)
}

// Authenticator 定义认证接口，用于验证 JWT Token 是否合法。
// 返回用户标识（可选）或错误。
type Authenticator interface {
	ValidateToken(tokenString string) (string, error)
}

// ============================================================
// App 结构体
// ============================================================

// App 封装了整个 Web 应用所需的依赖和 Gin 引擎实例。
type App struct {
	cfg           *config.Config
	engine        *gin.Engine
	httpSrv       *http.Server
	playlistGen   PlaylistGenerator
	epgGen        EPGGenerator
	authenticator Authenticator
}

// NewApp 创建一个新的 App 实例，并完成路由注册。
// 参数：
//   - cfg: 全局配置对象
//   - playlistGen: M3U 播放列表生成器实现
//   - epgGen: EPG 生成器实现
//   - authenticator: JWT 认证器实现
//
// 返回初始化完毕的 App 指针。
func NewApp(cfg *config.Config, playlistGen PlaylistGenerator, epgGen EPGGenerator, authenticator Authenticator) *App {
	// 设置 Gin 为生产模式，关闭调试输出
	gin.SetMode(gin.ReleaseMode)

	engine := gin.New()

	// 使用自定义中间件
	engine.Use(gin.Logger())     // 请求日志
	engine.Use(gin.Recovery())   // Panic 恢复
	engine.Use(corsMiddleware()) // 跨域支持

	app := &App{
		cfg:           cfg,
		engine:        engine,
		playlistGen:   playlistGen,
		epgGen:        epgGen,
		authenticator: authenticator,
	}

	// 注册所有路由
	app.registerRoutes()

	return app
}

// ============================================================
// 路由注册
// ============================================================

// registerRoutes 将所有 HTTP 端点绑定到 Gin 引擎上。
func (app *App) registerRoutes() {
	// ---------- 公开接口 ----------
	public := app.engine.Group("/api/v1")
	{
		// 播放列表接口：支持按格式筛选（?format=m3u 或 ?format=txt）
		public.GET("/playlist", app.handlePlaylist)
		// EPG 电子节目单接口
		public.GET("/epg.xml", app.handleEPG)
		// 健康检查端点
		public.GET("/health", app.handleHealth)
	}

	// ---------- 管理接口（需 JWT 认证） ----------
	adminGroup := app.engine.Group("/api/v1/admin")
	adminGroup.Use(app.jwtMiddleware())
	{
		// 源管理
		adminGroup.GET("/sources", admin.HandleGetSources)
		adminGroup.POST("/sources", admin.HandleAddSource)
		adminGroup.DELETE("/sources/:id", admin.HandleDeleteSource)
		// 订阅管理
		adminGroup.GET("/subscriptions", admin.HandleGetSubscriptions)
		adminGroup.POST("/subscriptions", admin.HandleAddSubscription)
		adminGroup.PUT("/subscriptions/:id", admin.HandleUpdateSubscription)
		adminGroup.DELETE("/subscriptions/:id", admin.HandleDeleteSubscription)
		// 分类管理
		adminGroup.GET("/categories", admin.HandleGetCategories)
		adminGroup.POST("/categories", admin.HandleAddCategory)
		adminGroup.PUT("/categories/:id", admin.HandleUpdateCategory)
		adminGroup.DELETE("/categories/:id", admin.HandleDeleteCategory)
		// 系统配置
		adminGroup.GET("/config", admin.HandleGetConfig)
		adminGroup.PUT("/config", admin.HandleUpdateConfig)
		// 日志查看
		adminGroup.GET("/logs", admin.HandleGetLogs)
	}

	// 前端静态文件（若配置了前端路径则启用）
	if app.cfg.Web.StaticDir != "" {
		app.engine.Static("/admin", app.cfg.Web.StaticDir)
	}
}

// ============================================================
// 中间件
// ============================================================

// jwtMiddleware 返回 Gin 中间件，用于验证请求头中的 JWT Token。
// 若验证失败则直接返回 401 JSON 响应并中断后续处理。
func (app *App) jwtMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			app.respondError(c, http.StatusUnauthorized, "缺少认证令牌")
			c.Abort()
			return
		}

		// 提取 Bearer Token
		tokenString := authHeader
		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenString = strings.TrimPrefix(authHeader, "Bearer ")
		}

		// 调用认证器验证令牌
		_, err := app.authenticator.ValidateToken(tokenString)
		if err != nil {
			logger.Warn("JWT 验证失败: %v", err)
			app.respondError(c, http.StatusUnauthorized, "无效的认证令牌")
			c.Abort()
			return
		}

		c.Next()
	}
}

// corsMiddleware 处理跨域请求，允许任意来源访问（可根据配置限制）。
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// ============================================================
// 请求处理函数
// ============================================================

// handlePlaylist 处理播放列表请求。
// 从查询参数中读取格式（m3u/txt），默认为 m3u，调用 PlaylistGenerator 生成内容并返回。
func (app *App) handlePlaylist(c *gin.Context) {
	format := c.DefaultQuery("format", "m3u")

	content, err := app.playlistGen.Generate(format)
	if err != nil {
		logger.Error("生成播放列表失败: %v", err)
		app.respondError(c, http.StatusInternalServerError, "无法生成播放列表")
		return
	}

	// 根据格式设置正确的 Content-Type
	contentType := "audio/mpegurl"
	if format == "txt" {
		contentType = "text/plain; charset=utf-8"
	}
	c.Data(http.StatusOK, contentType, content)
}

// handleEPG 处理 EPG 请求，返回 XMLTV 格式的电子节目单。
func (app *App) handleEPG(c *gin.Context) {
	content, err := app.epgGen.Generate()
	if err != nil {
		logger.Error("生成 EPG 失败: %v", err)
		app.respondError(c, http.StatusInternalServerError, "无法生成 EPG 数据")
		return
	}

	c.Data(http.StatusOK, "application/xml; charset=utf-8", content)
}

// handleHealth 健康检查端点，用于负载均衡器或监控系统探测服务状态。
func (app *App) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

// respondError 统一 JSON 格式的错误响应。
func (app *App) respondError(c *gin.Context, code int, message string) {
	c.JSON(code, gin.H{
		"code":    code,
		"message": message,
	})
}

// ============================================================
// 服务器启动与优雅关闭
// ============================================================

// Start 在指定地址启动 HTTP 服务器，并监听系统信号以实现优雅关闭。
// 当收到 SIGINT 或 SIGTERM 时，将在超时时间内尝试平滑关闭，确保正在处理的请求完成。
func (app *App) Start(addr string) error {
	app.httpSrv = &http.Server{
		Addr:         addr,
		Handler:      app.engine,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 在独立 goroutine 中监听操作系统信号
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		sig := <-quit
		logger.Info("收到关闭信号 %v，开始优雅关闭...", sig)

		// 创建一个带超时的 context，给予服务器清理时间
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := app.httpSrv.Shutdown(ctx); err != nil {
			logger.Error("服务器强制关闭: %v", err)
		} else {
			logger.Info("服务器已安全关闭")
		}
	}()

	logger.Info("Web 服务器启动，监听地址: %s", addr)
	// ListenAndServe 会阻塞直到服务器关闭，若关闭不是由 Shutdown 触发的则返回错误
	if err := app.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("服务器启动失败: %w", err)
	}
	return nil
}
