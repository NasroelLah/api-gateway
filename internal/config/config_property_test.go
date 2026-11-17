package config

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
	"github.com/spf13/viper"
)

const PropertyTestIterations = 100

// TestProperty14_ConfigLoadingPriority tests that ENV variables take precedence over YAML values
// **Property 14: Config Loading Priority**
// **Validates: Requirements 7.2, 7.3**
func TestProperty14_ConfigLoadingPriority(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	// Property: For any configuration key with different ENV and YAML values,
	// the ENV value SHALL be used
	// Note: In Viper, AutomaticEnv() binds ENV vars that override config file values
	// but NOT values set via Set() (which has highest priority)
	properties.Property("ENV variables take precedence over YAML file values for server.port", prop.ForAll(
		func(envPort, yamlPort int) bool {
			if envPort == yamlPort {
				return true // Skip when values are same
			}

			// Clean up any previous env
			os.Unsetenv("SERVER_PORT")

			v := viper.New()
			v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
			v.AutomaticEnv()

			// Simulate YAML file by using SetDefault (config file values have lower priority than ENV)
			v.SetDefault("server.port", yamlPort)

			// Set ENV value (this should take precedence over defaults/config file)
			os.Setenv("SERVER_PORT", strconv.Itoa(envPort))
			defer os.Unsetenv("SERVER_PORT")

			result := v.GetInt("server.port")
			return result == envPort
		},
		gen.IntRange(1, 65535),
		gen.IntRange(1, 65535),
	))


	// Property: For any string configuration key, ENV takes precedence over config file
	properties.Property("ENV variables take precedence over YAML file values for server.host", prop.ForAll(
		func(envIdx, yamlIdx int) bool {
			hosts := []string{"localhost", "127.0.0.1", "0.0.0.0", "192.168.1.1", "10.0.0.1"}
			envHost := hosts[envIdx%len(hosts)]
			yamlHost := hosts[yamlIdx%len(hosts)]

			if envHost == yamlHost {
				return true // Skip when values are same
			}

			// Clean up any previous env
			os.Unsetenv("SERVER_HOST")

			v := viper.New()
			v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
			v.AutomaticEnv()

			// Simulate YAML file by using SetDefault
			v.SetDefault("server.host", yamlHost)

			// Set ENV value (this should take precedence)
			os.Setenv("SERVER_HOST", envHost)
			defer os.Unsetenv("SERVER_HOST")

			result := v.GetString("server.host")
			return result == envHost
		},
		gen.IntRange(0, 4),
		gen.IntRange(0, 4),
	))

	// Property: For any logging level, ENV takes precedence over config file
	properties.Property("ENV variables take precedence over YAML file values for logging.level", prop.ForAll(
		func(envIdx, yamlIdx int) bool {
			levels := []string{"debug", "info", "warn", "error"}
			envLevel := levels[envIdx%len(levels)]
			yamlLevel := levels[yamlIdx%len(levels)]

			if envLevel == yamlLevel {
				return true
			}

			// Clean up any previous env
			os.Unsetenv("LOGGING_LEVEL")

			v := viper.New()
			v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
			v.AutomaticEnv()

			// Simulate YAML file by using SetDefault
			v.SetDefault("logging.level", yamlLevel)

			// Set ENV value
			os.Setenv("LOGGING_LEVEL", envLevel)
			defer os.Unsetenv("LOGGING_LEVEL")

			result := v.GetString("logging.level")
			return result == envLevel
		},
		gen.IntRange(0, 3),
		gen.IntRange(0, 3),
	))

	properties.TestingRun(t)
}

func intToString(i int) string {
	return strconv.Itoa(i)
}


// TestProperty15_ConfigValidation tests that invalid configs produce clear error messages
// **Property 15: Config Validation**
// **Validates: Requirements 7.4, 7.5**
func TestProperty15_ConfigValidation(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	// Property: Invalid port values SHALL produce validation error with field name
	properties.Property("Invalid server.port produces error mentioning the field", prop.ForAll(
		func(port int) bool {
			cfg := validConfig()
			cfg.Server.Port = port

			err := cfg.Validate()
			if port <= 0 || port > 65535 {
				// Should have error
				if err == nil {
					return false
				}
				// Error should mention the field
				return strings.Contains(err.Error(), "server.port")
			}
			// Valid port should not cause port-related error
			return err == nil || !strings.Contains(err.Error(), "server.port")
		},
		gen.IntRange(-100, 70000),
	))

	// Property: Missing required fields when feature enabled SHALL produce clear error
	properties.Property("TLS enabled without cert_file produces error mentioning the field", prop.ForAll(
		func(enabled bool) bool {
			cfg := validConfig()
			cfg.TLS.Enabled = enabled
			cfg.TLS.CertFile = ""
			cfg.TLS.KeyFile = "/path/to/key"

			err := cfg.Validate()
			if enabled {
				if err == nil {
					return false
				}
				return strings.Contains(err.Error(), "tls.cert_file")
			}
			return err == nil || !strings.Contains(err.Error(), "tls.cert_file")
		},
		gen.Bool(),
	))

	// Property: Invalid rate limit type SHALL produce error mentioning the field
	properties.Property("Invalid rate_limit.type produces error mentioning the field", prop.ForAll(
		func(typeStr string) bool {
			cfg := validConfig()
			cfg.RateLimit.Enabled = true
			cfg.RateLimit.Type = typeStr

			err := cfg.Validate()
			validTypes := map[string]bool{"memory": true}
			if !validTypes[typeStr] {
				if err == nil {
					return false
				}
				return strings.Contains(err.Error(), "rate_limit.type")
			}
			return err == nil || !strings.Contains(err.Error(), "rate_limit.type")
		},
		gen.OneConstOf("memory", "redis", "invalid", "memcached", ""),
	))

	// Property: Invalid logging level SHALL produce error mentioning the field
	properties.Property("Invalid logging.level produces error mentioning the field", prop.ForAll(
		func(level string) bool {
			cfg := validConfig()
			cfg.Logging.Level = level

			err := cfg.Validate()
			validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
			if !validLevels[strings.ToLower(level)] {
				if err == nil {
					return false
				}
				return strings.Contains(err.Error(), "logging.level")
			}
			return err == nil || !strings.Contains(err.Error(), "logging.level")
		},
		gen.OneConstOf("debug", "info", "warn", "error", "trace", "fatal", "invalid"),
	))

	// Property: Whitelist enabled with empty entries SHALL produce error
	properties.Property("Whitelist enabled with empty entries produces error", prop.ForAll(
		func(enabled bool, hasEntries bool) bool {
			cfg := validConfig()
			cfg.Whitelist.Enabled = enabled
			if hasEntries {
				cfg.Whitelist.Entries = []string{"10.0.0.0/8"}
			} else {
				cfg.Whitelist.Entries = []string{}
			}

			err := cfg.Validate()
			if enabled && !hasEntries {
				if err == nil {
					return false
				}
				return strings.Contains(err.Error(), "whitelist.entries")
			}
			return err == nil || !strings.Contains(err.Error(), "whitelist.entries")
		},
		gen.Bool(),
		gen.Bool(),
	))

	// Redis is not wired up and must be rejected regardless of address.
	properties.Property("Redis rate limiter is rejected", prop.ForAll(
		func(useRedis bool, hasAddress bool) bool {
			cfg := validConfig()
			cfg.RateLimit.Enabled = true
			if useRedis {
				cfg.RateLimit.Type = "redis"
			} else {
				cfg.RateLimit.Type = "memory"
			}
			if hasAddress {
				cfg.RateLimit.Redis.Address = "localhost:6379"
			} else {
				cfg.RateLimit.Redis.Address = ""
			}

			err := cfg.Validate()
			if useRedis {
				return err != nil && strings.Contains(err.Error(), "rate_limit.type")
			}
			return err == nil || !strings.Contains(err.Error(), "rate_limit.type")
		},
		gen.Bool(),
		gen.Bool(),
	))

	properties.TestingRun(t)
}

// validConfig returns a valid configuration for testing
func validConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Host:         "0.0.0.0",
			Port:         7070,
			ReadTimeout:  30000000000,  // 30s
			WriteTimeout: 30000000000,  // 30s
			IdleTimeout:  120000000000, // 120s
		},
		Proxy: ProxyConfig{
			MaxIdleConns:        1000,
			MaxIdleConnsPerHost: 100,
			Timeout:             60000000000, // 60s
			DynamicUpstream: DynamicUpstreamConfig{
				Enabled:    false,
				HeaderName: "X-Upstream-Target",
			},
		},
		Whitelist: WhitelistConfig{
			Enabled: false,
		},
		RateLimit: RateLimitConfig{
			Enabled:           false,
			Type:              "memory",
			RequestsPerSecond: 100,
			Burst:             200,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
			Output: "stdout",
		},
		Metrics: MetricsConfig{
			Enabled: true,
			Path:    "/metrics",
		},
		Tracing: TracingConfig{
			Enabled: false,
		},
		TLS: TLSConfig{
			Enabled:    false,
			MinVersion: "1.2",
		},
	}
}
