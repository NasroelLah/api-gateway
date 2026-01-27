package middleware

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiter interface untuk rate limiting
type RateLimiter interface {
	// Allow checks if a request from the given key is allowed
	// Returns true if allowed, false if rate limited
	// Also returns the time to wait before retry (0 if allowed)
	Allow(key string) (bool, time.Duration)
}

// clientLimiter holds a rate limiter and last seen time for cleanup
type clientLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// InMemoryRateLimiter implements rate limiting using golang.org/x/time/rate
type InMemoryRateLimiter struct {
	mu       sync.RWMutex
	limiters map[string]*clientLimiter
	rate     rate.Limit
	burst    int
	ttl      time.Duration // time after which inactive limiters are cleaned up
	stopCh   chan struct{}
	stopped  bool
}

// NewInMemoryRateLimiter creates a new in-memory rate limiter
// rps: requests per second allowed
// burst: maximum burst size (token bucket capacity)
func NewInMemoryRateLimiter(rps float64, burst int) *InMemoryRateLimiter {
	rl := &InMemoryRateLimiter{
		limiters: make(map[string]*clientLimiter),
		rate:     rate.Limit(rps),
		burst:    burst,
		ttl:      time.Minute * 5, // cleanup limiters not seen for 5 minutes
		stopCh:   make(chan struct{}),
	}

	// Start cleanup goroutine
	go rl.cleanup()

	return rl
}

// NewInMemoryRateLimiterWithTTL creates a rate limiter with custom TTL for cleanup
func NewInMemoryRateLimiterWithTTL(rps float64, burst int, ttl time.Duration) *InMemoryRateLimiter {
	rl := &InMemoryRateLimiter{
		limiters: make(map[string]*clientLimiter),
		rate:     rate.Limit(rps),
		burst:    burst,
		ttl:      ttl,
		stopCh:   make(chan struct{}),
	}

	// Start cleanup goroutine
	go rl.cleanup()

	return rl
}


// Allow checks if a request from the given key is allowed
// Returns (true, 0) if allowed, (false, retryAfter) if rate limited
func (rl *InMemoryRateLimiter) Allow(key string) (bool, time.Duration) {
	rl.mu.Lock()
	cl, exists := rl.limiters[key]
	if !exists {
		cl = &clientLimiter{
			limiter:  rate.NewLimiter(rl.rate, rl.burst),
			lastSeen: time.Now(),
		}
		rl.limiters[key] = cl
	} else {
		cl.lastSeen = time.Now()
	}
	rl.mu.Unlock()

	// Try to get a token
	reservation := cl.limiter.Reserve()
	if !reservation.OK() {
		// This shouldn't happen with our configuration, but handle it
		return false, time.Second
	}

	delay := reservation.Delay()
	if delay > 0 {
		// Cancel the reservation since we're not going to wait
		reservation.Cancel()
		return false, delay
	}

	return true, 0
}

// cleanup periodically removes expired limiters
func (rl *InMemoryRateLimiter) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.cleanupExpired()
		case <-rl.stopCh:
			return
		}
	}
}

// cleanupExpired removes limiters that haven't been seen within TTL
func (rl *InMemoryRateLimiter) cleanupExpired() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for key, cl := range rl.limiters {
		if now.Sub(cl.lastSeen) > rl.ttl {
			delete(rl.limiters, key)
		}
	}
}

// Stop stops the cleanup goroutine
func (rl *InMemoryRateLimiter) Stop() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if !rl.stopped {
		rl.stopped = true
		close(rl.stopCh)
	}
}

// Size returns the number of active limiters (useful for testing/metrics)
func (rl *InMemoryRateLimiter) Size() int {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return len(rl.limiters)
}

// RateLimitMiddleware creates HTTP middleware that applies rate limiting
func RateLimitMiddleware(limiter RateLimiter, getClientKey func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := getClientKey(r)
			allowed, retryAfter := limiter.Allow(key)

			if !allowed {
				w.Header().Set("Retry-After", formatRetryAfter(retryAfter))
				w.Header().Set("X-RateLimit-Retry-After", formatRetryAfter(retryAfter))
				http.Error(w, `{"error": "rate limit exceeded"}`, http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// formatRetryAfter formats duration as seconds for Retry-After header
func formatRetryAfter(d time.Duration) string {
	seconds := int(d.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("%d", seconds)
}

// GetClientIP extracts client IP from request for use as rate limit key
func GetClientIP(r *http.Request) string {
	// Check X-Forwarded-For first (for proxied requests)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	// Check X-Real-IP
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	// Fall back to RemoteAddr
	return r.RemoteAddr
}
