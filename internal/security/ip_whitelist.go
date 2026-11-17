package security

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// EntryType represents the type of whitelist entry
type EntryType int

const (
	EntryTypeIP       EntryType = iota // Single IP or CIDR
	EntryTypeHostname                  // Exact hostname (requires DNS lookup)
	EntryTypeDomain                    // Domain with wildcard support (e.g., *.example.com)
)

// WhitelistEntry represents a single whitelist entry
type WhitelistEntry struct {
	Type       EntryType
	Original   string     // Original entry string
	IPNet      *net.IPNet // For IP/CIDR entries
	Hostname   string     // For hostname entries (exact match)
	Domain     string     // For domain entries (suffix match)
	IsWildcard bool       // True if domain starts with *.
}

// DNSResolver interface for DNS lookups (allows mocking in tests)
type DNSResolver interface {
	LookupIP(host string) ([]net.IP, error)
}

// DefaultDNSResolver uses net.LookupIP for DNS resolution
type DefaultDNSResolver struct{}

func (d *DefaultDNSResolver) LookupIP(host string) ([]net.IP, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dnsLookupTimeout)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, len(addresses))
	for i, address := range addresses {
		ips[i] = address.IP
	}
	return ips, nil
}

// dnsCacheEntry stores cached DNS results
type dnsCacheEntry struct {
	IPs       []net.IP
	ExpiresAt time.Time
}

// IPWhitelist mengelola daftar IP/CIDR/Hostname/Domain yang diizinkan
// Implements default deny security model
// Supports:
//   - Single IPs: "192.168.1.1"
//   - CIDR notation: "10.0.0.0/8"
//   - Exact hostnames: "server.example.com" (resolved via DNS)
//   - Wildcard domains: "*.example.com" (matches any subdomain)
//   - Exact domains: "example.com" (matches only example.com)
type IPWhitelist struct {
	mu          sync.RWMutex
	ipEntries   []*net.IPNet
	hostEntries []WhitelistEntry
	allEntries  []WhitelistEntry
	dnsResolver DNSResolver
	dnsCacheTTL time.Duration
	dnsCache    map[string]*dnsCacheEntry
	cacheMu     sync.RWMutex
	closed      bool
	refreshing  bool
	refreshEpoch uint64
	refreshCh   chan struct{}
	stopCh      chan struct{}
	closeOnce   sync.Once
	workerDone   chan struct{}
}

// WhitelistOption configures the whitelist
type WhitelistOption func(*IPWhitelist)

// WithDNSResolver sets a custom DNS resolver
func WithDNSResolver(resolver DNSResolver) WhitelistOption {
	return func(w *IPWhitelist) {
		w.dnsResolver = resolver
	}
}

// WithDNSCacheTTL sets how long hostname results may authorize requests.
// A non-positive value disables hostname IP authorization (fail closed).
func WithDNSCacheTTL(ttl time.Duration) WhitelistOption {
	return func(w *IPWhitelist) {
		w.dnsCacheTTL = ttl
	}
}


// NewIPWhitelist membuat instance baru dengan daftar IP/CIDR/Hostname/Domain
// Accepts:
//   - Single IPs: "192.168.1.1"
//   - CIDR notation: "10.0.0.0/8"
//   - Hostnames: "host:server.example.com" or just hostname if not parseable as IP
//   - Wildcard domains: "*.example.com"
//   - Exact domains: "domain:example.com"
func NewIPWhitelist(entries []string, opts ...WhitelistOption) (*IPWhitelist, error) {
	w := &IPWhitelist{
		ipEntries:   make([]*net.IPNet, 0),
		hostEntries: make([]WhitelistEntry, 0),
		allEntries:  make([]WhitelistEntry, 0, len(entries)),
		dnsResolver: &DefaultDNSResolver{},
		dnsCacheTTL: 5 * time.Minute,
		dnsCache:    make(map[string]*dnsCacheEntry),
		refreshCh:   make(chan struct{}, 1),
		stopCh:      make(chan struct{}),
		workerDone:  make(chan struct{}),
	}

	for _, opt := range opts {
		opt(w)
	}

	for _, entry := range entries {
		if err := w.addEntry(entry); err != nil {
			return nil, fmt.Errorf("invalid whitelist entry %q: %w", entry, err)
		}
	}
	w.startRefreshLocked()
	return w, nil
}

// addEntry parses and adds a single entry to the whitelist
func (w *IPWhitelist) addEntry(entry string) error {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return errors.New("empty entry")
	}

	// Check for explicit prefixes
	if strings.HasPrefix(entry, "host:") {
		return w.addHostnameEntry(strings.TrimPrefix(entry, "host:"))
	}
	if strings.HasPrefix(entry, "domain:") {
		return w.addDomainEntry(strings.TrimPrefix(entry, "domain:"))
	}

	// Check for wildcard domain pattern
	if strings.HasPrefix(entry, "*.") {
		return w.addWildcardDomainEntry(entry)
	}

	// Try parsing as CIDR
	if strings.Contains(entry, "/") {
		return w.addCIDREntry(entry)
	}

	// Try parsing as IP
	if ip := net.ParseIP(entry); ip != nil {
		return w.addIPEntry(entry, ip)
	}

	// Assume it's a hostname if it looks like one (contains dots, not an IP)
	if strings.Contains(entry, ".") && !isIPAddress(entry) {
		return w.addHostnameEntry(entry)
	}

	return fmt.Errorf("cannot determine entry type: %s", entry)
}

// isIPAddress checks if a string looks like an IP address
func isIPAddress(s string) bool {
	return net.ParseIP(s) != nil
}

func (w *IPWhitelist) addIPEntry(original string, ip net.IP) error {
	var mask net.IPMask
	if ip.To4() != nil {
		mask = net.CIDRMask(32, 32)
		ip = ip.To4()
	} else {
		mask = net.CIDRMask(128, 128)
	}

	ipNet := &net.IPNet{IP: ip, Mask: mask}
	w.ipEntries = append(w.ipEntries, ipNet)
	w.allEntries = append(w.allEntries, WhitelistEntry{
		Type:     EntryTypeIP,
		Original: original,
		IPNet:    ipNet,
	})
	return nil
}

func (w *IPWhitelist) addCIDREntry(entry string) error {
	_, ipNet, err := net.ParseCIDR(entry)
	if err != nil {
		return fmt.Errorf("invalid CIDR: %w", err)
	}

	w.ipEntries = append(w.ipEntries, ipNet)
	w.allEntries = append(w.allEntries, WhitelistEntry{
		Type:     EntryTypeIP,
		Original: entry,
		IPNet:    ipNet,
	})
	return nil
}

func (w *IPWhitelist) addHostnameEntry(hostname string) error {
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return errors.New("empty hostname")
	}

	entry := WhitelistEntry{
		Type:     EntryTypeHostname,
		Original: hostname,
		Hostname: strings.ToLower(hostname),
	}
	w.hostEntries = append(w.hostEntries, entry)
	w.allEntries = append(w.allEntries, entry)
	return nil
}

func (w *IPWhitelist) addDomainEntry(domain string) error {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return errors.New("empty domain")
	}

	entry := WhitelistEntry{
		Type:       EntryTypeDomain,
		Original:   domain,
		Domain:     strings.ToLower(domain),
		IsWildcard: false,
	}
	w.hostEntries = append(w.hostEntries, entry)
	w.allEntries = append(w.allEntries, entry)
	return nil
}

func (w *IPWhitelist) addWildcardDomainEntry(entry string) error {
	// Remove *. prefix to get the base domain
	domain := strings.TrimPrefix(entry, "*.")
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return errors.New("empty domain after wildcard")
	}

	wlEntry := WhitelistEntry{
		Type:       EntryTypeDomain,
		Original:   entry,
		Domain:     strings.ToLower(domain),
		IsWildcard: true,
	}
	w.hostEntries = append(w.hostEntries, wlEntry)
	w.allEntries = append(w.allEntries, wlEntry)
	return nil
}


// IsAllowed memeriksa apakah IP diizinkan
// Returns true if IP matches any entry in whitelist, false otherwise (default deny)
func (w *IPWhitelist) IsAllowed(ip net.IP) bool {
	if ip == nil {
		return false
	}

	w.mu.RLock()
	defer w.mu.RUnlock()

	for _, entry := range w.ipEntries {
		if entry.Contains(ip) {
			return true
		}
	}

	for _, entry := range w.hostEntries {
		if entry.Type != EntryTypeHostname {
			continue
		}
		ips, ok := w.cachedHostname(entry.Hostname)
		if !ok {
			continue
		}
		for _, resolvedIP := range ips {
			if resolvedIP.Equal(ip) {
				return true
			}
		}
	}
	return false
}

// IsHostnameAllowed checks if a hostname is allowed
// Useful for checking request Host header or SNI
func (w *IPWhitelist) IsHostnameAllowed(hostname string) bool {
	if hostname == "" {
		return false
	}

	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if h, _, err := net.SplitHostPort(hostname); err == nil {
		hostname = h
	}

	w.mu.RLock()
	defer w.mu.RUnlock()

	for _, entry := range w.hostEntries {
		if w.matchesHostname(hostname, entry) {
			return true
		}
	}

	ips, ok := w.cachedHostname(hostname)
	if !ok {
		return false
	}
	for _, ip := range ips {
		for _, ipEntry := range w.ipEntries {
			if ipEntry.Contains(ip) {
				return true
			}
		}
	}
	return false
}


// matchesHostname checks if a hostname matches a hostname/domain entry
func (w *IPWhitelist) matchesHostname(hostname string, entry WhitelistEntry) bool {
	switch entry.Type {
	case EntryTypeHostname:
		return hostname == entry.Hostname
	case EntryTypeDomain:
		if entry.IsWildcard {
			// Wildcard: *.example.com matches sub.example.com, a.b.example.com
			// but NOT example.com itself
			return strings.HasSuffix(hostname, "."+entry.Domain)
		}
		// Exact domain match
		return hostname == entry.Domain
	}
	return false
}

func (w *IPWhitelist) cachedHostname(hostname string) ([]net.IP, bool) {
	w.cacheMu.RLock()
	defer w.cacheMu.RUnlock()
	entry, ok := w.dnsCache[hostname]
	if !ok || !time.Now().Before(entry.ExpiresAt) {
		return nil, false
	}
	return entry.IPs, true
}

const dnsLookupTimeout = 5 * time.Second

func (w *IPWhitelist) refreshDNS() {
	w.mu.RLock()
	epoch := w.refreshEpoch
	hosts := make([]string, 0, len(w.hostEntries))
	for _, entry := range w.hostEntries {
		if entry.Type == EntryTypeHostname {
			hosts = append(hosts, entry.Hostname)
		}
	}
	w.mu.RUnlock()

	for _, hostname := range hosts {
		result := make(chan struct {
			ips []net.IP
			err error
		}, 1)
		go func(host string) {
			ips, err := w.dnsResolver.LookupIP(host)
			result <- struct {
				ips []net.IP
				err error
			}{ips, err}
		}(hostname)

		var ips []net.IP
		select {
		case resolved := <-result:
			if resolved.err == nil {
				ips = resolved.ips
			}
		case <-time.After(dnsLookupTimeout):
			continue
		case <-w.stopCh:
			return
		}

		w.mu.RLock()
		stillPresent := epoch == w.refreshEpoch
		if stillPresent {
			stillPresent = false
			for _, entry := range w.hostEntries {
				if entry.Type == EntryTypeHostname && entry.Hostname == hostname {
					stillPresent = true
					break
				}
			}
		}
		if stillPresent {
			w.cacheMu.Lock()
			if len(ips) == 0 || w.dnsCacheTTL <= 0 {
				delete(w.dnsCache, hostname)
			} else {
				w.dnsCache[hostname] = &dnsCacheEntry{
					IPs:       ips,
					ExpiresAt: time.Now().Add(w.dnsCacheTTL),
				}
			}
			w.cacheMu.Unlock()
		}
		w.mu.RUnlock()
	}
}

func hasHostnameEntry(entries []WhitelistEntry) bool {
	for _, entry := range entries {
		if entry.Type == EntryTypeHostname {
			return true
		}
	}
	return false
}

func (w *IPWhitelist) startRefreshLocked() {
	if w.closed || w.refreshing || !hasHostnameEntry(w.hostEntries) {
		return
	}
	w.refreshing = true
	go func() {
		defer close(w.workerDone)
		interval := w.dnsCacheTTL / 2
		if w.dnsCacheTTL <= 0 {
			interval = 5 * time.Minute
		} else if interval <= 0 {
			interval = time.Nanosecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			w.refreshDNS()
			select {
			case <-w.stopCh:
				return
			case <-w.refreshCh:
			case <-ticker.C:
			}
		}
	}()
}

func (w *IPWhitelist) requestRefreshLocked() {
	w.startRefreshLocked()
	if !w.closed && w.refreshing {
		select {
		case w.refreshCh <- struct{}{}:
		default:
		}
	}
}

// Close stops background DNS refresh. It is safe to call more than once.
func (w *IPWhitelist) Close() {
	w.closeOnce.Do(func() {
		w.mu.Lock()
		w.closed = true
		refreshing := w.refreshing
		close(w.stopCh)
		w.mu.Unlock()
		if refreshing {
			<-w.workerDone
		}
	})
}

// Add menambahkan entry baru ke whitelist
func (w *IPWhitelist) Add(entry string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errors.New("whitelist is closed")
	}
	if err := w.addEntry(entry); err != nil {
		return err
	}
	w.requestRefreshLocked()
	return nil
}

// Reload me-reload whitelist dari konfigurasi baru
// Replaces all existing entries with new ones atomically
func (w *IPWhitelist) Reload(entries []string) error {
	newIPEntries := make([]*net.IPNet, 0)
	newHostEntries := make([]WhitelistEntry, 0)
	newAllEntries := make([]WhitelistEntry, 0, len(entries))
	temp := &IPWhitelist{
		ipEntries:   newIPEntries,
		hostEntries: newHostEntries,
		allEntries:  newAllEntries,
	}
	for _, entry := range entries {
		if err := temp.addEntry(entry); err != nil {
			return fmt.Errorf("invalid whitelist entry %q: %w", entry, err)
		}
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errors.New("whitelist is closed")
	}
	w.refreshEpoch++
	w.ipEntries = temp.ipEntries
	w.hostEntries = temp.hostEntries
	w.allEntries = temp.allEntries
	w.cacheMu.Lock()
	w.dnsCache = make(map[string]*dnsCacheEntry)
	w.cacheMu.Unlock()
	w.requestRefreshLocked()
	return nil
}


// ClearDNSCache clears the DNS cache
func (w *IPWhitelist) ClearDNSCache() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.refreshEpoch++
	w.cacheMu.Lock()
	w.dnsCache = make(map[string]*dnsCacheEntry)
	w.cacheMu.Unlock()
	w.requestRefreshLocked()
}


// Entries returns a copy of current whitelist entries as strings (for testing/debugging)
func (w *IPWhitelist) Entries() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	result := make([]string, len(w.allEntries))
	for i, entry := range w.allEntries {
		result[i] = entry.Original
	}
	return result
}

// IPEntries returns only IP/CIDR entries as strings
func (w *IPWhitelist) IPEntries() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	result := make([]string, len(w.ipEntries))
	for i, entry := range w.ipEntries {
		result[i] = entry.String()
	}
	return result
}

// HostEntries returns hostname and domain entries
func (w *IPWhitelist) HostEntries() []WhitelistEntry {
	w.mu.RLock()
	defer w.mu.RUnlock()

	result := make([]WhitelistEntry, len(w.hostEntries))
	copy(result, w.hostEntries)
	return result
}

// ParseIP mengekstrak IP dari request (mempertimbangkan X-Forwarded-For jika trusted)
// If trustProxy is true, uses X-Forwarded-For or X-Real-IP headers
// Otherwise, extracts IP from RemoteAddr
func ParseIP(r *http.Request, trustProxy bool) (net.IP, error) {
	if r == nil {
		return nil, errors.New("nil request")
	}

	var ipStr string

	if trustProxy {
		// Check X-Forwarded-For first (may contain multiple IPs)
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// Take the first IP (original client)
			parts := strings.Split(xff, ",")
			ipStr = strings.TrimSpace(parts[0])
		} else if xri := r.Header.Get("X-Real-IP"); xri != "" {
			ipStr = strings.TrimSpace(xri)
		}
	}

	// Fallback to RemoteAddr if no proxy headers or not trusting proxy
	if ipStr == "" {
		ipStr = r.RemoteAddr
	}

	// Remove port if present
	if host, _, err := net.SplitHostPort(ipStr); err == nil {
		ipStr = host
	}

	ip := net.ParseIP(ipStr)
	if ip == nil {
		return nil, fmt.Errorf("invalid IP address: %s", ipStr)
	}

	return ip, nil
}

// ParseHostname extracts hostname from request Host header
func ParseHostname(r *http.Request) string {
	if r == nil {
		return ""
	}

	host := r.Host
	if host == "" {
		host = r.URL.Host
	}

	// Remove port if present
	if h, _, err := net.SplitHostPort(host); err == nil {
		return strings.ToLower(h)
	}

	return strings.ToLower(host)
}

// IsEmpty returns true if whitelist has no entries
func (w *IPWhitelist) IsEmpty() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.allEntries) == 0
}

// Count returns the number of entries in the whitelist
func (w *IPWhitelist) Count() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.allEntries)
}

// CountByType returns count of entries by type
func (w *IPWhitelist) CountByType() map[EntryType]int {
	w.mu.RLock()
	defer w.mu.RUnlock()

	counts := make(map[EntryType]int)
	for _, entry := range w.allEntries {
		counts[entry.Type]++
	}
	return counts
}

// String returns entry type as string
func (t EntryType) String() string {
	switch t {
	case EntryTypeIP:
		return "ip"
	case EntryTypeHostname:
		return "hostname"
	case EntryTypeDomain:
		return "domain"
	default:
		return "unknown"
	}
}
