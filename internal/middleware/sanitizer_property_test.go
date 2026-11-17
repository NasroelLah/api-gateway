package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
)

// TestProperty3_HeaderSanitization tests header sanitization behavior
// **Property 3: Header Sanitization**
// **Validates: Requirements 1.4**
//
// *For any* incoming request dengan header X-Forwarded-For atau X-Real-IP,
// setelah sanitasi, header tersebut SHALL berisi client IP yang benar
// (bukan nilai dari incoming request yang tidak trusted).
func TestProperty3_HeaderSanitization(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	// Property: X-Forwarded-For SHALL be replaced with actual client IP
	properties.Property("X-Forwarded-For is replaced with actual client IP", prop.ForAll(
		func(spoofedIP string, clientIP string, clientPort int) bool {
			// Create request with spoofed X-Forwarded-For
			req := httptest.NewRequest("GET", "/test", nil)
			req.Header.Set("X-Forwarded-For", spoofedIP)
			req.RemoteAddr = clientIP + ":" + itoa(clientPort)

			// Track the sanitized header value
			var sanitizedXFF string
			handler := HeaderSanitizer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sanitizedXFF = r.Header.Get("X-Forwarded-For")
			}))

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// X-Forwarded-For should be the actual client IP, not the spoofed value
			return sanitizedXFF == clientIP && sanitizedXFF != spoofedIP
		},
		genIPv4String(),      // spoofedIP
		genDifferentIPv4(),   // clientIP (different from spoofed)
		gen.IntRange(1, 65535), // clientPort
	))

	// Property: X-Real-IP SHALL be replaced with actual client IP
	properties.Property("X-Real-IP is replaced with actual client IP", prop.ForAll(
		func(spoofedIP string, clientIP string, clientPort int) bool {
			// Create request with spoofed X-Real-IP
			req := httptest.NewRequest("GET", "/test", nil)
			req.Header.Set("X-Real-IP", spoofedIP)
			req.RemoteAddr = clientIP + ":" + itoa(clientPort)

			// Track the sanitized header value
			var sanitizedXRI string
			handler := HeaderSanitizer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sanitizedXRI = r.Header.Get("X-Real-IP")
			}))

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// X-Real-IP should be the actual client IP, not the spoofed value
			return sanitizedXRI == clientIP && sanitizedXRI != spoofedIP
		},
		genIPv4String(),      // spoofedIP
		genDifferentIPv4(),   // clientIP
		gen.IntRange(1, 65535), // clientPort
	))

	// Property: Both headers SHALL be set consistently to the same client IP
	properties.Property("X-Forwarded-For and X-Real-IP are set to same client IP", prop.ForAll(
		func(spoofedXFF string, spoofedXRI string, clientIP string, clientPort int) bool {
			// Create request with both spoofed headers
			req := httptest.NewRequest("GET", "/test", nil)
			req.Header.Set("X-Forwarded-For", spoofedXFF)
			req.Header.Set("X-Real-IP", spoofedXRI)
			req.RemoteAddr = clientIP + ":" + itoa(clientPort)

			var sanitizedXFF, sanitizedXRI string
			handler := HeaderSanitizer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sanitizedXFF = r.Header.Get("X-Forwarded-For")
				sanitizedXRI = r.Header.Get("X-Real-IP")
			}))

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// Both headers should be set to the same actual client IP
			return sanitizedXFF == clientIP && sanitizedXRI == clientIP
		},
		genIPv4String(),      // spoofedXFF
		genIPv4String(),      // spoofedXRI
		genDifferentIPv4(),   // clientIP
		gen.IntRange(1, 65535), // clientPort
	))

	// Property: Sanitization SHALL work when no headers are present
	properties.Property("Headers are set even when not present in original request", prop.ForAll(
		func(clientIP string, clientPort int) bool {
			// Create request without X-Forwarded-For or X-Real-IP
			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = clientIP + ":" + itoa(clientPort)

			var sanitizedXFF, sanitizedXRI string
			handler := HeaderSanitizer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sanitizedXFF = r.Header.Get("X-Forwarded-For")
				sanitizedXRI = r.Header.Get("X-Real-IP")
			}))

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// Both headers should be set to the client IP
			return sanitizedXFF == clientIP && sanitizedXRI == clientIP
		},
		genIPv4String(),      // clientIP
		gen.IntRange(1, 65535), // clientPort
	))

	// Property: RemoteAddr without port SHALL be handled correctly
	properties.Property("RemoteAddr without port is handled correctly", prop.ForAll(
		func(clientIP string) bool {
			// Create request with RemoteAddr without port
			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = clientIP // No port

			var sanitizedXFF, sanitizedXRI string
			handler := HeaderSanitizer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sanitizedXFF = r.Header.Get("X-Forwarded-For")
				sanitizedXRI = r.Header.Get("X-Real-IP")
			}))

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// Both headers should be set to the client IP
			return sanitizedXFF == clientIP && sanitizedXRI == clientIP
		},
		genIPv4String(),
	))

	properties.TestingRun(t)
}

// genIPv4String generates random IPv4 address strings
func genIPv4String() gopter.Gen {
	return gen.SliceOfN(4, gen.IntRange(0, 255)).Map(func(octets []int) string {
		return itoa(octets[0]) + "." + itoa(octets[1]) + "." + itoa(octets[2]) + "." + itoa(octets[3])
	})
}

// genDifferentIPv4 generates IPv4 addresses that are guaranteed to be different
// by using a different range (192.168.x.x vs 10.x.x.x)
func genDifferentIPv4() gopter.Gen {
	return gen.SliceOfN(2, gen.IntRange(0, 255)).Map(func(octets []int) string {
		// Use 10.x.x.x range to be different from 192.168.x.x
		return "10.0." + itoa(octets[0]) + "." + itoa(octets[1])
	})
}

// itoa converts int to string without importing strconv
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	if i < 0 {
		return "-" + itoa(-i)
	}
	
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
