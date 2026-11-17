package middleware

import (
	"fmt"
	"hash/maphash"
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

type limiterShard struct {
	mu       sync.RWMutex
	limiters map[string]*clientLimiter
}

// InMemoryRateLimiter implements rate limiting using golang.org/x/time/rate.
type InMemoryRateLimiter struct {
	shards   [32]limiterShard
	seed     maphash.Seed
	rate     rate.Limit
	burst    int
	ttl      time.Duration
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewInMemoryRateLimiter creates a new in-memory rate limiter
// rps: requests per second allowed
// burst: maximum burst size (token bucket capacity)
func NewInMemoryRateLimiter(rps float64, burst int) *InMemoryRateLimiter {
	return NewInMemoryRateLimiterWithTTL(rps, burst, 5*time.Minute)
}

// NewInMemoryRateLimiterWithTTL creates a rate limiter with custom TTL for cleanup
func NewInMemoryRateLimiterWithTTL(rps float64, burst int, ttl time.Duration) *InMemoryRateLimiter {
	rl := &InMemoryRateLimiter{
		seed:   maphash.MakeSeed(),
		rate:   rate.Limit(rps),
		burst:  burst,
		ttl:    ttl,
		stopCh: make(chan struct{}),
	}

	// Start cleanup goroutine
	go rl.cleanup()

	return rl
}


// Allow checks if a request from the given key is allowed
// Returns (true, 0) if allowed, (false, retryAfter) if rate limited
func (rl *InMemoryRateLimiter) Allow(key string) (bool, time.Duration) {
	shard := &rl.shards[maphash.String(rl.seed, key)%uint64(len(rl.shards))]
	shard.mu.Lock()
	cl, exists := shard.limiters[key]
	if !exists {
		if shard.limiters == nil {
			shard.limiters = make(map[string]*clientLimiter)
		}
		cl = &clientLimiter{limiter: rate.NewLimiter(rl.rate, rl.burst)}
		shard.limiters[key] = cl
	}
	cl.lastSeen = time.Now()
	shard.mu.Unlock()

	// The common allowed path does not need to allocate a reservation.
	if cl.limiter.Allow() {
		return true, 0
	}
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
	now := time.Now()
	for i := range rl.shards {
		shard := &rl.shards[i]
		shard.mu.Lock()
		for key, cl := range shard.limiters {
			if now.Sub(cl.lastSeen) > rl.ttl {
				delete(shard.limiters, key)
			}
		}
		shard.mu.Unlock()
	}
}

// Stop stops the cleanup goroutine
func (rl *InMemoryRateLimiter) Stop() {
	rl.stopOnce.Do(func() { close(rl.stopCh) })
}

// Size returns the number of active limiters (useful for testing/metrics)
func (rl *InMemoryRateLimiter) Size() int {
	count := 0
	for i := range rl.shards {
		shard := &rl.shards[i]
		shard.mu.RLock()
		count += len(shard.limiters)
		shard.mu.RUnlock()
	}
	return count
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
	if ip := GetSanitizedClientIP(r); ip != "" {
		return ip
	}
	return "unknown"
}
