package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
)

type sanitizedClientIPKey struct{}

// HeaderSanitizer replaces client-supplied forwarding headers with the peer IP.
func HeaderSanitizer(next http.Handler) http.Handler {
	return HeaderSanitizerWithTrust(nil)(next)
}

// HeaderSanitizerWithTrust accepts forwarding headers only from configured
// trusted peers and walks X-Forwarded-For from the nearest hop outward.
func HeaderSanitizerWithTrust(trustedProxies []string) func(http.Handler) http.Handler {
	trustedNets := make([]*net.IPNet, 0, len(trustedProxies))
	for _, proxy := range trustedProxies {
		if ip := net.ParseIP(proxy); ip != nil {
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			trustedNets = append(trustedNets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, ipNet, err := net.ParseCIDR(proxy)
		if err == nil {
			trustedNets = append(trustedNets, ipNet)
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peer := net.ParseIP(extractClientIP(r.RemoteAddr))
			clientIP := ""
			if isTrustedProxy(peer, trustedNets) {
				chain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
				for i := len(chain) - 1; i >= 0; i-- {
					hop := net.ParseIP(strings.TrimSpace(chain[i]))
					if hop == nil {
						break
					}
					clientIP = hop.String()
					if !isTrustedProxy(hop, trustedNets) {
						break
					}
				}
				if clientIP == "" {
					if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
						clientIP = ip.String()
					}
				}
			}
			if clientIP == "" && peer != nil {
				clientIP = peer.String()
			}
			r.Header.Set("X-Forwarded-For", clientIP)
			r.Header.Set("X-Real-IP", clientIP)
			r = r.WithContext(context.WithValue(r.Context(), sanitizedClientIPKey{}, clientIP))
			next.ServeHTTP(w, r)
		})
	}
}

func isTrustedProxy(ip net.IP, trustedNets []*net.IPNet) bool {
	for _, trusted := range trustedNets {
		if trusted.Contains(ip) {
			return true
		}
	}
	return false
}

// extractClientIP extracts the IP address from a RemoteAddr string.
func extractClientIP(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	return strings.TrimSpace(remoteAddr)
}

// GetSanitizedClientIP returns the identity established by the sanitizer.
func GetSanitizedClientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(sanitizedClientIPKey{}).(string); ok {
		return ip
	}
	if ip := net.ParseIP(extractClientIP(r.RemoteAddr)); ip != nil {
		return ip.String()
	}
	return ""
}

