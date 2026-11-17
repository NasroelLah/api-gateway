# Deployment Guide

This guide covers various deployment scenarios for the API Gateway.

## Table of Contents

- [Configuration Priority](#configuration-priority)
- [Local Development](#local-development)
- [Docker Deployment](#docker-deployment)
- [Kubernetes Deployment](#kubernetes-deployment)
- [Environment Variables](#environment-variables)

## Configuration Priority

The gateway uses the following configuration priority:

```
CLI Flags (highest)
    ↓
Environment Variables
    ↓
External Config File (-config flag)
    ↓
Built-in Defaults (lowest)
```

## Command Line Flags

| Flag | Description | Example |
|------|-------------|---------|
| `-config` | Path to YAML config file | `-config config.yaml` |
| `-port` | Server port | `-port 8080` |
| `-host` | Server bind address | `-host 0.0.0.0` |
| `-api-key` | API key (enables auth) | `-api-key my-secret` |
| `-log-level` | Log level | `-log-level debug` |

## Local Development

### Option 1: Run with Built-in Defaults

The simplest way to run the gateway for development:

```bash
# Build
go build -o gateway ./cmd/gateway

# Run with built-in defaults
./gateway
```

The gateway uses built-in defaults. Configure upstreams explicitly before forwarding traffic.

### Option 2: Run with CLI Flags (Quick Start)

For quick testing without config files:

```bash
# Run on custom port with API key
./gateway -port 8080 -api-key my-secret-key

# Run with debug logging
./gateway -port 8080 -log-level debug

# Combine multiple flags
./gateway -port 8080 -host 127.0.0.1 -api-key my-key -log-level debug
```

### Option 3: Run with External Config

For custom configuration:

```bash
# Copy example config
cp config.example.yaml config.yaml

# Edit config.yaml
# ...

# Run with external config
./gateway -config config.yaml
```

### Option 3: Override with Environment Variables

Quick overrides without config files:

```bash
export SERVER_PORT=8080
export LOGGING_LEVEL=debug
export WHITELIST_ENABLED=false
./gateway
```

## Docker Deployment

### Build Image

```bash
docker build -t api-gateway:latest .
```

### Run with Built-in Defaults

```bash
# Run with built-in defaults
docker run -p 7070:7070 api-gateway:latest

# Run with environment variable overrides
docker run -p 7070:7070 \
  -e SERVER_PORT=7070 \
  -e LOGGING_LEVEL=info \
  -e WHITELIST_ENABLED=true \
  -e WHITELIST_ENTRIES="10.0.0.0/8,172.16.0.0/12,192.168.0.0/16" \
  api-gateway:latest
```

### Run with External Config File

For complex configurations:

```bash
# Windows PowerShell
docker run -p 7070:7070 `
  -v ${PWD}/config.yaml:/app/config.yaml `
  api-gateway:latest -config /app/config.yaml

# Linux/Mac
docker run -p 7070:7070 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  api-gateway:latest -config /app/config.yaml
```

### Docker Compose

Create `docker-compose.yml`:

```yaml
version: '3.8'

services:
  api-gateway:
    image: api-gateway:latest
    ports:
      - "7070:7070"
    environment:
      # Override built-in defaults with environment variables
      SERVER_PORT: 7070
      LOGGING_LEVEL: info
      WHITELIST_ENABLED: "true"
      WHITELIST_ENTRIES: "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"
      RATE_LIMIT_ENABLED: "true"
      RATE_LIMIT_REQUESTS_PER_SECOND: 100
      RATE_LIMIT_BURST: 200
      METRICS_ENABLED: "true"
      TRACING_ENABLED: "false"
    # Optional: mount external config
    # volumes:
    #   - ./config.yaml:/app/config.yaml
    # command: ["-config", "/app/config.yaml"]
    restart: unless-stopped
    healthcheck:
      test: ["CMD-SHELL", "wget --no-verbose --tries=1 --spider http://localhost:7070/health || exit 1"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 40s

```

Run with:

```bash
docker-compose up -d
```

## Kubernetes Deployment

### Option 1: Using Built-in Defaults with ConfigMap Overrides

Create a ConfigMap for environment variable overrides:

For production, prefer a mounted config file as shown in Option 2 so the
upstream allowlist and trusted proxy CIDRs are explicit. The manifests under
`deploy/kubernetes/` mount the file and pass its path with `-config`.

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: api-gateway-config
  namespace: api-gateway
data:
  SERVER_PORT: "7070"
  LOGGING_LEVEL: "info"
  WHITELIST_ENABLED: "true"
  WHITELIST_ENTRIES: "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"
  RATE_LIMIT_ENABLED: "true"
  RATE_LIMIT_TYPE: "memory"
  RATE_LIMIT_REQUESTS_PER_SECOND: "100"
  RATE_LIMIT_BURST: "200"
  METRICS_ENABLED: "true"
  METRICS_PATH: "/metrics"
```

Deployment:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api-gateway
  namespace: api-gateway
spec:
  replicas: 3
  selector:
    matchLabels:
      app: api-gateway
  template:
    metadata:
      labels:
        app: api-gateway
    spec:
      containers:
      - name: gateway
        image: api-gateway:latest
        ports:
        - containerPort: 7070
          name: http
        envFrom:
        - configMapRef:
            name: api-gateway-config
        resources:
          requests:
            memory: "128Mi"
            cpu: "100m"
          limits:
            memory: "512Mi"
            cpu: "500m"
        livenessProbe:
          httpGet:
            path: /health
            port: 7070
          initialDelaySeconds: 10
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /ready
            port: 7070
          initialDelaySeconds: 5
          periodSeconds: 5
```

### Option 2: Using External Config File

Create ConfigMap from file:

```bash
kubectl create configmap api-gateway-yaml \
  --from-file=config.yaml=./config.yaml \
  -n api-gateway
```

Deployment with mounted config:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api-gateway
  namespace: api-gateway
spec:
  replicas: 3
  selector:
    matchLabels:
      app: api-gateway
  template:
    metadata:
      labels:
        app: api-gateway
    spec:
      containers:
      - name: gateway
        image: api-gateway:latest
        args: ["-config", "/etc/gateway/config.yaml"]
        ports:
        - containerPort: 7070
          name: http
        volumeMounts:
        - name: config
          mountPath: /etc/gateway
          readOnly: true
        resources:
          requests:
            memory: "128Mi"
            cpu: "100m"
          limits:
            memory: "512Mi"
            cpu: "500m"
      volumes:
      - name: config
        configMap:
          name: api-gateway-yaml
```

### Deploy All Resources

```bash
# Create namespace
kubectl create namespace api-gateway

# Apply all manifests
kubectl apply -f deploy/kubernetes/

# Check status
kubectl get pods -n api-gateway
kubectl logs -n api-gateway -l app=api-gateway -f
```

## Environment Variables

Complete list of environment variables:

### Server Configuration

```bash
SERVER_HOST=0.0.0.0
SERVER_PORT=7070
SERVER_READ_TIMEOUT=30s
SERVER_WRITE_TIMEOUT=30s
SERVER_IDLE_TIMEOUT=120s
```

### Proxy Configuration

```bash
PROXY_MAX_IDLE_CONNS=1000
PROXY_MAX_IDLE_CONNS_PER_HOST=100
PROXY_TIMEOUT=60s
PROXY_DYNAMIC_UPSTREAM_ENABLED=true
PROXY_DYNAMIC_UPSTREAM_HEADER_NAME=X-Upstream-Target
PROXY_DYNAMIC_UPSTREAM_ALLOWED_HOSTS="*.internal.example.com,*.svc.cluster.local"
```

### Whitelist Configuration

```bash
WHITELIST_ENABLED=true
WHITELIST_ENTRIES="10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,127.0.0.1/32"
WHITELIST_RELOAD_INTERVAL=60s
```

### Rate Limiting Configuration

```bash
RATE_LIMIT_ENABLED=true
RATE_LIMIT_TYPE=memory
RATE_LIMIT_REQUESTS_PER_SECOND=100
RATE_LIMIT_BURST=200
RATE_LIMIT_REDIS_ADDRESS=redis:6379
RATE_LIMIT_REDIS_PASSWORD=""
RATE_LIMIT_REDIS_DB=0
```

### Logging Configuration

```bash
LOGGING_LEVEL=info
LOGGING_FORMAT=json
LOGGING_OUTPUT=stdout
```

### Metrics Configuration

```bash
METRICS_ENABLED=true
METRICS_PATH=/metrics
```

### Tracing Configuration

```bash
TRACING_ENABLED=true
TRACING_SERVICE_NAME=api-gateway
TRACING_EXPORTER=otlp
TRACING_ENDPOINT=otel-collector:4317
TRACING_SAMPLE_RATE=0.1
```

### TLS Configuration

```bash
TLS_ENABLED=false
TLS_CERT_FILE=/etc/tls/tls.crt
TLS_KEY_FILE=/etc/tls/tls.key
TLS_MIN_VERSION=1.2
TLS_MTLS_ENABLED=false
TLS_MTLS_CA_FILE=/etc/tls/ca.crt
```

### Authentication Configuration

```bash
AUTH_ENABLED=true
AUTH_API_KEY=your-secret-key
AUTH_HEADER_NAME=X-API-Key
```

## Best Practices

### Development

- Use embedded config for quick local testing
- Override specific values with environment variables
- Use external config file for complex setups

### Staging/Production

- Use external config files mounted as ConfigMaps in Kubernetes
- Store secrets in Kubernetes Secrets, not ConfigMaps
- Use environment variables for environment-specific overrides
- Enable TLS/mTLS for production deployments
- Configure appropriate rate limits based on load testing
- Enable distributed tracing for debugging
- Set up Prometheus scraping for metrics

### Security

- Never commit sensitive values (API keys, passwords) to version control
- Use Kubernetes Secrets for sensitive configuration
- Enable IP whitelisting in production
- Use API key authentication for internal services
- Enable mTLS for service-to-service communication
- Regularly rotate API keys and certificates

## Troubleshooting

### Container won't start

Check logs:
```bash
docker logs <container-id>
```

Common issues:
- Invalid configuration (check validation errors)
- Port already in use
- Missing required configuration values

### Configuration not loading

Verify configuration priority:
1. Check environment variables: `docker exec <container-id> env`
2. Check mounted config file: `docker exec <container-id> cat /app/config.yaml`
3. Check embedded config is being used (look for log message)

### Kubernetes pod crashes

```bash
# Check pod status
kubectl describe pod <pod-name> -n api-gateway

# Check logs
kubectl logs <pod-name> -n api-gateway

# Check events
kubectl get events -n api-gateway --sort-by='.lastTimestamp'
```

## Monitoring

### Health Checks

- Liveness: `GET /health`
- Readiness: `GET /ready`

### Metrics

Access Prometheus metrics:
```bash
curl http://localhost:7070/metrics
```

### Logs

View structured JSON logs:
```bash
# Docker
docker logs -f <container-id>

# Kubernetes
kubectl logs -f <pod-name> -n api-gateway
```

## Scaling

### Horizontal Scaling

```bash
# Kubernetes
kubectl scale deployment api-gateway --replicas=5 -n api-gateway

# Docker Compose
docker-compose up -d --scale api-gateway=5
```

### Considerations

- Use Redis-based rate limiting for multi-replica deployments
- Configure appropriate resource limits
- Set up HPA (Horizontal Pod Autoscaler) in Kubernetes
- Monitor metrics to determine optimal replica count
