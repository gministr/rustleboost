package core

// Прямой путь для серверов RustleBoost (WDTT, SRTP) и WARP — как в приложениях
// для iOS и Android и как в консольном клиенте авторов транспорта.
//
// Раньше цепочка была длинной: адаптер sing-box → его стек TCP → SOCKS5 →
// второй стек TCP внутри ядра → WireGuard → релеи ВК. Каждое соединение
// программы обрывалось и собиралось заново дважды, а собственные пакеты ядра к
// релеям ВК тоже заходили в адаптер sing-box и выпускались наружу правилом
// «процесс daemon.exe — напрямую», которое на Windows срабатывает не всегда.
//
// Теперь WireGuard ядра работает прямо на адаптере Wintun: пакет программы
// уходит в WireGuard как есть. sing-box на этом пути не запускается. Адреса
// самого транспорта закрепляются за физическим шлюзом в момент соединения
// (pins.go), а не угадываются по имени процесса.
//
// Порядок как у авторов: адаптер создаётся без маршрутов, ядро поднимает
// WireGuard через всю цепочку, настоящий запрос проходит через туннель — и
// только тогда маршрут по умолчанию и DNS переводятся в туннель. До этого
// момента ни один пакет программ туда не идёт.

import (
	"errors"
	"fmt"
	"log"
	"net/netip"
	"path/filepath"
	"time"

	"github.com/vpnclient/daemon/config"
	"github.com/vpnclient/daemon/subscription"
	"rustleboost/vkturn"
)

const (
	directAdapterName = "RustleBoost"
	// directMTU — пакеты с адаптера идут в WireGuard как есть, поэтому больше
	// MTU самого WireGuard быть не может; как на iOS и Android.
	directMTU = 1280
	// directWarpMTU — у WARP внутренний WireGuard едет внутри WDTT, и его MTU
	// меньше (wg.ChainedMTU): адаптер не должен давать пакеты крупнее.
	directWarpMTU = 1200
	// directFollowEvery — как часто проверяется, не сменилась ли сеть.
	directFollowEvery = 3 * time.Second
)

var (
	// directAddress — адрес адаптера; ядро подменяет его на адрес WireGuard
	// на лету, как на iOS (TunnelDefaults.tunnelIPv4Address).
	directAddress = netip.MustParseAddr("198.18.0.1")
	// directDNS — DNS туннеля. Идёт через туннель и потому не закрепляется.
	directDNS = []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("1.0.0.1")}
)

// errDirectUnavailable — адаптер создать не удалось (например, нет wintun.dll):
// подключение идёт прежним путём через sing-box.
var errDirectUnavailable = errors.New("прямой путь недоступен")

// directPlatform — системная часть адаптера (direct_windows.go).
type directPlatform interface {
	activate() error
	deactivate()
	close()
	routes() pinRoutes
}

type directAdapter struct {
	platform directPlatform
	hub      *directHub
	pins     *pinner
	stop     chan struct{}
}

func openDirectAdapter(dataDir string, mtu int) (*directAdapter, error) {
	platform, dev, err := openDirectPlatform(mtu)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errDirectUnavailable, err)
	}
	journal := filepath.Join(dataDir, "direct-pins.json")
	routes := platform.routes()
	if n := cleanupPins(routes, journal); n > 0 {
		log.Printf("[direct] убраны маршруты прошлого сеанса: %d", n)
	}
	skip := make([]netip.Prefix, 0, len(directDNS)+1)
	skip = append(skip, netip.PrefixFrom(directAddress, 15).Masked())
	for _, dns := range directDNS {
		skip = append(skip, netip.PrefixFrom(dns, 32))
	}
	a := &directAdapter{
		platform: platform,
		hub:      newDirectHub(dev, directAdapterName, mtu),
		pins:     newPinner(routes, journal, skip),
		stop:     make(chan struct{}),
	}
	go a.follow()
	return a, nil
}

func (a *directAdapter) follow() {
	ticker := time.NewTicker(directFollowEvery)
	defer ticker.Stop()
	for {
		select {
		case <-a.stop:
			return
		case <-ticker.C:
			a.pins.follow()
		}
	}
}

// close снимает маршруты туннеля, свои закрепления и удаляет адаптер.
func (a *directAdapter) close() {
	select {
	case <-a.stop:
		return
	default:
		close(a.stop)
	}
	a.platform.close()
	a.hub.close()
	a.pins.clear()
}

// directRouting — правила маршрутизации ядра из режима маршрутов настроек:
// те же домены, что sing-box на прежнем пути пускал напрямую.
func directRouting(routeMode string) []map[string]any {
	suffixes := config.BypassSuffixes(routeMode)
	if len(suffixes) == 0 {
		return []map[string]any{}
	}
	domains := make([]string, 0, len(suffixes))
	for _, suffix := range suffixes {
		domains = append(domains, "domain:"+suffix)
	}
	return []map[string]any{{"action": "direct", "domains": domains, "ipRanges": []string{}}}
}

// directCoreOptions — что добавляется к конфигурации ядра на прямом пути.
func directCoreOptions(opts config.Options) map[string]any {
	return map[string]any{
		"nativeDevice": true,
		"tunAddresses": directAddress.String(),
		"routing":      directRouting(opts.RouteMode),
	}
}

// startDirectCore поднимает ядро на новой сессии адаптера.
func (m *Manager) startDirectCore(adapter *directAdapter, engine string, selected subscription.Server, opts config.Options) error {
	setCoreDevice(adapter.hub.session())
	extra := directCoreOptions(opts)
	hwid := GetHWIDInfo().HWID
	switch engine {
	case subscription.EngineWdtt:
		if err := m.wdtt.Start(m.dataDir, selected, config.XraySocksPort, hwid, false, extra); err != nil {
			return fmt.Errorf("wdtt: %w", err)
		}
	case subscription.EngineWarp:
		transport := m.transportServer()
		if transport == nil {
			return fmt.Errorf("WARP: в каталоге нет WDTT-сервера — обновите подписку")
		}
		if err := m.warp.Start(m.dataDir, *transport, config.XraySocksPort, hwid, false, extra); err != nil {
			return fmt.Errorf("warp: %w", err)
		}
	default:
		return fmt.Errorf("прямой путь для %s не предусмотрен", engine)
	}
	if err := waitForPort(config.XraySocksPort, 5*time.Second); err != nil {
		m.stopChains()
		return fmt.Errorf("ядро не открыло порт %d: %w", config.XraySocksPort, err)
	}
	return nil
}

// connectDirect — подключение прямым путём. Любая неудача возвращается как
// errDirectUnavailable: всё поднятое снято, и вызывающий идёт прежним путём.
func (m *Manager) connectDirect(selected subscription.Server, engine string, opts config.Options) error {
	mtu := directMTU
	if engine == subscription.EngineWarp {
		mtu = directWarpMTU
	}
	adapter, err := openDirectAdapter(m.dataDir, mtu)
	if err != nil {
		return err
	}
	vkturn.SetDialHook(adapter.pins.hook)
	// DNS сетевой карты, если он не в её сети (например, 8.8.8.8, прописанный
	// вручную), — мимо туннеля: через него ядро разрешает адреса ВК, когда
	// поднимает упавший туннель заново.
	for _, server := range opts.SystemDNS {
		if ip, err := netip.ParseAddr(server); err == nil {
			_ = adapter.pins.ensure(ip)
		}
	}
	m.mu.Lock()
	m.direct = adapter
	m.mu.Unlock()

	if err := m.startDirectCore(adapter, engine, selected, opts); err != nil {
		m.stopDirect(false)
		return fmt.Errorf("%w: ядро: %v", errDirectUnavailable, err)
	}

	// Канал проверен настоящим запросом — только теперь трафик программ идёт
	// в туннель. Запрос не прошёл — маршруты всё равно переводятся (как на
	// прежнем пути): адрес проверки мог быть недоступен при живом туннеле, и
	// человек увидит предупреждение.
	warning := ""
	started := time.Now()
	if livenessProbe(config.XraySocksPort, tunnelReadyTimeout) {
		log.Printf("[direct] канал проверен за %d мс", time.Since(started).Milliseconds())
	} else {
		warning = "Похоже, туннель пока не пропускает данные. Если сайты не открываются, " +
			"переподключитесь или выберите другой сервер."
		log.Printf("[direct] канал не ответил за %s", tunnelReadyTimeout)
	}
	if err := adapter.platform.activate(); err != nil {
		m.stopDirect(false)
		return fmt.Errorf("%w: перевод трафика в туннель: %v", errDirectUnavailable, err)
	}

	m.mu.Lock()
	m.state = StateConnected
	m.engine = engine + " · прямой путь"
	m.connectedAt = time.Now()
	m.lastWarning = warning
	m.session++
	session := m.session
	m.mu.Unlock()

	go m.guardDirect(session, adapter, engine, selected, opts)
	log.Printf("Connected to %s via %s directly on the adapter, %d pin(s)",
		selected.Name, engine, adapter.pins.count())
	return nil
}

// guardDirect — сторож прямого пути: адаптер и маршруты живут всё подключение,
// упавшее ядро поднимается заново на новой сессии того же адаптера.
func (m *Manager) guardDirect(session uint64, adapter *directAdapter, engine string, selected subscription.Server, opts config.Options) {
	stale := func() bool {
		m.mu.RLock()
		defer m.mu.RUnlock()
		return m.session != session || m.state != StateConnected
	}
	hooks := guardHooks{
		stale:        stale,
		singboxAlive: func() bool { return !adapter.hub.closed.Load() },
		chainAlive:   m.chainRunning,
		restartChain: func() error {
			if stale() {
				return nil
			}
			m.stopChains()
			if err := m.startDirectCore(adapter, engine, selected, opts); err != nil {
				return err
			}
			if stale() {
				m.stopChains()
			}
			return nil
		},
		giveUp: func(reason string) { m.handleUnexpectedStop(session, opts, reason) },
		logf:   log.Printf,
		now:    time.Now,
		sleep:  time.Sleep,
	}
	runGuard(defaultGuardTiming(), hooks)
}

// stopDirect снимает прямой путь: сначала ядро (его сокеты), затем адаптер,
// маршруты и закрепления. keepRoutes — аварийная блокировка: адаптер и
// маршруты остаются, сессии нет, и трафик программ никуда не уходит, пока
// человек не нажмёт «Отключить».
func (m *Manager) stopDirect(keepRoutes bool) {
	m.mu.Lock()
	adapter := m.direct
	if !keepRoutes {
		m.direct = nil
	}
	m.mu.Unlock()
	if adapter == nil {
		return
	}
	m.stopChains()
	setCoreDevice(nil)
	if keepRoutes {
		return
	}
	vkturn.SetDialHook(nil)
	adapter.close()
}

// directStats — счётчики адаптера: всё, что прошло через туннель.
func (m *Manager) directStats() (*TrafficStats, bool) {
	if m.direct == nil {
		return nil, false
	}
	return &TrafficStats{Upload: m.direct.hub.up.Load(), Download: m.direct.hub.down.Load()}, true
}
