package vkturn

// Туннель SRTP: транспорт vk-turn-proxy (anton48, GPL-3.0) вместо транспорта
// WDTT, а всё остальное — WireGuard в Go, SOCKS5, маршрутизация — то же, что у
// WDTT. Поэтому для приложения туннель выглядит как обычный WDTT: тот же
// SOCKS5, те же правила, тот же сторож.
//
// Отличия транспорта: пакеты оформлены как медиапоток звонка (DTLS-SRTP), а
// сервер раскладывает встречный поток по всем соединениям устройства и держит
// каждое ниже порога ограничителя VK. Устройство регистрируется на узле само:
// на каждом соединении уходит пакет с его ключом WireGuard и токеном подписки.

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cacggghp/vk-turn-proxy/pkg/proxy"
	"github.com/cacggghp/vk-turn-proxy/pkg/rbreg"
	"github.com/cacggghp/vk-turn-proxy/pkg/turnbind"

	"rustleboost/vkturn/wg"
)

// SRTPConfig — параметры туннеля SRTP.
type SRTPConfig struct {
	// Peer — адрес:порт сервера SRTP на узле.
	Peer string
	// VKHashes — хеши звонков VK через запятую, те же, что у WDTT.
	VKHashes string
	// Conns — число соединений (аллокаций).
	Conns int
	// UDP — UDP до релея вместо TCP.
	UDP bool
	// DevicePrivateKey — закрытый ключ WireGuard устройства, base64.
	DevicePrivateKey string
	// ServerPublicKey — открытый ключ WireGuard узла, base64.
	ServerPublicKey string
	// Token — токен подписки из каталога, для регистрации на узле.
	Token string
	// CredentialsDir — где держать кэш учёток VK.
	CredentialsDir string
	// MaxProcs — потоков Go: 0 — 4 (телефоны), < 0 — все ядра (компьютер).
	MaxProcs int
}

// srtpProxy — транспорт поднятого туннеля SRTP; читается счётчиками в обход
// замка туннеля.
var srtpProxy atomic.Pointer[proxy.Proxy]

// StartSRTPTunnel поднимает транспорт SRTP и WireGuard с SOCKS5 над ним.
func StartSRTPTunnel(cfg SRTPConfig, options TunnelOptions) error {
	tunnelMu.Lock()
	defer tunnelMu.Unlock()
	defer clearAbort()
	if tunnel != nil {
		return errors.New("vkturn: туннель уже поднят")
	}
	if options.SocksPort == 0 {
		return errors.New("vkturn: не задан порт SOCKS5")
	}
	if options.ConfigTimeout <= 0 {
		options.ConfigTimeout = defaultConfigTimeout
	}

	devicePub, err := publicKeyOf(cfg.DevicePrivateKey)
	if err != nil {
		return fmt.Errorf("vkturn: ключ устройства: %w", err)
	}
	var links []string
	for _, h := range strings.Split(cfg.VKHashes, ",") {
		if h = strings.TrimSpace(h); h != "" {
			links = append(links, "https://vk.ru/call/join/"+h)
		}
	}
	if len(links) == 0 || cfg.Peer == "" {
		return errors.New("vkturn: нет звонков VK или адреса узла")
	}
	conns := cfg.Conns
	if conns <= 0 {
		conns = 30
	}

	startedAt := time.Now()
	proxy.SetVKCookieAuth(false, "", nil)
	var cache string
	if cfg.CredentialsDir != "" {
		cache = filepath.Join(cfg.CredentialsDir, "srtp-creds-pool.json")
	}
	p := proxy.NewProxy(proxy.Config{
		PeerAddr:      cfg.Peer,
		VKLink:        strings.Join(links, "\n"),
		UseDTLS:       true,
		UseUDP:        cfg.UDP,
		UseSrtp:       true,
		NumConns:      conns,
		CredCachePath: cache,
		// Запас учёток втрое больше авторского (12 мест на 10 соединений):
		// при быстрой смене сервера места заняты учётками, которые релей ещё
		// держит после прошлых подключений, и свежую учётку брать некуда.
		CredPoolSize:   poolSizeFor(conns),
		RegisterPacket: rbreg.Packet(devicePub, cfg.Token),
		// Автор держит Go на двух потоках ради батареи — у него в Go только
		// транспорт. Здесь на тех же потоках ещё WireGuard и TCP-стек gVisor
		// всех соединений, и на двух потоках скорость упиралась в процессор.
		GOMAXPROCS: maxProcs(cfg.MaxProcs),
	})
	go func() {
		if err := p.Start(); err != nil {
			log.Printf("[SRTP] транспорт не поднялся: %v", err)
		}
	}()
	started := false
	defer func() {
		if !started {
			p.StopWithTimeout(2 * time.Second)
		}
	}()

	// Ожидание первого живого соединения прерывается остановкой туннеля.
	abortCtx, abortCancelFn := context.WithCancel(context.Background())
	defer abortCancelFn()
	if !registerAbort(abortCancelFn) {
		return errors.New("vkturn: запуск прерван")
	}
	ready := make(chan error, 1)
	go func() { ready <- p.WaitBootstrap(options.ConfigTimeout) }()
	select {
	case err := <-ready:
		if err != nil {
			return fmt.Errorf("vkturn: транспорт SRTP: %w", err)
		}
	case <-abortCtx.Done():
		return errors.New("vkturn: запуск прерван")
	}
	log.Printf("[SRTP] транспорт поднят через %d мс, соединений %d, %s до релея",
		time.Since(startedAt).Milliseconds(), conns, map[bool]string{true: "UDP", false: "TCP"}[cfg.UDP])

	var dns []netip.Addr
	for _, s := range options.DNSServers {
		if a, err := netip.ParseAddr(s); err == nil {
			dns = append(dns, a)
		}
	}
	if len(dns) == 0 {
		dns = []netip.Addr{netip.MustParseAddr("1.1.1.1")}
	}
	wgConfig := wg.Config{
		PrivateKey:    cfg.DevicePrivateKey,
		Addresses:     []netip.Prefix{netip.PrefixFrom(rbreg.TunnelIP(devicePub), 32)},
		DNS:           dns,
		MTU:           1280,
		PeerPublicKey: cfg.ServerPublicKey,
		AllowedIPs:    []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")},
		Keepalive:     25,
	}
	wg.SetMaxConnections(options.MaxConnections)
	t, err := wg.StartWithBind(wgConfig, options.SocksPort, wg.NewRouter(options.Routing),
		options.VerboseWireGuard, turnbind.NewTURNBind(p))
	if err != nil {
		return err
	}
	tunnel = t
	srtpProxy.Store(p)
	started = true
	log.Printf("[SRTP] туннель готов через %d мс, адрес %s", time.Since(startedAt).Milliseconds(),
		rbreg.TunnelIP(devicePub))
	return nil
}

// stopSRTP снимает транспорт SRTP, если он поднят. Вызывается из StopTunnel
// после WireGuard; время даётся на сдачу аллокаций релею — тогда следующее
// подключение может сразу взять те же учётки.
func stopSRTP() {
	if p := srtpProxy.Swap(nil); p != nil {
		p.StopWithTimeout(4 * time.Second)
	}
}

// srtpStatistics — счётчики транспорта SRTP.
func srtpStatistics() (up, down int64, active int32, ok bool) {
	p := srtpProxy.Load()
	if p == nil {
		return 0, 0, 0, false
	}
	s := p.GetStats()
	return s.TxBytes, s.RxBytes, s.ActiveConns, true
}

func poolSizeFor(conns int) int {
	size := (conns*12 + 9) / 10
	if size < 12 {
		size = 12
	}
	return size
}

func publicKeyOf(privateB64 string) ([32]byte, error) {
	var out [32]byte
	raw, err := base64.StdEncoding.DecodeString(privateB64)
	if err != nil || len(raw) != 32 {
		return out, errors.New("ключ не 32 байта в base64")
	}
	key, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return out, err
	}
	copy(out[:], key.PublicKey().Bytes())
	return out, nil
}

func maxProcs(n int) int {
	if n == 0 {
		return 4
	}
	return n
}
