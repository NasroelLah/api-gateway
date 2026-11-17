package testing

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/nasroellah/api-gateway/internal/config"
	"github.com/nasroellah/api-gateway/internal/observability"
	"github.com/nasroellah/api-gateway/internal/proxy"
	"github.com/nasroellah/api-gateway/internal/server"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

func gatewayForUpstream(t *testing.T, target string) *httptest.Server {
	t.Helper()
	manager := proxy.NewProxyManager(&config.ProxyConfig{
		Upstreams: []config.UpstreamConfig{{PathPrefix: "/", Target: target}},
		Timeout:   5 * time.Second,
	})
	t.Cleanup(manager.Close)
	router := chi.NewRouter()
	server.SetupRouter(router, &server.RouterConfig{MetricsEnabled: true, TracingEnabled: true}, &server.RouterDependencies{
		Logger: zap.NewNop(), ProxyMgr: manager,
		Metrics: observability.NewMetrics(prometheus.NewRegistry()),
		Tracer: trace.NewNoopTracerProvider().Tracer("integration"),
	})
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)
	return gateway
}

func TestProxyUpgradesConnectionThroughMiddleware(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "missing upgrade", http.StatusBadRequest)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		if err := rw.Flush(); err != nil {
			return
		}
		_, _ = io.Copy(conn, rw)
	}))
	defer upstream.Close()
	gateway := gatewayForUpstream(t, upstream.URL)
	address, err := url.Parse(gateway.URL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", address.Host, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "GET /socket HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n", address.Host)
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %d, want 101", resp.StatusCode)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 4)
	if _, err := io.ReadFull(reader, got); err != nil || string(got) != "ping" {
		t.Fatalf("tunnel echo = %q, error = %v", got, err)
	}
}

func TestProxyFlushesStreamingResponsesThroughMiddleware(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-release
		fmt.Fprint(w, "data: second\n\n")
	}))
	defer upstream.Close()
	defer close(release)
	gateway := gatewayForUpstream(t, upstream.URL)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(gateway.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	first := make([]byte, len("data: first\n\n"))
	if _, err := io.ReadFull(resp.Body, first); err != nil || string(first) != "data: first\n\n" {
		t.Fatalf("first event = %q, error = %v", first, err)
	}
}
