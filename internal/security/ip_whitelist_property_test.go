package security

import (
	"fmt"
	"net"
	"testing"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
)

const PropertyTestIterations = 100

// TestProperty5_IPWhitelistDefaultDeny tests the default deny security model
// **Property 5: IP Whitelist Default Deny**
// **Validates: Requirements 2.1, 2.2, 2.3**
//
// *For any* IP address, request dari IP tersebut diizinkan jika dan hanya jika
// IP tersebut match dengan setidaknya satu entry dalam whitelist (single IP atau CIDR range).
func TestProperty5_IPWhitelistDefaultDeny(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	// Property: Empty whitelist SHALL deny all IPs (default deny)
	properties.Property("Empty whitelist denies all IPs", prop.ForAll(
		func(ipBytes [4]byte) bool {
			wl, err := NewIPWhitelist([]string{})
			if err != nil {
				return false
			}

			ip := net.IPv4(ipBytes[0], ipBytes[1], ipBytes[2], ipBytes[3])
			return !wl.IsAllowed(ip) // Should be denied
		},
		genIPv4Bytes(),
	))

	// Property: IP in whitelist SHALL be allowed
	properties.Property("IP in whitelist is allowed", prop.ForAll(
		func(ipBytes [4]byte) bool {
			ip := net.IPv4(ipBytes[0], ipBytes[1], ipBytes[2], ipBytes[3])
			ipStr := ip.String()

			wl, err := NewIPWhitelist([]string{ipStr})
			if err != nil {
				return false
			}

			return wl.IsAllowed(ip) // Should be allowed
		},
		genIPv4Bytes(),
	))

	// Property: IP NOT in whitelist SHALL be denied
	properties.Property("IP not in whitelist is denied", prop.ForAll(
		func(whitelistIP, testIP [4]byte) bool {
			// Ensure IPs are different
			if whitelistIP == testIP {
				return true // Skip when same
			}

			wlIP := net.IPv4(whitelistIP[0], whitelistIP[1], whitelistIP[2], whitelistIP[3])
			wl, err := NewIPWhitelist([]string{wlIP.String()})
			if err != nil {
				return false
			}

			checkIP := net.IPv4(testIP[0], testIP[1], testIP[2], testIP[3])
			return !wl.IsAllowed(checkIP) // Should be denied
		},
		genIPv4Bytes(),
		genIPv4Bytes(),
	))

	// Property: IP within CIDR range SHALL be allowed
	properties.Property("IP within CIDR range is allowed", prop.ForAll(
		func(networkPrefix byte, hostPart byte) bool {
			// Create a /24 network: 10.networkPrefix.0.0/24
			cidr := fmt.Sprintf("10.%d.0.0/24", networkPrefix)

			wl, err := NewIPWhitelist([]string{cidr})
			if err != nil {
				return false
			}

			// Test IP within the range: 10.networkPrefix.0.hostPart
			ip := net.IPv4(10, networkPrefix, 0, hostPart)
			return wl.IsAllowed(ip) // Should be allowed
		},
		gen.UInt8(),
		gen.UInt8(),
	))

	// Property: IP outside CIDR range SHALL be denied
	properties.Property("IP outside CIDR range is denied", prop.ForAll(
		func(networkPrefix, differentPrefix byte) bool {
			// Ensure prefixes are different
			if networkPrefix == differentPrefix {
				return true // Skip when same
			}

			// Create a /24 network: 10.networkPrefix.0.0/24
			cidr := fmt.Sprintf("10.%d.0.0/24", networkPrefix)

			wl, err := NewIPWhitelist([]string{cidr})
			if err != nil {
				return false
			}

			// Test IP outside the range: 10.differentPrefix.0.1
			ip := net.IPv4(10, differentPrefix, 0, 1)
			return !wl.IsAllowed(ip) // Should be denied
		},
		gen.UInt8(),
		gen.UInt8(),
	))

	// Property: Multiple whitelist entries - IP matching ANY entry SHALL be allowed
	properties.Property("IP matching any whitelist entry is allowed", prop.ForAll(
		func(ip1Bytes, ip2Bytes, testIdx [4]byte) bool {
			ip1 := net.IPv4(ip1Bytes[0], ip1Bytes[1], ip1Bytes[2], ip1Bytes[3])
			ip2 := net.IPv4(ip2Bytes[0], ip2Bytes[1], ip2Bytes[2], ip2Bytes[3])

			wl, err := NewIPWhitelist([]string{ip1.String(), ip2.String()})
			if err != nil {
				return false
			}

			// Test both IPs should be allowed
			return wl.IsAllowed(ip1) && wl.IsAllowed(ip2)
		},
		genIPv4Bytes(),
		genIPv4Bytes(),
		genIPv4Bytes(),
	))

	// Property: nil IP SHALL be denied
	properties.Property("nil IP is denied", prop.ForAll(
		func(ipBytes [4]byte) bool {
			ip := net.IPv4(ipBytes[0], ipBytes[1], ipBytes[2], ipBytes[3])
			wl, err := NewIPWhitelist([]string{ip.String()})
			if err != nil {
				return false
			}

			return !wl.IsAllowed(nil) // nil should be denied
		},
		genIPv4Bytes(),
	))

	properties.TestingRun(t)
}

// genIPv4Bytes generates random IPv4 address bytes
func genIPv4Bytes() gopter.Gen {
	return gen.SliceOfN(4, gen.UInt8()).Map(func(s []uint8) [4]byte {
		var arr [4]byte
		copy(arr[:], s)
		return arr
	})
}


// TestProperty6_CIDRParsingAndMatching tests CIDR parsing and IP range matching
// **Property 6: CIDR Parsing and Matching**
// **Validates: Requirements 2.4**
//
// *For any* valid CIDR notation string, parsing SHALL berhasil dan untuk setiap IP
// dalam range tersebut, `IsAllowed` SHALL return true.
func TestProperty6_CIDRParsingAndMatching(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	// Property: Valid CIDR notation SHALL parse successfully
	properties.Property("Valid CIDR notation parses successfully", prop.ForAll(
		func(a, b, c, d byte, maskBits int) bool {
			// Ensure mask is valid (0-32 for IPv4)
			mask := maskBits % 33
			cidr := fmt.Sprintf("%d.%d.%d.%d/%d", a, b, c, d, mask)

			wl, err := NewIPWhitelist([]string{cidr})
			if err != nil {
				// CIDR parsing should succeed for valid notation
				// Note: net.ParseCIDR normalizes the IP to network address
				return false
			}

			return wl.Count() == 1
		},
		gen.UInt8(),
		gen.UInt8(),
		gen.UInt8(),
		gen.UInt8(),
		gen.IntRange(0, 32),
	))

	// Property: All IPs within /24 CIDR range SHALL be allowed
	properties.Property("All IPs within /24 CIDR range are allowed", prop.ForAll(
		func(a, b, c, hostByte byte) bool {
			// Create /24 network
			cidr := fmt.Sprintf("%d.%d.%d.0/24", a, b, c)

			wl, err := NewIPWhitelist([]string{cidr})
			if err != nil {
				return false
			}

			// Any host in this /24 should be allowed
			ip := net.IPv4(a, b, c, hostByte)
			return wl.IsAllowed(ip)
		},
		gen.UInt8(),
		gen.UInt8(),
		gen.UInt8(),
		gen.UInt8(),
	))

	// Property: All IPs within /16 CIDR range SHALL be allowed
	properties.Property("All IPs within /16 CIDR range are allowed", prop.ForAll(
		func(a, b, c, d byte) bool {
			// Create /16 network
			cidr := fmt.Sprintf("%d.%d.0.0/16", a, b)

			wl, err := NewIPWhitelist([]string{cidr})
			if err != nil {
				return false
			}

			// Any host in this /16 should be allowed
			ip := net.IPv4(a, b, c, d)
			return wl.IsAllowed(ip)
		},
		gen.UInt8(),
		gen.UInt8(),
		gen.UInt8(),
		gen.UInt8(),
	))

	// Property: All IPs within /8 CIDR range SHALL be allowed
	properties.Property("All IPs within /8 CIDR range are allowed", prop.ForAll(
		func(a, b, c, d byte) bool {
			// Create /8 network
			cidr := fmt.Sprintf("%d.0.0.0/8", a)

			wl, err := NewIPWhitelist([]string{cidr})
			if err != nil {
				return false
			}

			// Any host in this /8 should be allowed
			ip := net.IPv4(a, b, c, d)
			return wl.IsAllowed(ip)
		},
		gen.UInt8(),
		gen.UInt8(),
		gen.UInt8(),
		gen.UInt8(),
	))

	// Property: Single IP (/32) SHALL only match that exact IP
	properties.Property("Single IP /32 only matches exact IP", prop.ForAll(
		func(ipBytes, testBytes [4]byte) bool {
			ip := net.IPv4(ipBytes[0], ipBytes[1], ipBytes[2], ipBytes[3])
			cidr := ip.String() + "/32"

			wl, err := NewIPWhitelist([]string{cidr})
			if err != nil {
				return false
			}

			testIP := net.IPv4(testBytes[0], testBytes[1], testBytes[2], testBytes[3])

			// Should only be allowed if IPs match exactly
			if ipBytes == testBytes {
				return wl.IsAllowed(testIP)
			}
			return !wl.IsAllowed(testIP)
		},
		genIPv4Bytes(),
		genIPv4Bytes(),
	))

	// Property: Single IP without CIDR notation SHALL be treated as /32
	properties.Property("Single IP without CIDR is treated as /32", prop.ForAll(
		func(ipBytes, testBytes [4]byte) bool {
			ip := net.IPv4(ipBytes[0], ipBytes[1], ipBytes[2], ipBytes[3])

			wl, err := NewIPWhitelist([]string{ip.String()}) // No /32 suffix
			if err != nil {
				return false
			}

			testIP := net.IPv4(testBytes[0], testBytes[1], testBytes[2], testBytes[3])

			// Should only be allowed if IPs match exactly
			if ipBytes == testBytes {
				return wl.IsAllowed(testIP)
			}
			return !wl.IsAllowed(testIP)
		},
		genIPv4Bytes(),
		genIPv4Bytes(),
	))

	// Property: Invalid CIDR notation SHALL return error
	properties.Property("Invalid CIDR notation returns error", prop.ForAll(
		func(invalidIdx int) bool {
			invalidCIDRs := []string{
				"invalid",
				"256.0.0.0/8",
				"10.0.0.0/33",
				"10.0.0.0/-1",
				"",
				"   ",
				"10.0.0/8",
				"10.0.0.0.0/8",
			}

			cidr := invalidCIDRs[invalidIdx%len(invalidCIDRs)]
			_, err := NewIPWhitelist([]string{cidr})

			// Should return error for invalid CIDR
			return err != nil
		},
		gen.IntRange(0, 7),
	))

	// Property: CIDR boundary - first and last IP in range SHALL be allowed
	properties.Property("CIDR boundary IPs are allowed", prop.ForAll(
		func(a, b byte) bool {
			// Create /24 network
			cidr := fmt.Sprintf("%d.%d.0.0/24", a, b)

			wl, err := NewIPWhitelist([]string{cidr})
			if err != nil {
				return false
			}

			// First IP in range (network address)
			firstIP := net.IPv4(a, b, 0, 0)
			// Last IP in range (broadcast address)
			lastIP := net.IPv4(a, b, 0, 255)

			return wl.IsAllowed(firstIP) && wl.IsAllowed(lastIP)
		},
		gen.UInt8(),
		gen.UInt8(),
	))

	properties.TestingRun(t)
}
