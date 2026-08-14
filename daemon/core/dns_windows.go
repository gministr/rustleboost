//go:build windows

package core

import (
	"log"
	"net"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SystemDNSServers returns the resolvers Windows is configured to use,
// excluding our own tunnel adapter.
//
// These are read before the tunnel comes up and handed to sing-box as
// explicit upstreams. Using the ISP's own resolver matters on the networks
// this client exists for: a fixed public address like 1.1.1.1 is precisely
// what tends to be blocked there, and a resolver that never answers takes the
// whole connection down with it.
//
// This calls GetAdaptersAddresses rather than running PowerShell. Launching
// powershell.exe from a hidden, unsigned process to enumerate network
// configuration is a pattern behavioural scanners weigh heavily, and it cost
// a process spawn on every connect for information the API returns directly.
func SystemDNSServers() []string {
	adapters, err := adapterAddresses()
	if err != nil {
		log.Printf("[dns] could not read system resolvers: %v", err)
		return nil
	}

	var servers []string
	seen := map[string]bool{}

	for _, adapter := range adapters {
		if adapter.OperStatus != windows.IfOperStatusUp {
			continue
		}
		// Our own tunnel advertises a resolver too; using it would point the
		// lookup straight back into the tunnel we are trying to bootstrap.
		if strings.Contains(windows.UTF16PtrToString(adapter.FriendlyName), "RustleBoost") {
			continue
		}

		for dns := adapter.FirstDnsServerAddress; dns != nil; dns = dns.Next {
			ip := sockaddrIP(dns.Address.Sockaddr)
			if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || isFakeIP(ip) {
				continue
			}
			address := ip.String()
			if seen[address] {
				continue
			}
			seen[address] = true
			servers = append(servers, address)
		}
	}

	if len(servers) > 0 {
		log.Printf("[dns] system resolvers: %s", strings.Join(servers, ", "))
	}
	return servers
}

// adapterAddresses wraps GetAdaptersAddresses, which reports the buffer size
// it needs rather than allocating one.
func adapterAddresses() ([]*windows.IpAdapterAddresses, error) {
	const flags = windows.GAA_FLAG_SKIP_ANYCAST |
		windows.GAA_FLAG_SKIP_MULTICAST |
		windows.GAA_FLAG_SKIP_FRIENDLY_NAME

	size := uint32(15000)
	for attempt := 0; attempt < 3; attempt++ {
		buffer := make([]byte, size)
		head := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0]))

		// FriendlyName is needed to spot our own adapter, so ask for it.
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, flags&^windows.GAA_FLAG_SKIP_FRIENDLY_NAME,
			0, head, &size)
		if err == windows.ERROR_BUFFER_OVERFLOW {
			continue // size now holds what is actually required
		}
		if err != nil {
			return nil, err
		}

		var adapters []*windows.IpAdapterAddresses
		for a := head; a != nil; a = a.Next {
			adapters = append(adapters, a)
		}
		return adapters, nil
	}
	return nil, windows.ERROR_BUFFER_OVERFLOW
}

// The address list uses the syscall sockaddr types, not the x/sys ones.
func sockaddrIP(sa *syscall.RawSockaddrAny) net.IP {
	if sa == nil {
		return nil
	}
	switch sa.Addr.Family {
	case syscall.AF_INET:
		v4 := (*syscall.RawSockaddrInet4)(unsafe.Pointer(sa))
		ip := make(net.IP, net.IPv4len)
		copy(ip, v4.Addr[:])
		return ip
	case syscall.AF_INET6:
		v6 := (*syscall.RawSockaddrInet6)(unsafe.Pointer(sa))
		ip := make(net.IP, net.IPv6len)
		copy(ip, v6.Addr[:])
		return ip
	}
	return nil
}

// fakeIPRange mirrors the pool declared in the generated sing-box config.
var fakeIPRange = &net.IPNet{
	IP:   net.IPv4(198, 18, 0, 0),
	Mask: net.CIDRMask(15, 32),
}

func isFakeIP(ip net.IP) bool { return fakeIPRange.Contains(ip) }
