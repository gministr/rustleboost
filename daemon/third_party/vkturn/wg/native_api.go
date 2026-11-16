package wg

import (
	"net/netip"

	"golang.zx2c4.com/wireguard/tun"
)

// NativeOptions — параметры прямого пути.
type NativeOptions struct {
	// TunFD — дескриптор utun. Внутри берётся его копия: оригиналом владеет
	// система, и он переживает перезапуск ядра.
	TunFD int
	// Device — готовый интерфейс вместо дескриптора: на Windows адаптер Wintun
	// создаёт и настраивает демон, а ядро только читает и пишет пакеты.
	// Закрывать его ядро может: демон отдаёт обёртку, у которой закрытие
	// отпускает чтение, а сам адаптер остаётся.
	Device tun.Device
	// TunAddresses — адреса, которые система назначила utun. Адрес WireGuard
	// выдаёт сервер уже после того, как настройки сети применены, поэтому
	// адреса подменяются на лету, а настройки не трогаются: их смена рвёт
	// соединения всех приложений.
	TunAddresses []netip.Addr
	SocksPort    int
	Router       *Router
	Verbose      bool
}

// NativeTraffic — байты через utun: вверх и вниз. ok=false на прежнем пути,
// где счётчики ведёт мост.
func (t *Tunnel) NativeTraffic() (up, down int64, ok bool) {
	if t == nil || t.native == nil {
		return 0, 0, false
	}
	return t.native.up.Load(), t.native.down.Load(), true
}
