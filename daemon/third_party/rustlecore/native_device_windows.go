//go:build windows

package rustlecore

import "golang.zx2c4.com/wireguard/tun"

// SetTunDevice передаёт ядру интерфейс для прямого пути: на Windows адаптер
// Wintun создаёт и настраивает демон. Используется при запуске с
// "nativeDevice": true; nil снимает. На iOS, Android и macOS файла нет:
// там интерфейс приходит дескриптором.
func SetTunDevice(device tun.Device) {
	if device == nil {
		tunDevice.Store(nil)
		return
	}
	tunDevice.Store(&tunDeviceBox{device: device})
}
