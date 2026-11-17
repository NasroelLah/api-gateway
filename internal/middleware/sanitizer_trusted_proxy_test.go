package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nasroellah/api-gateway/internal/security"
)

func TestHeaderSanitizerWithTrustRejectsUntrustedForwardedHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "198.51.100.44:3210"
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	r.Header.Set("X-Real-IP", "203.0.113.9")

	var got string
	h := HeaderSanitizerWithTrust([]string{"10.0.0.0/8"})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = GetSanitizedClientIP(r)
		if r.Header.Get("X-Forwarded-For") != got || r.Header.Get("X-Real-IP") != got {
			t.Errorf("forwarded headers not normalized: XFF=%q XRI=%q identity=%q", r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Real-IP"), got)
		}
	}))
	h.ServeHTTP(httptest.NewRecorder(), r)

	if got != "198.51.100.44" {
		t.Fatalf("client identity = %q, want direct peer", got)
	}
}

func TestHeaderSanitizerWithTrustWalksForwardedHopsFromNearest(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.2:3210"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	r.Header.Set("X-Real-IP", "192.0.2.99")

	var got string
	h := HeaderSanitizerWithTrust([]string{"10.0.0.0/8"})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = GetSanitizedClientIP(r)
	}))
	h.ServeHTTP(httptest.NewRecorder(), r)

	if got != "203.0.113.9" {
		t.Fatalf("client identity = %q, want first untrusted hop", got)
	}
}

func TestTrustedProxyIdentityIsSharedWithWhitelist(t *testing.T) {
	whitelist, err := security.NewIPWhitelist([]string{"203.0.113.9/32"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.2:3210"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")

	called := false
	h := HeaderSanitizerWithTrust([]string{"10.0.0.0/8"})(WhitelistMiddleware(whitelist, false)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	})))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !called || w.Code != http.StatusOK {
		t.Fatalf("whitelist rejected sanitized client IP: called=%v status=%d", called, w.Code)
	}
}

type recordingRateLimiter struct {
	key string
}

func (l *recordingRateLimiter) Allow(key string) (bool, time.Duration) {
	l.key = key
	return true, 0
}

func TestTrustedProxyIdentityIsSharedWithRateLimiter(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.2:3210"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	limiter := &recordingRateLimiter{}
	h := HeaderSanitizerWithTrust([]string{"10.0.0.0/8"})(RateLimitByIPMiddleware(limiter)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))
	h.ServeHTTP(httptest.NewRecorder(), r)
	if limiter.key != "203.0.113.9" {
		t.Fatalf("rate-limit key = %q, want sanitized client IP", limiter.key)
	}
}
