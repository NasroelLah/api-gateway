package main

import (
	"strings"
	"testing"

	"github.com/nasroellah/api-gateway/internal/config"
	"github.com/nasroellah/api-gateway/internal/observability"
	"go.uber.org/zap"
)

func TestInitializeComponentsRejectsRedisRateLimiter(t *testing.T) {
	cfg := &config.Config{
		RateLimit: config.RateLimitConfig{
			Enabled: true,
			Type:    "redis",
		},
	}
	logger := &observability.Logger{Logger: zap.NewNop()}
	if _, err := initializeComponents(cfg, logger); err == nil || !strings.Contains(err.Error(), "Redis rate limiting is not implemented") {
		t.Fatalf("initializeComponents() error = %v, want explicit unsupported Redis error", err)
	}
}
