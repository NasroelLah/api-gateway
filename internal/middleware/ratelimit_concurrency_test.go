package middleware

import (
	"sync"
	"testing"
)

// A burst must be shared by concurrent requests from the same client.
func TestRateLimiterConcurrentBurst(t *testing.T) {
	rl := NewInMemoryRateLimiter(0.000001, 1)
	defer rl.Stop()

	const requests = 64
	var wg sync.WaitGroup
	results := make(chan bool, requests)
	for range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, _ := rl.Allow("same-client")
			results <- allowed
		}()
	}
	wg.Wait()
	close(results)
	allowed := 0
	for result := range results {
		if result {
			allowed++
		}
	}
	if allowed != 1 {
		t.Fatalf("allowed %d concurrent requests, want 1", allowed)
	}
	if ok, _ := rl.Allow("other-client"); !ok {
		t.Fatal("another client should have an independent burst")
	}
}
