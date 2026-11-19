package wg

import (
	"net/netip"
	"testing"

	"golang.zx2c4.com/wireguard/tun/netstack"
)

// Привязка UDP-сокета в стеке.
//
// Нулевой `netip.AddrPort` оставляет номер сетевого протокола нулевым —
// значение, которого в gVisor не существует. Проверяется, чем это кончается:
// ошибкой (которую можно обработать) или паникой (которая уносит всё
// расширение туннеля, и журнал ядра после перезапуска оказывается пуст).
func TestListenUDPRequiresAddressFamily(t *testing.T) {
	_, stack, err := netstack.CreateNetTUN(
		[]netip.Addr{netip.MustParseAddr("10.66.66.2")},
		[]netip.Addr{netip.MustParseAddr("1.1.1.1")},
		1280,
	)
	if err != nil {
		t.Fatalf("стек не создан: %v", err)
	}

	// Зафиксировано как поведение стека, а не как наше желание: нулевой адрес
	// именно паникует. Если однажды gVisor начнёт возвращать ошибку, тест
	// упадёт и об этом станет известно — но полагаться на это нельзя.
	t.Run("нулевой адрес паникует", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("ожидалась паника на номере протокола 0")
			}
		}()
		conn, err := stack.ListenUDPAddrPort(netip.AddrPort{})
		if err == nil {
			conn.Close()
		}
	})

	t.Run("явное семейство адресов", func(t *testing.T) {
		conn, err := stack.ListenUDPAddrPort(netip.AddrPortFrom(netip.IPv4Unspecified(), 0))
		if err != nil {
			t.Fatalf("привязка с явным семейством не удалась: %v", err)
		}
		defer conn.Close()
		t.Logf("привязано: %s", conn.LocalAddr())
	})

	t.Run("адрес интерфейса", func(t *testing.T) {
		conn, err := stack.ListenUDPAddrPort(
			netip.AddrPortFrom(netip.MustParseAddr("10.66.66.2"), 0),
		)
		if err != nil {
			t.Fatalf("привязка к адресу интерфейса не удалась: %v", err)
		}
		defer conn.Close()
		t.Logf("привязано: %s", conn.LocalAddr())
	})
}
