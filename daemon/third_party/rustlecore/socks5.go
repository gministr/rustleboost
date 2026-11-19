package rustlecore

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/apernet/hysteria/core/v2/client"

	"rustleboost/vkturn/wg"
)

// Минимальный SOCKS5-сервер поверх клиента Hysteria2.
//
// Готовый сервер из состава hysteria лежит во внутреннем пакете и снаружи
// недоступен, а полноценные библиотеки тянут собственную модель соединений.
// Здесь ровно то, что нужно мосту: CONNECT и UDP ASSOCIATE без аутентификации,
// вход слушается только на петлевом интерфейсе.

const (
	socksVersion = 0x05

	cmdConnect      = 0x01
	cmdUDPAssociate = 0x03

	addrIPv4   = 0x01
	addrDomain = 0x03
	addrIPv6   = 0x04

	replySuccess          = 0x00
	replyGeneralFailure   = 0x01
	replyCommandNotSupported = 0x07
)

func handleSocks(state *instance, conn net.Conn) {
	defer conn.Close()

	if err := socksHandshake(conn); err != nil {
		return
	}

	command, address, err := readRequest(conn)
	if err != nil {
		return
	}

	switch command {
	case cmdConnect:
		serveConnect(state, conn, address)
	case cmdUDPAssociate:
		serveUDPAssociate(state, conn)
	default:
		_ = writeReply(conn, replyCommandNotSupported, net.IPv4zero, 0)
	}
}

// Рукопожатие: принимаем только режим без аутентификации — вход локальный.
func socksHandshake(conn net.Conn) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[0] != socksVersion {
		return errors.New("socks5: чужая версия")
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}
	_, err := conn.Write([]byte{socksVersion, 0x00})
	return err
}

func readRequest(conn net.Conn) (command byte, address string, err error) {
	header := make([]byte, 4)
	if _, err = io.ReadFull(conn, header); err != nil {
		return
	}
	if header[0] != socksVersion {
		err = errors.New("socks5: чужая версия запроса")
		return
	}
	command = header[1]
	address, err = readAddress(conn, header[3])
	return
}

func readAddress(reader io.Reader, kind byte) (string, error) {
	var host string
	switch kind {
	case addrIPv4:
		buffer := make([]byte, 4)
		if _, err := io.ReadFull(reader, buffer); err != nil {
			return "", err
		}
		host = net.IP(buffer).String()
	case addrIPv6:
		buffer := make([]byte, 16)
		if _, err := io.ReadFull(reader, buffer); err != nil {
			return "", err
		}
		host = net.IP(buffer).String()
	case addrDomain:
		length := make([]byte, 1)
		if _, err := io.ReadFull(reader, length); err != nil {
			return "", err
		}
		buffer := make([]byte, int(length[0]))
		if _, err := io.ReadFull(reader, buffer); err != nil {
			return "", err
		}
		host = string(buffer)
	default:
		return "", errors.New("socks5: неизвестный тип адреса")
	}

	port := make([]byte, 2)
	if _, err := io.ReadFull(reader, port); err != nil {
		return "", err
	}
	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port)))), nil
}

func writeReply(conn net.Conn, code byte, ip net.IP, port int) error {
	reply := []byte{socksVersion, code, 0x00}
	if v4 := ip.To4(); v4 != nil {
		reply = append(reply, addrIPv4)
		reply = append(reply, v4...)
	} else {
		reply = append(reply, addrIPv6)
		reply = append(reply, ip.To16()...)
	}
	reply = append(reply, byte(port>>8), byte(port))
	_, err := conn.Write(reply)
	return err
}

// MARK: - CONNECT

func serveConnect(state *instance, conn net.Conn, address string) {
	if state.router.Active() {
		serveRoutedConnect(state, conn, address)
		return
	}

	remote, err := state.hy.TCP(address)
	if err != nil {
		_ = writeReply(conn, replyGeneralFailure, net.IPv4zero, 0)
		return
	}
	defer remote.Close()

	if err := writeReply(conn, replySuccess, net.IPv4zero, 0); err != nil {
		return
	}

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, conn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, remote); done <- struct{}{} }()
	<-done
}

// serveRoutedConnect — CONNECT при включённой маршрутизации. Ответ отдаётся до
// выбора пути: клиент не пришлёт ни байта, пока не узнает, что соединение
// принято, а без этих байтов не узнать имя узла.
func serveRoutedConnect(state *instance, conn net.Conn, address string) {
	if err := writeReply(conn, replySuccess, net.IPv4zero, 0); err != nil {
		return
	}
	head, domain := wg.SniffConnection(conn, state.router)

	host, _, _ := net.SplitHostPort(address)
	destination, _ := netip.ParseAddr(host)

	var remote net.Conn
	var err error
	switch state.router.Resolve(domain, destination) {
	case wg.ActionBlock:
		return
	case wg.ActionDirect:
		// Собственные сокеты расширения в его же туннель не заворачиваются:
		// это действительно прямой выход с устройства.
		remote, err = net.DialTimeout("tcp", address, 10*time.Second)
	default:
		remote, err = state.hy.TCP(address)
	}
	if err != nil {
		return
	}
	defer remote.Close()

	if len(head) > 0 {
		if _, err := remote.Write(head); err != nil {
			return
		}
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, conn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, remote); done <- struct{}{} }()
	<-done
}

// MARK: - UDP ASSOCIATE

// Сеанс UDP живёт, пока держится управляющее TCP-соединение: так его закрытие
// освобождает и порт, и канал до сервера.
func serveUDPAssociate(state *instance, control net.Conn) {
	local, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		_ = writeReply(control, replyGeneralFailure, net.IPv4zero, 0)
		return
	}
	defer local.Close()

	remote, err := state.hy.UDP()
	if err != nil {
		_ = writeReply(control, replyGeneralFailure, net.IPv4zero, 0)
		return
	}
	defer remote.Close()

	bound := local.LocalAddr().(*net.UDPAddr)
	if err := writeReply(control, replySuccess, bound.IP, bound.Port); err != nil {
		return
	}

	// Адрес клиента известен только с первого пакета: сначала он приходит
	// сюда, и лишь потом есть куда возвращать ответы.
	clientAddr := make(chan *net.UDPAddr, 1)
	go relayFromClient(state.router, local, remote, clientAddr)
	go relayToClient(local, remote, clientAddr)

	// Ждём закрытия управляющего соединения.
	buffer := make([]byte, 1)
	for {
		if _, err := control.Read(buffer); err != nil {
			return
		}
	}
}

func relayFromClient(router *wg.Router, local *net.UDPConn, remote client.HyUDPConn, clientAddr chan<- *net.UDPAddr) {
	buffer := make([]byte, 65535)
	announced := false

	direct := wg.NewDirectUDP(local, func(payload []byte, from *net.UDPAddr) ([]byte, error) {
		return buildUDPReply(payload, from.String())
	}, nil)
	defer direct.Close()
	for {
		read, from, err := local.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		if !announced {
			clientAddr <- from
			announced = true
		}
		payload, target, err := parseUDPRequest(buffer[:read])
		if err != nil {
			continue
		}
		if router.Active() {
			switch router.ResolveTarget(target) {
			case wg.ActionBlock:
				continue
			case wg.ActionDirect:
				_ = direct.Send(from, payload, target)
				continue
			}
		}
		if err := remote.Send(payload, target); err != nil {
			return
		}
	}
}

func relayToClient(local *net.UDPConn, remote client.HyUDPConn, clientAddr <-chan *net.UDPAddr) {
	var destination *net.UDPAddr
	select {
	case destination = <-clientAddr:
	case <-time.After(30 * time.Second):
		return
	}
	for {
		payload, from, err := remote.Receive()
		if err != nil {
			return
		}
		packet, err := buildUDPReply(payload, from)
		if err != nil {
			continue
		}
		if _, err := local.WriteToUDP(packet, destination); err != nil {
			return
		}
	}
}

// Заголовок датаграммы SOCKS5: RSV(2) FRAG(1) ATYP ADDR PORT DATA.
func parseUDPRequest(packet []byte) ([]byte, string, error) {
	if len(packet) < 5 {
		return nil, "", errors.New("socks5: слишком короткая датаграмма")
	}
	if packet[2] != 0 {
		return nil, "", errors.New("socks5: фрагментация не поддерживается")
	}

	reader := newByteReader(packet[3:])
	address, err := readAddress(reader, packet[3])
	if err != nil {
		return nil, "", err
	}
	// readAddress прочитал тип адреса отдельно, поэтому сдвигаем на него сами.
	consumed := 3 + 1 + reader.consumed
	if consumed > len(packet) {
		return nil, "", errors.New("socks5: датаграмма обрывается")
	}
	return packet[consumed:], address, nil
}

func buildUDPReply(payload []byte, from string) ([]byte, error) {
	host, portText, err := net.SplitHostPort(from)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, err
	}

	packet := []byte{0x00, 0x00, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			packet = append(packet, addrIPv4)
			packet = append(packet, v4...)
		} else {
			packet = append(packet, addrIPv6)
			packet = append(packet, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return nil, fmt.Errorf("socks5: слишком длинное имя узла")
		}
		packet = append(packet, addrDomain, byte(len(host)))
		packet = append(packet, host...)
	}
	packet = append(packet, byte(port>>8), byte(port))
	return append(packet, payload...), nil
}

// Читатель, помнящий, сколько байт отдал: длина адреса переменная, а знать
// границу данных нужно точно.
type byteReader struct {
	data     []byte
	consumed int
}

func newByteReader(data []byte) *byteReader {
	// Первый байт — тип адреса, его читает вызывающий.
	return &byteReader{data: data[1:]}
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.consumed >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.consumed:])
	r.consumed += n
	return n, nil
}
