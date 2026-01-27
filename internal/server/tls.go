package server

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/nasroellah/api-gateway/internal/config"
	"go.uber.org/zap"
)

// TLSManager handles TLS configuration for the server
// Requirements: 9.1, 9.2, 9.3, 9.4
type TLSManager struct {
	config *config.TLSConfig
	logger *zap.Logger
}

// NewTLSManager creates a new TLS manager
func NewTLSManager(cfg *config.TLSConfig, logger *zap.Logger) *TLSManager {
	return &TLSManager{
		config: cfg,
		logger: logger,
	}
}

// BuildTLSConfig creates a tls.Config based on the configuration
// Requirements: 9.1, 9.2, 9.3
func (tm *TLSManager) BuildTLSConfig() (*tls.Config, error) {
	if !tm.config.Enabled {
		return nil, nil
	}

	// Load server certificate and key
	cert, err := tls.LoadX509KeyPair(tm.config.CertFile, tm.config.KeyFile)
	if err != nil {
		tm.logger.Error("failed to load TLS certificate",
			zap.String("cert_file", tm.config.CertFile),
			zap.String("key_file", tm.config.KeyFile),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to load TLS certificate: %w", err)
	}

	// Determine minimum TLS version
	// Requirements: 9.3
	minVersion, err := parseTLSVersion(tm.config.MinVersion)
	if err != nil {
		return nil, err
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   minVersion,
	}

	// Configure mTLS if enabled
	// Requirements: 9.2
	if tm.config.MTLS.Enabled {
		clientCAPool, err := tm.loadClientCAPool()
		if err != nil {
			return nil, err
		}
		tlsConfig.ClientCAs = clientCAPool
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	}

	tm.logger.Info("TLS configuration built",
		zap.String("min_version", tm.config.MinVersion),
		zap.Bool("mtls_enabled", tm.config.MTLS.Enabled),
	)

	return tlsConfig, nil
}

// loadClientCAPool loads the CA certificate pool for mTLS client verification
func (tm *TLSManager) loadClientCAPool() (*x509.CertPool, error) {
	caCert, err := os.ReadFile(tm.config.MTLS.CAFile)
	if err != nil {
		tm.logger.Error("failed to read CA certificate",
			zap.String("ca_file", tm.config.MTLS.CAFile),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to read CA certificate: %w", err)
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCert) {
		tm.logger.Error("failed to parse CA certificate",
			zap.String("ca_file", tm.config.MTLS.CAFile),
		)
		return nil, fmt.Errorf("failed to parse CA certificate from %s", tm.config.MTLS.CAFile)
	}

	return caPool, nil
}

// parseTLSVersion converts a version string to tls.Version constant
func parseTLSVersion(version string) (uint16, error) {
	switch version {
	case "1.2":
		return tls.VersionTLS12, nil
	case "1.3":
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("unsupported TLS version: %s (supported: 1.2, 1.3)", version)
	}
}

// GetTLSVersionString returns the string representation of a TLS version
func GetTLSVersionString(version uint16) string {
	switch version {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return "Unknown"
	}
}

// IsEnabled returns whether TLS is enabled
func (tm *TLSManager) IsEnabled() bool {
	return tm.config.Enabled
}

// IsMTLSEnabled returns whether mTLS is enabled
func (tm *TLSManager) IsMTLSEnabled() bool {
	return tm.config.Enabled && tm.config.MTLS.Enabled
}
