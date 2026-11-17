package config

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestSecureDefaultsForTrustedProxyAndDynamicUpstream(t *testing.T) {
	v := viper.New()
	setDefaults(v)
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Server.TrustedProxies) != 0 {
		t.Fatalf("trusted proxies default = %v, want empty", cfg.Server.TrustedProxies)
	}
	if cfg.Proxy.DynamicUpstream.Enabled || len(cfg.Proxy.DynamicUpstream.AllowedHosts) != 0 {
		t.Fatalf("dynamic upstream defaults are not fail-closed: %+v", cfg.Proxy.DynamicUpstream)
	}
	if cfg.Server.WriteTimeout <= cfg.Proxy.Timeout {
		t.Fatalf("write timeout %s must exceed proxy timeout %s", cfg.Server.WriteTimeout, cfg.Proxy.Timeout)
	}
	if cfg.Server.WriteTimeout != 90*time.Second || cfg.Proxy.Timeout != 60*time.Second {
		t.Fatalf("unexpected timeout defaults: write=%s proxy=%s", cfg.Server.WriteTimeout, cfg.Proxy.Timeout)
	}
}

func TestValidateRejectsMalformedTrustedProxy(t *testing.T) {
	cfg := validTestConfig()
	cfg.Server.TrustedProxies = []string{"10.0.0.0/8", "10.10.10.10/"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "server.trusted_proxies") {
		t.Fatalf("Validate() error = %v, want invalid trusted proxy error", err)
	}
}

func TestValidateRejectsRedisRateLimit(t *testing.T) {
	cfg := validTestConfig()
	cfg.RateLimit.Type = "redis"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "redis is not implemented") {
		t.Fatalf("Validate() error = %v, want explicit Redis unsupported error", err)
	}
}

func validTestConfig() *Config {
	v := viper.New()
	setDefaults(v)
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		panic(err)
	}
	return &cfg
}
