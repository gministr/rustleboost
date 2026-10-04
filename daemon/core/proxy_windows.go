//go:build windows

package core

import (
	"log"

	"golang.org/x/sys/windows/registry"
)

const proxyOverride = "localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;192.168.*;<local>"

const internetSettings = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// The proxy settings are written through the registry API rather than by
// shelling out to reg.exe and netsh.exe.
//
// Spawning those two to point the system proxy at a local port is, byte for
// byte, what a traffic-intercepting trojan does, and behavioural engines
// score it that way — several flagged the installer on exactly this kind of
// pattern. Doing the same work in-process removes the child processes and the
// command lines they expose, without changing what the user gets.
//
// netsh's WinHTTP proxy is deliberately not set any more. It is machine-wide
// state that affects Windows services well outside this app, it needs
// elevation, and it is the single most trojan-like thing the daemon did. TUN
// mode — the default — carries those clients anyway.

// SetSystemProxy points the Windows per-user proxy at addr, e.g.
// "127.0.0.1:2080". Chrome, Edge and most Windows applications follow it.
func SetSystemProxy(addr string) {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, internetSettings, registry.SET_VALUE)
	if err != nil {
		log.Printf("[proxy] open Internet Settings: %v", err)
		return
	}
	defer key.Close()

	if err := key.SetDWordValue("ProxyEnable", 1); err != nil {
		log.Printf("[proxy] set ProxyEnable: %v", err)
		return
	}
	if err := key.SetStringValue("ProxyServer", addr); err != nil {
		log.Printf("[proxy] set ProxyServer: %v", err)
		return
	}
	if err := key.SetStringValue("ProxyOverride", proxyOverride); err != nil {
		log.Printf("[proxy] set ProxyOverride: %v", err)
	}

	log.Printf("[proxy] system proxy set → %s", addr)
}

// ClearSystemProxy turns the per-user proxy back off.
func ClearSystemProxy() {
	key, err := registry.OpenKey(registry.CURRENT_USER, internetSettings, registry.SET_VALUE)
	if err != nil {
		// Nothing to undo if the key was never there.
		return
	}
	defer key.Close()

	if err := key.SetDWordValue("ProxyEnable", 0); err != nil {
		log.Printf("[proxy] clear ProxyEnable: %v", err)
		return
	}
	log.Println("[proxy] system proxy cleared")
}
