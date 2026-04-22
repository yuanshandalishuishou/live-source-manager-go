// Package web 提供 Web 管理界面和 REST API，包括用户认证、配置管理、源管理、实时进度推送等。
package web

import (
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/filter"
	"live-source-manager-go/internal/generator"
	"live-source-manager-go/internal/geo"
	"live-source-manager-go/internal/rules"
	"live-source-manager-go/internal/source"
	"live-source-manager-go/internal/tester"
	"live-source-manager-go/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

//go:embed templates/* static/*
var embeddedFS embed.FS

// Server Web 服务器
type Server struct {
	cfg        *config.Config
	log        *logger.Logger
	router     *gin.Engine
	db         *sql.DB
	rulesMgr   *rules.Manager
	sourceMgr  *source.Manager
	tester     *tester.Tester
	filter     *filter.Filter
	generator  *generator.Generator
	geo        *geo.Resolver
	wsUpgrader websocket.Upgrader

	// 工作流触发器
	workflowFunc func()
}

// NewServer 创建 Web 服务器实例
func NewServer(cfg *config.Config, log *logger.Logger, db *sql.DB,
	rulesMgr *rules.Manager, sourceMgr *source.Manager, testerInst *tester.Tester,
	filterInst *filter.Filter, generatorInst *generator.Generator, geoResolver *geo.Resolver,
	workflowFunc func()) *Server {

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(corsMiddleware())
	router.Use(requestLoggerMiddleware(log))

	s := &Server{
		cfg:          cfg,
		log:          log,
		router:       router,
		db:           db,
		rulesMgr:     rulesMgr,
		sourceMgr:    sourceMgr,
		tester:       testerInst,
		filter:       filterInst,
		generator:    generatorInst,
		geo:          geoResolver,
		workflowFunc: workflowFunc,
		wsUpgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}

	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	// 静态文件和模板
	if _, err := os.Stat("./web/templates"); err == nil {
		s.router.LoadHTMLGlob("./web/templates/*")
		s.router.Static("/static", "./web/static")
	} else {
		tmplFS, _ := fs.Sub(embeddedFS, "templates")
		staticFS, _ := fs.Sub(embeddedFS, "static")
		s.router.SetHTMLTemplate(template.Must(template.ParseFS(tmplFS, "*.html")))
		s.router.StaticFS("/static", http.FS(staticFS))
	}

	// 公开路由
	s.router.GET("/login", s.LoginPage)
	s.router.POST("/api/login", s.Login)
	s.router.GET("/health", s.HealthCheck)
	s.router.GET("/ready", s.ReadyCheck)

	// API 路由组（需认证）
	api := s.router.Group("/api")
	api.Use(s.AuthMiddleware())
	{
		api.GET("/stats", s.GetStats)
		api.GET("/sources", s.ListSources)
		api.POST("/sources/:id/toggle", s.ToggleSource)
		api.POST("/sources/test", s.TestSingleSource)
		api.GET("/subscriptions", s.ListSubscriptions)
		api.POST("/subscriptions", s.CreateSubscription)
		api.PUT("/subscriptions/:id", s.UpdateSubscription)
		api.DELETE("/subscriptions/:id", s.DeleteSubscription)
		api.GET("/categories", s.ListCategories)
		api.POST("/categories", s.CreateCategory)
		api.PUT("/categories/:id", s.UpdateCategory)
		api.DELETE("/categories/:id", s.DeleteCategory)
		api.GET("/display-rules", s.ListDisplayRules)
		api.PUT("/display-rules", s.UpdateDisplayRules)
		api.GET("/config", s.GetConfig)
		api.POST("/config", s.SaveConfig)
		api.GET("/logs", s.GetLogs)
		api.POST("/scan/hotel", s.TriggerHotelScan)
		api.POST("/scan/multicast", s.TriggerMulticastScan)
		api.POST("/trigger-update", s.TriggerUpdate)
		api.GET("/task/status", s.GetTaskStatus)
	}

	// WebSocket 路由（需认证）
	s.router.GET("/ws/progress", s.AuthMiddleware(), s.WebSocketHandler)

	// 页面路由（需认证）
	pages := s.router.Group("/")
	pages.Use(s.AuthMiddleware())
	{
		pages.GET("/", s.IndexPage)
		pages.GET("/sources", s.SourcesPage)
		pages.GET("/subscriptions", s.SubscriptionsPage)
		pages.GET("/categories", s.CategoriesPage)
		pages.GET("/display-rules", s.DisplayRulesPage)
		pages.GET("/config", s.ConfigPage)
		pages.GET("/logs", s.LogsPage)
		pages.GET("/preview", s.PreviewPage)
	}
}

// Run 启动服务器
func (s *Server) Run() error {
	addr := fmt.Sprintf(":%d", s.cfg.WebServer.Port)
	s.log.Info("Web 管理界面启动于 http://0.0.0.0%s", addr)
	return s.router.Run(addr)
}
