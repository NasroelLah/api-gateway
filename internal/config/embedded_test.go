package config

import (
	"testing"
)

func TestLoadConfigWithEmbedded(t *testing.T) {
	embeddedYAML := []byte(`
server:
  host: "0.0.0.0"
  port: 9090
  read_timeout: 15s
  write_timeout: 15s
  idle_timeout: 60s

proxy:
  max_idle_conns: 500
  max_idle_conns_per_host: 50
  timeout: 30s
  upstreams: []

whitelist:
  enabled: true
  entries:
    - "127.0.0.1/32"
  reload_interval: 30s

rate_limit:
  enabled: true
  type: "memory"
  requests_per_second: 50
  burst: 100

logging:
  level: "debug"
  format: "json"
  output: "stdout"

metrics:
  enabled: true
  path: "/metrics"

tracing:
  enabled: false

tls:
  enabled: false

auth:
  enabled: false
`)

	t.Run("Load embedded config without external file", func(t *testing.T) {
		cfg, err := LoadConfigWithEmbedded("", embeddedYAML)
		if err != nil {
			t.Fatalf("Failed to load embedded config: %v", err)
		}

		if cfg.Server.Port != 9090 {
			t.Errorf("Expected port 9090 from embedded config, got %d", cfg.Server.Port)
		}

		if cfg.Proxy.MaxIdleConns != 500 {
			t.Errorf("Expected max_idle_conns 500 from embedded config, got %d", cfg.Proxy.MaxIdleConns)
		}

		if cfg.Logging.Level != "debug" {
			t.Errorf("Expected log level 'debug' from embedded config, got %s", cfg.Logging.Level)
		}
	})

	t.Run("Load with nil embedded config uses defaults", func(t *testing.T) {
		cfg, err := LoadConfigWithEmbedded("", nil)
		if err != nil {
			t.Fatalf("Failed to load config with nil embedded: %v", err)
		}

		// Should use defaults
		if cfg.Server.Port != 7070 {
			t.Errorf("Expected default port 7070, got %d", cfg.Server.Port)
		}
	})

	t.Run("Load with empty embedded config uses defaults", func(t *testing.T) {
		cfg, err := LoadConfigWithEmbedded("", []byte{})
		if err != nil {
			t.Fatalf("Failed to load config with empty embedded: %v", err)
		}

		// Should use defaults
		if cfg.Server.Port != 7070 {
			t.Errorf("Expected default port 7070, got %d", cfg.Server.Port)
		}
	})
}
