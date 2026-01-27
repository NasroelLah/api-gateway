package middleware

import (
	"fmt"
	"testing"
	"time"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
)

const PropertyTestIterations = 100

// TestProperty7_RateLimitingBehavior tests rate limiting behavior
// **Property 7: Rate Limiting Behavior**
// **Validates: Requirements 3.1, 3.2, 3.3**
//
// *For any* client identifier dan sequence of N requests dalam window waktu tertentu,
// jika N <= limit maka semua request diizinkan, jika N > limit maka request ke-(limit+1)
// dan seterusnya ditolak dengan HTTP 429.
func TestProperty7_RateLimitingBehavior(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	// Property: Requests within limit SHALL be allowed
	properties.Property("All requests within burst limit are allowed", prop.ForAll(
		func(burst int, numRequests int) bool {
			if burst < 1 || burst > 100 {
				return true // Skip invalid burst values
			}
			if numRequests < 1 {
				return true // Skip invalid request counts
			}

			// Use high RPS to ensure burst is the limiting factor
			rl := NewInMemoryRateLimiter(1000.0, burst)
			defer rl.Stop()

			clientKey := "test-client"
			allowedCount := 0

			// Make requests up to burst limit
			requestsToMake := numRequests
			if requestsToMake > burst {
				requestsToMake = burst
			}

			for i := 0; i < requestsToMake; i++ {
				allowed, _ := rl.Allow(clientKey)
				if allowed {
					allowedCount++
				}
			}

			// All requests within burst should be allowed
			return allowedCount == requestsToMake
		},
		gen.IntRange(1, 50),  // burst
		gen.IntRange(1, 100), // numRequests
	))

	// Property: Requests exceeding limit SHALL be denied
	properties.Property("Requests exceeding burst limit are denied", prop.ForAll(
		func(burst int, extraRequests int) bool {
			if burst < 1 || burst > 50 {
				return true // Skip invalid burst values
			}
			if extraRequests < 1 || extraRequests > 20 {
				return true // Skip invalid extra request counts
			}

			// Use very low RPS so tokens don't replenish during test
			rl := NewInMemoryRateLimiter(0.001, burst)
			defer rl.Stop()

			clientKey := "test-client-exceed"

			// Exhaust the burst
			for i := 0; i < burst; i++ {
				rl.Allow(clientKey)
			}

			// Additional requests should be denied
			deniedCount := 0
			for i := 0; i < extraRequests; i++ {
				allowed, _ := rl.Allow(clientKey)
				if !allowed {
					deniedCount++
				}
			}

			// All extra requests should be denied
			return deniedCount == extraRequests
		},
		gen.IntRange(1, 50), // burst
		gen.IntRange(1, 20), // extraRequests
	))

	// Property: Different clients have independent limits
	properties.Property("Different clients have independent rate limits", prop.ForAll(
		func(burst int, numClients int) bool {
			if burst < 1 || burst > 20 {
				return true
			}
			if numClients < 2 || numClients > 10 {
				return true
			}

			rl := NewInMemoryRateLimiter(1000.0, burst)
			defer rl.Stop()

			// Each client should be able to make burst requests
			for clientIdx := 0; clientIdx < numClients; clientIdx++ {
				clientKey := "client-" + string(rune('A'+clientIdx))
				allowedCount := 0

				for i := 0; i < burst; i++ {
					allowed, _ := rl.Allow(clientKey)
					if allowed {
						allowedCount++
					}
				}

				if allowedCount != burst {
					return false
				}
			}

			return true
		},
		gen.IntRange(1, 20), // burst
		gen.IntRange(2, 10), // numClients
	))

	// Property: Rate limiter creates per-client limiters
	properties.Property("Rate limiter tracks correct number of clients", prop.ForAll(
		func(numClients int) bool {
			if numClients < 1 || numClients > 50 {
				return true
			}

			rl := NewInMemoryRateLimiter(100.0, 10)
			defer rl.Stop()

			// Make request from each client
			for i := 0; i < numClients; i++ {
				clientKey := "client-" + string(rune('A'+i))
				rl.Allow(clientKey)
			}

			// Should have exactly numClients limiters
			return rl.Size() == numClients
		},
		gen.IntRange(1, 50),
	))

	properties.TestingRun(t)
}

// TestProperty8_RetryAfterHeader tests that rate limited responses include Retry-After header
// **Property 8: Retry-After Header**
// **Validates: Requirements 3.6**
//
// *For any* request yang ditolak karena rate limit, response SHALL mengandung header
// Retry-After dengan nilai positif yang menunjukkan waktu tunggu.
func TestProperty8_RetryAfterHeader(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	// Property: Rate limited requests SHALL return positive Retry-After duration
	properties.Property("Rate limited requests return positive Retry-After duration", prop.ForAll(
		func(burst int) bool {
			if burst < 1 || burst > 20 {
				return true // Skip invalid burst values
			}

			// Use very low RPS so tokens don't replenish
			rl := NewInMemoryRateLimiter(0.001, burst)
			defer rl.Stop()

			clientKey := "test-retry-after"

			// Exhaust the burst
			for i := 0; i < burst; i++ {
				rl.Allow(clientKey)
			}

			// Next request should be denied with positive retry duration
			allowed, retryAfter := rl.Allow(clientKey)

			if allowed {
				return false // Should be denied
			}

			// Retry-After should be positive
			return retryAfter > 0
		},
		gen.IntRange(1, 20),
	))

	// Property: Retry-After value SHALL be reasonable (not excessively long)
	properties.Property("Retry-After value is reasonable", prop.ForAll(
		func(burst int) bool {
			if burst < 1 || burst > 20 {
				return true
			}

			// Use 1 RPS rate
			rl := NewInMemoryRateLimiter(1.0, burst)
			defer rl.Stop()

			clientKey := "test-retry-reasonable"

			// Exhaust the burst
			for i := 0; i < burst; i++ {
				rl.Allow(clientKey)
			}

			// Get retry duration
			_, retryAfter := rl.Allow(clientKey)

			// Retry-After should be at most a few seconds for 1 RPS rate
			// With token bucket, it should be around 1 second
			return retryAfter <= 5*1000000000 // 5 seconds in nanoseconds
		},
		gen.IntRange(1, 20),
	))

	// Property: formatRetryAfter SHALL return at least 1 second
	properties.Property("formatRetryAfter returns at least 1 second", prop.ForAll(
		func(ms int) bool {
			// Test with various durations including sub-second
			duration := time.Duration(ms) * time.Millisecond
			result := formatRetryAfter(duration)

			// Parse the result
			var seconds int
			_, err := fmt.Sscanf(result, "%d", &seconds)
			if err != nil {
				return false
			}

			// Should be at least 1
			return seconds >= 1
		},
		gen.IntRange(0, 5000), // 0 to 5000 ms
	))

	properties.TestingRun(t)
}
