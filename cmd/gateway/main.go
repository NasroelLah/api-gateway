package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nasroellah/api-gateway/internal/config"
	"github.com/nasroellah/api-gateway/internal/middleware"
	"github.com/nasroellah/api-gateway/internal/observability"
	"github.com/nasroellah/api-gateway/internal/proxy"
	"github.com/nasroellah/api-gateway/internal/security"
	"github.com/nasroellah/api-gateway/internal/server"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

func main() {
	// Parse command line flags
	configPath := flag.String("config", "", "Path to configuration file (YAML). If not provided, uses built-in defaults.")
	port := flag.Int("port", 0, "Server port (overrides config)")
	host := flag.String("host", "", "Server host/bind address (overrides config)")
	apiKey := flag.String("api-key", "", "API key for authentication (enables auth if set)")
	logLevel := flag.String("log-level", "", "Log level: debug, info, warn, error (overrides config)")
	flag.Parse()

	// Load configuration with defaults
	// Priority: CLI flags > ENV variables > External config file > Built-in defaults
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Apply CLI flag overrides (highest priority)
	applyFlagOverrides(cfg, *port, *host, *apiKey, *logLevel)

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	logger, err := observability.NewLogger(observability.LoggerConfig{
		Level:  cfg.Logging.Level,
		Format: cfg.Logging.Format,
		Output: cfg.Logging.Output,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	// Log config source
	if *configPath != "" {
		logger.Info("starting API Gateway with external config",
			zap.String("config_file", *configPath),
			zap.String("host", cfg.Server.Host),
			zap.Int("port", cfg.Server.Port),
		)
	} else {
		logger.Info("starting API Gateway with built-in defaults",
			zap.String("host", cfg.Server.Host),
			zap.Int("port", cfg.Server.Port),
		)
	}

	// Run the gateway
	if err := run(cfg, logger); err != nil {
		logger.Error("gateway error", zap.Error(err))
		os.Exit(1)
	}
}

// applyFlagOverrides applies CLI flag values to config (highest priority)
func applyFlagOverrides(cfg *config.Config, port int, host, apiKey, logLevel string) {
	// Override port if specified
	if port > 0 {
		cfg.Server.Port = port
	}

	// Override host if specified
	if host != "" {
		cfg.Server.Host = host
	}

	// Override API key if specified (also enables auth)
	if apiKey != "" {
		cfg.Auth.Enabled = true
		cfg.Auth.APIKey = apiKey
	}

	// Override log level if specified
	if logLevel != "" {
		cfg.Logging.Level = logLevel
	}
}

func run(cfg *config.Config, logger *observability.Logger) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize components
	components, err := initializeComponents(cfg, logger)
	if err != nil {
		return fmt.Errorf("failed to initialize components: %w", err)
	}
	defer components.cleanup()

	// Create HTTP server
	srv := server.NewServer(&cfg.Server, logger.GetZapLogger())

	// Setup router with middleware and routes
	routerCfg := &server.RouterConfig{
		WhitelistEnabled: cfg.Whitelist.Enabled,
		TrustedProxies:   cfg.Server.TrustedProxies,
		RateLimitEnabled: cfg.RateLimit.Enabled,
		TracingEnabled:   cfg.Tracing.Enabled,
		MetricsEnabled:   cfg.Metrics.Enabled,
		MetricsPath:      cfg.Metrics.Path,
		AuthEnabled:      cfg.Auth.Enabled,
	}

	deps := &server.RouterDependencies{
		Logger:      logger.GetZapLogger(),
		ProxyMgr:    components.proxyMgr,
		Whitelist:   components.whitelist,
		RateLimiter: components.rateLimiter,
		APIKeyAuth:  components.apiKeyAuth,
		Metrics:     components.metrics,
		Tracer:      components.tracer,
	}

	server.SetupRouter(srv.Router(), routerCfg, deps)

	// Setup TLS if enabled
	if cfg.TLS.Enabled {
		tlsMgr := server.NewTLSManager(&cfg.TLS, logger.GetZapLogger())
		tlsConfig, err := tlsMgr.BuildTLSConfig()
		if err != nil {
			return fmt.Errorf("failed to build TLS config: %w", err)
		}
		srv.SetTLSConfig(tlsConfig)
	}

	// Setup signal handling for graceful shutdown
	// Requirements: 10.3
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start server in goroutine
	errCh := make(chan error, 1)
	go func() {
		var err error
		if cfg.TLS.Enabled {
			err = srv.StartWithTLSConfig()
		} else {
			err = srv.Start()
		}
		if err != nil {
			errCh <- err
		}
	}()

	// Wait for shutdown signal or error
	select {
	case sig := <-sigCh:
		logger.Info("received shutdown signal", zap.String("signal", sig.String()))
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
		logger.Info("context cancelled")
	}

	// Graceful shutdown
	// Requirements: 10.3 - complete in-flight requests before shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	logger.Info("initiating graceful shutdown")
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown error: %w", err)
	}

	logger.Info("gateway shutdown complete")
	return nil
}


// components holds all initialized gateway components
type components struct {
	proxyMgr       *proxy.ProxyManager
	whitelist      *security.IPWhitelist
	rateLimiter    middleware.RateLimiter
	apiKeyAuth     *middleware.APIKeyAuth
	metrics        *observability.Metrics
	tracer         trace.Tracer
	tracerProvider *observability.TracerProvider
	inMemoryRL     *middleware.InMemoryRateLimiter
}

// cleanup releases resources held by components
func (c *components) cleanup() {
	if c.proxyMgr != nil {
		c.proxyMgr.Close()
	}
	if c.whitelist != nil {
		c.whitelist.Close()
	}
	if c.inMemoryRL != nil {
		c.inMemoryRL.Stop()
	}
	if c.tracerProvider != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.tracerProvider.Shutdown(ctx)
	}
}

// initializeComponents creates and configures all gateway components
func initializeComponents(cfg *config.Config, logger *observability.Logger) (*components, error) {
	c := &components{}
	initialized := false
	defer func() {
		if !initialized {
			c.cleanup()
		}
	}()
	// Initialize proxy manager
	c.proxyMgr = proxy.NewProxyManager(&cfg.Proxy)
	logger.Info("proxy manager initialized",
		zap.Int("upstreams", len(cfg.Proxy.Upstreams)),
		zap.Bool("dynamic_upstream", cfg.Proxy.DynamicUpstream.Enabled),
	)

	// Initialize IP whitelist if enabled
	if cfg.Whitelist.Enabled {
		whitelist, err := security.NewIPWhitelist(cfg.Whitelist.Entries)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize IP whitelist: %w", err)
		}
		c.whitelist = whitelist
		logger.Info("IP whitelist initialized", zap.Int("entries", whitelist.Count()))
	}

	// Initialize rate limiter if enabled
	if cfg.RateLimit.Enabled {
		switch cfg.RateLimit.Type {
		case "memory":
			rl := middleware.NewInMemoryRateLimiter(
				cfg.RateLimit.RequestsPerSecond,
				cfg.RateLimit.Burst,
			)
			c.rateLimiter = rl
			c.inMemoryRL = rl
			logger.Info("in-memory rate limiter initialized",
				zap.Float64("rps", cfg.RateLimit.RequestsPerSecond),
				zap.Int("burst", cfg.RateLimit.Burst),
			)
		case "redis":
			return nil, fmt.Errorf("Redis rate limiting is not implemented")
		default:
			return nil, fmt.Errorf("unsupported rate limiter type: %s", cfg.RateLimit.Type)
		}
	}

	// Initialize Prometheus metrics if enabled
	if cfg.Metrics.Enabled {
		c.metrics = observability.NewMetrics(prometheus.DefaultRegisterer)
		logger.Info("Prometheus metrics initialized", zap.String("path", cfg.Metrics.Path))
	}

	// Initialize OpenTelemetry tracing if enabled
	if cfg.Tracing.Enabled {
		tp, err := observability.NewTracerProvider(&cfg.Tracing)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize tracer: %w", err)
		}
		c.tracerProvider = tp
		c.tracer = tp.Tracer()
		logger.Info("OpenTelemetry tracing initialized",
			zap.String("service_name", cfg.Tracing.ServiceName),
			zap.String("exporter", cfg.Tracing.Exporter),
		)
	}

	// Initialize API key auth if enabled
	if cfg.Auth.Enabled {
		c.apiKeyAuth = middleware.NewAPIKeyAuth(
			cfg.Auth.APIKey,
			cfg.Auth.HeaderName,
			true,
		)
		// Log the API key if it was auto-generated (for initial setup)
		if cfg.Auth.APIKey == "" {
			logger.Info("API key authentication enabled (auto-generated key)",
				zap.String("header_name", c.apiKeyAuth.GetHeaderName()),
				zap.String("api_key", c.apiKeyAuth.GetAPIKey()),
			)
		} else {
			logger.Info("API key authentication enabled",
				zap.String("header_name", c.apiKeyAuth.GetHeaderName()),
			)
		}
	}

	initialized = true
	return c, nil
}
