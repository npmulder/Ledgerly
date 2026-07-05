package httpserver

import (
	"context"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"runtime/debug"
	"sync/atomic"
	"time"
)

type requestIDContextKey struct{}

var requestSequence uint64

func requestIDMiddleware(next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = nextRequestID()
		}

		w.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(r.Context(), requestIDContextKey{}, requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFromContext returns the request ID assigned by middleware.
func RequestIDFromContext(ctx context.Context) (string, bool) {
	requestID, ok := ctx.Value(requestIDContextKey{}).(string)
	return requestID, ok
}

func nextRequestID() string {
	seq := atomic.AddUint64(&requestSequence, 1)
	return fmt.Sprintf("req-%d-%d", time.Now().UTC().UnixNano(), seq)
}

func requestLogMiddleware(logger *slog.Logger) func(nethttp.Handler) nethttp.Handler {
	return func(next nethttp.Handler) nethttp.Handler {
		return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			start := time.Now()
			recorder := &statusRecorder{
				ResponseWriter: w,
				status:         nethttp.StatusOK,
			}

			next.ServeHTTP(recorder, r)

			requestID, _ := RequestIDFromContext(r.Context())
			logger.InfoContext(r.Context(), "http request",
				"request_id", requestID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", recorder.status,
				"bytes", recorder.bytes,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

type statusRecorder struct {
	nethttp.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(data []byte) (int, error) {
	n, err := r.ResponseWriter.Write(data)
	r.bytes += n
	return n, err
}

func recoveryMiddleware(logger *slog.Logger) func(nethttp.Handler) nethttp.Handler {
	return func(next nethttp.Handler) nethttp.Handler {
		return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}

				requestID, _ := RequestIDFromContext(r.Context())
				logger.ErrorContext(r.Context(), "http panic recovered",
					"request_id", requestID,
					"panic", recovered,
					"stack", string(debug.Stack()),
				)

				WriteProblem(w, r, Problem{
					Type:   problemTypeInternal,
					Title:  nethttp.StatusText(nethttp.StatusInternalServerError),
					Status: nethttp.StatusInternalServerError,
					Detail: "internal server error",
				})
			}()

			next.ServeHTTP(w, r)
		})
	}
}

func timeoutMiddleware(timeout time.Duration) func(nethttp.Handler) nethttp.Handler {
	return func(next nethttp.Handler) nethttp.Handler {
		if timeout <= 0 {
			return next
		}

		return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
