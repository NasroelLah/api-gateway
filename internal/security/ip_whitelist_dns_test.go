package security

import (
	"net"
	"sync"
	"testing"
	"time"
)

type controlledDNSResolver struct {
	mu      sync.Mutex
	results map[string][]net.IP
	started chan string
	block   chan struct{}
}

func (r *controlledDNSResolver) LookupIP(host string) ([]net.IP, error) {
	if r.started != nil {
		r.started <- host
	}
	if r.block != nil {
		<-r.block
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.results[host], nil
}

func (r *controlledDNSResolver) set(host string, ips ...net.IP) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results[host] = ips
}

func waitForHostnameResolution(t *testing.T, started <-chan string, host string) {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case got := <-started:
			if got == host {
				return
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for resolution of %q", host)
		}
	}
}

func TestIsAllowedDoesNotWaitForSlowDNS(t *testing.T) {
	resolver := &controlledDNSResolver{
		results: map[string][]net.IP{"allowed.example": {net.ParseIP("192.0.2.1")}},
		started: make(chan string, 4),
		block:   make(chan struct{}),
	}
	whitelist, err := NewIPWhitelist([]string{"allowed.example"}, WithDNSResolver(resolver))
	if err != nil {
		t.Fatal(err)
	}
	defer whitelist.Close()
	waitForHostnameResolution(t, resolver.started, "allowed.example")

	done := make(chan bool, 1)
	go func() { done <- whitelist.IsAllowed(net.ParseIP("192.0.2.1")) }()
	select {
	case allowed := <-done:
		if allowed {
			t.Fatal("unresolved hostname unexpectedly allowed an IP")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("IsAllowed blocked on DNS resolution")
	}
	close(resolver.block)
}

func TestExpiredDNSCacheFailsClosed(t *testing.T) {
	resolver := &controlledDNSResolver{
		results: map[string][]net.IP{"allowed.example": {net.ParseIP("192.0.2.1")}},
		started: make(chan string, 4),
	}
	whitelist, err := NewIPWhitelist([]string{"allowed.example"}, WithDNSResolver(resolver), WithDNSCacheTTL(30*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer whitelist.Close()
	waitForHostnameResolution(t, resolver.started, "allowed.example")

	deadline := time.Now().Add(time.Second)
	for !whitelist.IsAllowed(net.ParseIP("192.0.2.1")) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !whitelist.IsAllowed(net.ParseIP("192.0.2.1")) {
		t.Fatal("IP was not allowed after successful DNS refresh")
	}
	resolver.set("allowed.example")
	time.Sleep(60 * time.Millisecond)
	if whitelist.IsAllowed(net.ParseIP("192.0.2.1")) {
		t.Fatal("expired DNS cache continued allowing an IP")
	}
}

func TestCloseStopsDNSRefresh(t *testing.T) {
	resolver := &controlledDNSResolver{
		results: map[string][]net.IP{},
		started: make(chan string, 4),
	}
	whitelist, err := NewIPWhitelist([]string{"allowed.example"}, WithDNSResolver(resolver), WithDNSCacheTTL(200*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	waitForHostnameResolution(t, resolver.started, "allowed.example")
	whitelist.Close()
	whitelist.Close()
	time.Sleep(220 * time.Millisecond)
	select {
	case host := <-resolver.started:
		t.Fatalf("DNS refresh continued after Close: %q", host)
	default:
	}
}

func TestAddAndReloadRefreshDNSConsistently(t *testing.T) {
	resolver := &controlledDNSResolver{
		results: map[string][]net.IP{
			"first.example":  {net.ParseIP("192.0.2.1")},
			"second.example": {net.ParseIP("192.0.2.2")},
		},
		started: make(chan string, 8),
	}
	whitelist, err := NewIPWhitelist([]string{"first.example"}, WithDNSResolver(resolver))
	if err != nil {
		t.Fatal(err)
	}
	defer whitelist.Close()
	waitForHostnameResolution(t, resolver.started, "first.example")
	if err := whitelist.Add("second.example"); err != nil {
		t.Fatal(err)
	}
	waitForHostnameResolution(t, resolver.started, "second.example")
	deadline := time.Now().Add(time.Second)
	for !whitelist.IsAllowed(net.ParseIP("192.0.2.2")) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !whitelist.IsAllowed(net.ParseIP("192.0.2.2")) {
		t.Fatal("Add did not refresh the newly added hostname")
	}
	if err := whitelist.Reload([]string{"192.0.2.3", "second.example"}); err != nil {
		t.Fatal(err)
	}
	if whitelist.IsAllowed(net.ParseIP("192.0.2.1")) {
		t.Fatal("Reload retained access from removed hostname")
	}
	if !whitelist.IsAllowed(net.ParseIP("192.0.2.3")) {
		t.Fatal("Reload did not atomically install the new IP entry")
	}
}
