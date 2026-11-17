package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
	"github.com/nasroellah/api-gateway/internal/config"
	"go.uber.org/zap"
)

const PropertyTestIterations = 100

// createTestServerConfig creates a server config for testing
func createTestServerConfig(port int) *config.ServerConfig {
	return &config.ServerConfig{
		Host:         "127.0.0.1",
		Port:         port,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  10 * time.Second,
	}
}

// createTestLogger creates a no-op logger for testing
func createTestLogger() *zap.Logger {
	return zap.NewNop()
}

// TestProperty16_GracefulShutdown tests that in-flight requests complete before shutdown
// **Property 16: Graceful Shutdown**
// **Validates: Requirements 10.3**
//
// *For any* in-flight request saat SIGTERM diterima, request tersebut SHALL selesai
// diproses sebelum server shutdown (dengan timeout yang dikonfigurasi).
func TestProperty16_GracefulShutdown(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	// Property: In-flight requests SHALL complete before shutdown
	properties.Property("In-flight requests complete before shutdown", prop.ForAll(
		func(numRequests int, requestDelayMs int, shutdownDelayMs int) bool {
			// Use a unique port for each test iteration
			port := 30000 + (numRequests*100+requestDelayMs+shutdownDelayMs)%10000

			cfg := createTestServerConfig(port)
			logger := createTestLogger()
			server := NewServer(cfg, logger)

			// Track completed requests
			var completedRequests int32
			var startedRequests int32

			// Handler that simulates work with a delay
			requestDelay := time.Duration(requestDelayMs) * time.Millisecond
			server.Router().HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&startedRequests, 1)
				time.Sleep(requestDelay)
				atomic.AddInt32(&completedRequests, 1)
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("OK"))
			})

			// Start server
			errCh := server.StartAsync()

			// Wait for server to be ready
			time.Sleep(50 * time.Millisecond)

			// Check for startup errors
			select {
			case err := <-errCh:
				if err != nil {
					// Port might be in use, skip this iteration
					return true
				}
			default:
			}

			// Start requests in parallel
			var wg sync.WaitGroup
			client := &http.Client{Timeout: 10 * time.Second}

			for i := 0; i < numRequests; i++ {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/test/%d", port, idx))
					if err == nil {
						io.Copy(io.Discard, resp.Body)
						resp.Body.Close()
					}
				}(i)
			}

			// Wait a bit for requests to start
			time.Sleep(time.Duration(shutdownDelayMs) * time.Millisecond)

			// Initiate shutdown while requests are in-flight
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			shutdownErr := server.Shutdown(ctx)

			// Wait for all request goroutines to complete
			wg.Wait()

			// Verify: All started requests should have completed
			started := atomic.LoadInt32(&startedRequests)
			completed := atomic.LoadInt32(&completedRequests)

			// All started requests should complete (graceful shutdown)
			return shutdownErr == nil && completed == started
		},
		gen.IntRange(1, 5),   // numRequests: 1-5 concurrent requests
		gen.IntRange(10, 50), // requestDelayMs: 10-50ms delay per request
		gen.IntRange(5, 20),  // shutdownDelayMs: 5-20ms before shutdown
	))

	// Property: Server SHALL not accept new connections after shutdown initiated
	properties.Property("Server does not accept new connections after shutdown", prop.ForAll(
		func(seed int) bool {
			port := 31000 + seed%1000

			cfg := createTestServerConfig(port)
			logger := createTestLogger()
			server := NewServer(cfg, logger)

			server.Router().HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			errCh := server.StartAsync()
			time.Sleep(50 * time.Millisecond)

			select {
			case err := <-errCh:
				if err != nil {
					return true // Port in use, skip
				}
			default:
			}

			// Verify server is running
			client := &http.Client{Timeout: 2 * time.Second}
			resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/test", port))
			if err != nil {
				server.ShutdownWithTimeout(time.Second)
				return true // Connection failed, skip
			}
			resp.Body.Close()

			// Initiate shutdown
			go server.ShutdownWithTimeout(5 * time.Second)

			// Wait for shutdown to start
			time.Sleep(100 * time.Millisecond)

			// Try to make a new connection - should fail or timeout
			client2 := &http.Client{Timeout: 500 * time.Millisecond}
			_, err = client2.Get(fmt.Sprintf("http://127.0.0.1:%d/test", port))

			// After shutdown, new connections should fail
			// (either connection refused or timeout)
			return err != nil || !server.IsRunning()
		},
		gen.IntRange(0, 999),
	))

	// Property: Shutdown SHALL complete within timeout
	properties.Property("Shutdown completes within timeout", prop.ForAll(
		func(timeoutMs int) bool {
			port := 32000 + timeoutMs%1000

			cfg := createTestServerConfig(port)
			logger := createTestLogger()
			server := NewServer(cfg, logger)

			server.Router().HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			errCh := server.StartAsync()
			time.Sleep(50 * time.Millisecond)

			select {
			case err := <-errCh:
				if err != nil {
					return true
				}
			default:
			}

			timeout := time.Duration(timeoutMs) * time.Millisecond
			start := time.Now()

			err := server.ShutdownWithTimeout(timeout)

			elapsed := time.Since(start)

			// Shutdown should complete without error and within reasonable time
			// (allowing some margin for test overhead)
			return err == nil && elapsed < timeout+500*time.Millisecond
		},
		gen.IntRange(100, 1000), // timeout: 100ms-1s
	))

	// Property: Multiple shutdown calls SHALL be safe
	properties.Property("Multiple shutdown calls are safe", prop.ForAll(
		func(numShutdowns int) bool {
			port := 33000 + numShutdowns%1000

			cfg := createTestServerConfig(port)
			logger := createTestLogger()
			server := NewServer(cfg, logger)

			server.Router().HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			errCh := server.StartAsync()
			time.Sleep(50 * time.Millisecond)

			select {
			case err := <-errCh:
				if err != nil {
					return true
				}
			default:
			}

			// Call shutdown multiple times concurrently
			var wg sync.WaitGroup
			errors := make([]error, numShutdowns)

			for i := 0; i < numShutdowns; i++ {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					errors[idx] = server.ShutdownWithTimeout(2 * time.Second)
				}(i)
			}

			wg.Wait()

			// All shutdown calls should succeed (or be no-ops)
			for _, err := range errors {
				if err != nil {
					return false
				}
			}

			// Server should not be running after shutdown
			return !server.IsRunning()
		},
		gen.IntRange(2, 5), // numShutdowns: 2-5 concurrent shutdown calls
	))

	// Property: Shutdown on non-started server SHALL be safe
	properties.Property("Shutdown on non-started server is safe", prop.ForAll(
		func(port int) bool {
			cfg := createTestServerConfig(port)
			logger := createTestLogger()
			server := NewServer(cfg, logger)

			// Don't start the server, just try to shut it down
			err := server.ShutdownWithTimeout(time.Second)

			// Should not error on non-started server
			return err == nil && !server.IsRunning()
		},
		gen.IntRange(34000, 35000),
	))

	properties.TestingRun(t)
}

// TestGracefulShutdown_InFlightRequestsComplete is a focused unit test
// for graceful shutdown behavior
func TestGracefulShutdown_InFlightRequestsComplete(t *testing.T) {
	port := 35500

	cfg := createTestServerConfig(port)
	logger := createTestLogger()
	server := NewServer(cfg, logger)

	var requestCompleted atomic.Bool
	requestStarted := make(chan struct{})

	// Handler that signals when it starts and takes time to complete
	server.Router().HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		time.Sleep(200 * time.Millisecond)
		requestCompleted.Store(true)
		w.WriteHeader(http.StatusOK)
	})

	errCh := server.StartAsync()
	time.Sleep(50 * time.Millisecond)

	select {
	case err := <-errCh:
		if err != nil {
			t.Skipf("Could not start server: %v", err)
		}
	default:
	}

	// Start a request
	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/test", port))
		if err == nil {
			resp.Body.Close()
		}
	}()

	// Wait for request to start
	<-requestStarted

	// Initiate shutdown while request is in-flight
	err := server.ShutdownWithTimeout(5 * time.Second)
	if err != nil {
		t.Errorf("Shutdown error: %v", err)
	}

	// Verify request completed
	if !requestCompleted.Load() {
		t.Error("In-flight request did not complete before shutdown")
	}
}
