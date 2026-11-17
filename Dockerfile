# Build stage
FROM golang:1.27.4-alpine AS builder

# Install ca-certificates for HTTPS requests
RUN apk add --no-cache ca-certificates git

WORKDIR /app

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary with optimizations
# CGO_ENABLED=0 for static binary
# -ldflags for smaller binary size
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-w -s -X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo 'dev')" \
    -o /gateway \
    ./cmd/gateway

# Final stage - distroless for minimal attack surface
FROM gcr.io/distroless/static-debian13:nonroot

# Copy CA certificates for HTTPS
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the binary
COPY --from=builder /gateway /gateway

# Use non-root user (65532 is the nonroot user in distroless)
USER 65532:65532

# Expose default port
EXPOSE 7070

# Health check endpoint
# Note: distroless doesn't have curl/wget, health checks should be done via K8s probes

ENTRYPOINT ["/gateway"]
