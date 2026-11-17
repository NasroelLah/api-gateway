# Built-in Configuration

The gateway uses built-in defaults; it does not load an embedded YAML file at
startup. Configuration precedence, highest first:

1. Command-line flags.
2. Environment variables such as `SERVER_PORT` and `LOGGING_LEVEL`.
3. YAML file specified with `-config`.
4. Built-in defaults.

Dynamic upstream routing is disabled by default and no upstreams are configured.
To route requests, provide a YAML file:

```bash
cp config.example.yaml config.yaml
# Edit upstream targets, allowed hosts, and trusted proxy CIDRs for your network.
./gateway -config config.yaml
```

Kubernetes manifests mount the ConfigMap and pass the file with `-config`. See
[README.md](README.md#configuration) and [DEPLOYMENT.md](DEPLOYMENT.md) for
configuration, trusted ingress, and deployment details.
