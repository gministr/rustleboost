//go:build windows

package core

import (
	"log"
	"net"
	"strings"
)

// SystemDNSServers returns the resolvers Windows is configured to use,
// excluding our own tunnel adapter.
//
// These are read before the tunnel comes up and handed to sing-box as
// explicit upstreams dialled through the direct outbound. Using the ISP's own
// resolver matters on the networks this client exists for: a fixed public
// address like 1.1.1.1 is precisely what tends to be blocked there, and a
// resolver that never answers takes the whole connection down with it.
func SystemDNSServers() []string {
	const script = `Get-DnsClientServerAddress -AddressFamily IPv4 |
		Where-Object { $_.InterfaceAlias -notlike '*RustleBoost*' } |
		Select-Object -ExpandProperty ServerAddresses`

	out, err := hiddenCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		log.Printf("[dns] could not read system resolvers: %v", err)
		return nil
	}

	var servers []string
	seen := map[string]bool{}

	for _, line := range strings.Split(string(out), "\n") {
		address := strings.TrimSpace(line)
		if address == "" || seen[address] {
			continue
		}

		ip := net.ParseIP(address)
		// Loopback entries point at a local resolver stub, which would just
		// route the query back through the same machine.
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			continue
		}
		// Anything inside the FakeIP pool is a leftover from a previous
		// session, not a real resolver.
		if isFakeIP(ip) {
			continue
		}

		seen[address] = true
		servers = append(servers, address)
	}

	if len(servers) > 0 {
		log.Printf("[dns] system resolvers: %s", strings.Join(servers, ", "))
	}
	return servers
}

// fakeIPRange mirrors the pool declared in the generated sing-box config.
var fakeIPRange = &net.IPNet{
	IP:   net.IPv4(198, 18, 0, 0),
	Mask: net.CIDRMask(15, 32),
}

func isFakeIP(ip net.IP) bool { return fakeIPRange.Contains(ip) }
