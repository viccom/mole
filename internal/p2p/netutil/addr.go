//go:build p2p

// Package netutil provides network address discovery and classification utilities.
package netutil

import (
	"fmt"
	"log"
	"math/rand"
	"net"
)

// DiscoverIPv6 finds the first global unicast IPv6 address on this host.
//
// Filters out loopback (::1), link-local (fe80::), and IPv4-mapped addresses.
// Returns "" if no suitable IPv6 address is found.
func DiscoverIPv6() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP.To4() != nil {
				continue
			}
			ip := ipnet.IP
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				continue
			}
			if ip.IsGlobalUnicast() || ip.IsPrivate() {
				return ip.String()
			}
		}
	}
	return ""
}

// PickFreeTCP6Port tries to bind a free TCP port in the 50000-60000 range
// on the given IPv6 address. Retries up to 5 times on failure (Windows port
// exclusion zones).
func PickFreeTCP6Port(ipv6Addr string) int {
	for retry := 0; retry < 5; retry++ {
		port := 50000 + rand.Intn(10000)
		ln, e := net.Listen("tcp6", net.JoinHostPort(ipv6Addr, fmt.Sprint(port)))
		if e == nil {
			ln.Close()
			return port
		}
	}
	return 0
}

// ParseIPv6FromAddr extracts an IPv6 address from "host:port" or "[ipv6]:port".
// Returns nil if the address is not a valid IPv6 address.
func ParseIPv6FromAddr(s string) net.IP {
	host, _, err := net.SplitHostPort(s)
	if err != nil {
		host = s
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() != nil {
		return nil
	}
	return ip
}

// ParsePort extracts the port number from a "host:port" or "[ipv6]:port" string.
func ParsePort(addr string) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return port
}

// ListenUDP6WithRetry binds a UDP6 socket on the given IPv6 address.
// If listenPort is 0, picks a random port in 50000-60000. Retries up to 10 times.
func ListenUDP6WithRetry(ipv6 string, listenPort int) (*net.UDPConn, error) {
	port := listenPort
	for retry := 0; retry < 10; retry++ {
		if port == 0 {
			port = 50000 + rand.Intn(10000)
		}
		conn, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.ParseIP(ipv6), Port: port})
		if err == nil {
			return conn, nil
		}
		log.Printf("[v6] port %d: %v (retrying...)", port, err)
		port = 0
	}
	return nil, fmt.Errorf("all retries failed")
}

// IsPrivateIP reports whether ip is an IPv4 private/loopback address.
func IsPrivateIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	ip = ip.To4()
	if ip == nil {
		return false
	}
	return ip[0] == 10 ||
		(ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31) ||
		(ip[0] == 192 && ip[1] == 168) ||
		ip[0] == 127
}

// ClassifyConnection returns a human-readable description of the connection type
// between localAddr and peer, for logging purposes.
func ClassifyConnection(localAddr, peer *net.UDPAddr) string {
	if peer.IP.IsLoopback() || localAddr.IP.IsLoopback() {
		return "UDP direct (loopback)"
	}
	if localAddr.IP != nil && peer.IP != nil && localAddr.IP.Equal(peer.IP) {
		return "UDP direct (same machine, different ports)"
	}
	if IsPrivateIP(localAddr.IP) && !IsPrivateIP(peer.IP) {
		return "UDP direct (NAT hole-punched)"
	}
	if !IsPrivateIP(localAddr.IP) && IsPrivateIP(peer.IP) {
		return "UDP direct (NAT hole-punched, peer behind NAT)"
	}
	return "UDP direct (hole-punched)"
}

// ---- Comprehensive local address discovery ----

// AddrType classifies a local address.
type AddrType int

const (
	AddrLoopback  AddrType = iota // 127.0.0.1, ::1 — same-machine only
	AddrPrivateV4                 // RFC 1918 private IPv4
	AddrPublicV4                  // public IPv4
	AddrGlobalV6                  // global unicast IPv6
	AddrULAV6                     // unique local IPv6 (fd00::/8)
	AddrLinkLocal                 // link-local (filtered out by default)
)

// LocalAddr is a discovered local address with metadata for candidate prioritization.
type LocalAddr struct {
	IP      net.IP
	IfName  string // interface name, for logging
	Port    int    // 0 until bound to a socket
	Type    AddrType
	IsIPv6  bool
}

// DiscoverAllLocalAddrs returns all non-link-local unicast addresses on all UP interfaces.
//
// Priority order (highest first):
//  1. Loopback — same-machine detection
//  2. Private IPv4 — LAN / enterprise subnets
//  3. Global unicast IPv6 — direct connect
//  4. ULA IPv6 — enterprise IPv6
//  5. Public IPv4 — fallback / NAT traversal
//
// Link-local addresses (169.254.x.x, fe80::) are excluded.
func DiscoverAllLocalAddrs() []LocalAddr {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	// Collect into buckets first, then flatten in priority order.
	var loopback, privateV4, publicV4, globalV6, ulaV6 []LocalAddr

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP

			// Skip multicast and unspecified
			if ip.IsMulticast() || ip.IsUnspecified() {
				continue
			}

			la := LocalAddr{IP: ip, IfName: iface.Name}

			if ip.To4() != nil {
				// IPv4
				la.IsIPv6 = false
				if ip.IsLoopback() {
					la.Type = AddrLoopback
					loopback = append(loopback, la)
				} else if ip.IsLinkLocalUnicast() {
					// 169.254.x.x — skip
					continue
				} else if IsPrivateIP(ip) {
					la.Type = AddrPrivateV4
					privateV4 = append(privateV4, la)
				} else {
					la.Type = AddrPublicV4
					publicV4 = append(publicV4, la)
				}
			} else {
				// IPv6
				la.IsIPv6 = true
				if ip.IsLoopback() {
					la.Type = AddrLoopback
					loopback = append(loopback, la)
				} else if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
					// fe80:: — skip
					continue
				} else if ip.IsGlobalUnicast() {
					la.Type = AddrGlobalV6
					globalV6 = append(globalV6, la)
				} else if ip.IsPrivate() {
					// ULA fd00::/8
					la.Type = AddrULAV6
					ulaV6 = append(ulaV6, la)
				}
				// Site-local (fec0::) is deprecated, skip
			}
		}
	}

	// Flatten in priority order
	var result []LocalAddr
	result = append(result, loopback...)
	result = append(result, privateV4...)
	result = append(result, globalV6...)
	result = append(result, ulaV6...)
	result = append(result, publicV4...)
	return result
}

// FormatSize formats a byte count as a human-readable string (GB/MB/KB/B).
func FormatSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.2f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
