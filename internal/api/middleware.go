package api

import (
	"net/http"
	"runtime/debug"
	"time"

	"github.com/rs/zerolog/log"
)

// recoverPanic is a middleware that turns a handler panic into a logged stack
// trace and a 500 response, instead of a silently dropped connection.
func recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Error().Str("method", r.Method).Str("path", r.URL.Path).Interface("panic", v).Bytes("stack", debug.Stack()).Msg("panic recovered")
				writeError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// logRequests is a middleware that logs each request's method, path and status.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		log.Info().Str("method", r.Method).Str("path", r.URL.Path).Int("status", rec.status).Dur("duration", time.Since(start).Round(time.Millisecond)).Msg("request")
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap exposes the underlying ResponseWriter so http.ResponseController can
// reach its Hijacker/Flusher — required for the WebSocket upgrade to work
// through this middleware.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}
