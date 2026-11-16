//go:build !darwin && !linux && !windows

package wg

import (
	"errors"
	"net/netip"
	"sync/atomic"

	"golang.zx2c4.com/wireguard/conn"
)

// Прямой путь есть на iOS, Android и Windows. Здесь — системы без него.
type nativeDevice struct {
	up, down atomic.Int64
}

// StartNative на этих системах недоступен.
func StartNative(Config, netip.AddrPort, conn.Bind, NativeOptions) (*Tunnel, error) {
	return nil, errors.New("wg: прямой путь на этой системе недоступен")
}
