package wg

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// WireGuard в пользовательском пространстве с сетевым стеком внутри процесса.
//
// Системный интерфейс здесь не поднимается намеренно. Расширение туннеля уже
// владеет utun, и трафик в него заводит tun2socks — тот же путь, что у Xray и
// Hysteria2. Чтобы WireGuard встал в этот ряд, он должен выглядеть так же:
// принимать соединения по SOCKS5 на петлевом интерфейсе. Поэтому пакеты
// разбирает netstack внутри Go, а наружу торчит только локальный порт.
//
// Побочная выгода: остальная часть приложения — замер задержки, мост, разбор
// состояния — не отличает этот движок от прочих и не требует правок.

// Tunnel — поднятый WireGuard вместе с локальным SOCKS5.
type Tunnel struct {
	device   *device.Device
	listener net.Listener
	stack    *netstack.Net
	// Адрес интерфейса внутри стека. Нужен для привязки UDP-сокетов: без
	// явного адреса gVisor не знает семейства и паникует на номере протокола
	// 0, унося с собой всё расширение туннеля.
	local  netip.Addr
	router *Router
	// Открытый ключ пира — чтобы перевести его на другой адрес, не
	// пересоздавая туннель, см. SetEndpoint.
	peerPublicKey string
}

// Start поднимает WireGuard и локальный SOCKS5.
//
// endpoint — адрес, куда WireGuard шлёт свои UDP-пакеты; это локальный порт
// TURN-клиента, а не адрес сервера. Сам сервер увидит их уже после того, как
// relay VK передаст их дальше.
func Start(
	cfg Config,
	endpoint netip.AddrPort,
	socksPort int,
	router *Router,
	verbose bool,
) (*Tunnel, error) {
	return startWithBind(cfg, endpoint, socksPort, router, verbose, conn.NewDefaultBind())
}

// startWithBind — Start с готовым транспортом WireGuard. Обычный путь даёт
// системный UDP, цепочка — сокет внутри чужого туннеля (см. chain.go).
func startWithBind(
	cfg Config,
	endpoint netip.AddrPort,
	socksPort int,
	router *Router,
	verbose bool,
	bind conn.Bind,
) (*Tunnel, error) {
	addresses := make([]netip.Addr, 0, len(cfg.Addresses))
	for _, prefix := range cfg.Addresses {
		addresses = append(addresses, prefix.Addr())
	}

	tun, stack, err := netstack.CreateNetTUN(addresses, cfg.DNS, cfg.MTU)
	if err != nil {
		return nil, fmt.Errorf("wg: сетевой стек: %w", err)
	}

	dev := device.NewDevice(tun, bind, newLogger(verbose))

	settings, err := cfg.uapi(endpoint)
	if err != nil {
		dev.Close()
		return nil, fmt.Errorf("wg: %w", err)
	}
	if err := dev.IpcSet(settings); err != nil {
		dev.Close()
		return nil, fmt.Errorf("wg: приём конфигурации: %w", err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("wg: подъём интерфейса: %w", err)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", socksPort))
	if err != nil {
		dev.Close()
		return nil, fmt.Errorf("wg: порт SOCKS5 %d: %w", socksPort, err)
	}

	t := &Tunnel{
		device: dev, listener: listener, stack: stack,
		local: addresses[0], router: router,
		peerPublicKey: cfg.PeerPublicKey,
	}
	go t.serve()

	log.Printf("[WG] Интерфейс поднят, SOCKS5 на 127.0.0.1:%d, пир через %s", socksPort, endpoint)
	return t, nil
}

// Close останавливает SOCKS5 и WireGuard.
func (t *Tunnel) Close() {
	if t == nil {
		return
	}
	if t.listener != nil {
		_ = t.listener.Close()
	}
	if t.device != nil {
		t.device.Close()
	}
}

// Предел одновременно обслуживаемых соединений.
//
// Каждое живое соединение — это буферы копирования, буферы TCP внутри
// сетевого стека и горутины. На устройстве полный набор из 128 соединений
// держал расширение на 46–47 МБ, у самой границы, за которой iOS его снимает:
// очередной всплеск — и туннель пропадал без возврата. Девяносто шесть при
// уменьшенных буферах копирования оставляют запас около десяти мегабайт.
//
// Предел не останавливает приём: при полной таблице место освобождает самое
// давно молчавшее соединение (см. sessions.go).
const maxConcurrentConnections = 96

// connectionLimit — действующий предел. На Android у сервиса VPN нет
// жёсткого потолка памяти, как у расширения iOS, и там предел поднимают:
// при девяноста шести соединениях браузер с десятком вкладок уже упирался
// в него, и новые соединения вытесняли живые.
var connectionLimit atomic.Int32

// SetMaxConnections задаёт предел до запуска туннеля. Ноль — значение по
// умолчанию для iOS.
func SetMaxConnections(n int) {
	if n <= 0 {
		n = maxConcurrentConnections
	}
	connectionLimit.Store(int32(n))
}

func currentConnectionLimit() int {
	if n := connectionLimit.Load(); n > 0 {
		return int(n)
	}
	return maxConcurrentConnections
}

func (t *Tunnel) serve() {
	table := newSessionTable(currentConnectionLimit())

	for {
		conn, err := t.listener.Accept()
		if err != nil {
			// Ошибка приёма означает закрытый слушатель: туннель остановлен.
			return
		}

		s := table.admit(conn)
		go func() {
			defer table.release(s)
			serveSocks(t.stack, t.local, t.router, sessionConn{conn, s})
		}()
	}
}

// stackNet — то, чем SOCKS5 пользуется от сетевого стека. Вынесено
// интерфейсом, чтобы разбор протокола можно было держать отдельно от gVisor.
//
// UDP здесь не роскошь: через SOCKS5 идут и DNS-запросы, и QUIC, а без них
// работает лишь то, чьи адреса уже в кеше.
type stackNet interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
	ListenUDPAddrPort(laddr netip.AddrPort) (*gonet.UDPConn, error)
	LookupHost(host string) ([]string, error)
}

// newLogger заводит журнал WireGuard, который виден нам.
//
// Готовый device.NewLogger не годится: он пишет напрямую в stdout, а у
// расширения туннеля на iOS stdout уходит в системный журнал, откуда его не
// достать вместе с журналом приложения. Из-за этого не были видны даже
// ошибки, а они здесь главное: по ним отличается «пакет не дошёл» от «пакет
// дошёл, но отвергнут». Стандартный журнал Go перенаправлен в кольцевой
// буфер, который приложение выгружает на диск, поэтому пишем через него.
func newLogger(verbose bool) *device.Logger {
	logger := &device.Logger{
		Verbosef: func(string, ...any) {},
		Errorf: func(format string, args ...any) {
			log.Printf("[WG] ошибка: "+format, args...)
		},
	}
	if verbose {
		logger.Verbosef = func(format string, args ...any) {
			log.Printf("[WG] "+format, args...)
		}
	}
	return logger
}

// SetEndpoint переводит пира на другой адрес, не пересоздавая туннель.
//
// Нужно для перебора адресов: какой из них у оператора живой, заранее не
// известно, а пересоздание туннеля на каждую попытку стоило бы новой
// регистрации в стеке и лишних секунд.
func (t *Tunnel) SetEndpoint(endpoint netip.AddrPort) error {
	if t == nil || t.device == nil {
		return errors.New("wg: туннель не поднят")
	}
	key, err := hexKey(t.peerPublicKey)
	if err != nil {
		return fmt.Errorf("wg: открытый ключ пира: %w", err)
	}
	// update_only — чтобы не создать второго пира, если ключ вдруг разошёлся.
	return t.device.IpcSet(fmt.Sprintf(
		"public_key=%s\nupdate_only=true\nendpoint=%s\n", key, endpoint,
	))
}

// Probe проверяет, что через туннель действительно ходят данные.
//
// Рукопожатия для этого недостаточно: на сети оператора оно проходило, а
// пакеты с данными не доходили — туннель выглядел поднятым, и приложение
// показывало «подключено», хотя не работало ничего.
//
// Считать байты во входящих тоже оказалось нельзя: ответ на рукопожатие и
// подтверждение соединения приходят раньше, чем их успеваешь отделить от
// данных, и живой адрес то проходил проверку по ответу на рукопожатие, то не
// проходил, успев получить всё до начала подсчёта. Поэтому проверка прямая:
// отправляем запрос и ждём ответ. Пришёл хоть байт — данные ходят.
func (t *Tunnel) Probe(ctx context.Context, target netip.AddrPort) error {
	if t == nil || t.stack == nil {
		return errors.New("wg: туннель не поднят")
	}
	conn, err := t.stack.DialContextTCPAddrPort(ctx, target)
	if err != nil {
		return fmt.Errorf("соединение: %w", err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	request := fmt.Sprintf("HEAD / HTTP/1.0\r\nHost: %s\r\n\r\n", target.Addr())
	if _, err := conn.Write([]byte(request)); err != nil {
		return fmt.Errorf("запрос: %w", err)
	}

	// Содержимое ответа неважно — важно, что он вообще дошёл.
	answer := make([]byte, 64)
	read, err := conn.Read(answer)
	if read > 0 {
		return nil
	}
	if err == nil {
		err = errors.New("пустой ответ")
	}
	return fmt.Errorf("ответ: %w", err)
}

// PeerStats — состояние единственного пира: когда с ним в последний раз
// состоялось рукопожатие и сколько байт прошло в обе стороны.
//
// Нужно для разбора самой частой неполадки: приложение показывает
// «подключено», а ничего не грузится. Интерфейс внутри процесса поднимается
// всегда, независимо от того, дошли ли пакеты до пира, поэтому отличить
// «пир не отвечает» от «ошибка в нашем стеке» можно только отсюда. Нулевое
// рукопожатие при растущей отправке означает, что UDP до пира не доходит.
type PeerStats struct {
	HandshakeAgo time.Duration // сколько прошло с последнего рукопожатия
	Handshaken   bool          // было ли рукопожатие вообще
	Received     int64
	Sent         int64
}

// PeerStats снимает счётчики с устройства WireGuard.
func (t *Tunnel) PeerStats() PeerStats {
	var stats PeerStats
	if t == nil || t.device == nil {
		return stats
	}
	var buffer strings.Builder
	if err := t.device.IpcGetOperation(&buffer); err != nil {
		return stats
	}
	var seconds, nanoseconds int64
	for _, line := range strings.Split(buffer.String(), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		number, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "last_handshake_time_sec":
			seconds = number
		case "last_handshake_time_nsec":
			nanoseconds = number
		case "rx_bytes":
			stats.Received = number
		case "tx_bytes":
			stats.Sent = number
		}
	}
	if seconds > 0 || nanoseconds > 0 {
		stats.Handshaken = true
		stats.HandshakeAgo = time.Since(time.Unix(seconds, nanoseconds))
	}
	return stats
}
