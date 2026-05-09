// internal/web/server.go
package web

import (
    "context"
    "net/http"
    "time"

    "github.com/gorilla/mux"
    "live-source-manager-go/internal/config"
    "live-source-manager-go/internal/db"
    "live-source-manager-go/pkg/logger"
)

// App 封装 Web 应用
type App struct {
    cfg   *config.Config
    db    *db.DB
    srv   *http.Server
    mux   *mux.Router
}

// NewApp 创建一个新的 Web 应用实例
func NewApp(cfg *config.Config, database *db.DB) *App {
    app := &App{
        cfg: cfg,
        db:  database,
    }
    app.initRoutes()
    return app
}

func (a *App) initRoutes() {
    r := mux.NewRouter()

    // 全局中间件
    r.Use(corsMiddleware)
    r.Use(requestLoggerMiddleware)
    r.Use(recoveryMiddleware)
    r.Use(noCacheMiddleware)

    // 静态资源（前端页面）
    r.PathPrefix("/static/").Handler(http.StripPrefix("/static/",
        http.FileServer(http.Dir("web/static"))))

    // 认证相关
    r.HandleFunc("/api/auth/login", a.handleLogin).Methods("POST")
    r.HandleFunc("/api/auth/logout", a.handleLogout).Methods("POST")
    r.HandleFunc("/api/auth/status", a.handleAuthStatus).Methods("GET")

    // 仪表盘统计
    r.HandleFunc("/api/dashboard/stats", a.authMiddleware(a.handleDashboardStats)).Methods("GET")

    // 源管理
    r.HandleFunc("/api/sources", a.authMiddleware(a.handleGetSources)).Methods("GET")
    r.HandleFunc("/api/sources", a.authMiddleware(a.handleAddSource)).Methods("POST")
    r.HandleFunc("/api/sources/{id}", a.authMiddleware(a.handleDeleteSource)).Methods("DELETE")
    r.HandleFunc("/api/sources/{id}/test", a.authMiddleware(a.handleTestSource)).Methods("POST")

    // 有效源（通过测试的源）
    r.HandleFunc("/api/passed", a.authMiddleware(a.handleGetPassedSources)).Methods("GET")
    r.HandleFunc("/api/passed/export", a.authMiddleware(a.handleExportM3U)).Methods("GET")

    // 过滤规则
    r.HandleFunc("/api/filter/rules", a.authMiddleware(a.handleGetFilterRules)).Methods("GET")
    r.HandleFunc("/api/filter/rules", a.authMiddleware(a.handleAddFilterRule)).Methods("POST")
    r.HandleFunc("/api/filter/rules/{id}", a.authMiddleware(a.handleUpdateFilterRule)).Methods("PUT")
    r.HandleFunc("/api/filter/rules/{id}", a.authMiddleware(a.handleDeleteFilterRule)).Methods("DELETE")

    // 分类管理
    r.HandleFunc("/api/categories", a.authMiddleware(a.handleGetCategories)).Methods("GET")
    r.HandleFunc("/api/categories", a.authMiddleware(a.handleAddCategory)).Methods("POST")
    r.HandleFunc("/api/categories/{id}", a.authMiddleware(a.handleUpdateCategory)).Methods("PUT")
    r.HandleFunc("/api/categories/{id}", a.authMiddleware(a.handleDeleteCategory)).Methods("DELETE")

    // 配置管理
    r.HandleFunc("/api/config", a.authMiddleware(a.handleGetConfig)).Methods("GET")
    r.HandleFunc("/api/config", a.authMiddleware(a.handleUpdateConfig)).Methods("PUT")

    // 前端页面回退（SPA支持）
    r.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        http.ServeFile(w, r, "web/static/index.html")
    })

    a.mux = r
}

// Start 启动 HTTP 服务
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

// Shutdown 优雅关闭
func (a *App) Shutdown(ctx context.Context) error {
    if a.srv != nil {
        return a.srv.Shutdown(ctx)
    }
    return nil
}
