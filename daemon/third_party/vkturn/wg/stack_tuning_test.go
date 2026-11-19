package wg

import (
	"net/netip"
	"testing"

	"golang.zx2c4.com/wireguard/tun/netstack"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
)

// Настройка достаёт стек через разметку чужой структуры: проверяем, что
// после обновления WireGuard она всё ещё попадает в стек.
func TestTuneStackReachesStack(t *testing.T) {
	_, n, err := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr("10.0.0.2")}, nil, 1280)
	if err != nil {
		t.Fatal(err)
	}
	tuneStack(n)
	s := (*netTunPrefix)(unsafeNet(n)).stack
	var rcv tcpip.TCPReceiveBufferSizeRangeOption
	if err := s.TransportProtocolOption(tcp.ProtocolNumber, &rcv); err != nil {
		t.Fatal(err)
	}
	if rcv.Max != tcpBufferMax {
		t.Fatalf("окно приёма не настроено: %+v", rcv)
	}
}
