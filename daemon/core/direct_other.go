//go:build !windows

package core

import (
	"errors"

	"golang.zx2c4.com/wireguard/tun"
)

// Прямой путь демона есть только на Windows; на других системах демон
// собирается для разработки и тестов и подключается прежним путём.
func openDirectPlatform(int) (directPlatform, tun.Device, error) {
	return nil, nil, errors.New("только на Windows")
}

func setCoreDevice(tun.Device) {}
