package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config menyimpan semua konfigurasi Gateway
type Config struct {
	Server    ServerConfig    `mapstructure:"server"`
	Proxy     ProxyConfig     `mapstructure:"proxy"`
	Whitelist WhitelistConfig `mapstructure:"whitelist"`
	RateLimit RateLimitConfig `mapstructure:"rate_limit"`
	Logging   LoggingConfig   `mapstructure:"logging"`
	Metrics   MetricsConfig   `mapstructure:"metrics"`
	Tracing   TracingConfig   `mapstructure:"tracing"`
	TLS       TLSConfig       `mapstructure:"tls"`
}

type ServerConfig struct {
	Host         string        `mapstructure:"host"`
	Port         int           `mapstructure:"port"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
	IdleTimeout  time.Duration `mapstructure:"idle_timeout"`
}

type ProxyConfig struct {
	Upstreams           []UpstreamConfig      `mapstructure:"upstreams"`
	MaxIdleConns        int                   `mapstructure:"max_idle_conns"`
	MaxIdleConnsPerHost int                   `mapstructure:"max_idle_conns_per_host"`
	Timeout             time.Duration         `mapstructure:"timeout"`
	DynamicUpstream     DynamicUpstreamConfig `mapstructure:"dynamic_upstream"`
}

type DynamicUpstreamConfig struct {
	Enabled      bool     `mapstructure:"enabled"`
	HeaderName   string   `mapstructure:"header_name"`
	AllowedHosts []string `mapstructure:"allowed_hosts"`
}

type UpstreamConfig struct {
	PathPrefix string `mapstructure:"path_prefix"`
	Target     string `mapstructure:"target"`
}


type WhitelistConfig struct {
	Enabled        bool          `mapstructure:"enabled"`
	Entries        []string      `mapstructure:"entries"`
	ReloadInterval time.Duration `mapstructure:"reload_interval"`
}

type RateLimitConfig struct {
	Enabled           bool        `mapstructure:"enabled"`
	Type              string      `mapstructure:"type"`
	RequestsPerSecond float64     `mapstructure:"requests_per_second"`
	Burst             int         `mapstructure:"burst"`
	Redis             RedisConfig `mapstructure:"redis"`
}

type RedisConfig struct {
	Address  string `mapstructure:"address"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
	Output string `mapstructure:"output"`
}

type MetricsConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Path    string `mapstructure:"path"`
}

type TracingConfig struct {
	Enabled     bool    `mapstructure:"enabled"`
	ServiceName string  `mapstructure:"service_name"`
	Exporter    string  `mapstructure:"exporter"`
	Endpoint    string  `mapstructure:"endpoint"`
	SampleRate  float64 `mapstructure:"sample_rate"`
}

type TLSConfig struct {
	Enabled    bool       `mapstructure:"enabled"`
	CertFile   string     `mapstructure:"cert_file"`
	KeyFile    string     `mapstructure:"key_file"`
	MinVersion string     `mapstructure:"min_version"`
	MTLS       MTLSConfig `mapstructure:"mtls"`
}

type MTLSConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	CAFile  string `mapstructure:"ca_file"`
}


// LoadConfig memuat konfigurasi dari ENV atau YAML
// ENV variables take precedence over YAML file values
func LoadConfig(configPath string) (*Config, error) {
	v := viper.New()

	// Set defaults
	setDefaults(v)

	// Enable automatic environment variable binding
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Load from YAML file if path provided
	if configPath != "" {
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err != nil {
			// Only return error if file was explicitly specified but not found
			var configFileNotFoundError viper.ConfigFileNotFoundError
			if !errors.As(err, &configFileNotFoundError) {
				return nil, fmt.Errorf("failed to read config file: %w", err)
			}
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &cfg, nil
}

// LoadConfigWithViper loads config using a provided viper instance (for testing)
func LoadConfigWithViper(v *viper.Viper) (*Config, error) {
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}
	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	// Server defaults
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.read_timeout", 30*time.Second)
	v.SetDefault("server.write_timeout", 30*time.Second)
	v.SetDefault("server.idle_timeout", 120*time.Second)

	// Proxy defaults
	v.SetDefault("proxy.max_idle_conns", 1000)
	v.SetDefault("proxy.max_idle_conns_per_host", 100)
	v.SetDefault("proxy.timeout", 60*time.Second)
	v.SetDefault("proxy.dynamic_upstream.enabled", false)
	v.SetDefault("proxy.dynamic_upstream.header_name", "X-Upstream-Target")

	// Whitelist defaults
	v.SetDefault("whitelist.enabled", true)
	v.SetDefault("whitelist.reload_interval", 60*time.Second)

	// Rate limit defaults
	v.SetDefault("rate_limit.enabled", true)
	v.SetDefault("rate_limit.type", "memory")
	v.SetDefault("rate_limit.requests_per_second", 100.0)
	v.SetDefault("rate_limit.burst", 200)
	v.SetDefault("rate_limit.redis.db", 0)

	// Logging defaults
	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "json")
	v.SetDefault("logging.output", "stdout")

	// Metrics defaults
	v.SetDefault("metrics.enabled", true)
	v.SetDefault("metrics.path", "/metrics")

	// Tracing defaults
	v.SetDefault("tracing.enabled", false)
	v.SetDefault("tracing.service_name", "api-gateway")
	v.SetDefault("tracing.exporter", "otlp")
	v.SetDefault("tracing.sample_rate", 1.0)

	// TLS defaults
	v.SetDefault("tls.enabled", false)
	v.SetDefault("tls.min_version", "1.2")
	v.SetDefault("tls.mtls.enabled", false)
}


// ValidationError represents a configuration validation error
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("config validation error: %s - %s", e.Field, e.Message)
}

// ValidationErrors is a collection of validation errors
type ValidationErrors []ValidationError

func (e ValidationErrors) Error() string {
	if len(e) == 0 {
		return ""
	}
	var msgs []string
	for _, err := range e {
		msgs = append(msgs, err.Error())
	}
	return strings.Join(msgs, "; ")
}

// Validate memvalidasi konfigurasi
func (c *Config) Validate() error {
	var errs ValidationErrors

	// Validate server config
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		errs = append(errs, ValidationError{
			Field:   "server.port",
			Message: "must be between 1 and 65535",
		})
	}
	if c.Server.ReadTimeout <= 0 {
		errs = append(errs, ValidationError{
			Field:   "server.read_timeout",
			Message: "must be positive",
		})
	}
	if c.Server.WriteTimeout <= 0 {
		errs = append(errs, ValidationError{
			Field:   "server.write_timeout",
			Message: "must be positive",
		})
	}

	// Validate proxy config
	if c.Proxy.MaxIdleConns <= 0 {
		errs = append(errs, ValidationError{
			Field:   "proxy.max_idle_conns",
			Message: "must be positive",
		})
	}
	if c.Proxy.MaxIdleConnsPerHost <= 0 {
		errs = append(errs, ValidationError{
			Field:   "proxy.max_idle_conns_per_host",
			Message: "must be positive",
		})
	}
	if c.Proxy.Timeout <= 0 {
		errs = append(errs, ValidationError{
			Field:   "proxy.timeout",
			Message: "must be positive",
		})
	}

	// Validate dynamic upstream config
	if c.Proxy.DynamicUpstream.Enabled {
		if c.Proxy.DynamicUpstream.HeaderName == "" {
			errs = append(errs, ValidationError{
				Field:   "proxy.dynamic_upstream.header_name",
				Message: "required when dynamic upstream is enabled",
			})
		}
	}

	// Validate whitelist config
	if c.Whitelist.Enabled && len(c.Whitelist.Entries) == 0 {
		errs = append(errs, ValidationError{
			Field:   "whitelist.entries",
			Message: "at least one entry required when whitelist is enabled",
		})
	}

	// Validate rate limit config
	if c.RateLimit.Enabled {
		if c.RateLimit.RequestsPerSecond <= 0 {
			errs = append(errs, ValidationError{
				Field:   "rate_limit.requests_per_second",
				Message: "must be positive when rate limiting is enabled",
			})
		}
		if c.RateLimit.Burst <= 0 {
			errs = append(errs, ValidationError{
				Field:   "rate_limit.burst",
				Message: "must be positive when rate limiting is enabled",
			})
		}
		validTypes := map[string]bool{"memory": true, "redis": true}
		if !validTypes[c.RateLimit.Type] {
			errs = append(errs, ValidationError{
				Field:   "rate_limit.type",
				Message: "must be 'memory' or 'redis'",
			})
		}
		if c.RateLimit.Type == "redis" && c.RateLimit.Redis.Address == "" {
			errs = append(errs, ValidationError{
				Field:   "rate_limit.redis.address",
				Message: "required when rate limit type is 'redis'",
			})
		}
	}

	// Validate logging config
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[strings.ToLower(c.Logging.Level)] {
		errs = append(errs, ValidationError{
			Field:   "logging.level",
			Message: "must be one of: debug, info, warn, error",
		})
	}

	// Validate TLS config
	if c.TLS.Enabled {
		if c.TLS.CertFile == "" {
			errs = append(errs, ValidationError{
				Field:   "tls.cert_file",
				Message: "required when TLS is enabled",
			})
		}
		if c.TLS.KeyFile == "" {
			errs = append(errs, ValidationError{
				Field:   "tls.key_file",
				Message: "required when TLS is enabled",
			})
		}
		validVersions := map[string]bool{"1.2": true, "1.3": true}
		if !validVersions[c.TLS.MinVersion] {
			errs = append(errs, ValidationError{
				Field:   "tls.min_version",
				Message: "must be '1.2' or '1.3'",
			})
		}
		if c.TLS.MTLS.Enabled && c.TLS.MTLS.CAFile == "" {
			errs = append(errs, ValidationError{
				Field:   "tls.mtls.ca_file",
				Message: "required when mTLS is enabled",
			})
		}
	}

	// Validate tracing config
	if c.Tracing.Enabled {
		if c.Tracing.ServiceName == "" {
			errs = append(errs, ValidationError{
				Field:   "tracing.service_name",
				Message: "required when tracing is enabled",
			})
		}
		if c.Tracing.Endpoint == "" {
			errs = append(errs, ValidationError{
				Field:   "tracing.endpoint",
				Message: "required when tracing is enabled",
			})
		}
		validExporters := map[string]bool{"otlp": true, "jaeger": true, "zipkin": true}
		if !validExporters[c.Tracing.Exporter] {
			errs = append(errs, ValidationError{
				Field:   "tracing.exporter",
				Message: "must be one of: otlp, jaeger, zipkin",
			})
		}
		if c.Tracing.SampleRate < 0 || c.Tracing.SampleRate > 1 {
			errs = append(errs, ValidationError{
				Field:   "tracing.sample_rate",
				Message: "must be between 0 and 1",
			})
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}
