//go:build linux

package wg

import (
	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/tun"
)

// openNativeTun открывает копию дескриптора интерфейса VPN на Android. Оригиналом
// владеет служба: он переживает перезапуск ядра. Вариант без наблюдения за
// интерфейсом: сокеты netlink приложениям на Android закрыты.
func openNativeTun(fd int) (tun.Device, error) {
	duplicate, err := unix.Dup(fd)
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(duplicate)
	device, _, err := tun.CreateUnmonitoredTUNFromFD(duplicate)
	if err != nil {
		_ = unix.Close(duplicate)
		return nil, err
	}
	return device, nil
}
