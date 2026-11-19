//go:build windows

/* SPDX-License-Identifier: MIT
 *
 * Copyright (C) 2019-2026 WireGuard LLC. All Rights Reserved.
 * Copyright (C) 2026 RustleBoost.
 */

package firewall

import (
	"errors"
	"net/netip"
)

// Пакет — копия golang.zx2c4.com/wireguard/windows/tunnel/firewall (MIT, см.
// COPYING) с одним добавлением ниже. Сам пакет умеет только «всё или ничего»:
// либо блокировать всё мимо туннеля (принтеры и локальная сеть отваливаются),
// либо не защищать ничего.

// EnableDNSProtection не пускает DNS-запросы (порт 53, UDP и TCP) никуда,
// кроме DNS туннеля. Остальной трафик не трогает.
//
// Без этого Windows спрашивает имена сразу у всех сетей («умное разрешение
// имён») и берёт первый ответ, а провайдер на заблокированные сайты отвечает
// заглушкой быстрее, чем туннель — настоящим адресом.
//
// Свой процесс (служба) пропускается целиком: ядро само ходит в сеть мимо
// туннеля. Сессия динамическая — правила исчезают, если служба упадёт.
func EnableDNSProtection(dnsServers []netip.Addr) error {
	if wfpSession != 0 {
		return errors.New("The firewall has already been enabled")
	}
	if len(dnsServers) == 0 {
		return errors.New("no tunnel DNS servers")
	}
	session, err := createWfpSession()
	if err != nil {
		return wrapErr(err)
	}
	err = runTransaction(session, func(session uintptr) error {
		baseObjects, err := registerBaseObjects(session)
		if err != nil {
			return wrapErr(err)
		}
		if err := permitWireGuardService(session, baseObjects, 15); err != nil {
			return wrapErr(err)
		}
		return blockDNS(dnsServers, session, baseObjects, 15, 14)
	})
	if err != nil {
		fwpmEngineClose0(session)
		return wrapErr(err)
	}
	wfpSession = session
	return nil
}

// Enabled — включены ли сейчас правила.
func Enabled() bool { return wfpSession != 0 }
