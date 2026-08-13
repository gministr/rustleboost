package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/vpnclient/daemon/config"
	"github.com/vpnclient/daemon/storage"
	"github.com/vpnclient/daemon/subscription"
)

type ConnectionState string

const (
	StateDisconnected  ConnectionState = "disconnected"
	StateConnecting    ConnectionState = "connecting"
	StateConnected     ConnectionState = "connected"
	StateDisconnecting ConnectionState = "disconnecting"
)

type Stats struct {
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
	Uptime   int64 `json:"uptime"` // seconds
}

type Status struct {
	State  ConnectionState      `json:"state"`
	Server *subscription.Server `json:"server,omitempty"`
	Stats  Stats                `json:"stats"`
	Engine string               `json:"engine,omitempty"`
	Error  string               `json:"error,omitempty"`
}

type Manager struct {
	mu          sync.RWMutex
	dataDir     string
	store       *storage.Store
	singbox     *SingBoxRunner
	xray        *XrayRunner
	prober      *LatencyProber
	servers     []subscription.Server
	info        subscription.Info
	current     *subscription.Server
	state       ConnectionState
	engine      string
	lastError   string
	session     uint64 // bumped per connect; identifies the live watchdog
	connectedAt time.Time
	subCancel   context.CancelFunc
}

func NewManager(dataDir string, store *storage.Store) *Manager {
	m := &Manager{
		dataDir: dataDir,
		store:   store,
		state:   StateDisconnected,
		singbox: NewSingBoxRunner(dataDir),
		xray:    NewXrayRunner(dataDir),
		prober:  NewLatencyProber(dataDir),
	}

	m.loadCache()

	settings := store.GetSettings()
	if settings.AutoUpdate && settings.SubscriptionURL != "" {
		m.startAutoUpdate()
	}

	return m
}

// ── cache ─────────────────────────────────────────────────────────────────

type cachePayload struct {
	Servers []subscription.Server `json:"servers"`
	Info    subscription.Info     `json:"info"`
}

func (m *Manager) loadCache() {
	data, err := m.store.LoadCache()
	if err != nil {
		return
	}
	var payload cachePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return
	}

	m.mu.Lock()
	m.servers = payload.Servers
	m.info = payload.Info
	m.mu.Unlock()

	log.Printf("Loaded %d servers from cache", len(payload.Servers))
}

func (m *Manager) saveCache() {
	m.mu.RLock()
	payload := cachePayload{Servers: m.servers, Info: m.info}
	m.mu.RUnlock()

	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if err := m.store.SaveCache(data); err != nil {
		log.Printf("Save cache: %v", err)
	}
}

// ── state ─────────────────────────────────────────────────────────────────

func (m *Manager) GetStatus() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := Status{
		State:  m.state,
		Server: m.current,
		Engine: m.engine,
		Error:  m.lastError,
	}

	if m.state == StateConnected && !m.connectedAt.IsZero() {
		status.Stats.Uptime = int64(time.Since(m.connectedAt).Seconds())
		if stats := m.singbox.GetStats(); stats != nil {
			status.Stats.Upload = stats.Upload
			status.Stats.Download = stats.Download
		}
	}

	return status
}

func (m *Manager) GetServers() []subscription.Server {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.servers
}

func (m *Manager) GetInfo() subscription.Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.info
}

// ── connect ───────────────────────────────────────────────────────────────

func (m *Manager) Connect(serverID string) error {
	m.mu.Lock()

	if m.state == StateConnecting || m.state == StateConnected {
		currentID := ""
		if m.current != nil {
			currentID = m.current.ID
		}
		if currentID == serverID {
			m.mu.Unlock()
			return nil
		}
		m.mu.Unlock()
		if err := m.Disconnect(); err != nil {
			return fmt.Errorf("disconnect before reconnect: %w", err)
		}
		m.mu.Lock()
	}

	var server *subscription.Server
	for i := range m.servers {
		if m.servers[i].ID == serverID {
			server = &m.servers[i]
			break
		}
	}
	if server == nil {
		m.mu.Unlock()
		return fmt.Errorf("server %s not found", serverID)
	}

	selected := *server
	m.state = StateConnecting
	m.current = &selected
	m.lastError = ""
	m.mu.Unlock()

	settings := m.store.GetSettings()
	opts := config.Options{
		TUNMode:   settings.TUNMode,
		RouteMode: settings.RouteMode,
		DNSMode:   settings.DNSMode,
		AllowLAN:  settings.AllowLAN,
	}

	engine := "sing-box"
	if config.NeedsXray(selected) {
		engine = "sing-box + xray"

		xrayCfg, err := config.GenerateXray(selected, opts)
		if err != nil {
			return m.failConnect(fmt.Errorf("generate xray config: %w", err))
		}
		if err := m.xray.Start(xrayCfg); err != nil {
			return m.failConnect(fmt.Errorf("xray: %w", err))
		}
		// sing-box forwards into Xray's SOCKS port; starting the tunnel
		// before that port accepts would fail the first connections.
		if err := waitForPort(config.XraySocksPort, 5*time.Second); err != nil {
			m.xray.Stop()
			return m.failConnect(fmt.Errorf("xray did not open port %d: %w\n%s",
				config.XraySocksPort, err, m.xray.LogTail(6)))
		}
	}

	sbCfg, err := config.Generate(selected, opts)
	if err != nil {
		m.xray.Stop()
		return m.failConnect(fmt.Errorf("generate config: %w", err))
	}
	if err := m.singbox.Start(sbCfg); err != nil {
		m.xray.Stop()
		return m.failConnect(fmt.Errorf("sing-box: %w", err))
	}

	// In TUN mode the adapter already carries everything. Setting a system
	// proxy on top adds a second, redundant path and misleads apps that
	// honour it, so it belongs to proxy mode only.
	if !settings.TUNMode {
		SetSystemProxy(fmt.Sprintf("127.0.0.1:%d", config.MixedPort))
	}

	// Cores accept connections before the first handshake completes, so
	// reporting "connected" the moment they start leaves the user staring at
	// a green screen while nothing loads. Hold the state until real traffic
	// makes it through.
	if err := waitForTunnelReady(tunnelReadyTimeout); err != nil {
		log.Printf("[connect] tunnel not verified within %s: %v", tunnelReadyTimeout, err)
	}

	if settings.TUNMode {
		go setNetworkCategoryPrivate()
	}

	m.mu.Lock()
	m.state = StateConnected
	m.engine = engine
	m.connectedAt = time.Now()
	m.session++
	session := m.session
	m.mu.Unlock()

	go m.watchCores(session, config.NeedsXray(selected), opts)

	m.store.UpdateSettings(func(s *storage.Settings) {
		s.LastServerID = serverID
		s.LastServerKey = serverKey(selected)
	})

	mode := "proxy"
	if settings.TUNMode {
		mode = "tun"
	}
	log.Printf("Connected to %s (%s/%s) via %s, %s mode",
		selected.Name, selected.Protocol, selected.Transport, engine, mode)
	return nil
}

// watchCores notices a core dying on its own — a crash, or the server
// dropping the tunnel — as opposed to the user pressing disconnect.
//
// session guards against a stale watcher acting on a later connection: every
// connect bumps the counter, so a watcher whose session no longer matches
// simply exits.
func (m *Manager) watchCores(session uint64, usesXray bool, opts config.Options) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		m.mu.RLock()
		stale := m.session != session || m.state != StateConnected
		m.mu.RUnlock()
		if stale {
			return
		}

		if m.singbox.IsRunning() && (!usesXray || m.xray.IsRunning()) {
			continue
		}

		log.Println("[watchdog] a core stopped unexpectedly")
		m.handleUnexpectedStop(session, opts)
		return
	}
}

func (m *Manager) handleUnexpectedStop(session uint64, opts config.Options) {
	killSwitch := m.store.GetSettings().KillSwitch

	m.singbox.Stop()
	m.xray.Stop()
	ClearSystemProxy()

	message := "соединение с сервером прервано"

	if killSwitch && opts.TUNMode {
		// Raise the blocking tunnel before reporting, so there is no window
		// in which traffic could reach the network unprotected.
		if err := m.singbox.Start(config.GenerateBlocking(opts)); err != nil {
			log.Printf("[watchdog] kill switch failed to engage: %v", err)
			message = "соединение прервано, и заблокировать трафик не удалось — отключитесь от сети вручную"
		} else {
			log.Println("[watchdog] kill switch engaged — traffic blocked")
			message = "соединение прервано. Kill Switch блокирует трафик — нажмите «Отключить», чтобы вернуть обычный доступ"
		}
	}

	m.mu.Lock()
	if m.session == session {
		m.state = StateDisconnected
		m.current = nil
		m.engine = ""
		m.connectedAt = time.Time{}
		m.lastError = message
	}
	m.mu.Unlock()
}

// failConnect rolls the manager back to a clean disconnected state and keeps
// the reason so the UI can show it instead of a bare "connection failed".
func (m *Manager) failConnect(err error) error {
	m.singbox.Stop()
	m.xray.Stop()
	ClearSystemProxy()

	m.mu.Lock()
	m.state = StateDisconnected
	m.current = nil
	m.engine = ""
	m.lastError = err.Error()
	m.connectedAt = time.Time{}
	m.mu.Unlock()

	log.Printf("Connection error: %v", err)
	return err
}

func (m *Manager) Disconnect() error {
	m.mu.Lock()
	if m.state == StateDisconnected {
		m.mu.Unlock()
		return nil
	}
	m.state = StateDisconnecting
	m.mu.Unlock()

	if err := m.singbox.Stop(); err != nil {
		log.Printf("Stop sing-box: %v", err)
	}
	if err := m.xray.Stop(); err != nil {
		log.Printf("Stop xray: %v", err)
	}
	ClearSystemProxy()

	m.mu.Lock()
	m.state = StateDisconnected
	m.current = nil
	m.engine = ""
	m.connectedAt = time.Time{}
	m.mu.Unlock()

	log.Println("Disconnected")
	return nil
}

func (m *Manager) Stop() {
	if m.subCancel != nil {
		m.subCancel()
	}
	m.Disconnect()
}

// ── subscription ──────────────────────────────────────────────────────────

func (m *Manager) UpdateSubscription(subURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	settings := m.store.GetSettings()
	if subURL == "" {
		subURL = settings.SubscriptionURL
	}
	if subURL == "" {
		return fmt.Errorf("no subscription URL configured")
	}

	hwid := GetHWIDInfo()
	result, err := subscription.Fetch(ctx, subURL, subscription.HWIDHeaders{
		HWID:  hwid.HWID,
		OS:    hwid.OS,
		OSVer: hwid.OSVer,
		Model: hwid.Model,
	})
	if err != nil {
		return translateSubscriptionError(err)
	}

	m.mu.Lock()
	// Carry measured latency across refreshes — the panel re-issues server
	// IDs on every fetch, so match on the address instead.
	latency := make(map[string]int, len(m.servers))
	for _, s := range m.servers {
		latency[serverKey(s)] = s.Latency
	}
	for i := range result.Servers {
		if lat, ok := latency[serverKey(result.Servers[i])]; ok {
			result.Servers[i].Latency = lat
		}
	}

	// Keep the running server selected across a refresh.
	if m.current != nil {
		currentKey := serverKey(*m.current)
		for i := range result.Servers {
			if serverKey(result.Servers[i]) == currentKey {
				result.Servers[i].ID = m.current.ID
				break
			}
		}
	}

	// Re-point the saved auto-connect target at its new ID, so the choice
	// outlives a refresh even when the app was closed during it.
	remappedLastID := ""
	if settings.LastServerKey != "" {
		for _, s := range result.Servers {
			if serverKey(s) == settings.LastServerKey {
				remappedLastID = s.ID
				break
			}
		}
	}

	m.servers = result.Servers
	m.info = result.Info
	m.mu.Unlock()

	if subURL != settings.SubscriptionURL || remappedLastID != "" {
		m.store.UpdateSettings(func(s *storage.Settings) {
			if subURL != "" {
				s.SubscriptionURL = subURL
			}
			if remappedLastID != "" {
				s.LastServerID = remappedLastID
			}
		})
	}
	m.saveCache()

	log.Printf("Subscription updated: %d servers", len(result.Servers))
	return nil
}

// ConnectLast reconnects to the server used last.
//
// It cannot rely on the stored ID alone: the panel issues fresh IDs on every
// subscription refresh, so after an overnight refresh the saved ID matches
// nothing. The stored key — name, address, port and transport — survives that.
func (m *Manager) ConnectLast() error {
	settings := m.store.GetSettings()

	m.mu.RLock()
	var byID, byKey string
	for _, s := range m.servers {
		if s.ID == settings.LastServerID {
			byID = s.ID
			break
		}
		if settings.LastServerKey != "" && serverKey(s) == settings.LastServerKey {
			byKey = s.ID
		}
	}
	m.mu.RUnlock()

	target := byID
	if target == "" {
		target = byKey
	}
	if target == "" {
		return fmt.Errorf("последний сервер больше не доступен в подписке")
	}
	return m.Connect(target)
}

// serverKey identifies a node across refreshes, independent of its random ID.
func serverKey(s subscription.Server) string {
	return fmt.Sprintf("%s|%s:%d|%s", s.Name, s.Address, s.Port, s.Transport)
}

// translateSubscriptionError turns panel signalling into text a user can act on.
func translateSubscriptionError(err error) error {
	switch {
	case errors.Is(err, subscription.ErrHWIDMaxDevices):
		return fmt.Errorf("достигнут лимит устройств для этой подписки — отвяжите одно из устройств в личном кабинете")
	case errors.Is(err, subscription.ErrHWIDNotSupported):
		return fmt.Errorf("панель не приняла идентификатор устройства — обновите приложение или обратитесь в поддержку")
	case errors.Is(err, subscription.ErrHWIDRejected):
		return fmt.Errorf("подписка не найдена — проверьте ссылку подписки")
	case errors.Is(err, subscription.ErrPlaceholderOnly):
		return fmt.Errorf("подписка не вернула ни одного сервера — проверьте её статус в личном кабинете")
	}
	return fmt.Errorf("не удалось обновить подписку: %w", err)
}

func (m *Manager) startAutoUpdate() {
	ctx, cancel := context.WithCancel(context.Background())
	m.subCancel = cancel

	go func() {
		settings := m.store.GetSettings()
		interval := time.Duration(settings.UpdateInterval) * time.Hour
		if interval < time.Hour {
			interval = 12 * time.Hour
		}

		if err := m.UpdateSubscription(""); err != nil {
			log.Printf("Initial subscription update failed: %v", err)
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := m.UpdateSubscription(""); err != nil {
					log.Printf("Auto subscription update failed: %v", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

// ── latency ───────────────────────────────────────────────────────────────

func (m *Manager) PingServer(serverID string) (int, error) {
	m.mu.RLock()
	var target *subscription.Server
	for i := range m.servers {
		if m.servers[i].ID == serverID {
			s := m.servers[i]
			target = &s
			break
		}
	}
	m.mu.RUnlock()

	if target == nil {
		return -1, fmt.Errorf("server not found")
	}

	results := m.prober.Measure([]subscription.Server{*target})
	latency, ok := results[serverID]
	if !ok {
		latency = -1
	}
	m.applyLatency(results)

	return latency, nil
}

func (m *Manager) PingAll() {
	m.mu.RLock()
	servers := make([]subscription.Server, len(m.servers))
	copy(servers, m.servers)
	m.mu.RUnlock()

	if len(servers) == 0 {
		return
	}
	m.applyLatency(m.prober.Measure(servers))
}

func (m *Manager) applyLatency(results map[string]int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.servers {
		if lat, ok := results[m.servers[i].ID]; ok {
			m.servers[i].Latency = lat
		}
	}
}

func pingTCP(host string, port int) int {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return -1
	}
	conn.Close()
	return int(time.Since(start).Milliseconds())
}

// waitForPort blocks until a local port accepts connections.
func waitForPort(port int, timeout time.Duration) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout after %s", timeout)
}

// GetHWID returns the device HWID info for display in UI
func (m *Manager) GetHWID() HWIDInfo { return GetHWIDInfo() }

// tunnelReadyTimeout bounds how long a connect waits for proof that traffic
// flows. Past it we report connected anyway: the probe host may be
// unreachable while the rest of the internet works fine.
const tunnelReadyTimeout = 15 * time.Second

// waitForTunnelReady polls through the local mixed inbound until a request
// completes end to end — inbound, routing, proxy outbound, and the node
// itself. That is the first moment the connection is genuinely usable.
func waitForTunnelReady(timeout time.Duration) error {
	proxy := fmt.Sprintf("127.0.0.1:%d", config.MixedPort)

	proxyURL, err := url.Parse("http://" + proxy)
	if err != nil {
		return err
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:               http.ProxyURL(proxyURL),
			DisableKeepAlives:   true,
			TLSHandshakeTimeout: 5 * time.Second,
		},
		Timeout: 6 * time.Second,
	}

	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		start := time.Now()
		resp, err := client.Get(probeURL)
		if err == nil {
			resp.Body.Close()
			log.Printf("[connect] tunnel ready in %dms", time.Since(start).Milliseconds())
			return nil
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("timeout")
	}
	return lastErr
}

func setNetworkCategoryPrivate() {
	cmd := `Set-NetConnectionProfile -InterfaceAlias 'RustleBoost' -NetworkCategory Private`
	out, err := hiddenCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", cmd).CombinedOutput()
	if err != nil {
		log.Printf("[nla] Set-NetConnectionProfile failed: %v — %s", err, out)
	} else {
		log.Println("[nla] Network category set to Private")
	}
}
