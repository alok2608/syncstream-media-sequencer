package httpx

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

// Middleware is the standard decorator shape.
type Middleware func(http.Handler) http.Handler

// Chain applies middleware so that the first argument is the outermost layer.
func Chain(h http.Handler, middleware ...Middleware) http.Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		h = middleware[i](h)
	}
	return h
}

// CORS answers preflight requests and echoes back only origins that are on the
// allow-list. allowAny relaxes it to "*" for local development and demos.
func CORS(originAllowed func(string) bool, allowAny bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			if origin != "" && originAllowed(origin) {
				if allowAny {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				} else {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					// Caches must not serve one origin's response to another.
					w.Header().Add("Vary", "Origin")
				}
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
				w.Header().Set("Access-Control-Max-Age", "600")
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// statusRecorder captures the status code for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer, which the
// WebSocket upgrade needs.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// RequestLogger logs one line per request.
func RequestLogger(logger *slog.Logger, enabled bool) Middleware {
	return func(next http.Handler) http.Handler {
		if !enabled {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The WebSocket endpoint hijacks the connection and stays open for
			// the life of the client; logging its duration is noise.
			if strings.HasPrefix(r.URL.Path, "/ws") {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			logger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration", time.Since(start).Round(time.Millisecond).String())
		})
	}
}

// Recoverer turns a panic in a handler into a 500 instead of killing the
// process and every live WebSocket connection with it.
func Recoverer(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					// A hijacked connection cannot be written to any more.
					if rec == http.ErrAbortHandler {
						panic(rec)
					}
					logger.Error("panic in handler",
						"path", r.URL.Path, "panic", rec, "stack", string(debug.Stack()))
					WriteError(w, http.StatusInternalServerError, CodeInternal, "something went wrong on our side")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
