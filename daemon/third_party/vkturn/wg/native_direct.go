//go:build darwin || linux || windows

package wg

import (
	"context"
	"errors"
	"log"
	"net"
	"net/netip"
	"strconv"
	"sync/atomic"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

// Стек для соединений «напрямую».
//
// Отправить чужой IP-пакет мимо utun расширение не может, поэтому соединение
// заканчивается здесь, а наружу открывается обычный сокет. Собственные сокеты
// расширения в его же туннель не заворачиваются, так что это настоящий прямой
// выход с устройства. До телефона рукой подать, поэтому окно TCP скромное.

const (
	directTCPBufferMin     = 4 << 10
	directTCPBufferDefault = 64 << 10
	directTCPBufferMax     = 512 << 10

	directTCPMaxInFlight = 128
	directUDPLimit       = 128
	directUDPIdle        = 60 * time.Second

	directNIC tcpip.NICID = 1
)

// dialDirect — настоящий сокет мимо туннеля. Переменная ради тестов: адреса
// назначения в них вымышленные.
var dialDirect = func(ctx context.Context, network, target string) (net.Conn, error) {
	dialer := net.Dialer{Timeout: handshakeTimeout, Control: dialControl}
	return dialer.DialContext(ctx, network, target)
}

type directStack struct {
	owner      *nativeDevice
	ep         *channel.Endpoint
	stack      *stack.Stack
	handle     *channel.NotificationHandle
	sessions   *sessionTable
	udpFlows   atomic.Int32
	closed     atomic.Bool
	closeLocal context.CancelFunc
	context    context.Context
}

func newDirectStack(owner *nativeDevice) (*directStack, error) {
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	ctx, cancel := context.WithCancel(context.Background())
	d := &directStack{
		owner:      owner,
		ep:         channel.New(256, uint32(owner.mtu), ""),
		stack:      s,
		sessions:   newSessionTable(currentConnectionLimit()),
		context:    ctx,
		closeLocal: cancel,
	}

	sack := tcpip.TCPSACKEnabled(true)
	moderate := tcpip.TCPModerateReceiveBufferOption(true)
	receive := tcpip.TCPReceiveBufferSizeRangeOption{
		Min: directTCPBufferMin, Default: directTCPBufferDefault, Max: directTCPBufferMax,
	}
	send := tcpip.TCPSendBufferSizeRangeOption{
		Min: directTCPBufferMin, Default: directTCPBufferDefault, Max: directTCPBufferMax,
	}
	for _, option := range []tcpip.SettableTransportProtocolOption{&sack, &moderate, &receive, &send} {
		if err := s.SetTransportProtocolOption(tcp.ProtocolNumber, option); err != nil {
			log.Printf("[WG] настройка прямого стека: %v", err)
		}
	}

	d.handle = d.ep.AddNotify(d)
	if err := s.CreateNIC(directNIC, d.ep); err != nil {
		return nil, errors.New(err.String())
	}
	// Назначение здесь — чужие адреса: стек обязан принимать их как свои и
	// отвечать от их имени.
	if err := s.SetPromiscuousMode(directNIC, true); err != nil {
		return nil, errors.New(err.String())
	}
	if err := s.SetSpoofing(directNIC, true); err != nil {
		return nil, errors.New(err.String())
	}
	s.SetRouteTable([]tcpip.Route{
		{Destination: header.IPv4EmptySubnet, NIC: directNIC},
		{Destination: header.IPv6EmptySubnet, NIC: directNIC},
	})

	s.SetTransportProtocolHandler(tcp.ProtocolNumber,
		tcp.NewForwarder(s, 0, directTCPMaxInFlight, d.handleTCP).HandlePacket)
	// Без udp.Forwarder: сигнатура его обработчика зависит от версии gVisor, а
	// собирается этот код с разными (iOS и Windows). Конечная точка создаётся
	// прямо внутри вызова, поэтому пакет здесь ещё действителен.
	s.SetTransportProtocolHandler(udp.ProtocolNumber,
		func(id stack.TransportEndpointID, packet *stack.PacketBuffer) bool {
			d.handleUDP(udp.NewForwarderRequest(s, id, packet))
			return true
		})
	return d, nil
}

// inject передаёт стеку пакет с utun. Копия обязательна: буфером владеет WireGuard.
func (d *directStack) inject(packet []byte) {
	if d == nil || d.closed.Load() || len(packet) == 0 {
		return
	}
	var protocol tcpip.NetworkProtocolNumber
	switch packet[0] >> 4 {
	case 4:
		protocol = header.IPv4ProtocolNumber
	case 6:
		protocol = header.IPv6ProtocolNumber
	default:
		return
	}
	buffered := stack.NewPacketBuffer(stack.PacketBufferOptions{
		Payload: buffer.MakeWithData(append([]byte(nil), packet...)),
	})
	d.ep.InjectInbound(protocol, buffered)
	buffered.DecRef()
}

// WriteNotify забирает исходящий пакет стека: он уходит на utun, приложению.
func (d *directStack) WriteNotify() {
	packet := d.ep.Read()
	if packet == nil {
		return
	}
	view := packet.ToView()
	packet.DecRef()
	defer view.Release()
	if d.closed.Load() {
		return
	}
	d.owner.writeFromDirect(view.AsSlice())
}

func (d *directStack) close() {
	if d.closed.Swap(true) {
		return
	}
	d.closeLocal()
	d.ep.RemoveNotify(d.handle)
	d.stack.RemoveNIC(directNIC)
	d.stack.Close()
	d.ep.Close()
}

func addressOf(id stack.TransportEndpointID) (netip.Addr, string) {
	address, _ := netip.AddrFromSlice(id.LocalAddress.AsSlice())
	return address, net.JoinHostPort(address.String(), strconv.Itoa(int(id.LocalPort)))
}

func (d *directStack) handleTCP(request *tcp.ForwarderRequest) {
	address, target := addressOf(request.ID())
	// Сюда же приходит заблокированный TCP: отказ здесь — немедленный сброс.
	if d.owner.actionFor(address) == ActionBlock {
		request.Complete(true)
		return
	}

	remote, err := dialDirect(d.context, "tcp", target)
	if err != nil {
		request.Complete(true)
		return
	}
	var queue waiter.Queue
	endpoint, tcpErr := request.CreateEndpoint(&queue)
	if tcpErr != nil {
		_ = remote.Close()
		request.Complete(true)
		return
	}
	request.Complete(false)

	local := gonet.NewTCPConn(&queue, endpoint)
	s := d.sessions.admit(local)
	defer d.sessions.release(s)
	defer local.Close()
	defer func() {
		// Паника в горутине снимает весь процесс расширения.
		if r := recover(); r != nil {
			log.Printf("[WG] паника в прямом соединении: %v", r)
		}
	}()
	relay(sessionConn{local, s}, nil, func() (net.Conn, error) { return remote, nil })
}

func (d *directStack) handleUDP(request *udp.ForwarderRequest) {
	if d.udpFlows.Add(1) > directUDPLimit {
		d.udpFlows.Add(-1)
		return
	}
	_, target := addressOf(request.ID())
	var queue waiter.Queue
	endpoint, tcpErr := request.CreateEndpoint(&queue)
	if tcpErr != nil {
		d.udpFlows.Add(-1)
		return
	}
	local := gonet.NewUDPConn(&queue, endpoint)
	go func() {
		defer d.udpFlows.Add(-1)
		defer local.Close()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[WG] паника в прямой связке UDP: %v", r)
			}
		}()
		remote, err := dialDirect(d.context, "udp", target)
		if err != nil {
			return
		}
		defer remote.Close()

		_ = local.SetDeadline(time.Now().Add(directUDPIdle))
		_ = remote.SetDeadline(time.Now().Add(directUDPIdle))
		done := make(chan struct{}, 2)
		pump := func(dst, src net.Conn) {
			copyWithPooledBuffer(idleConn{dst, directUDPIdle}, idleConn{src, directUDPIdle})
			done <- struct{}{}
		}
		go pump(remote, local)
		go pump(local, remote)
		// Кончилось одно направление (молчание, закрытие) — связка закончена.
		<-done
		_ = local.Close()
		_ = remote.Close()
		<-done
	}()
}
