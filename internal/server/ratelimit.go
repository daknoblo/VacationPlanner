package server

import (
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ipRateLimiter is a simple per-client-IP token-bucket limiter. It is a basic
// safety net; production deployments should also rate limit at the edge/proxy.
type ipRateLimiter struct {
	mu        sync.Mutex
	clients   map[string]*clientLimiter
	rate      rate.Limit
	burst     int
	lastSweep time.Time
}

type clientLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// newIPRateLimiter allows perMinute sustained requests with the given burst.
func newIPRateLimiter(perMinute, burst int) *ipRateLimiter {
	return &ipRateLimiter{
		clients:   make(map[string]*clientLimiter),
		rate:      rate.Limit(float64(perMinute) / 60.0),
		burst:     burst,
		lastSweep: time.Now(),
	}
}

func (l *ipRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	if now.Sub(l.lastSweep) > 10*time.Minute {
		for k, c := range l.clients {
			if now.Sub(c.lastSeen) > 10*time.Minute {
				delete(l.clients, k)
			}
		}
		l.lastSweep = now
	}

	c, ok := l.clients[ip]
	if !ok {
		c = &clientLimiter{limiter: rate.NewLimiter(l.rate, l.burst)}
		l.clients[ip] = c
	}
	c.lastSeen = now
	return c.limiter.Allow()
}

func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.limiter.allow(clientIP(r)) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
