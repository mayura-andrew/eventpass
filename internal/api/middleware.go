// SPDX-License-Identifier: Apache-2.0

package api

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// CSRFHeader must be present on every mutating API request. Browsers only
// send custom headers from same-origin scripts, so together with SameSite
// cookies it stops cross-site form posts.
const CSRFHeader = "X-EventPass-CSRF"

func csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if strings.HasPrefix(r.URL.Path, "/api/") && r.Header.Get(CSRFHeader) == "" {
				writeError(w, r, errCSRF)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// contentSecurityPolicy forbids inline scripts. The app is a static bundle
// served from this origin; maps tiles are added when M3 needs them.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; " +
	"img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; object-src 'none'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func securityHeaders(secure bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		if h.Get("Content-Security-Policy") == "" {
			h.Set("Content-Security-Policy", contentSecurityPolicy)
		}
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		camera := "()"
		if r.URL.Path == "/scan" || strings.HasPrefix(r.URL.Path, "/scan/") {
			camera = "(self)"
		}
		h.Set("Permissions-Policy", "camera="+camera+", microphone=(), geolocation=(), payment=()")
		if secure {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// logging records method, path and status. Query strings are left out: they
// can carry search terms with names or emails.
func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			return
		}
		slog.Debug("http", "method", r.Method, "path", r.URL.Path, "status", rec.status, "ms", time.Since(start).Milliseconds())
	})
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(v)
				}
				slog.Error("panic", "path", r.URL.Path, "value", v)
				writeError(w, r, errorf(http.StatusInternalServerError, "internal", "Something went wrong."))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// limiter is a set of token buckets keyed by IP or email.
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	every   rate.Limit
	burst   int
	now     func() time.Time
}

type bucket struct {
	l    *rate.Limiter
	seen time.Time
}

// newLimiter allows burst requests at once, refilling n per period.
func newLimiter(n int, per time.Duration, burst int) *limiter {
	return &limiter{buckets: map[string]*bucket{}, every: rate.Limit(float64(n) / per.Seconds()), burst: burst, now: time.Now}
}

func (l *limiter) allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buckets) > 50000 {
		for k, b := range l.buckets {
			if now.Sub(b.seen) > 10*time.Minute {
				delete(l.buckets, k)
			}
		}
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{l: rate.NewLimiter(l.every, l.burst)}
		l.buckets[key] = b
	}
	b.seen = now
	return b.l.AllowN(now, 1)
}

// clientIP is the caller's address. X-Forwarded-For is only trusted behind
// a known reverse proxy (TRUST_PROXY=1), otherwise anyone could spoof it to
// dodge rate limits.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
