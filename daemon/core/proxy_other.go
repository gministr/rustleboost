//go:build !windows

package core

import "log"

func SetSystemProxy(addr string) {
	log.Printf("[proxy] set system proxy %s (no-op on this OS)", addr)
}

func ClearSystemProxy() {
	log.Println("[proxy] clear system proxy (no-op on this OS)")
}
