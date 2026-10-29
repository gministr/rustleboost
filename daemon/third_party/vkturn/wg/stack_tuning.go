package wg

// Настройка TCP сетевого стека gVisor.
//
// WireGuard для Go создаёт стек с настройками по умолчанию: окно приёма без
// автоподстройки. При задержке через релеи VK (150–250 мс) оно и задаёт
// потолок скорости каждого соединения — на SRTP, где транспорт даёт 60–80
// Мбит/с, через стек выходило около 20. Автоподстройка поднимает окно по мере
// надобности; потолок 2 МБ на соединение держит память расширения iOS (на 4 МБ при
// 109 Мбит/с расширение доходило до 40 МБ и система его снимала).

import (
	"log"
	"unsafe"

	"golang.zx2c4.com/wireguard/tun/netstack"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
)

// netTunPrefix повторяет начало netstack.netTun: стек там не экспортирован, а
// настроить его иначе нельзя. Порядок полей сверен с версией WireGuard из
// go.mod; поменяется он — перестанет работать настройка, не туннель (см.
// проверку на nil ниже и тест).
type netTunPrefix struct {
	ep    *channel.Endpoint
	stack *stack.Stack
}

const (
	tcpBufferMin     = 4 << 10
	tcpBufferDefault = 256 << 10
	tcpBufferMax     = 2 << 20
)

func tuneStack(n *netstack.Net) {
	s := (*netTunPrefix)(unsafe.Pointer(n)).stack
	if s == nil {
		log.Printf("[WG] настройка стека пропущена: стек не найден")
		return
	}
	rcv := tcpip.TCPReceiveBufferSizeRangeOption{Min: tcpBufferMin, Default: tcpBufferDefault, Max: tcpBufferMax}
	snd := tcpip.TCPSendBufferSizeRangeOption{Min: tcpBufferMin, Default: tcpBufferDefault, Max: tcpBufferMax}
	moderate := tcpip.TCPModerateReceiveBufferOption(true)
	for name, opt := range map[string]tcpip.SettableTransportProtocolOption{
		"окно приёма": &rcv, "окно отправки": &snd, "автоподстройка": &moderate,
	} {
		if err := s.SetTransportProtocolOption(tcp.ProtocolNumber, opt); err != nil {
			log.Printf("[WG] настройка стека (%s): %v", name, err)
		}
	}
}

func unsafeNet(n *netstack.Net) unsafe.Pointer { return unsafe.Pointer(n) }
