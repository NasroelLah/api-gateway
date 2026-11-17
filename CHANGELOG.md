# Changelog

## [2.0.0] - 2026-11-17

### Breaking changes

- Dynamic upstream routing is disabled by default and has no allowed hosts.
  Configure static upstreams or explicitly enable dynamic routing with a
  restrictive `proxy.dynamic_upstream.allowed_hosts` list.
- Forwarded client IP headers are accepted only from explicitly configured
  `server.trusted_proxies` IP addresses or CIDR ranges. Without trusted proxies,
  the connection peer is used for rate limiting, logging, and IP whitelisting.
- `rate_limit.type: redis` is rejected during startup because the application
  does not yet integrate a Redis client. The memory limiter is per process.
- Hostname-based IP whitelist entries are resolved asynchronously. Requests are
  denied until a result is available and after it expires.
- The default server write timeout is 90 seconds; the upstream response-header
  timeout remains 60 seconds. Prometheus `path` labels now contain bounded route
  patterns rather than raw request paths; dashboards may require updates.
- Go 1.27.4 or newer is required.

### Security and reliability

- Load the mounted Kubernetes ConfigMap with the `-config` flag and validate
  trusted proxy entries.
- Preserve streaming and protocol upgrades through response middleware and the
  reverse proxy. Avoid recording URL query strings in tracing attributes.
- Refresh hostname whitelist DNS entries outside the request path and fail
  closed when cached results expire.

### Performance

- Shard the in-memory rate limiter and avoid reservation allocations on the
  allowed request path.
- Limit Prometheus method, route, and upstream error label cardinality.

### Dependencies and operations

- Update Go modules and GitHub Actions to current stable versions, including
  chi 5.5.0, OpenTelemetry 1.47.0, and Prometheus client 1.25.0.
- Add CI for tests, race detection, build, `go vet`, and `govulncheck`.
- OpenTelemetry's Zipkin exporter remains available for compatibility but is
  deprecated upstream. Migrate to OTLP or an OpenTelemetry Collector. The
  current OTLP gRPC exporter uses an insecure connection; protect its network
  or provide transport encryption outside the gateway.
