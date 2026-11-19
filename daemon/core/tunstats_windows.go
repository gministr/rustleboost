//go:build windows

package core

import (
	"net"

	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"

	"github.com/vpnclient/daemon/config"
)

// adapterCounters — байты через адаптер туннеля по счётчикам самой Windows.
// Отдано в сеть — то, что программы отправили (Upload), получено — Download.
func adapterCounters() (TrafficStats, bool) {
	iface, err := net.InterfaceByName(config.TunAdapterName)
	if err != nil {
		return TrafficStats{}, false
	}
	luid, err := winipcfg.LUIDFromIndex(uint32(iface.Index))
	if err != nil {
		return TrafficStats{}, false
	}
	row, err := luid.Interface()
	if err != nil {
		return TrafficStats{}, false
	}
	// Для адаптера туннеля «исходящие» Windows — это пакеты программ в туннель.
	return TrafficStats{Upload: int64(row.OutOctets), Download: int64(row.InOctets)}, true
}
