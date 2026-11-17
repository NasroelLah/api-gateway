# API Gateway

API Gateway built with Go, providing routing, security, and observability.

## Features

- **Reverse Proxy**: Static and dynamic upstream routing with longest prefix matching
- **Security**: IP whitelisting, API key authentication, request sanitization
- **Rate Limiting**: Per-client in-memory rate limiting
- **Observability**: Structured logging, Prometheus metrics, OpenTelemetry tracing
- **TLS/mTLS**: TLS termination with mutual TLS support
- **High Performance**: Connection pooling, graceful shutdown, configurable timeouts

## Quick Start

### Prerequisites

- Go 1.27.4 or higher
- (Optional) OpenTelemetry Collector for tracing

### Installation

```bash
# Clone the repository
git clone https://github.com/nasroellah/api-gateway.git
cd api-gateway

# Install dependencies
go mod download

# Copy example config
cp config.example.yaml config.yaml

# Edit config.yaml with your settings
```

### Running the Gateway

```bash
# Option 1: Run with built-in defaults (no config file needed)
go run ./cmd/gateway

# Option 2: Run with external config file
go run ./cmd/gateway -config config.yaml

# Option 3: Quick start with CLI flags
go run ./cmd/gateway -port 8080 -api-key my-secret-key

# Option 4: Build and run with built-in defaults
go build -o gateway ./cmd/gateway
./gateway

# Option 5: Build and run with CLI flags
./gateway -port 8080 -host 0.0.0.0 -api-key my-secret-key -log-level debug

# Option 6: Override with environment variables
export SERVER_PORT=8080
export LOGGING_LEVEL=debug
./gateway
```

### Command Line Flags

| Flag | Description | Example |
|------|-------------|---------|
| `-config` | Path to YAML config file | `-config config.yaml` |
| `-port` | Server port (overrides config) | `-port 8080` |
| `-host` | Server bind address | `-host 0.0.0.0` |
| `-api-key` | API key (enables auth if set) | `-api-key my-secret` |
| `-log-level` | Log level: debug, info, warn, error | `-log-level debug` |

The gateway will start on `http://0.0.0.0:7070` by default.

## Configuration

Configuration can be provided via built-in defaults, a YAML file selected with
`-config`, environment variables, and CLI flags.

Environment variables use the format: `<KEY>_<SUBKEY>` (e.g., `SERVER_PORT=7070`)

### Configuration Priority

```
CLI Flags > Environment Variables > External Config File > Built-in Defaults
```

### Using Built-in Defaults

The gateway can run without an external config file:

```bash
# Run with built-in defaults (no -config flag needed)
./gateway

# Or with Docker
docker run -p 7070:7070 api-gateway:latest
```

Built-in defaults can be overridden with environment variables. Dynamic upstream routing
is disabled until explicitly configured; the default configuration has no upstreams.

```bash
# Override defaults with environment variables
export SERVER_PORT=8080
export LOGGING_LEVEL=debug
export WHITELIST_ENABLED=false
./gateway
```

### Using External Configuration

Override defaults with an external YAML file:

```bash
# Copy example config
cp config.example.yaml config.yaml

# Edit config.yaml with your settings
# ...

# Run with external config
./gateway -config config.yaml

# Or with Docker
docker run -p 7070:7070 \
  -v ${PWD}/config.yaml:/app/config.yaml \
  api-gateway:latest -config /app/config.yaml
```

### Example Configuration

```yaml
server:
  host: "0.0.0.0"
  port: 7070
  read_timeout: 30s
  write_timeout: 90s
  idle_timeout: 120s
  # Only trust forwarding headers from these proxies; empty by default.
  trusted_proxies: []

proxy:
  max_idle_conns: 1000
  max_idle_conns_per_host: 100
  timeout: 60s
  
  # Static routing rules
  upstreams:
    - path_prefix: "/api/v1/users"
      target: "http://user-service:7070"
    - path_prefix: "/api/v1/orders"
      target: "http://order-service:7070"
  
  # Dynamic routing via header
  dynamic_upstream:
    enabled: true
    header_name: "X-Upstream-Target"
    allowed_hosts:
      - "*.internal.example.com"
      - "*.svc.cluster.local"

whitelist:
  enabled: true
  entries:
    - "10.0.0.0/8"
    - "172.16.0.0/12"
    - "192.168.0.0/16"
    - "127.0.0.1/32"
  reload_interval: 60s

rate_limit:
  enabled: true
  type: "memory"  # redis is not integrated and startup rejects it
  requests_per_second: 100
  burst: 200

logging:
  level: "info"
  format: "json"
  output: "stdout"

metrics:
  enabled: true
  path: "/metrics"

tracing:
  enabled: true
  service_name: "api-gateway"
  exporter: "otlp"
  endpoint: "otel-collector:4317"
  sample_rate: 0.1
```

### Environment Variables

Override any config value using environment variables:

```bash
export SERVER_PORT=7070
export LOGGING_LEVEL=debug
export WHITELIST_ENABLED=false
export RATE_LIMIT_REQUESTS_PER_SECOND=200
```

Dynamic upstreams are disabled by default. If enabled, configure a restrictive
`allowed_hosts` list and network egress policy; `*` allows requests to arbitrary
HTTP(S) hosts. Behind a trusted ingress, configure its IPs/CIDRs in
`server.trusted_proxies`; never trust proxy headers from arbitrary clients.

In-memory rate limits are per gateway process, not shared between replicas.
Selecting `rate_limit.type: redis` currently fails startup rather than silently
using a local limit.

Use OTLP for new tracing deployments. The Zipkin exporter remains available for
compatibility but is deprecated upstream; migrate to Zipkin's OTLP ingestion or
an OpenTelemetry Collector before its removal.

The current OTLP gRPC exporter uses an insecure connection. Protect the
collector network or provide transport encryption outside the gateway.

Hostname-based IP whitelist entries are resolved asynchronously. They deny
access until a DNS result is available and after it expires; IP/CIDR entries
do not depend on DNS. Metrics `path` labels now use route patterns rather than
raw URLs; update dashboards that previously selected literal request paths.

## Usage Examples

### Static Routing

Requests are routed based on path prefix:

```bash
# Routes to http://user-service:7070/api/v1/users/123
curl http://localhost:7070/api/v1/users/123

# Routes to http://order-service:7070/api/v1/orders/456
curl http://localhost:7070/api/v1/orders/456
```

### Dynamic Routing

Route to any allowed upstream via header:

```bash
curl -H "X-Upstream-Target: http://service.internal.example.com" \
     http://localhost:7070/api/endpoint
```

### API Key Authentication

When enabled, include API key in requests:

```bash
curl -H "X-API-Key: your-api-key-here" \
     http://localhost:7070/api/v1/users
```

### Metrics

Access Prometheus metrics:

```bash
curl http://localhost:7070/metrics
```

## Architecture

```
┌─────────────┐
│   Client    │
└──────┬──────┘
       │
       ▼
┌─────────────────────────────────────┐
│         API Gateway                 │
│  ┌───────────────────────────────┐  │
│  │  Middleware Chain             │  │
│  │  - Request ID                 │  │
│  │  - Logging                    │  │
│  │  - Tracing                    │  │
│  │  - Metrics                    │  │
│  │  - IP Whitelist               │  │
│  │  - Rate Limiting              │  │
│  │  - API Key Auth               │  │
│  │  - Request Sanitization       │  │
│  └───────────────────────────────┘  │
│  ┌───────────────────────────────┐  │
│  │  Reverse Proxy                │  │
│  │  - Static Routing             │  │
│  │  - Dynamic Routing            │  │
│  └───────────────────────────────┘  │
└─────────────────────────────────────┘
       │
       ▼
┌─────────────────────────────────────┐
│      Upstream Services              │
│  ┌──────────┐  ┌──────────┐        │
│  │  Users   │  │  Orders  │  ...   │
│  └──────────┘  └──────────┘        │
└─────────────────────────────────────┘
```

## Development

### Project Structure

```
.
├── cmd/
│   └── gateway/          # Main application entry point
├── internal/
│   ├── config/           # Configuration management
│   ├── middleware/       # HTTP middleware
│   ├── observability/    # Logging, metrics, tracing
│   ├── proxy/            # Reverse proxy logic
│   ├── security/         # Security features
│   └── server/           # HTTP server setup
├── deploy/
│   └── kubernetes/       # Kubernetes manifests
├── config.yaml           # Configuration file
└── Dockerfile            # Container image
```

### Running Tests

```bash
# Run all tests
go test ./...

# Run with coverage
go test -cover ./...

# Run property-based tests
go test -v ./internal/... -run Property
```

### Building

```bash
# Build binary
CGO_ENABLED=0 GOOS=linux GOARCH=amd64
go build -trimpath -ldflags="-s -w" -o bin/api-gateway ./cmd/gateway

# Build Docker image
docker build -t api-gateway:latest .
```

## Deployment

### Docker

```bash
# Build image
docker build -t api-gateway:latest .

# Option 1: Run with embedded config (simplest)
docker run -p 7070:7070 api-gateway:latest

# Option 2: Run with CLI flags
docker run -p 8080:8080 api-gateway:latest -port 8080 -api-key my-secret-key

# Option 3: Run with external config file
docker run -p 7070:7070 \
  -v ${PWD}/config.yaml:/app/config.yaml \
  api-gateway:latest -config /app/config.yaml

# Option 4: Run with environment variables
docker run -p 7070:7070 \
  -e SERVER_PORT=7070 \
  -e LOGGING_LEVEL=debug \
  -e WHITELIST_ENABLED=false \
  api-gateway:latest
```

### Kubernetes

```bash
# Apply manifests
kubectl apply -f deploy/kubernetes/

# Check status
kubectl get pods -n api-gateway
kubectl logs -n api-gateway -l app=api-gateway
```

## Monitoring

### Prometheus Metrics

Available metrics:
- `gateway_requests_total` - Total HTTP requests
- `gateway_request_duration_seconds` - Request duration histogram
- `gateway_requests_in_flight` - Current in-flight requests
- `gateway_upstream_requests_total` - Upstream requests by target
- `gateway_upstream_request_duration_seconds` - Upstream latency

### Tracing

Distributed tracing with OpenTelemetry:
- Automatic trace propagation (W3C Trace Context)
- Span creation for each request
- Upstream request tracking
- Configurable sampling rate

### Logging

Structured JSON logging with fields:
- `request_id` - Unique request identifier
- `method` - HTTP method
- `path` - Request path
- `status` - Response status code
- `duration` - Request duration
- `client_ip` - Client IP address

## Security

### IP Whitelisting

Restrict access to specific IP ranges:

```yaml
whitelist:
  enabled: true
  entries:
    - "10.0.0.0/8"
    - "192.168.1.100/32"
```

### API Key Authentication

Protect endpoints with API keys:

```yaml
auth:
  enabled: true
  api_key: "your-secret-key"
  header_name: "X-API-Key"
```

### Request Sanitization

Automatic sanitization of:
- SQL injection patterns
- XSS attempts
- Path traversal
- Command injection

### TLS/mTLS

Enable TLS termination:

```yaml
tls:
  enabled: true
  cert_file: "/etc/tls/tls.crt"
  key_file: "/etc/tls/tls.key"
  min_version: "1.3"
  
  mtls:
    enabled: true
    ca_file: "/etc/tls/ca.crt"
```

## Performance

- Connection pooling for upstream requests
- Configurable timeouts and limits
- In-memory rate limiting with minimal overhead
- Graceful shutdown with 30s timeout
- Efficient request routing with prefix matching

## Troubleshooting

### Gateway won't start

Check configuration validation:
```bash
go run ./cmd/gateway/main.go -config config.yaml
```

### High latency

- Check upstream service health
- Review rate limiting settings
- Monitor metrics at `/metrics`
- Check trace data for bottlenecks

### Rate limiting issues

- Verify rate limit configuration
- Check client IP detection
- Consider using Redis for distributed rate limiting

## License

MIT License - see LICENSE file for details

## Contributing

Contributions welcome! Please open an issue or submit a pull request.
