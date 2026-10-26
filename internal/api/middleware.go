package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"agent-dashboard/internal/core/auth"
	"agent-dashboard/internal/store"
)

type contextKey string

const (
	UserContextKey contextKey = "user"
)

// RateLimiterMiddleware wraps core rate limiting logic with concurrency synchronization.
type RateLimiterMiddleware struct {
	mu    sync.Mutex
	state auth.LimiterState
}

func NewRateLimiterMiddleware(cfg auth.RateLimiterConfig) *RateLimiterMiddleware {
	return &RateLimiterMiddleware{
		state: auth.NewLimiterState(cfg),
	}
}

func (rl *RateLimiterMiddleware) Limit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		now := time.Now()

		rl.mu.Lock()
		var cmds []auth.LimiterCommand
		rl.state, cmds = auth.Step(rl.state, auth.CheckAllowed{Key: ip, Now: now})
		rl.mu.Unlock()

		for _, cmd := range cmds {
			if res, ok := cmd.(auth.CheckResult); ok {
				if !res.Allowed {
					w.Header().Set("Retry-After", res.RetryAfter.String())
					writeJSON(w, http.StatusTooManyRequests, map[string]string{
						"error": "too many attempts, please try again later",
					})
					return
				}
			}
		}

		next(w, r)
	}
}

func (rl *RateLimiterMiddleware) RecordAttempt(ip string, success bool) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.state, _ = auth.Step(rl.state, auth.RecordAttempt{
		Key:     ip,
		Success: success,
		Now:     time.Now(),
	})
}

// SecurityHeadersMiddleware adds strict security headers.
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self' ws: wss:;")
		next.ServeHTTP(w, r)
	})
}

// CSRFProtectionMiddleware ensures mutating requests originate from our frontend.
func CSRFProtectionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := strings.ToUpper(r.Method)
		if method == "POST" || method == "PUT" || method == "DELETE" || method == "PATCH" {
			// Internal hook IPC endpoints do not require CSRF token
			if strings.HasPrefix(r.URL.Path, "/api/internal/") {
				next.ServeHTTP(w, r)
				return
			}
			// Require custom header which standard browser form submissions cannot forge
			if r.Header.Get("X-Requested-With") == "" && r.Header.Get("X-CSRF-Token") == "" {
				writeJSON(w, http.StatusForbidden, map[string]string{
					"error": "missing required anti-CSRF header (X-Requested-With)",
				})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAuth middleware verifies active session cookie and injects user into context.
func RequireAuth(s *store.Store, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("agentd_session")
		if err != nil || cookie.Value == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "unauthorized: session required",
			})
			return
		}

		u, err := s.ValidateSession(r.Context(), cookie.Value)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "unauthorized: invalid or expired session",
			})
			return
		}

		ctx := context.WithValue(r.Context(), UserContextKey, u)
		next(w, r.WithContext(ctx))
	}
}

// GetUserFromContext retrieves authenticated user from request context.
func GetUserFromContext(ctx context.Context) *store.User {
	if u, ok := ctx.Value(UserContextKey).(*store.User); ok {
		return u
	}
	return nil
}

func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		parts := strings.Split(fwd, ",")
		return strings.TrimSpace(parts[0])
	}
	return r.RemoteAddr
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func RecoveryMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("panic recovered", "error", rec, "path", r.URL.Path)
				writeJSON(w, http.StatusInternalServerError, map[string]string{
					"error": "internal server error",
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
