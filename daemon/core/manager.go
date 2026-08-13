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
	"os/exec"
	"sort"
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
	servers     []subscription.Server
	info        subscription.Info
	current     *subscription.Server
	state       ConnectionState
	engine      string
	lastError   string
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

	// The system proxy carries all traffic in proxy mode; in TUN mode it also
	// gives Windows' connectivity check a fast path, so the tray flips to
	// "Connected" in a couple of seconds instead of a polling cycle later.
	SetSystemProxy(fmt.Sprintf("127.0.0.1:%d", config.MixedPort))
	go warmupVPNTunnel()

	m.mu.Lock()
	m.state = StateConnected
	m.engine = engine
	m.connectedAt = time.Now()
	m.mu.Unlock()

	m.store.UpdateSettings(func(s *storage.Settings) { s.LastServerID = serverID })

	mode := "proxy"
	if settings.TUNMode {
		mode = "tun"
	}
	log.Printf("Connected to %s (%s/%s) via %s, %s mode",
		selected.Name, selected.Protocol, selected.Transport, engine, mode)
	return nil
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

	m.servers = result.Servers
	m.info = result.Info
	m.mu.Unlock()

	if subURL != settings.SubscriptionURL {
		m.store.UpdateSettings(func(s *storage.Settings) { s.SubscriptionURL = subURL })
	}
	m.saveCache()

	log.Printf("Subscription updated: %d servers", len(result.Servers))
	return nil
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

	latency := pingTCP(target.Address, target.Port)

	m.mu.Lock()
	for i := range m.servers {
		if m.servers[i].ID == serverID {
			m.servers[i].Latency = latency
			break
		}
	}
	m.mu.Unlock()

	return latency, nil
}

func (m *Manager) PingAll() {
	m.mu.RLock()
	servers := make([]subscription.Server, len(m.servers))
	copy(servers, m.servers)
	m.mu.RUnlock()

	results := make([]int, len(servers))
	var wg sync.WaitGroup
	for i, s := range servers {
		wg.Add(1)
		go func(idx int, srv subscription.Server) {
			defer wg.Done()
			results[idx] = pingTCP(srv.Address, srv.Port)
		}(i, s)
	}
	wg.Wait()

	m.mu.Lock()
	byID := make(map[string]int, len(servers))
	for i, s := range servers {
		byID[s.ID] = results[i]
	}
	for i := range m.servers {
		if lat, ok := byID[m.servers[i].ID]; ok {
			m.servers[i].Latency = lat
		}
	}
	m.mu.Unlock()
}

// FastestServerID returns the reachable server with the lowest latency,
// measuring first when no ping data exists yet.
func (m *Manager) FastestServerID() (string, error) {
	m.mu.RLock()
	measured := false
	for _, s := range m.servers {
		if s.Latency > 0 {
			measured = true
			break
		}
	}
	empty := len(m.servers) == 0
	m.mu.RUnlock()

	if empty {
		return "", fmt.Errorf("нет доступных серверов")
	}
	if !measured {
		m.PingAll()
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	candidates := make([]subscription.Server, 0, len(m.servers))
	for _, s := range m.servers {
		if s.Latency > 0 {
			candidates = append(candidates, s)
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("ни один сервер не отвечает — проверьте интернет-соединение")
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Latency < candidates[j].Latency })
	return candidates[0].ID, nil
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

// warmupVPNTunnel pre-establishes the tunnel and marks the adapter as a
// Private network, so Windows reports "Connected" rather than sitting on
// "No Internet" until its next NLA re-check.
func warmupVPNTunnel() {
	proxy := fmt.Sprintf("127.0.0.1:%d", config.MixedPort)

	for i := 0; i < 30; i++ {
		conn, err := net.DialTimeout("tcp", proxy, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	proxyURL, err := url.Parse("http://" + proxy)
	if err != nil {
		return
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   8 * time.Second,
	}
	if resp, err := client.Head("http://connectivitycheck.gstatic.com/generate_204"); err == nil {
		resp.Body.Close()
		log.Println("[warmup] VPN tunnel warmed up successfully")
	} else {
		log.Printf("[warmup] warmup failed (non-critical): %v", err)
	}

	setNetworkCategoryPrivate()
}

func setNetworkCategoryPrivate() {
	cmd := `Set-NetConnectionProfile -InterfaceAlias 'RustleBoost' -NetworkCategory Private`
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", cmd).CombinedOutput()
	if err != nil {
		log.Printf("[nla] Set-NetConnectionProfile failed: %v — %s", err, out)
	} else {
		log.Println("[nla] Network category set to Private")
	}
}
