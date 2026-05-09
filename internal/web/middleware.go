// internal/web/middleware.go
// 基于 gorilla/mux 的 HTTP 中间件集合。
// 原文件错误地使用了 gin 框架的 API，导致与 server.go 中的 mux 路由器不兼容。
// 这里完全重写为 mux 兼容的中间件，并补充请求日志和恢复中间件。
//
// 中间件说明：
//   - corsMiddleware: 处理跨域请求，允许所有来源的 GET/POST/PUT/DELETE/OPTIONS 请求。
//   - requestLoggerMiddleware: 记录每个请求的方法、路径、状态码和耗时。
//   - recoveryMiddleware: 捕获 handler 中的 panic，返回 500 错误并记录日志。
//   - noCacheMiddleware: 对 API 响应设置禁用缓存的头信息。

package web

import (
	"log"
	"net/http"
	"runtime/debug"
	"time"
)

// corsMiddleware 处理跨域资源共享（CORS）请求。
// 允许所有来源访问 API，并设置允许的 HTTP 方法和头信息。
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
		w.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization, X-Requested-With")
		w.Header().Set("Access-Control-Max-Age", "86400") // 预检请求缓存 24 小时

		// 对预检请求直接返回 204 No Content
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// requestLoggerMiddleware 记录每个 HTTP 请求的详细信息。
// 输出格式：方法、路径、状态码、耗时。
// 使用标准库 log 输出，避免对 logger 包的循环依赖。
func requestLoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// 包装 ResponseWriter 以捕获状态码
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		duration := time.Since(start)
		log.Printf("[HTTP] %s %s → %d (%v)",
			r.Method, r.URL.Path, wrapped.statusCode, duration)
	})
}

// responseWriter 包装 http.ResponseWriter，捕获写入的状态码。
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// recoveryMiddleware 捕获 handler 中的 panic，防止整个服务崩溃。
// 发生 panic 时记录完整堆栈信息，并返回 500 Internal Server Error。
func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[PANIC] 请求处理发生 panic: %v\n堆栈:\n%s",
					rec, string(debug.Stack()))
				http.Error(w, `{"error":"内部服务器错误"}`,
					http.StatusInternalServerError)
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// noCacheMiddleware 对 API 响应设置禁用缓存的头信息。
// 用于确保前端始终获取最新数据。
func noCacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		next.ServeHTTP(w, r)
	})
}

// chainMiddleware 按顺序组合多个中间件。
// 第一个参数是最终的 handler，后面的参数按从外到内的顺序包裹。
func chainMiddleware(handler http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}
