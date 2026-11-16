//go:build darwin || linux || windows

package wg

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/net/dns/dnsmessage"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// WireGuard прямо на интерфейсе utun — как в приложении авторов транспорта.
//
// Прежний путь пакета на iOS: utun → hev (свой стек TCP) → локальный SOCKS5 →
// gVisor (второй стек TCP) → WireGuard. Каждое соединение стоило там около
// 200 КБ: сессия моста, горутины, буферы двух стеков. Всплеск соединений упирал
// расширение в лимит памяти в 50 МБ, и система снимала его молча.
//
// Здесь TCP идёт напрямую между приложением на телефоне и сервером назначения,
// а через WireGuard проходят готовые IP-пакеты. Соединение не стоит почти
// ничего, сколько бы их ни было, и лишних копий нет.
//
// Маршрутизацию это не отменяет — её здесь решает устройство nativeDevice:
//   - идущее через туннель уходит в WireGuard пакетом;
//   - идущее напрямую приходится заканчивать в устройстве и открывать настоящий
//     сокет: сырой пакет мимо utun отправить нельзя. Для этого есть маленький
//     стек gVisor (native_direct.go), он создаётся при первой такой необходимости
//     и обслуживает только эти соединения;
//   - имя узла из потока здесь не прочитать: решение нужно ещё на SYN, — поэтому
//     правила по доменам работают по ответам DNS, которые идут через устройство.
//
// Локальный SOCKS5 остаётся: через него ходят собственные запросы расширения
// (проверка живости, пульс на сервер), которым нужен путь через туннель. Он
// работает на отдельном маленьком стеке, чьи пакеты подмешиваются к пакетам utun.

const (
	// Порты внутреннего стека. У приложений телефона исходящие порты из
	// диапазона 49152–65535, поэтому ответ по порту назначения однозначно
	// отличается от ответа приложению.
	internalPortMin = 20000
	internalPortMax = 29999

	// Сколько помнить решение по соединению, секунды. Решение обязано быть
	// постоянным: появись посреди соединения ответ DNS с другим правилом для
	// того же адреса, поток ушёл бы в другую сторону и оборвался.
	flowIdleTCP = 600
	flowIdleUDP = 120
	flowLimit   = 8192

	// Записи «адрес → правило» по ответам DNS: живут не меньше пяти минут, потому
	// что приложения держат адрес дольше, чем велит TTL.
	dnsVerdictMinTTL = 300
	dnsVerdictMaxTTL = 3600
	dnsVerdictLimit  = 4096

	nativeQueueLen = 64
)

// StartNative поднимает WireGuard прямо на utun.
func StartNative(cfg Config, endpoint netip.AddrPort, bind conn.Bind, options NativeOptions) (*Tunnel, error) {
	inner := options.Device
	if inner == nil {
		opened, err := openNativeTun(options.TunFD)
		if err != nil {
			return nil, fmt.Errorf("wg: utun: %w", err)
		}
		inner = opened
	}
	return startNativeOn(inner, cfg, endpoint, bind, options)
}

func startNativeOn(
	inner tun.Device,
	cfg Config,
	endpoint netip.AddrPort,
	bind conn.Bind,
	options NativeOptions,
) (*Tunnel, error) {
	addresses := make([]netip.Addr, 0, len(cfg.Addresses))
	for _, prefix := range cfg.Addresses {
		addresses = append(addresses, prefix.Addr())
	}
	if len(addresses) == 0 {
		_ = inner.Close()
		return nil, errors.New("wg: сервер не назвал адрес")
	}

	internal, stack, err := netstack.CreateNetTUN(addresses, cfg.DNS, cfg.MTU)
	if err != nil {
		_ = inner.Close()
		return nil, fmt.Errorf("wg: внутренний стек: %w", err)
	}
	setInternalPortRange(stack)

	dev := newNativeDevice(inner, internal, cfg.MTU, addresses, options.TunAddresses, options.Router)
	wgDevice := device.NewDevice(dev, bind, newLogger(options.Verbose))

	settings, err := cfg.uapi(endpoint)
	if err != nil {
		wgDevice.Close()
		return nil, fmt.Errorf("wg: %w", err)
	}
	if err := wgDevice.IpcSet(settings); err != nil {
		wgDevice.Close()
		return nil, fmt.Errorf("wg: приём конфигурации: %w", err)
	}
	if err := wgDevice.Up(); err != nil {
		wgDevice.Close()
		return nil, fmt.Errorf("wg: подъём интерфейса: %w", err)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", options.SocksPort))
	if err != nil {
		wgDevice.Close()
		return nil, fmt.Errorf("wg: порт SOCKS5 %d: %w", options.SocksPort, err)
	}

	// Внутренний SOCKS5 всегда идёт через туннель: правила относятся к трафику
	// приложений, а проверка живости обязана проверять именно канал.
	t := &Tunnel{
		device: wgDevice, listener: listener, stack: stack,
		local: addresses[0], peerPublicKey: cfg.PeerPublicKey, native: dev,
	}
	go t.serve()
	log.Printf("[WG] Прямой путь на utun: адрес %s, SOCKS5 на 127.0.0.1:%d, пир через %s",
		addresses[0], options.SocksPort, endpoint)
	return t, nil
}

// setInternalPortRange уводит порты внутреннего стека из диапазона приложений.
func setInternalPortRange(n *netstack.Net) {
	s := (*netTunPrefix)(unsafe.Pointer(n)).stack
	if s == nil {
		log.Printf("[WG] порты внутреннего стека не заданы: стек не найден")
		return
	}
	if err := s.SetPortRange(internalPortMin, internalPortMax); err != nil {
		log.Printf("[WG] порты внутреннего стека: %v", err)
	}
}

// MARK: - Устройство

type packetRoute uint8

const (
	routeTunnel packetRoute = iota
	routeDirect
	routeDrop
)

type flowKey struct {
	dst     [16]byte
	src     uint16
	dstPort uint16
	proto   uint8
}

type flowEntry struct {
	route packetRoute
	seen  int64
}

type dnsVerdict struct {
	action  Action
	expires int64
}

// nativeDevice — tun.Device для WireGuard поверх настоящего utun.
//
// WireGuard вызывает Read из одной горутины и Write из одной, так что таблица
// соединений принадлежит читающей горутине и замка не требует.
type nativeDevice struct {
	inner    tun.Device
	internal tun.Device
	mtu      int
	router   *Router
	domains  bool

	wg4, tun4 [4]byte
	has4      bool
	wg6, tun6 [16]byte
	has6      bool

	flows     map[flowKey]flowEntry
	lastPurge int64

	dnsMu sync.RWMutex
	dns   map[netip.Addr]dnsVerdict

	direct     atomic.Pointer[directStack]
	directOnce sync.Once

	internalOut chan []byte
	internalTCP [(internalPortMax - internalPortMin + 64) / 64]atomic.Uint64
	internalUDP [(internalPortMax - internalPortMin + 64) / 64]atomic.Uint64
	wake        func()
	closed      chan struct{}
	closeOnce   sync.Once

	writeOne [1][]byte
	readOne  [1][]byte

	up, down atomic.Int64
}

func newNativeDevice(
	inner, internal tun.Device,
	mtu int,
	wgAddresses, tunAddresses []netip.Addr,
	router *Router,
) *nativeDevice {
	d := &nativeDevice{
		inner:       inner,
		internal:    internal,
		mtu:         mtu,
		router:      router,
		domains:     router.HasDomainRules(),
		flows:       make(map[flowKey]flowEntry),
		dns:         make(map[netip.Addr]dnsVerdict),
		internalOut: make(chan []byte, nativeQueueLen),
		closed:      make(chan struct{}),
	}
	var wg4, wg6, tun4, tun6 bool
	for _, a := range wgAddresses {
		if a.Is4() && !wg4 {
			d.wg4, wg4 = a.As4(), true
		} else if a.Is6() && !wg6 {
			d.wg6, wg6 = a.As16(), true
		}
	}
	for _, a := range tunAddresses {
		if a.Is4() && !tun4 {
			d.tun4, tun4 = a.As4(), true
		} else if a.Is6() && !tun6 {
			d.tun6, tun6 = a.As16(), true
		}
	}
	d.has4 = wg4 && tun4
	d.has6 = wg6 && tun6

	// Чтение utun блокируется, пока нет пакетов, а пакеты внутреннего стека ждать
	// их не могут. Отдельная горутина на чтение utun стоила бы лишнего
	// переключения на каждый пакет: вместо этого редкий внутренний пакет прерывает
	// чтение сроком — см. Read.
	file := inner.File()
	if waker, ok := inner.(interface{ WakeRead() }); ok {
		d.wake = waker.WakeRead
	} else {
		d.wake = func() {
			if file == nil {
				return
			}
			if err := file.SetReadDeadline(time.Now()); err != nil {
				log.Printf("[WG] не удалось прервать чтение utun: %v", err)
			}
		}
	}
	if internal != nil {
		go d.pumpInternal()
	}
	return d
}

func (d *nativeDevice) pumpInternal() {
	buffer := make([]byte, d.mtu+64)
	bufs := [][]byte{buffer}
	sizes := []int{0}
	for {
		count, err := d.internal.Read(bufs, sizes, 0)
		if err != nil {
			return
		}
		if count == 0 {
			continue
		}
		packet := append([]byte(nil), buffer[:sizes[0]]...)
		d.markInternalPort(packet)
		select {
		case d.internalOut <- packet:
			d.wake()
		case <-d.closed:
			return
		default:
			// Очередь полна: для TCP это обычная потеря, он повторит.
		}
	}
}

func (d *nativeDevice) File() *os.File           { return d.inner.File() }
func (d *nativeDevice) MTU() (int, error)        { return d.mtu, nil }
func (d *nativeDevice) Name() (string, error)    { return d.inner.Name() }
func (d *nativeDevice) Events() <-chan tun.Event { return d.inner.Events() }
func (d *nativeDevice) BatchSize() int           { return 1 }

func (d *nativeDevice) Close() error {
	var err error
	d.closeOnce.Do(func() {
		close(d.closed)
		err = d.inner.Close()
		if d.internal != nil {
			_ = d.internal.Close()
		}
		if direct := d.direct.Load(); direct != nil {
			direct.close()
		}
	})
	return err
}

// Read отдаёт WireGuard следующий пакет, идущий через туннель.
func (d *nativeDevice) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	for {
		select {
		case packet := <-d.internalOut:
			sizes[0] = copy(bufs[0][offset:], packet)
			return 1, nil
		default:
		}

		d.readOne[0] = bufs[0]
		count, err := d.inner.Read(d.readOne[:], sizes, offset)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				// Снять срок надо до следующей проверки очереди: срок, поставленный
				// после неё, прервёт чтение сразу, и пакет не задержится.
				if file := d.inner.File(); file != nil {
					_ = file.SetReadDeadline(time.Time{})
				}
				continue
			}
			return 0, err
		}
		if count == 0 {
			continue
		}

		packet := bufs[0][offset : offset+sizes[0]]
		d.up.Add(int64(len(packet)))
		switch d.routePacket(packet) {
		case routeTunnel:
			if d.translateOut(packet) {
				return 1, nil
			}
		case routeDirect:
			d.directStack().inject(packet)
		}
	}
}

// Write принимает от WireGuard расшифрованные пакеты.
func (d *nativeDevice) Write(bufs [][]byte, offset int) (int, error) {
	for index, buffer := range bufs {
		packet := buffer[offset:]
		if len(packet) == 0 {
			continue
		}
		if d.internal != nil && d.isInternalPacket(packet) {
			d.writeOne[0] = buffer
			_, _ = d.internal.Write(d.writeOne[:], offset)
			continue
		}
		d.snoopDNS(packet)
		if !d.translateIn(packet) {
			continue
		}
		d.down.Add(int64(len(packet)))
		d.writeOne[0] = buffer
		if _, err := d.inner.Write(d.writeOne[:], offset); err != nil {
			return index, err
		}
	}
	return len(bufs), nil
}

func (d *nativeDevice) directStack() *directStack {
	d.directOnce.Do(func() {
		stack, err := newDirectStack(d)
		if err != nil {
			log.Printf("[WG] стек прямых соединений не создан: %v", err)
			return
		}
		d.direct.Store(stack)
		log.Printf("[WG] Создан стек прямых соединений")
	})
	return d.direct.Load()
}

// writeFromDirect возвращает на utun пакет стека прямых соединений. Вызывается
// из горутин gVisor, поэтому не трогает writeOne.
func (d *nativeDevice) writeFromDirect(packet []byte) {
	const headroom = 4
	buffer := make([]byte, headroom+len(packet))
	copy(buffer[headroom:], packet)
	d.snoopDNS(buffer[headroom:])
	d.down.Add(int64(len(packet)))
	if _, err := d.inner.Write([][]byte{buffer}, headroom); err != nil {
		select {
		case <-d.closed:
		default:
			log.Printf("[WG] запись в utun: %v", err)
		}
	}
}

// MARK: - Выбор пути

const (
	protoICMP = 1
	protoTCP  = 6
	protoUDP  = 17

	tcpFlagSYN = 0x02
	tcpFlagACK = 0x10
)

// packetInfo разбирает заголовки. l4 пуст у фрагментов, кроме первого.
func packetInfo(packet []byte) (dst netip.Addr, proto uint8, l4 []byte, ok bool) {
	if len(packet) < 1 {
		return dst, 0, nil, false
	}
	switch packet[0] >> 4 {
	case 4:
		if len(packet) < 20 {
			return dst, 0, nil, false
		}
		headerLen := int(packet[0]&0x0f) * 4
		if headerLen < 20 || len(packet) < headerLen {
			return dst, 0, nil, false
		}
		dst = netip.AddrFrom4([4]byte(packet[16:20]))
		proto = packet[9]
		if (uint16(packet[6]&0x1f)<<8 | uint16(packet[7])) == 0 {
			l4 = packet[headerLen:]
		}
		return dst, proto, l4, true
	case 6:
		if len(packet) < 40 {
			return dst, 0, nil, false
		}
		dst = netip.AddrFrom16([16]byte(packet[24:40]))
		return dst, packet[6], packet[40:], true
	}
	return dst, 0, nil, false
}

// isLocalAddress — адреса локальной сети: через сервер до них не дойти.
func isLocalAddress(a netip.Addr) bool {
	return a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLoopback()
}

func (d *nativeDevice) routePacket(packet []byte) packetRoute {
	dst, proto, l4, ok := packetInfo(packet)
	if !ok {
		return routeDrop
	}
	if dst.IsMulticast() || dst == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
		return routeDrop
	}
	if !d.router.Active() {
		if isLocalAddress(dst) {
			return routeDirect
		}
		return routeTunnel
	}

	if proto != protoTCP && proto != protoUDP || len(l4) < 4 {
		// Решение по адресу без памяти: у ICMP и фрагментов нет портов.
		return d.routeForAction(d.actionFor(dst), proto)
	}

	key := flowKey{
		dst:     dst.As16(),
		src:     uint16(l4[0])<<8 | uint16(l4[1]),
		dstPort: uint16(l4[2])<<8 | uint16(l4[3]),
		proto:   proto,
	}
	now := time.Now().Unix()
	fresh := proto == protoTCP && len(l4) >= 14 && l4[13]&(tcpFlagSYN|tcpFlagACK) == tcpFlagSYN
	if !fresh {
		if entry, found := d.flows[key]; found {
			if entry.seen != now {
				entry.seen = now
				d.flows[key] = entry
			}
			return entry.route
		}
	}

	route := d.routeForAction(d.actionFor(dst), proto)
	d.flows[key] = flowEntry{route: route, seen: now}
	if now-d.lastPurge >= 30 || len(d.flows) > flowLimit {
		d.purgeFlows(now)
	}
	return route
}

// routeForAction: заблокированный TCP уходит в прямой стек, чтобы приложение
// сразу получило отказ, а не ждало до таймаута.
func (d *nativeDevice) routeForAction(action Action, proto uint8) packetRoute {
	switch action {
	case ActionDirect:
		return routeDirect
	case ActionBlock:
		if proto == protoTCP {
			return routeDirect
		}
		return routeDrop
	}
	return routeTunnel
}

// actionFor решает по адресу: сначала имя из ответа DNS, затем правила. Локальная
// сеть всегда напрямую, если правила не сказали иного.
func (d *nativeDevice) actionFor(dst netip.Addr) Action {
	d.dnsMu.RLock()
	verdict, found := d.dns[dst]
	d.dnsMu.RUnlock()
	if found {
		return verdict.action
	}
	action := d.router.Resolve("", dst)
	if action == ActionProxy && isLocalAddress(dst) {
		return ActionDirect
	}
	return action
}

func (d *nativeDevice) purgeFlows(now int64) {
	d.lastPurge = now
	for key, entry := range d.flows {
		limit := int64(flowIdleUDP)
		if key.proto == protoTCP {
			limit = flowIdleTCP
		}
		if now-entry.seen > limit {
			delete(d.flows, key)
		}
	}
	// Разом открытых соединений столько не бывает: это мусор от сканеров и
	// тому подобного. Порядок обхода карты случаен, поэтому половина — наугад.
	if len(d.flows) > flowLimit {
		excess := len(d.flows) - flowLimit/2
		for key := range d.flows {
			if excess <= 0 {
				break
			}
			delete(d.flows, key)
			excess--
		}
	}
}

// markInternalPort запоминает порт, с которого говорит внутренний стек. Одного
// диапазона мало: приложение с постоянным портом могло бы в него попасть, и
// ответы ему ушли бы не туда.
func (d *nativeDevice) markInternalPort(packet []byte) {
	_, proto, l4, ok := packetInfo(packet)
	if !ok || len(l4) < 2 {
		return
	}
	port := uint16(l4[0])<<8 | uint16(l4[1])
	if port < internalPortMin || port > internalPortMax {
		return
	}
	index := int(port - internalPortMin)
	switch proto {
	case protoTCP:
		d.internalTCP[index/64].Or(1 << (index % 64))
	case protoUDP:
		d.internalUDP[index/64].Or(1 << (index % 64))
	}
}

func (d *nativeDevice) isInternalPacket(packet []byte) bool {
	_, proto, l4, ok := packetInfo(packet)
	if !ok || len(l4) < 4 {
		return false
	}
	port := uint16(l4[2])<<8 | uint16(l4[3])
	if port < internalPortMin || port > internalPortMax {
		return false
	}
	index := int(port - internalPortMin)
	switch proto {
	case protoTCP:
		return d.internalTCP[index/64].Load()&(1<<(index%64)) != 0
	case protoUDP:
		return d.internalUDP[index/64].Load()&(1<<(index%64)) != 0
	}
	return false
}

// MARK: - Подмена адреса

// translateOut заменяет адрес utun на адрес WireGuard. false — семейство адресов
// через этот туннель не идёт, пакет пропадает.
func (d *nativeDevice) translateOut(packet []byte) bool {
	switch packet[0] >> 4 {
	case 4:
		if d.has4 {
			rewriteIPv4(packet, 12, d.wg4)
		}
		return d.has4
	case 6:
		if d.has6 {
			rewriteIPv6(packet, 8, d.wg6)
		}
		return d.has6
	}
	return false
}

// translateIn — обратная замена для пакетов от сервера.
func (d *nativeDevice) translateIn(packet []byte) bool {
	switch packet[0] >> 4 {
	case 4:
		if len(packet) >= 20 && d.has4 {
			rewriteIPv4(packet, 16, d.tun4)
			return true
		}
	case 6:
		if len(packet) >= 40 && d.has6 {
			rewriteIPv6(packet, 24, d.tun6)
			return true
		}
	}
	return false
}

// rewriteIPv4 меняет адрес по смещению и правит контрольные суммы приращением
// (RFC 1624): пересчитывать их целиком на каждый пакет незачем. IP-заголовок
// считается всегда, TCP и UDP — потому что адрес входит в их псевдозаголовок.
func rewriteIPv4(packet []byte, offset int, address [4]byte) {
	headerLen := int(packet[0]&0x0f) * 4
	if headerLen < 20 || len(packet) < headerLen {
		return
	}
	old := packet[offset : offset+4]
	delta := checksumDelta(old, address[:])
	if delta == 0 {
		return
	}
	copy(old, address[:])
	applyChecksumDelta(packet[10:12], delta)

	// Фрагмент без заголовка TCP/UDP: его контрольная сумма в первом фрагменте.
	if uint16(packet[6]&0x1f)<<8|uint16(packet[7]) != 0 {
		return
	}
	adjustTransportChecksum(packet[9], packet[headerLen:], delta)
}

func rewriteIPv6(packet []byte, offset int, address [16]byte) {
	old := packet[offset : offset+16]
	delta := checksumDelta(old, address[:])
	if delta == 0 {
		return
	}
	copy(old, address[:])
	adjustTransportChecksum(packet[6], packet[40:], delta)
}

func adjustTransportChecksum(proto uint8, l4 []byte, delta uint32) {
	switch proto {
	case protoTCP:
		if len(l4) >= 18 {
			applyChecksumDelta(l4[16:18], delta)
		}
	case protoUDP:
		// Ноль в UDP значит «суммы нет», и его трогать нельзя. Результат, равный
		// нулю, по той же причине записывается как 0xFFFF.
		if len(l4) >= 8 && (l4[6] != 0 || l4[7] != 0) {
			applyChecksumDelta(l4[6:8], delta)
			if l4[6] == 0 && l4[7] == 0 {
				l4[6], l4[7] = 0xff, 0xff
			}
		}
	}
}

// checksumDelta — сумма ~старое + новое по 16-битным словам.
func checksumDelta(old, updated []byte) uint32 {
	var sum uint32
	for i := 0; i+1 < len(old); i += 2 {
		sum += uint32(^(uint16(old[i])<<8 | uint16(old[i+1])))
		sum += uint32(uint16(updated[i])<<8 | uint16(updated[i+1]))
	}
	return sum
}

func applyChecksumDelta(checksum []byte, delta uint32) {
	sum := uint32(^(uint16(checksum[0])<<8 | uint16(checksum[1]))) + delta
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	result := ^uint16(sum)
	checksum[0], checksum[1] = byte(result>>8), byte(result)
}

// MARK: - Домены по ответам DNS

// snoopDNS запоминает, какое правило сработает на адреса из ответа DNS.
//
// Решение о пути нужно уже на первом пакете соединения, когда имени в потоке ещё
// нет. Зато имя было в запросе DNS перед ним, а ответ проходит здесь. Заодно это
// покрывает QUIC, где имя из потока и прежний путь не доставал.
func (d *nativeDevice) snoopDNS(packet []byte) {
	if !d.domains {
		return
	}
	_, proto, l4, ok := packetInfo(packet)
	if !ok || proto != protoUDP || len(l4) < 8 || uint16(l4[0])<<8|uint16(l4[1]) != 53 {
		return
	}
	d.rememberDNSAnswer(l4[8:], time.Now().Unix())
}

func (d *nativeDevice) rememberDNSAnswer(message []byte, now int64) {
	var parser dnsmessage.Parser
	header, err := parser.Start(message)
	if err != nil || !header.Response || header.RCode != dnsmessage.RCodeSuccess {
		return
	}
	questions, err := parser.AllQuestions()
	if err != nil || len(questions) == 0 {
		return
	}
	action, matched := d.router.ResolveName(questions[0].Name.String())

	type answer struct {
		address netip.Addr
		ttl     uint32
	}
	var answers []answer
	for {
		resource, err := parser.Answer()
		if err != nil {
			break
		}
		switch body := resource.Body.(type) {
		case *dnsmessage.AResource:
			answers = append(answers, answer{netip.AddrFrom4(body.A), resource.Header.TTL})
		case *dnsmessage.AAAAResource:
			answers = append(answers, answer{netip.AddrFrom16(body.AAAA), resource.Header.TTL})
		case *dnsmessage.CNAMEResource:
			// Сайт на чужом CDN: правило может подойти к цели переадресации.
			if !matched {
				action, matched = d.router.ResolveName(body.CNAME.String())
			}
		}
	}
	if !matched || len(answers) == 0 {
		return
	}

	d.dnsMu.Lock()
	defer d.dnsMu.Unlock()
	for _, item := range answers {
		ttl := int64(item.ttl)
		if ttl < dnsVerdictMinTTL {
			ttl = dnsVerdictMinTTL
		} else if ttl > dnsVerdictMaxTTL {
			ttl = dnsVerdictMaxTTL
		}
		d.dns[item.address] = dnsVerdict{action: action, expires: now + ttl}
	}
	if len(d.dns) > dnsVerdictLimit {
		for address, verdict := range d.dns {
			if verdict.expires <= now {
				delete(d.dns, address)
			}
		}
		excess := len(d.dns) - dnsVerdictLimit/2
		for address := range d.dns {
			if excess <= 0 {
				break
			}
			delete(d.dns, address)
			excess--
		}
	}
}
