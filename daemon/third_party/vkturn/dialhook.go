package vkturn

import (
	"fmt"
	"net"
	"sync/atomic"
	"syscall"

	"github.com/cacggghp/vk-turn-proxy/pkg/proxy"

	"rustleboost/vkturn/wg"
)

// Проверка перед каждым соединением транспорта: релеи ВК, API ВК за учётками,
// DNS ядра, прямой выход по правилам.
//
// На iOS и Android не ставится: собственные сокеты расширения и службы в их же
// туннель не попадают. На Windows маршрут по умолчанию уходит в туннель для
// всего компьютера, и демон через эту функцию закрепляет каждый адрес за
// физическим шлюзом до первого пакета — как консольный клиент авторов SRTP
// (pkg/proxy/dialhook.go). Иначе транспорт уходит сам в себя.
var dialHook atomic.Pointer[func(network, address string) error]

// SetDialHook ставит проверку во всё ядро сразу: здесь, в транспорте SRTP
// (proxy) и в прямом выходе WireGuard (wg). nil снимает везде.
func SetDialHook(h func(network, address string) error) {
	if h == nil {
		dialHook.Store(nil)
	} else {
		dialHook.Store(&h)
	}
	proxy.SetDialHook(h)
	wg.SetDialHook(h)
}

func beforeDial(network, address string) error {
	h := dialHook.Load()
	if h == nil {
		return nil
	}
	if err := (*h)(network, address); err != nil {
		return fmt.Errorf("проверка перед соединением %s %s: %w", network, address, err)
	}
	return nil
}

// dialControl — beforeDial в форме net.Dialer.Control.
func dialControl(network, address string, _ syscall.RawConn) error {
	return beforeDial(network, address)
}

// hookedDialer — net.Dialer с проверкой: для клиентов HTTP к API ВК.
func hookedDialer() net.Dialer {
	return net.Dialer{Control: dialControl}
}

// DialControl — та же проверка для соединений вне ядра (регистрация WARP).
func DialControl(network, address string, raw syscall.RawConn) error {
	return dialControl(network, address, raw)
}
