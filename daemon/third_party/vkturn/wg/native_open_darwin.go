//go:build darwin

package wg

import (
	"os"

	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/tun"
)

// openNativeTun открывает копию дескриптора utun.
//
// Неблокирующий режим — до os.NewFile: по нему Go решает, вести ли чтение через
// свой опрос. Иначе чтение сидело бы в read(2), а внутренний пакет и остановка
// ждали бы, пока придёт пакет от системы.
func openNativeTun(fd int) (tun.Device, error) {
	duplicate, err := unix.Dup(fd)
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(duplicate)
	if err := unix.SetNonblock(duplicate, true); err != nil {
		_ = unix.Close(duplicate)
		return nil, err
	}
	file := os.NewFile(uintptr(duplicate), "/dev/tun")
	device, err := tun.CreateTUNFromFile(file, 0)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return device, nil
}
