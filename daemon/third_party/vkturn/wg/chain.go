package wg

// WireGuard внутри WireGuard.
//
// Зачем. Прямой UDP с телефона до Cloudflare на сетях российских операторов не
// проходит: рукопожатие уходит, а ответы с данными гасятся (замерено на
// устройстве). Туннель WDTT при этом работает — его трафик оператор не режет.
// Поэтому пакеты WARP едут внутри WDTT: внешний туннель отдаёт сокет наружу,
// внутренний WireGuard шлёт через него свои зашифрованные пакеты в Cloudflare.
//
// Ключи внешнего и внутреннего туннелей разные. Внешний знает только узел
// RustleBoost, внутренний — только устройство и Cloudflare; узел пересылает
// непрозрачные пакеты, как и раньше.

import (
	"errors"
	"log"
	"net"
	"net/netip"
	"sync"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/tun/netstack"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
)

// chainBind — conn.Bind, который вместо системного сокета пишет в UDP-сокет
// внешнего стека. Каждый Open заводит свой сокет и свою функцию приёма, и
// приём останавливается ровно тогда, когда закрыт именно его сокет. Общий флаг
// закрытия здесь не годится: устройство закрывает и открывает привязку заново,
// и старый флаг мешал приёму новой (проверено на харнессе: приём стартовал и
// тут же выходил без ошибки).
type chainBind struct {
	outer  *netstack.Net
	local  netip.Addr
	remote netip.AddrPort

	mu      sync.Mutex
	sockets []*gonet.UDPConn
	sent    int
	// Счётчики для журнала: видно, уходят ли пакеты во внешний стек и
	// возвращаются ли ответы. Пишем первые несколько, дальше по редким.
	received int
}

// chainLogEvery — как часто писать в журнал после первых пакетов.
const chainLogEvery = 50

func logChain(direction string, count int, size int) {
	if count <= 5 || count%chainLogEvery == 0 {
		log.Printf("[WARP-цепочка] %s #%d, %d байт", direction, count, size)
	}
}

// chainEndpoint — адрес Cloudflare в форме, которую понимает WireGuard.
type chainEndpoint struct{ addr netip.AddrPort }

func (e chainEndpoint) ClearSrc()           {}
func (e chainEndpoint) SrcToString() string { return "" }
func (e chainEndpoint) DstToString() string { return e.addr.String() }
func (e chainEndpoint) DstIP() netip.Addr   { return e.addr.Addr() }
func (e chainEndpoint) SrcIP() netip.Addr   { return netip.Addr{} }
func (e chainEndpoint) DstToBytes() []byte {
	port := e.addr.Port()
	return append(e.addr.Addr().AsSlice(), byte(port>>8), byte(port))
}

func (b *chainBind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	// Неподключённый сокет: ответы принимаются по адресу без привязки к
	// четвёрке. Подключённый (DialUDP) во внешнем стеке терял входящие.
	socket, err := b.outer.ListenUDPAddrPort(netip.AddrPortFrom(b.local, port))
	if err != nil {
		return nil, 0, err
	}
	b.mu.Lock()
	b.sockets = append(b.sockets, socket)
	b.mu.Unlock()

	actual := port
	if addr, ok := socket.LocalAddr().(*net.UDPAddr); ok {
		actual = uint16(addr.Port)
	}
	receive := func(packets [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
		return b.receive(socket, packets, sizes, eps)
	}
	return []conn.ReceiveFunc{receive}, actual, nil
}

// receive читает один пакет из своего сокета. Ошибка чтения — сокет закрыт,
// и приём этой функции должен остановиться.
func (b *chainBind) receive(socket *gonet.UDPConn, packets [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
	n, from, err := socket.ReadFrom(packets[0])
	if err != nil {
		return 0, err
	}
	b.mu.Lock()
	b.received++
	logChain("получено из внешнего стека", b.received, n)
	b.mu.Unlock()
	if from != nil {
		log.Printf("[WARP-цепочка] ответ от %v, %d байт", from, n)
	}
	sizes[0] = n
	eps[0] = chainEndpoint{addr: b.remote}
	return 1, nil
}

func (b *chainBind) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	var first error
	for _, socket := range b.sockets {
		if err := socket.Close(); err != nil && first == nil {
			first = err
		}
	}
	b.sockets = nil
	return first
}

func (b *chainBind) SetMark(uint32) error { return nil }

// current — сокет для отправки: последний открытый.
func (b *chainBind) current() *gonet.UDPConn {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.sockets) == 0 {
		return nil
	}
	return b.sockets[len(b.sockets)-1]
}

func (b *chainBind) Send(bufs [][]byte, _ conn.Endpoint) error {
	socket := b.current()
	if socket == nil {
		return net.ErrClosed
	}
	target := net.UDPAddrFromAddrPort(b.remote)
	for _, buf := range bufs {
		if _, err := socket.WriteTo(buf, target); err != nil {
			log.Printf("[WARP-цепочка] запись во внешний стек: %v", err)
			return err
		}
		b.mu.Lock()
		b.sent++
		logChain("отправлено во внешний стек", b.sent, len(buf))
		b.mu.Unlock()
	}
	return nil
}

func (b *chainBind) ParseEndpoint(s string) (conn.Endpoint, error) {
	addr, err := netip.ParseAddrPort(s)
	if err != nil {
		return nil, err
	}
	return chainEndpoint{addr: addr}, nil
}

func (b *chainBind) BatchSize() int { return 1 }

// StartChained поднимает внутренний WireGuard поверх внешнего туннеля outer.
//
// Внутренний MTU ниже обычного: его пакеты сами становятся датаграммами
// внешнего стека, а тот ограничен своим MTU. 1200 оставляет запас на заголовки
// обоих WireGuard и UDP.
func StartChained(outer *Tunnel, cfg Config, remote netip.AddrPort, socksPort int, router *Router, verbose bool) (*Tunnel, error) {
	if outer == nil || outer.stack == nil {
		return nil, errors.New("wg: внешний туннель не поднят")
	}
	cfg.MTU = ChainedMTU
	bind := &chainBind{outer: outer.stack, local: outer.local, remote: remote}
	return startWithBind(cfg, remote, socksPort, router, verbose, bind)
}

// ChainedMTU — см. StartChained.
const ChainedMTU = 1200
