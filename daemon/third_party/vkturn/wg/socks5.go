package wg

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

// SOCKS5-сервер поверх сетевого стека WireGuard.
//
// Поддерживаются CONNECT и UDP ASSOCIATE — обе команды обязательны, и это не
// вопрос полноты. Мост tun2socks заводит в SOCKS5 весь трафик интерфейса, в
// том числе UDP. Без UDP ASSOCIATE молча отваливается ровно две вещи: QUIC
// (YouTube, Spotify и всё, что ходит по HTTP/3) и **DNS**, потому что
// расширение объявляет системе свои DNS-серверы и запросы к ним идут внутрь
// туннеля датаграммами. Снаружи это выглядит как «часть приложений работает,
// остальные пишут „нет интернета“»: живёт то, у кого адреса уже в кеше или
// зашиты, остальное не может разрешить имя.
//
// Вход слушается на петлевом интерфейсе внутри расширения туннеля, поэтому
// аутентификации нет — добраться до него снаружи невозможно.

const (
	socksVersion = 0x05

	cmdConnect      = 0x01
	cmdUDPAssociate = 0x03

	addrIPv4   = 0x01
	addrDomain = 0x03
	addrIPv6   = 0x04

	replySuccess             = 0x00
	replyGeneralFailure      = 0x01
	replyCommandNotSupported = 0x07

	// Срок на рукопожатие и разбор запроса.
	handshakeTimeout = 10 * time.Second

	// Сколько соединению разрешено провисеть без единого байта данных в любую
	// сторону, прежде чем оно считается брошенным и закрывается.
	//
	// Раньше срок снимался насовсем после рукопожатия, а обрывать соединение
	// был обязан клиент или сервер. На практике на переходе через TURN-релей
	// это не редкий случай, а обычный: пакет с FIN теряется, приложение на
	// телефоне уходит в фон посреди закачки, сервер перестаёт отвечать не
	// закрывая сокет. Ни одна из горутин `io.Copy` в relay() тогда не
	// завершалась никогда, а вместе с ней не освобождался и слот из
	// maxConcurrentConnections.
	//
	// Подтверждено дампом горутин на живом устройстве: за две минуты работы
	// накопилось 556 горутин, из них 247 блокированы на чтении из настоящего
	// сокета (internal/poll.runtime_pollWait) и 127 — на чтении из сетевого
	// стека (gonet.commonRead). Туннель рос в памяти, пока система не снимала
	// расширение как самый заметный процесс.
	defaultIdleTimeout = 90 * time.Second

	// Сколько ждать первую датаграмму от клиента. До неё неизвестно, куда
	// возвращать ответы, и держать связку бесконечно незачем.
	udpClientWait = 30 * time.Second

	// Потолок датаграммы. Больше в UDP всё равно не приходит.
	udpBufferSize = 65535

	// Сколько ждать первые байты клиента при поиске имени узла.
	sniffTimeout = 300 * time.Millisecond
)

// idleTimeout — переменная, а не константа: тестам нужен короткий срок,
// чтобы не ждать девяносто секунд ради проверки самого механизма.
var idleTimeout = defaultIdleTimeout

func serveSocks(stack stackNet, localAddr netip.Addr, router *Router, client net.Conn) {
	defer client.Close()

	// Паника в горутине уносит весь процесс расширения, а вместе с ним и
	// соединение пользователя. Одна кривая датаграмма или неожиданное
	// состояние стека не стоят разрыва туннеля — соединение просто
	// закрывается, остальные продолжают работать.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[SOCKS] Паника при обслуживании соединения: %v", r)
		}
	}()

	_ = client.SetDeadline(time.Now().Add(handshakeTimeout))

	if err := handshake(client); err != nil {
		return
	}

	command, address, err := readRequest(client)
	if err != nil {
		return
	}

	switch command {
	case cmdConnect:
		serveConnect(stack, router, client, address)
	case cmdUDPAssociate:
		serveUDPAssociate(stack, localAddr, client)
	default:
		_ = writeReply(client, replyCommandNotSupported, net.IPv4zero, 0)
	}
}

// MARK: - CONNECT

func serveConnect(stack stackNet, router *Router, client net.Conn, address string) {
	// Ответ отдаётся до чтения потока: клиент не пришлёт ни байта, пока не
	// узнает, что соединение принято, и ждать от него имя до ответа значит
	// встать насмерть.
	if err := writeReply(client, replySuccess, net.IPv4zero, 0); err != nil {
		return
	}
	_ = client.SetDeadline(time.Time{})

	head, domain := sniffConnection(client, router)

	host, _, _ := net.SplitHostPort(address)
	destination, _ := netip.ParseAddr(host)

	switch router.Resolve(domain, destination) {
	case ActionBlock:
		return
	case ActionDirect:
		// Мимо туннеля — обычным сокетом расширения. Собственный трафик
		// расширения в его же туннель не заворачивается, поэтому это
		// действительно прямой выход с устройства.
		relay(client, head, func() (net.Conn, error) {
			return net.DialTimeout("tcp", address, handshakeTimeout)
		})
	default:
		relay(client, head, func() (net.Conn, error) {
			ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
			defer cancel()
			return stack.DialContext(ctx, "tcp", address)
		})
	}
}

// sniffConnection читает начало потока и достаёт из него имя узла.
//
// Читать имеет смысл только когда правила вообще есть: иначе это лишняя
// задержка на каждом соединении ради значения, которое никто не спросит.
func sniffConnection(client net.Conn, router *Router) (head []byte, domain string) {
	if router == nil || len(router.rules) == 0 {
		return nil, ""
	}

	// Короткий срок: протоколы, где первым говорит сервер (SMTP, часть
	// игровых), имени всё равно не дадут, и ждать их нечего.
	_ = client.SetReadDeadline(time.Now().Add(sniffTimeout))
	buffer := make([]byte, sniffLimit)
	read, _ := client.Read(buffer)
	_ = client.SetReadDeadline(time.Time{})

	if read <= 0 {
		return nil, ""
	}
	head = buffer[:read]
	return head, sniffHost(head)
}

// relay открывает исходящее соединение и перекачивает данные в обе стороны.
//
// Прочитанное при сниффинге отправляется первым: для той стороны поток должен
// выглядеть нетронутым.
func relay(client net.Conn, head []byte, dial func() (net.Conn, error)) {
	remote, err := dial()
	if err != nil {
		return
	}
	defer remote.Close()

	if len(head) > 0 {
		if _, err := remote.Write(head); err != nil {
			return
		}
	}

	// Скользящий срок вместо общего: долгая, но живая передача — закачка,
	// поток видео — не обрывается по таймауту на всё соединение, обрывается
	// только та сторона, где данных действительно нет.
	_ = client.SetDeadline(time.Now().Add(idleTimeout))
	_ = remote.SetDeadline(time.Now().Add(idleTimeout))
	pumped := idleConn{client, idleTimeout}
	sunk := idleConn{remote, idleTimeout}

	// Кончилось одно направление — второе не рвём, а передаём полузакрытие
	// и ждём, пока закончится и оно. Клиент, отправивший запрос и закрывший
	// свою сторону на запись (так делают nc, часть HTTP-клиентов, git),
	// иначе не получал ответа: пересылка закрывала соединение целиком, едва
	// увидев конец запроса. Второе направление ограничено тем же сроком
	// простоя, поэтому соединение не повиснет.
	done := make(chan struct{}, 2)
	go func() { copyWithPooledBuffer(sunk, pumped); closeWrite(remote); done <- struct{}{} }()
	go func() { copyWithPooledBuffer(pumped, sunk); closeWrite(client); done <- struct{}{} }()
	<-done
	<-done
}

// closeWrite закрывает соединение на запись, если оно это умеет (TCP — и
// обычный, и из сетевого стека), иначе целиком. Обёртки пересылки
// разворачиваются: сами они полузакрытия не знают.
func closeWrite(c net.Conn) {
	for {
		if writer, ok := c.(interface{ CloseWrite() error }); ok {
			_ = writer.CloseWrite()
			return
		}
		switch wrapped := c.(type) {
		case idleConn:
			c = wrapped.Conn
		case sessionConn:
			c = wrapped.Conn
		default:
			_ = c.Close()
			return
		}
	}
}

// Размер буфера копирования на одно направление.
//
// io.Copy берёт по 32 КБ на направление, то есть 64 КБ на соединение на всё
// время его жизни — при сотне соединений это шесть с лишним мегабайт из
// пятидесяти, отведённых расширению. Шестнадцати килобайт хватает с запасом:
// пакет WireGuard меньше полутора, и буфер больше лишь реже будит горутину.
const copyBufferSize = 16 * 1024

var copyBuffers = sync.Pool{
	New: func() any {
		buffer := make([]byte, copyBufferSize)
		return &buffer
	},
}

func copyWithPooledBuffer(dst io.Writer, src io.Reader) {
	buffer := copyBuffers.Get().(*[]byte)
	defer copyBuffers.Put(buffer)
	_, _ = io.CopyBuffer(dst, src, *buffer)
}

// idleConn продлевает срок соединения при каждом успешном чтении или записи.
//
// `Read` вызывается из одной горутины копирования, `Write` — из другой:
// `net.Conn` рассчитан на вызовы из разных горутин одновременно, в том числе
// `SetDeadline`, поэтому гонки здесь нет.
type idleConn struct {
	net.Conn
	timeout time.Duration
}

func (c idleConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err == nil {
		_ = c.Conn.SetDeadline(time.Now().Add(c.timeout))
	}
	return n, err
}

func (c idleConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if err == nil {
		_ = c.Conn.SetDeadline(time.Now().Add(c.timeout))
	}
	return n, err
}

// MARK: - UDP ASSOCIATE

// serveUDPAssociate поднимает связку «локальный порт ↔ сокет в стеке».
//
// Сокет в стеке один на всю связку и работает как NAT: адрес назначения
// берётся из заголовка каждой датаграммы, ответы возвращаются оттуда же.
// Отдельный сокет на каждое назначение означал бы сотни сокетов на один
// браузерный сеанс QUIC.
func serveUDPAssociate(stack stackNet, localAddr netip.Addr, control net.Conn) {
	local, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		_ = writeReply(control, replyGeneralFailure, net.IPv4zero, 0)
		return
	}
	defer local.Close()

	// Адрес обязателен. С нулевым `netip.AddrPort` номер сетевого протокола
	// остаётся нулевым, а такого в gVisor не существует: вызов не возвращает
	// ошибку, а паникует — и уносит расширение целиком.
	remote, err := stack.ListenUDPAddrPort(netip.AddrPortFrom(localAddr, 0))
	if err != nil {
		_ = writeReply(control, replyGeneralFailure, net.IPv4zero, 0)
		return
	}
	defer remote.Close()

	bound := local.LocalAddr().(*net.UDPAddr)
	if err := writeReply(control, replySuccess, bound.IP, bound.Port); err != nil {
		return
	}
	_ = control.SetDeadline(time.Time{})

	// Датаграммы идут мимо управляющего соединения, поэтому активность
	// связки отмечают сами циклы пересылки — иначе живая связка выглядела бы
	// молчащей и вытеснялась бы первой.
	touch := func() {}
	if toucher, ok := control.(activityToucher); ok {
		touch = toucher.touch
	}

	// Адрес клиента известен только с первого пакета: сначала он приходит
	// сюда, и лишь потом есть куда возвращать ответы.
	clientAddr := make(chan *net.UDPAddr, 1)
	go relayFromClient(stack, local, remote, clientAddr, touch)
	go relayToClient(local, remote, clientAddr, touch)

	// Связка живёт, пока открыто управляющее соединение: так её закрывает
	// сам мост, и висящих сокетов после отключения не остаётся.
	buffer := make([]byte, 1)
	for {
		if _, err := control.Read(buffer); err != nil {
			return
		}
	}
}

func relayFromClient(
	stack stackNet,
	local *net.UDPConn,
	remote net.PacketConn,
	clientAddr chan<- *net.UDPAddr,
	touch func(),
) {
	buffer := make([]byte, udpBufferSize)
	announced := false

	for {
		read, from, err := local.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		touch()
		if !announced {
			clientAddr <- from
			announced = true
		}

		payload, target, err := parseUDPRequest(buffer[:read])
		if err != nil {
			continue
		}
		destination, err := resolveUDPTarget(stack, target)
		if err != nil {
			// Не обрываем связку: не разрешилось одно имя, остальные датаграммы
			// этой же связки могут идти по адресам и работать.
			continue
		}
		if _, err := remote.WriteTo(payload, destination); err != nil {
			return
		}
	}
}

func relayToClient(
	local *net.UDPConn,
	remote net.PacketConn,
	clientAddr <-chan *net.UDPAddr,
	touch func(),
) {
	var destination *net.UDPAddr
	select {
	case destination = <-clientAddr:
	case <-time.After(udpClientWait):
		return
	}

	buffer := make([]byte, udpBufferSize)
	for {
		read, from, err := remote.ReadFrom(buffer)
		if err != nil {
			return
		}
		touch()
		packet, err := buildUDPReply(buffer[:read], from)
		if err != nil {
			continue
		}
		if _, err := local.WriteToUDP(packet, destination); err != nil {
			return
		}
	}
}

// resolveUDPTarget превращает адрес из заголовка в адрес сетевого стека.
//
// Имя разрешается силами стека — то есть DNS-сервером, который назвал сервер
// WireGuard, а не системным резолвером устройства. Иначе запрос ушёл бы мимо
// туннеля.
func resolveUDPTarget(stack stackNet, target string) (net.Addr, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		return nil, err
	}

	if address, err := netip.ParseAddr(host); err == nil {
		return net.UDPAddrFromAddrPort(netip.AddrPortFrom(address, uint16(number))), nil
	}

	addresses, err := stack.LookupHost(host)
	if err != nil || len(addresses) == 0 {
		return nil, fmt.Errorf("socks: имя %q не разрешилось: %w", host, err)
	}
	address, err := netip.ParseAddr(addresses[0])
	if err != nil {
		return nil, err
	}
	return net.UDPAddrFromAddrPort(netip.AddrPortFrom(address, uint16(number))), nil
}

// Заголовок датаграммы SOCKS5: RSV(2) FRAG(1) ATYP ADDR PORT DATA.
func parseUDPRequest(packet []byte) ([]byte, string, error) {
	if len(packet) < 5 {
		return nil, "", fmt.Errorf("socks: датаграмма короче заголовка")
	}
	// Сборка фрагментов не поддерживается — её не использует ни один
	// известный клиент, а полумера здесь опаснее отказа.
	if packet[2] != 0x00 {
		return nil, "", fmt.Errorf("socks: фрагментированные датаграммы не поддерживаются")
	}

	reader := newByteReader(packet[4:])
	host, err := readHost(reader, packet[3])
	if err != nil {
		return nil, "", err
	}

	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(reader, portBytes); err != nil {
		return nil, "", err
	}
	port := binary.BigEndian.Uint16(portBytes)

	return packet[len(packet)-reader.remaining():], net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func buildUDPReply(payload []byte, from net.Addr) ([]byte, error) {
	address, ok := from.(*net.UDPAddr)
	if !ok {
		return nil, fmt.Errorf("socks: неизвестный тип адреса отправителя")
	}

	packet := []byte{0x00, 0x00, 0x00}
	if ipv4 := address.IP.To4(); ipv4 != nil {
		packet = append(packet, addrIPv4)
		packet = append(packet, ipv4...)
	} else {
		packet = append(packet, addrIPv6)
		packet = append(packet, address.IP.To16()...)
	}
	packet = append(packet, byte(address.Port>>8), byte(address.Port))
	return append(packet, payload...), nil
}

// MARK: - Разбор протокола

func handshake(conn net.Conn) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[0] != socksVersion {
		return fmt.Errorf("socks: версия %d", header[0])
	}
	methods := make([]byte, header[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}
	// Второй байт — выбранный метод: 0x00, то есть без аутентификации.
	_, err := conn.Write([]byte{socksVersion, 0x00})
	return err
}

func readRequest(conn net.Conn) (command byte, address string, err error) {
	header := make([]byte, 4)
	if _, err = io.ReadFull(conn, header); err != nil {
		return 0, "", err
	}
	if header[0] != socksVersion {
		return 0, "", fmt.Errorf("socks: версия %d", header[0])
	}

	host, err := readHost(conn, header[3])
	if err != nil {
		return 0, "", err
	}

	portBytes := make([]byte, 2)
	if _, err = io.ReadFull(conn, portBytes); err != nil {
		return 0, "", err
	}
	port := binary.BigEndian.Uint16(portBytes)

	return header[1], net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func readHost(reader io.Reader, kind byte) (string, error) {
	switch kind {
	case addrIPv4:
		buf := make([]byte, 4)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return "", err
		}
		return net.IP(buf).String(), nil
	case addrIPv6:
		buf := make([]byte, 16)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return "", err
		}
		return net.IP(buf).String(), nil
	case addrDomain:
		length := make([]byte, 1)
		if _, err := io.ReadFull(reader, length); err != nil {
			return "", err
		}
		name := make([]byte, length[0])
		if _, err := io.ReadFull(reader, name); err != nil {
			return "", err
		}
		return string(name), nil
	default:
		return "", fmt.Errorf("socks: тип адреса %d", kind)
	}
}

// writeReply отвечает клиенту кодом результата и адресом привязки.
//
// Для CONNECT адрес клиент игнорирует, а для UDP ASSOCIATE это тот порт, на
// который он будет слать датаграммы, — там нулями не обойтись.
func writeReply(conn net.Conn, code byte, ip net.IP, port int) error {
	packet := []byte{socksVersion, code, 0x00}
	if ipv4 := ip.To4(); ipv4 != nil {
		packet = append(packet, addrIPv4)
		packet = append(packet, ipv4...)
	} else if ip != nil && len(ip) == net.IPv6len {
		packet = append(packet, addrIPv6)
		packet = append(packet, ip...)
	} else {
		packet = append(packet, addrIPv4, 0, 0, 0, 0)
	}
	packet = append(packet, byte(port>>8), byte(port))
	_, err := conn.Write(packet)
	return err
}

// byteReader — чтение из среза с подсчётом остатка.
//
// Нужен, чтобы после разбора заголовка датаграммы отделить полезную нагрузку:
// длина заголовка заранее неизвестна, она зависит от типа адреса.
type byteReader struct {
	data   []byte
	offset int
}

func newByteReader(data []byte) *byteReader {
	return &byteReader{data: data}
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}

func (r *byteReader) remaining() int {
	return len(r.data) - r.offset
}
