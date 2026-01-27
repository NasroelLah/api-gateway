package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/nasroellah/api-gateway/internal/config"
	"go.uber.org/zap"
)

// Server adalah HTTP server utama
// Requirements: 8.1, 8.2, 8.3, 8.4
type Server struct {
	httpServer *http.Server
	router     chi.Router
	logger     *zap.Logger
	config     *config.ServerConfig
	tlsConfig  *tls.Config

	mu           sync.RWMutex
	started      bool
	shutdownOnce sync.Once
	shutdownCh   chan struct{}
}

// NewServer membuat server baru dengan konfigurasi timeout
// Requirements: 8.1, 8.2, 8.3
func NewServer(cfg *config.ServerConfig, logger *zap.Logger) *Server {
	router := chi.NewRouter()

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	httpServer := &http.Server{
		Addr:    addr,
		Handler: router,
		// ReadTimeout membatasi waktu membaca request
		// Requirements: 8.1
		ReadTimeout: cfg.ReadTimeout,
		// WriteTimeout membatasi waktu menulis response
		// Requirements: 8.2
		WriteTimeout: cfg.WriteTimeout,
		// IdleTimeout membatasi waktu idle connection
		// Requirements: 8.3
		IdleTimeout: cfg.IdleTimeout,
	}

	return &Server{
		httpServer: httpServer,
		router:     router,
		logger:     logger,
		config:     cfg,
		shutdownCh: make(chan struct{}),
	}
}

// Router returns the Chi router for middleware and route configuration
func (s *Server) Router() chi.Router {
	return s.router
}

// SetHandler sets a custom handler for the server
func (s *Server) SetHandler(handler http.Handler) {
	s.httpServer.Handler = handler
}

// Start memulai server
// Returns error if server fails to start
func (s *Server) Start() error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return fmt.Errorf("server already started")
	}
	s.started = true
	s.mu.Unlock()

	addr := s.httpServer.Addr
	s.logger.Info("starting HTTP server",
		zap.String("address", addr),
		zap.Duration("read_timeout", s.config.ReadTimeout),
		zap.Duration("write_timeout", s.config.WriteTimeout),
		zap.Duration("idle_timeout", s.config.IdleTimeout),
	)

	// Create listener
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		s.mu.Lock()
		s.started = false
		s.mu.Unlock()
		return fmt.Errorf("failed to create listener: %w", err)
	}

	// Start serving
	err = s.httpServer.Serve(listener)
	if err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}

	return nil
}

// StartAsync starts the server in a goroutine and returns immediately
// Returns a channel that will receive any startup errors
func (s *Server) StartAsync() <-chan error {
	errCh := make(chan error, 1)

	go func() {
		if err := s.Start(); err != nil {
			errCh <- err
		}
		close(errCh)
	}()

	return errCh
}

// Shutdown melakukan graceful shutdown dengan menyelesaikan in-flight requests
// Requirements: 8.4, 10.3
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.RLock()
	if !s.started {
		s.mu.RUnlock()
		return nil
	}
	s.mu.RUnlock()

	var shutdownErr error

	// Use sync.Once to ensure shutdown logic runs only once
	s.shutdownOnce.Do(func() {
		s.logger.Info("initiating graceful shutdown")

		// Signal shutdown (safe to close only once)
		close(s.shutdownCh)

		// Graceful shutdown - waits for in-flight requests to complete
		shutdownErr = s.httpServer.Shutdown(ctx)
		if shutdownErr != nil {
			s.logger.Error("shutdown error", zap.Error(shutdownErr))
		} else {
			s.logger.Info("server shutdown complete")
		}

		s.mu.Lock()
		s.started = false
		s.mu.Unlock()
	})

	return shutdownErr
}

// ShutdownWithTimeout performs graceful shutdown with a timeout
func (s *Server) ShutdownWithTimeout(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return s.Shutdown(ctx)
}

// IsRunning returns whether the server is currently running
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.started
}

// ShutdownCh returns a channel that is closed when shutdown is initiated
func (s *Server) ShutdownCh() <-chan struct{} {
	return s.shutdownCh
}

// Addr returns the server address
func (s *Server) Addr() string {
	return s.httpServer.Addr
}

// SetTLSConfig sets the TLS configuration for the server
// Requirements: 9.1, 9.2, 9.3
func (s *Server) SetTLSConfig(tlsConfig *tls.Config) {
	s.tlsConfig = tlsConfig
	s.httpServer.TLSConfig = tlsConfig
}

// StartTLS starts the server with TLS enabled
// Requirements: 9.1, 9.4
func (s *Server) StartTLS(certFile, keyFile string) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return fmt.Errorf("server already started")
	}
	s.started = true
	s.mu.Unlock()

	addr := s.httpServer.Addr
	s.logger.Info("starting HTTPS server with TLS",
		zap.String("address", addr),
		zap.Duration("read_timeout", s.config.ReadTimeout),
		zap.Duration("write_timeout", s.config.WriteTimeout),
		zap.Duration("idle_timeout", s.config.IdleTimeout),
	)

	// Create listener
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		s.mu.Lock()
		s.started = false
		s.mu.Unlock()
		return fmt.Errorf("failed to create listener: %w", err)
	}

	// Start serving with TLS
	err = s.httpServer.ServeTLS(listener, certFile, keyFile)
	if err != nil && err != http.ErrServerClosed {
		s.logger.Error("TLS server error", zap.Error(err))
		return fmt.Errorf("TLS server error: %w", err)
	}

	return nil
}

// StartWithTLSConfig starts the server using the pre-configured TLS settings
// Requirements: 9.1, 9.2, 9.3, 9.4
func (s *Server) StartWithTLSConfig() error {
	if s.tlsConfig == nil {
		return fmt.Errorf("TLS config not set, use SetTLSConfig first")
	}

	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return fmt.Errorf("server already started")
	}
	s.started = true
	s.mu.Unlock()

	addr := s.httpServer.Addr
	s.logger.Info("starting HTTPS server with TLS config",
		zap.String("address", addr),
		zap.Duration("read_timeout", s.config.ReadTimeout),
		zap.Duration("write_timeout", s.config.WriteTimeout),
		zap.Duration("idle_timeout", s.config.IdleTimeout),
	)

	// Create TLS listener
	listener, err := tls.Listen("tcp", addr, s.tlsConfig)
	if err != nil {
		s.mu.Lock()
		s.started = false
		s.mu.Unlock()
		s.logger.Error("failed to create TLS listener", zap.Error(err))
		return fmt.Errorf("failed to create TLS listener: %w", err)
	}

	// Start serving
	err = s.httpServer.Serve(listener)
	if err != nil && err != http.ErrServerClosed {
		s.logger.Error("TLS server error", zap.Error(err))
		return fmt.Errorf("TLS server error: %w", err)
	}

	return nil
}

// StartTLSAsync starts the TLS server in a goroutine and returns immediately
func (s *Server) StartTLSAsync(certFile, keyFile string) <-chan error {
	errCh := make(chan error, 1)

	go func() {
		if err := s.StartTLS(certFile, keyFile); err != nil {
			errCh <- err
		}
		close(errCh)
	}()

	return errCh
}

// StartWithTLSConfigAsync starts the server with TLS config in a goroutine
func (s *Server) StartWithTLSConfigAsync() <-chan error {
	errCh := make(chan error, 1)

	go func() {
		if err := s.StartWithTLSConfig(); err != nil {
			errCh <- err
		}
		close(errCh)
	}()

	return errCh
}

// IsTLSEnabled returns whether TLS is configured
func (s *Server) IsTLSEnabled() bool {
	return s.tlsConfig != nil
}
