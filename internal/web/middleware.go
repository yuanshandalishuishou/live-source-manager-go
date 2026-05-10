// internal/web/middleware.go
// 基于 gorilla/mux 的 HTTP 中间件集合。
// 该文件是 server.go 中所有中间件引用的具体实现。
// 包含：CORS 处理、请求日志、panic 恢复、缓存控制。
package web

import (
	"log"
	"net/http"
	"runtime/debug"
	"time"
)

// corsMiddleware 处理跨域资源共享（CORS）请求。
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
		w.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization, X-Requested-With")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requestLoggerMiddleware 记录每个 HTTP 请求的详细信息。
func requestLoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		log.Printf("[HTTP] %s %s → %d (%v)", r.Method, r.URL.Path, wrapped.statusCode, time.Since(start))
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// recoveryMiddleware 捕获 handler 中的 panic，防止整个服务崩溃。
func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[PANIC] 请求处理发生 panic: %v\n堆栈:\n%s", rec, string(debug.Stack()))
				http.Error(w, `{"error":"内部服务器错误"}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// noCacheMiddleware 对 API 响应设置禁用缓存的头信息。
func noCacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		next.ServeHTTP(w, r)
	})
}
