// Package rustlecore — единственный мост приложения к сетевым ядрам.
//
// Ядер два: Xray-core покрывает VLESS, VMess, Trojan, Shadowsocks и SOCKS,
// а Hysteria2 построен на QUIC, и в Xray его нет. Оба живут в одном пакете
// намеренно: каждая сборка gomobile несёт собственный рантайм Go, а два
// рантайма в одном процессе не уживаются — каждый ставит свои обработчики
// сигналов, и расширение туннеля падало с SIGSEGV сразу после запуска.
//
// Наружу оба ядра выглядят одинаково: поднимают SOCKS5 на петлевом
// интерфейсе, а трафик в него заводит tun2socks.
package rustlecore

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/apernet/hysteria/core/v2/client"
	"github.com/apernet/hysteria/extras/v2/obfs"

	"rustleboost/vkturn/wg"
)

// Config — параметры подключения, приходящие из приложения.
type Config struct {
	Server     string `json:"server"`     // host:port
	Auth       string `json:"auth"`       // пароль
	SNI        string `json:"sni"`        // имя для TLS
	Insecure   bool   `json:"insecure"`   // не проверять сертификат
	ObfsType   string `json:"obfsType"`   // пока только "salamander"
	ObfsPass   string `json:"obfsPass"`   // пароль обфускации
	SocksPort  int    `json:"socksPort"`  // локальный порт SOCKS5
	UpMbps     int    `json:"upMbps"`     // 0 — договориться автоматически
	DownMbps   int    `json:"downMbps"`   //
	PinnedSHA  string `json:"pinnedSHA"`  // не используется, оставлено для совместимости
	FastOpen   bool   `json:"fastOpen"`   //
	// Сборка маршрутизации приложения. У Hysteria2 своего маршрутизатора нет,
	// поэтому правила применяются в SOCKS5-слое — так же, как у WDTT.
	Routing []wg.Rule `json:"routing"`
}

var (
	mu      sync.Mutex
	running *instance
)

type instance struct {
	hy       client.Client
	listener net.Listener
	router   *wg.Router
	done     chan struct{}
}

// Start поднимает клиент и локальный SOCKS5. Повторный вызов без Stop —
// ошибка: два клиента на одном порту всё равно не ужились бы.
func Start(configJSON string) error {
	mu.Lock()
	defer mu.Unlock()
	if running != nil {
		return errors.New("hysteria2: клиент уже запущен")
	}

	var config Config
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return fmt.Errorf("hysteria2: не разбирается конфигурация: %w", err)
	}
	if config.Server == "" || config.SocksPort == 0 {
		return errors.New("hysteria2: не заданы адрес сервера или порт SOCKS")
	}

	address, err := net.ResolveUDPAddr("udp", config.Server)
	if err != nil {
		return fmt.Errorf("hysteria2: не разрешается адрес сервера: %w", err)
	}

	clientConfig := &client.Config{
		ServerAddr: address,
		Auth:       config.Auth,
		TLSConfig: client.TLSConfig{
			ServerName:         config.SNI,
			InsecureSkipVerify: config.Insecure,
		},
		FastOpen: config.FastOpen,
	}
	if config.UpMbps > 0 {
		clientConfig.BandwidthConfig.MaxTx = uint64(config.UpMbps) * 1000000 / 8
	}
	if config.DownMbps > 0 {
		clientConfig.BandwidthConfig.MaxRx = uint64(config.DownMbps) * 1000000 / 8
	}
	if config.ObfsType == "salamander" && config.ObfsPass != "" {
		clientConfig.ConnFactory = &salamanderFactory{password: []byte(config.ObfsPass)}
	}



	hy, _, err := client.NewClient(clientConfig)
	if err != nil {
		return fmt.Errorf("hysteria2: не удалось подключиться: %w", err)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", config.SocksPort))
	if err != nil {
		_ = hy.Close()
		return fmt.Errorf("hysteria2: не удалось занять порт SOCKS: %w", err)
	}

	state := &instance{hy: hy, listener: listener, router: wg.NewRouter(config.Routing), done: make(chan struct{})}
	running = state
	go serveSocks(state)
	return nil
}

// Stop останавливает клиент. Вызов на остановленном клиенте безвреден.
func Stop() {
	mu.Lock()
	defer mu.Unlock()
	if running == nil {
		return
	}
	close(running.done)
	_ = running.listener.Close()
	_ = running.hy.Close()
	running = nil
}

// IsRunning сообщает, поднят ли клиент.
func IsRunning() bool {
	mu.Lock()
	defer mu.Unlock()
	return running != nil
}

// Обфускация salamander заворачивает UDP-пакеты QUIC, чтобы соединение не
// опознавалось по сигнатуре.
type salamanderFactory struct {
	password []byte
}

func (f *salamanderFactory) New(addr net.Addr) (net.PacketConn, error) {
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}
	wrapped, err := obfs.WrapPacketConnSalamander(conn, f.password)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return wrapped, nil
}

func serveSocks(state *instance) {
	for {
		conn, err := state.listener.Accept()
		if err != nil {
			select {
			case <-state.done:
				return
			default:
			}
			// Разовая ошибка приёма не повод ронять весь вход.
			time.Sleep(50 * time.Millisecond)
			continue
		}
		go handleSocks(state, conn)
	}
}
