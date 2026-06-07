package httpapi

import (
	"context"
	"net/http"
	"time"
)

// handleHealthz is liveness: the process is up. No dependencies checked.
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeText(w, http.StatusOK, "ok")
}

// handleReadyz is readiness: the DB round-trips. 503 otherwise.
func handleReadyz(ready Readiness) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if _, err := ready.HealthCheck(ctx); err != nil {
			writeText(w, http.StatusServiceUnavailable, "db unavailable")
			return
		}
		writeText(w, http.StatusOK, "ready")
	}
}

func writeText(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(msg))
}
