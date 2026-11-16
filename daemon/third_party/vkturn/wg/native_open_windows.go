//go:build windows

package wg

import (
	"errors"

	"golang.zx2c4.com/wireguard/tun"
)

// На Windows дескрипторов интерфейса нет: адаптер Wintun создаёт демон и
// передаёт его в NativeOptions.Device.
func openNativeTun(int) (tun.Device, error) {
	return nil, errors.New("на Windows интерфейс передаёт демон (NativeOptions.Device)")
}
