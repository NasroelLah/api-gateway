package security

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
)

// IPWhitelist mengelola daftar IP/CIDR yang diizinkan
// Implements default deny security model - only IPs in whitelist are allowed
type IPWhitelist struct {
	mu      sync.RWMutex
	entries []*net.IPNet
}

// NewIPWhitelist membuat instance baru dengan daftar IP/CIDR
// Accepts both single IPs (e.g., "192.168.1.1") and CIDR notation (e.g., "10.0.0.0/8")
func NewIPWhitelist(entries []string) (*IPWhitelist, error) {
	w := &IPWhitelist{
		entries: make([]*net.IPNet, 0, len(entries)),
	}

	for _, entry := range entries {
		ipNet, err := parseIPOrCIDR(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid whitelist entry %q: %w", entry, err)
		}
		w.entries = append(w.entries, ipNet)
	}

	return w, nil
}

// parseIPOrCIDR parses a string as either a single IP or CIDR notation
func parseIPOrCIDR(entry string) (*net.IPNet, error) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return nil, errors.New("empty entry")
	}

	// Try parsing as CIDR first
	if strings.Contains(entry, "/") {
		_, ipNet, err := net.ParseCIDR(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR: %w", err)
		}
		return ipNet, nil
	}

	// Parse as single IP
	ip := net.ParseIP(entry)
	if ip == nil {
		return nil, fmt.Errorf("invalid IP address: %s", entry)
	}

	// Convert single IP to /32 (IPv4) or /128 (IPv6) CIDR
	var mask net.IPMask
	if ip.To4() != nil {
		mask = net.CIDRMask(32, 32)
		ip = ip.To4()
	} else {
		mask = net.CIDRMask(128, 128)
	}

	return &net.IPNet{IP: ip, Mask: mask}, nil
}

// IsAllowed memeriksa apakah IP diizinkan
// Returns true if IP matches any entry in whitelist, false otherwise (default deny)
func (w *IPWhitelist) IsAllowed(ip net.IP) bool {
	if ip == nil {
		return false
	}

	w.mu.RLock()
	defer w.mu.RUnlock()

	// Default deny - only allow if IP matches an entry
	for _, entry := range w.entries {
		if entry.Contains(ip) {
			return true
		}
	}

	return false
}

// Add menambahkan entry baru ke whitelist
func (w *IPWhitelist) Add(entry string) error {
	ipNet, err := parseIPOrCIDR(entry)
	if err != nil {
		return err
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.entries = append(w.entries, ipNet)
	return nil
}

// Reload me-reload whitelist dari konfigurasi baru
// Replaces all existing entries with new ones atomically
func (w *IPWhitelist) Reload(entries []string) error {
	newEntries := make([]*net.IPNet, 0, len(entries))

	for _, entry := range entries {
		ipNet, err := parseIPOrCIDR(entry)
		if err != nil {
			return fmt.Errorf("invalid whitelist entry %q: %w", entry, err)
		}
		newEntries = append(newEntries, ipNet)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.entries = newEntries
	return nil
}


// Entries returns a copy of current whitelist entries as strings (for testing/debugging)
func (w *IPWhitelist) Entries() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	result := make([]string, len(w.entries))
	for i, entry := range w.entries {
		result[i] = entry.String()
	}
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

// IsEmpty returns true if whitelist has no entries
func (w *IPWhitelist) IsEmpty() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.entries) == 0
}

// Count returns the number of entries in the whitelist
func (w *IPWhitelist) Count() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.entries)
}
