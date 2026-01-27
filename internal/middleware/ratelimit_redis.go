package middleware

import (
	"context"
	"fmt"
	"time"
)

// RedisClient interface for Redis operations (allows for mocking)
type RedisClient interface {
	// Eval executes a Lua script
	Eval(ctx context.Context, script string, keys []string, args ...interface{}) (interface{}, error)
}

// RedisRateLimiter implements rate limiting using Redis for distributed deployments
// Uses sliding window algorithm for accurate rate limiting across multiple replicas
type RedisRateLimiter struct {
	client     RedisClient
	rate       int           // requests allowed per window
	window     time.Duration // time window
	keyPrefix  string        // prefix for Redis keys
}

// NewRedisRateLimiter creates a new Redis-based rate limiter
// rate: number of requests allowed per window
// window: time window duration
func NewRedisRateLimiter(client RedisClient, rate int, window time.Duration) *RedisRateLimiter {
	return &RedisRateLimiter{
		client:    client,
		rate:      rate,
		window:    window,
		keyPrefix: "ratelimit:",
	}
}

// NewRedisRateLimiterWithPrefix creates a Redis rate limiter with custom key prefix
func NewRedisRateLimiterWithPrefix(client RedisClient, rate int, window time.Duration, prefix string) *RedisRateLimiter {
	return &RedisRateLimiter{
		client:    client,
		rate:      rate,
		window:    window,
		keyPrefix: prefix,
	}
}

// slidingWindowScript is a Lua script for sliding window rate limiting
// It atomically checks and increments the counter
const slidingWindowScript = `
local key = KEYS[1]
local window = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local now = tonumber(ARGV[3])

-- Remove old entries outside the window
redis.call('ZREMRANGEBYSCORE', key, 0, now - window)

-- Count current requests in window
local count = redis.call('ZCARD', key)

if count < limit then
    -- Add new request with current timestamp as score
    redis.call('ZADD', key, now, now .. '-' .. math.random())
    -- Set expiry on the key
    redis.call('PEXPIRE', key, window)
    return {1, 0}  -- allowed, no wait time
else
    -- Get the oldest entry to calculate retry time
    local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
    if #oldest >= 2 then
        local oldestTime = tonumber(oldest[2])
        local retryAfter = (oldestTime + window) - now
        if retryAfter < 0 then
            retryAfter = 0
        end
        return {0, retryAfter}  -- denied, retry after ms
    end
    return {0, window}  -- denied, retry after full window
end
`

// Allow checks if a request from the given key is allowed
// Returns (true, 0) if allowed, (false, retryAfter) if rate limited
func (rl *RedisRateLimiter) Allow(key string) (bool, time.Duration) {
	ctx := context.Background()
	redisKey := rl.keyPrefix + key
	now := time.Now().UnixMilli()
	windowMs := rl.window.Milliseconds()

	result, err := rl.client.Eval(ctx, slidingWindowScript, []string{redisKey}, windowMs, rl.rate, now)
	if err != nil {
		// On Redis error, fail open (allow the request)
		// In production, you might want to fail closed or use a fallback
		return true, 0
	}

	// Parse result
	arr, ok := result.([]interface{})
	if !ok || len(arr) < 2 {
		return true, 0
	}

	allowed, _ := toInt64(arr[0])
	retryAfterMs, _ := toInt64(arr[1])

	if allowed == 1 {
		return true, 0
	}

	return false, time.Duration(retryAfterMs) * time.Millisecond
}

// toInt64 converts interface{} to int64
func toInt64(v interface{}) (int64, error) {
	switch val := v.(type) {
	case int64:
		return val, nil
	case int:
		return int64(val), nil
	case float64:
		return int64(val), nil
	default:
		return 0, fmt.Errorf("cannot convert %T to int64", v)
	}
}
