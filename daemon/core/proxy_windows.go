//go:build windows

package core

import (
	"log"
)

const proxyOverride = "localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;192.168.*;<local>"

// SetSystemProxy sets the Windows system HTTP/S proxy to addr (e.g. "127.0.0.1:2080").
// This makes Chrome, Edge, and most Windows apps route traffic through sing-box.
func SetSystemProxy(addr string) {
	base := `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`

	run("reg", "add", base, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f")
	run("reg", "add", base, "/v", "ProxyServer", "/t", "REG_SZ", "/d", addr, "/f")
	run("reg", "add", base, "/v", "ProxyOverride", "/t", "REG_SZ", "/d", proxyOverride, "/f")

	// WinHTTP (used by Windows services, PowerShell, curl, etc.)
	run("netsh", "winhttp", "set", "proxy", addr, proxyOverride)

	log.Printf("[proxy] system proxy set → %s", addr)
}

// ClearSystemProxy removes the Windows system proxy.
func ClearSystemProxy() {
	base := `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	run("reg", "add", base, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f")
	run("netsh", "winhttp", "reset", "proxy")
	log.Println("[proxy] system proxy cleared")
}

func run(name string, args ...string) {
	if err := hiddenCommand(name, args...).Run(); err != nil {
		log.Printf("[proxy] %s %v: %v", name, args, err)
	}
}
