package wg

import (
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
)

// Датаграммы мимо туннеля.
//
// Правила обязаны действовать и на UDP: иначе QUIC российского сайта, у
// которого адрес попал в правило «напрямую», всё равно уходит за границу.
// Имени в QUIC без расшифровки не достать, поэтому сопоставление здесь по
// адресу — как и у ядра Xray, пока сниффинг не вернул имя.

// Active сообщает, есть ли вообще правила: без них сопоставлять незачем.
func (r *Router) Active() bool {
	return r != nil && len(r.rules) > 0
}

// ResolveTarget — Resolve для адреса вида `хост:порт`.
func (r *Router) ResolveTarget(target string) Action {
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		host = target
	}
	if address, err := netip.ParseAddr(host); err == nil {
		return r.Resolve("", address)
	}
	return r.Resolve(host, netip.Addr{})
}

// SniffConnection читает начало потока и достаёт из него имя узла. Прочитанное
// возвращается: его надо первым отправить той стороне.
func SniffConnection(client net.Conn, router *Router) (head []byte, domain string) {
	return sniffConnection(client, router)
}

// DirectUDP пересылает датаграммы обычным сокетом расширения, а ответы
// возвращает клиенту моста. Сокет открывается первой же датаграммой: у
// большинства связок правило «напрямую» не срабатывает никогда.
type DirectUDP struct {
	local *net.UDPConn
	// build оборачивает ответ заголовком SOCKS5.
	build func(payload []byte, from *net.UDPAddr) ([]byte, error)
	touch func()

	mu     sync.Mutex
	conn   *net.UDPConn
	closed bool
	client atomic.Pointer[net.UDPAddr]
}

// NewDirectUDP заводит пересылку. local — сокет связки с клиентом моста.
func NewDirectUDP(
	local *net.UDPConn,
	build func(payload []byte, from *net.UDPAddr) ([]byte, error),
	touch func(),
) *DirectUDP {
	return &DirectUDP{local: local, build: build, touch: touch}
}

// Send отправляет датаграмму. target — `адрес:порт`; имя здесь разрешается
// системой, как и у прямого TCP.
func (d *DirectUDP) Send(client *net.UDPAddr, payload []byte, target string) error {
	address, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return err
	}
	d.client.Store(client)

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return net.ErrClosed
	}
	if d.conn == nil {
		conn, err := net.ListenUDP("udp", nil)
		if err != nil {
			d.mu.Unlock()
			return err
		}
		d.conn = conn
		go d.readLoop(conn)
	}
	conn := d.conn
	d.mu.Unlock()

	if err := beforeSendUDP(address); err != nil {
		return err
	}
	_, err = conn.WriteToUDP(payload, address)
	return err
}

func (d *DirectUDP) readLoop(conn *net.UDPConn) {
	buffer := make([]byte, udpBufferSize)
	for {
		read, from, err := conn.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		if d.touch != nil {
			d.touch()
		}
		client := d.client.Load()
		if client == nil {
			continue
		}
		packet, err := d.build(buffer[:read], from)
		if err != nil {
			continue
		}
		if _, err := d.local.WriteToUDP(packet, client); err != nil {
			return
		}
	}
}

// Close закрывает сокет, если он был открыт. Вызывать можно не раз.
func (d *DirectUDP) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	if d.conn != nil {
		_ = d.conn.Close()
	}
}
