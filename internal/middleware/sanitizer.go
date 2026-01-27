package middleware

import (
	"net"
	"net/http"
	"strings"
)

// HeaderSanitizer sanitizes X-Forwarded-For and X-Real-IP headers
// with the correct client IP from the request's RemoteAddr.
// This prevents header spoofing attacks.
// Requirements: 1.4
func HeaderSanitizer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract the real client IP from RemoteAddr
		clientIP := extractClientIP(r.RemoteAddr)

		// Sanitize X-Forwarded-For - replace with actual client IP
		// This prevents clients from spoofing their IP via this header
		r.Header.Set("X-Forwarded-For", clientIP)

		// Sanitize X-Real-IP - replace with actual client IP
		r.Header.Set("X-Real-IP", clientIP)

		next.ServeHTTP(w, r)
	})
}

// HeaderSanitizerWithTrust creates a sanitizer that optionally trusts
// existing proxy headers from trusted proxies.
// If the request comes from a trusted proxy, it appends the client IP
// to the existing X-Forwarded-For chain instead of replacing it.
func HeaderSanitizerWithTrust(trustedProxies []string) func(http.Handler) http.Handler {
	// Parse trusted proxy CIDRs
	trustedNets := make([]*net.IPNet, 0, len(trustedProxies))
	for _, proxy := range trustedProxies {
		_, ipNet, err := net.ParseCIDR(proxy)
		if err != nil {
			// Try as single IP
			ip := net.ParseIP(proxy)
			if ip != nil {
				var mask net.IPMask
				if ip.To4() != nil {
					mask = net.CIDRMask(32, 32)
				} else {
					mask = net.CIDRMask(128, 128)
				}
				trustedNets = append(trustedNets, &net.IPNet{IP: ip, Mask: mask})
			}
			continue
		}
		trustedNets = append(trustedNets, ipNet)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientIP := extractClientIP(r.RemoteAddr)
			clientNetIP := net.ParseIP(clientIP)

			// Check if request is from a trusted proxy
			isTrusted := false
			if clientNetIP != nil {
				for _, trusted := range trustedNets {
					if trusted.Contains(clientNetIP) {
						isTrusted = true
						break
					}
				}
			}

			if isTrusted {
				// Append client IP to existing X-Forwarded-For chain
				existingXFF := r.Header.Get("X-Forwarded-For")
				if existingXFF != "" {
					r.Header.Set("X-Forwarded-For", existingXFF+", "+clientIP)
				} else {
					r.Header.Set("X-Forwarded-For", clientIP)
				}
			} else {
				// Not from trusted proxy - replace headers entirely
				r.Header.Set("X-Forwarded-For", clientIP)
				r.Header.Set("X-Real-IP", clientIP)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// extractClientIP extracts the IP address from a RemoteAddr string
// RemoteAddr is typically in the format "IP:port" or just "IP"
func extractClientIP(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}

	// Try to split host:port
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// No port, return as-is (might be just IP)
		return strings.TrimSpace(remoteAddr)
	}

	return host
}

// GetSanitizedClientIP returns the sanitized client IP from the request
// This should be called after HeaderSanitizer middleware has run
func GetSanitizedClientIP(r *http.Request) string {
	// After sanitization, X-Real-IP contains the correct client IP
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	// Fallback to extracting from RemoteAddr
	return extractClientIP(r.RemoteAddr)
}
