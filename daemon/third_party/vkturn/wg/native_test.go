//go:build darwin || linux || windows

package wg

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/conn/bindtest"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// MARK: - Контрольные суммы

// checksum — полный расчёт по RFC 1071: им проверяется расчёт приращением.
func checksum(parts ...[]byte) uint16 {
	var sum uint32
	for _, part := range parts {
		for i := 0; i+1 < len(part); i += 2 {
			sum += uint32(part[i])<<8 | uint32(part[i+1])
		}
		if len(part)%2 == 1 {
			sum += uint32(part[len(part)-1]) << 8
		}
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

func buildIPv4(proto uint8, src, dst [4]byte, l4 []byte) []byte {
	packet := make([]byte, 20+len(l4))
	packet[0] = 0x45
	packet[2], packet[3] = byte(len(packet)>>8), byte(len(packet))
	packet[8] = 64
	packet[9] = proto
	copy(packet[12:], src[:])
	copy(packet[16:], dst[:])
	copy(packet[20:], l4)
	c := checksum(packet[:20])
	packet[10], packet[11] = byte(c>>8), byte(c)

	checksumOffset := map[uint8]int{protoTCP: 16, protoUDP: 6}[proto]
	if checksumOffset > 0 {
		pseudo := append(append(append([]byte{}, src[:]...), dst[:]...), 0, proto, byte(len(l4)>>8), byte(len(l4)))
		packet[20+checksumOffset], packet[21+checksumOffset] = 0, 0
		c = checksum(pseudo, packet[20:])
		packet[20+checksumOffset], packet[21+checksumOffset] = byte(c>>8), byte(c)
	}
	return packet
}

func assertValidIPv4(t *testing.T, packet []byte, proto uint8) {
	t.Helper()
	if checksum(packet[:20]) != 0 {
		t.Fatalf("сумма IP неверна: % x", packet[:20])
	}
	l4 := packet[20:]
	pseudo := append(append([]byte{}, packet[12:20]...), 0, proto, byte(len(l4)>>8), byte(len(l4)))
	if checksum(pseudo, l4) != 0 {
		t.Fatalf("сумма протокола %d неверна", proto)
	}
}

func TestRewriteKeepsChecksumsValid(t *testing.T) {
	from, to := [4]byte{198, 18, 0, 1}, [4]byte{10, 83, 7, 200}
	dst := [4]byte{8, 8, 8, 8}
	for name, proto := range map[string]uint8{"tcp": protoTCP, "udp": protoUDP} {
		t.Run(name, func(t *testing.T) {
			l4 := make([]byte, 41) // нечётная длина — проверка заодно
			for i := range l4 {
				l4[i] = byte(i * 7)
			}
			l4[0], l4[1] = 0xc0, 0x01
			l4[2], l4[3] = 0x01, 0xbb
			if proto == protoUDP {
				l4[4], l4[5] = 0, byte(len(l4))
			} else {
				l4[12] = 0x50
			}
			packet := buildIPv4(proto, from, dst, l4)
			assertValidIPv4(t, packet, proto)
			rewriteIPv4(packet, 12, to)
			if [4]byte(packet[12:16]) != to {
				t.Fatalf("адрес не заменён")
			}
			assertValidIPv4(t, packet, proto)
			rewriteIPv4(packet, 12, from)
			assertValidIPv4(t, packet, proto)
		})
	}
}

// Ноль в UDP значит «суммы нет», и подмена адреса не должна его трогать.
func TestRewriteLeavesAbsentUDPChecksum(t *testing.T) {
	l4 := make([]byte, 16)
	packet := buildIPv4(protoICMP, [4]byte{198, 18, 0, 1}, [4]byte{8, 8, 8, 8}, l4)
	packet[9] = protoUDP
	c := checksum(append(append([]byte{}, packet[:10]...), append([]byte{0, 0}, packet[12:20]...)...))
	packet[10], packet[11] = byte(c>>8), byte(c)
	rewriteIPv4(packet, 12, [4]byte{10, 0, 0, 2})
	if packet[26] != 0 || packet[27] != 0 {
		t.Fatalf("сумма UDP появилась из ниоткуда: % x", packet[26:28])
	}
	if checksum(packet[:20]) != 0 {
		t.Fatalf("сумма IP неверна")
	}
}

// Фрагмент без заголовка транспорта: трогать можно только IP.
func TestRewriteSkipsTransportInLaterFragments(t *testing.T) {
	payload := make([]byte, 32)
	for i := range payload {
		payload[i] = 0xAB
	}
	packet := buildIPv4(protoICMP, [4]byte{198, 18, 0, 1}, [4]byte{8, 8, 8, 8}, payload)
	packet[9] = protoUDP
	packet[6], packet[7] = 0, 8 // смещение фрагмента
	before := append([]byte{}, packet[20:]...)
	rewriteIPv4(packet, 12, [4]byte{10, 0, 0, 2})
	for i := range before {
		if before[i] != packet[20+i] {
			t.Fatalf("данные фрагмента изменены")
		}
	}
}

// MARK: - Выбор пути

func rawTCP(dst [4]byte, srcPort uint16, flags byte) []byte {
	l4 := make([]byte, 20)
	l4[0], l4[1] = byte(srcPort>>8), byte(srcPort)
	l4[2], l4[3] = 0x01, 0xbb
	l4[12] = 0x50
	l4[13] = flags
	return buildIPv4(protoTCP, [4]byte{198, 18, 0, 1}, dst, l4)
}

func rawUDP(dst [4]byte, srcPort uint16) []byte {
	l4 := make([]byte, 8)
	l4[0], l4[1] = byte(srcPort>>8), byte(srcPort)
	l4[2], l4[3] = 0x01, 0xbb
	l4[5] = 8
	return buildIPv4(protoUDP, [4]byte{198, 18, 0, 1}, dst, l4)
}

func testDevice(router *Router) *nativeDevice {
	return &nativeDevice{
		router:  router,
		domains: router.HasDomainRules(),
		flows:   make(map[flowKey]flowEntry),
		dns:     make(map[netip.Addr]dnsVerdict),
	}
}

func TestRoutePacket(t *testing.T) {
	router := NewRouter([]Rule{
		{Action: ActionBlock, IPRanges: []string{"203.0.113.0/24"}},
		{Action: ActionDirect, IPRanges: []string{"95.163.0.0/16"}},
	})
	d := testDevice(router)
	cases := []struct {
		name   string
		packet []byte
		want   packetRoute
	}{
		{"чужой адрес — в туннель", rawTCP([4]byte{8, 8, 8, 8}, 50000, tcpFlagSYN), routeTunnel},
		{"адрес из правила — напрямую", rawTCP([4]byte{95, 163, 1, 1}, 50001, tcpFlagSYN), routeDirect},
		{"UDP подчиняется тем же правилам", rawUDP([4]byte{95, 163, 1, 1}, 50002), routeDirect},
		{"блокировка TCP — через стек, чтобы был сброс", rawTCP([4]byte{203, 0, 113, 1}, 50003, tcpFlagSYN), routeDirect},
		{"блокировка UDP — тишина", rawUDP([4]byte{203, 0, 113, 1}, 50004), routeDrop},
		{"локальная сеть — напрямую", rawTCP([4]byte{192, 168, 1, 5}, 50005, tcpFlagSYN), routeDirect},
		{"рассылка не уходит никуда", rawUDP([4]byte{224, 0, 0, 251}, 5353), routeDrop},
	}
	for _, c := range cases {
		if got := d.routePacket(c.packet); got != c.want {
			t.Errorf("%s: %d, ожидалось %d", c.name, got, c.want)
		}
	}

	// Без правил — всё в туннель, кроме локальной сети.
	plain := testDevice(NewRouter(nil))
	if got := plain.routePacket(rawTCP([4]byte{95, 163, 1, 1}, 1, tcpFlagSYN)); got != routeTunnel {
		t.Errorf("без правил адрес должен идти в туннель: %d", got)
	}
	if got := plain.routePacket(rawTCP([4]byte{10, 0, 0, 1}, 1, tcpFlagSYN)); got != routeDirect {
		t.Errorf("без правил локальная сеть должна идти напрямую: %d", got)
	}
}

func dnsAnswer(t *testing.T, name string, ttl uint32, cname string, address [4]byte) []byte {
	t.Helper()
	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true, RCode: dnsmessage.RCodeSuccess})
	question := dnsmessage.MustNewName(name)
	if err := builder.StartQuestions(); err != nil {
		t.Fatal(err)
	}
	if err := builder.Question(dnsmessage.Question{Name: question, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}); err != nil {
		t.Fatal(err)
	}
	if err := builder.StartAnswers(); err != nil {
		t.Fatal(err)
	}
	owner := question
	if cname != "" {
		target := dnsmessage.MustNewName(cname)
		header := dnsmessage.ResourceHeader{Name: owner, Class: dnsmessage.ClassINET, TTL: ttl}
		if err := builder.CNAMEResource(header, dnsmessage.CNAMEResource{CNAME: target}); err != nil {
			t.Fatal(err)
		}
		owner = target
	}
	header := dnsmessage.ResourceHeader{Name: owner, Class: dnsmessage.ClassINET, TTL: ttl}
	if err := builder.AResource(header, dnsmessage.AResource{A: address}); err != nil {
		t.Fatal(err)
	}
	message, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return message
}

// Правила по именам: адрес попадает под правило из ответа DNS. Решение по уже
// идущему соединению при этом не меняется, иначе поток оборвался бы.
func TestDomainRulesWorkFromDNSAnswers(t *testing.T) {
	router := NewRouter([]Rule{{Action: ActionDirect, Domains: []string{"domain:example.ru"}}})
	d := testDevice(router)
	address := [4]byte{93, 184, 216, 34}

	established := rawTCP(address, 50000, tcpFlagSYN)
	if got := d.routePacket(established); got != routeTunnel {
		t.Fatalf("до ответа DNS адрес ничем не отличается: %d", got)
	}

	l4 := make([]byte, 8)
	message := dnsAnswer(t, "www.example.ru.", 60, "", address)
	l4 = append(l4, message...)
	l4[0], l4[1] = 0, 53
	l4[4], l4[5] = byte(len(l4)>>8), byte(len(l4))
	d.snoopDNS(buildIPv4(protoUDP, [4]byte{1, 1, 1, 1}, [4]byte{10, 0, 0, 2}, l4))

	if got := d.routePacket(rawTCP(address, 50000, tcpFlagACK)); got != routeTunnel {
		t.Fatalf("идущее соединение не должно менять путь: %d", got)
	}
	if got := d.routePacket(rawTCP(address, 50001, tcpFlagSYN)); got != routeDirect {
		t.Fatalf("новое соединение должно идти напрямую: %d", got)
	}
	// QUIC тоже: имя из потока ему не достать, а ответ DNS — пожалуйста.
	if got := d.routePacket(rawUDP(address, 50002)); got != routeDirect {
		t.Fatalf("QUIC должен идти напрямую: %d", got)
	}

	// Правило подходит к цели переадресации: сайт на чужом CDN.
	other := [4]byte{104, 16, 0, 1}
	d.rememberDNSAnswer(dnsAnswer(t, "cdn.other.com.", 60, "edge.example.ru.", other), time.Now().Unix())
	if got := d.routePacket(rawTCP(other, 50003, tcpFlagSYN)); got != routeDirect {
		t.Fatalf("имя из CNAME должно попасть под правило: %d", got)
	}

	// Посторонний ответ ничего не добавляет.
	alien := [4]byte{1, 2, 3, 4}
	d.rememberDNSAnswer(dnsAnswer(t, "foreign.example.com.", 60, "", alien), time.Now().Unix())
	if got := d.routePacket(rawTCP(alien, 50004, tcpFlagSYN)); got != routeTunnel {
		t.Fatalf("имя без правила должно идти в туннель: %d", got)
	}
}

// Пакет внутренний, только если этот порт занял сам внутренний стек: приложение с
// постоянным портом из того же диапазона получает свои ответы.
func TestInternalPacketDetection(t *testing.T) {
	d := testDevice(NewRouter(nil))
	port := uint16(internalPortMin + 777)
	reply := make([]byte, 20)
	reply[2], reply[3] = byte(port>>8), byte(port)
	packet := buildIPv4(protoTCP, [4]byte{1, 1, 1, 1}, [4]byte{10, 0, 0, 2}, reply)
	if d.isInternalPacket(packet) {
		t.Fatal("порт из диапазона, но его никто не занимал")
	}
	outgoing := make([]byte, 20)
	outgoing[0], outgoing[1] = byte(port>>8), byte(port)
	d.markInternalPort(buildIPv4(protoTCP, [4]byte{10, 0, 0, 2}, [4]byte{1, 1, 1, 1}, outgoing))
	if !d.isInternalPacket(packet) {
		t.Fatal("порт внутреннего стека не опознан")
	}
	udp := buildIPv4(protoUDP, [4]byte{1, 1, 1, 1}, [4]byte{10, 0, 0, 2}, reply)
	if d.isInternalPacket(udp) {
		t.Fatal("TCP и UDP считаются отдельно")
	}
	reply[2], reply[3] = 0xc0, 0x01
	if d.isInternalPacket(buildIPv4(protoTCP, [4]byte{1, 1, 1, 1}, [4]byte{10, 0, 0, 2}, reply)) {
		t.Fatal("порт приложения принят за внутренний")
	}
}

// MARK: - Весь путь целиком

// phoneTun изображает utun: пакеты идут от стека gVisor, который играет роль
// ядра iOS со всеми его приложениями. Поднимать настоящие туннели на компьютере
// нельзя, а проверить надо именно то, что настоящий TCP с обеих сторон принимает
// переписанные пакеты.
type phoneTun struct {
	inner tun.Device
	in    chan []byte
	wake  chan struct{}
	stop  chan struct{}
	once  sync.Once
}

func newPhoneTun(inner tun.Device) *phoneTun {
	p := &phoneTun{inner: inner, in: make(chan []byte, 256), wake: make(chan struct{}, 1), stop: make(chan struct{})}
	go func() {
		buffer := make([]byte, 2000)
		bufs := [][]byte{buffer}
		sizes := []int{0}
		for {
			count, err := inner.Read(bufs, sizes, 0)
			if err != nil {
				return
			}
			if count > 0 {
				select {
				case p.in <- append([]byte(nil), buffer[:sizes[0]]...):
				case <-p.stop:
					return
				}
			}
		}
	}()
	return p
}

func (p *phoneTun) WakeRead() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}
func (p *phoneTun) File() *os.File { return nil }
func (p *phoneTun) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	select {
	case packet := <-p.in:
		sizes[0] = copy(bufs[0][offset:], packet)
		return 1, nil
	case <-p.wake:
		return 0, os.ErrDeadlineExceeded
	case <-p.stop:
		return 0, os.ErrClosed
	}
}
func (p *phoneTun) Write(bufs [][]byte, offset int) (int, error) { return p.inner.Write(bufs, offset) }
func (p *phoneTun) MTU() (int, error)                            { return 1280, nil }
func (p *phoneTun) Name() (string, error)                        { return "phone", nil }
func (p *phoneTun) Events() <-chan tun.Event                     { return nil }
func (p *phoneTun) BatchSize() int                               { return 1 }
func (p *phoneTun) Close() error {
	p.once.Do(func() { close(p.stop) })
	return nil
}

func newKey(t *testing.T) (private, public []byte) {
	t.Helper()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key.Bytes(), key.PublicKey().Bytes()
}

type nativeWorld struct {
	tunnel *Tunnel
	phone  *netstack.Net
	server *netstack.Net
	dev    *nativeDevice
}

func startNativeWorld(t *testing.T, router *Router) nativeWorld {
	t.Helper()
	wgAddress := netip.MustParseAddr("10.80.0.2")
	serverAddress := netip.MustParseAddr("198.51.100.1")
	phoneAddress := netip.MustParseAddr("198.18.0.1")
	dns := netip.MustParseAddr("10.80.0.1")

	clientPrivate, clientPublic := newKey(t)
	serverPrivate, serverPublic := newKey(t)
	binds := bindtest.NewChannelBinds()

	serverTun, serverNet, err := netstack.CreateNetTUN([]netip.Addr{serverAddress}, []netip.Addr{dns}, 1280)
	if err != nil {
		t.Fatal(err)
	}
	serverDevice := device.NewDevice(serverTun, binds[1], device.NewLogger(device.LogLevelSilent, ""))
	if err := serverDevice.IpcSet(fmt.Sprintf(
		"private_key=%s\npublic_key=%s\nallowed_ip=10.80.0.2/32\nendpoint=127.0.0.1:2\n",
		hex.EncodeToString(serverPrivate), hex.EncodeToString(clientPublic),
	)); err != nil {
		t.Fatal(err)
	}
	if err := serverDevice.Up(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(serverDevice.Close)

	phoneInner, phoneNet, err := netstack.CreateNetTUN([]netip.Addr{phoneAddress}, []netip.Addr{dns}, 1280)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	socksPort := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	tunnel, err := startNativeOn(newPhoneTun(phoneInner), Config{
		PrivateKey:    base64.StdEncoding.EncodeToString(clientPrivate),
		Addresses:     []netip.Prefix{netip.PrefixFrom(wgAddress, 32)},
		DNS:           []netip.Addr{dns},
		MTU:           1280,
		PeerPublicKey: base64.StdEncoding.EncodeToString(serverPublic),
		AllowedIPs:    []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")},
	}, netip.MustParseAddrPort("127.0.0.1:1"), conn.Bind(binds[0]), NativeOptions{
		TunAddresses: []netip.Addr{phoneAddress},
		SocksPort:    socksPort,
		Router:       router,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tunnel.Close)
	return nativeWorld{tunnel: tunnel, phone: phoneNet, server: serverNet, dev: tunnel.native}
}

func exchange(t *testing.T, c net.Conn, size int) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = c.Write(payload) }()
	echoed := make([]byte, size)
	if _, err := io.ReadFull(c, echoed); err != nil {
		t.Fatalf("эхо: %v", err)
	}
	for i := range echoed {
		if echoed[i] != payload[i] {
			t.Fatalf("данные искажены на байте %d", i)
		}
	}
}

func echoLoop(listener net.Listener) {
	for {
		c, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			_, _ = io.Copy(c, c)
		}()
	}
}

// Настоящий TCP телефона через прямой путь и WireGuard до настоящего TCP сервера.
func TestNativeTCPThroughTunnel(t *testing.T) {
	world := startNativeWorld(t, NewRouter(nil))
	listener, err := world.server.ListenTCP(&net.TCPAddr{IP: net.IPv4zero, Port: 8080})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go echoLoop(listener)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := world.phone.DialContext(ctx, "tcp", "198.51.100.1:8080")
	if err != nil {
		t.Fatalf("соединение через туннель: %v", err)
	}
	defer c.Close()
	exchange(t, c, 256*1024)

	up, down, ok := world.tunnel.NativeTraffic()
	if !ok || up == 0 || down == 0 {
		t.Fatalf("счётчики трафика: %d/%d/%v", up, down, ok)
	}
}

// Внутренний SOCKS5 (проверка живости, пульс) идёт через туннель, а его пакеты
// подмешиваются к пакетам utun и не мешают им.
func TestNativeInternalStackReachesServer(t *testing.T) {
	world := startNativeWorld(t, NewRouter(nil))
	listener, err := world.server.ListenTCP(&net.TCPAddr{IP: net.IPv4zero, Port: 8081})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go echoLoop(listener)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := world.tunnel.stack.DialContext(ctx, "tcp", "198.51.100.1:8081")
	if err != nil {
		t.Fatalf("внутренний стек: %v", err)
	}
	defer c.Close()
	port := c.LocalAddr().(*net.TCPAddr).Port
	if port < internalPortMin || port > internalPortMax {
		t.Fatalf("порт внутреннего стека %d вне своего диапазона", port)
	}
	exchange(t, c, 64*1024)
}

// Соединение «напрямую» заканчивается в стеке и уходит настоящим сокетом.
func TestNativeDirectConnection(t *testing.T) {
	realServer, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer realServer.Close()
	go echoLoop(realServer)

	dialed := make(chan string, 1)
	original := dialDirect
	dialDirect = func(ctx context.Context, network, target string) (net.Conn, error) {
		dialed <- target
		return original(ctx, network, realServer.Addr().String())
	}
	defer func() { dialDirect = original }()

	router := NewRouter([]Rule{
		{Action: ActionDirect, IPRanges: []string{"203.0.113.7/32"}},
		{Action: ActionBlock, IPRanges: []string{"203.0.113.8/32"}},
	})
	world := startNativeWorld(t, router)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := world.phone.DialContext(ctx, "tcp", "203.0.113.7:443")
	if err != nil {
		t.Fatalf("прямое соединение: %v", err)
	}
	defer c.Close()
	if target := <-dialed; target != "203.0.113.7:443" {
		t.Fatalf("сокет открыт к %s", target)
	}
	exchange(t, c, 256*1024)
	if world.dev.direct.Load() == nil {
		t.Fatal("стек прямых соединений не создан")
	}

	// Заблокированное получает отказ сразу, а не ждёт таймаута.
	started := time.Now()
	if blocked, err := world.phone.DialContext(ctx, "tcp", "203.0.113.8:443"); err == nil {
		blocked.Close()
		t.Fatal("заблокированный адрес принял соединение")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("отказ занял %s", elapsed)
	}
}
