package core

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os/exec"
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
	State    ConnectionState      `json:"state"`
	Server   *subscription.Server `json:"server,omitempty"`
	Stats    Stats                `json:"stats"`
	Error    string               `json:"error,omitempty"`
}

type Manager struct {
	mu       sync.RWMutex
	dataDir  string
	store    *storage.Store
	runner   *SingBoxRunner
	servers  []subscription.Server
	current  *subscription.Server
	state    ConnectionState
	connectedAt time.Time
	cancelPing  context.CancelFunc
	subCancel   context.CancelFunc
}

func NewManager(dataDir string, store *storage.Store) *Manager {
	m := &Manager{
		dataDir: dataDir,
		store:   store,
		state:   StateDisconnected,
	}
	m.runner = NewSingBoxRunner(dataDir)

	settings := store.GetSettings()
	if settings.AutoUpdate && settings.SubscriptionURL != "" {
		m.startAutoUpdate()
	}

	return m
}

func (m *Manager) GetStatus() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := Status{
		State:  m.state,
		Server: m.current,
	}

	if m.state == StateConnected && !m.connectedAt.IsZero() {
		status.Stats.Uptime = int64(time.Since(m.connectedAt).Seconds())
		if stats := m.runner.GetStats(); stats != nil {
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

	m.state = StateConnecting
	m.current = server
	m.mu.Unlock()

	settings := m.store.GetSettings()
	opts := config.Options{
		TUNMode:   settings.TUNMode,
		RouteMode: settings.RouteMode,
		DNSMode:   settings.DNSMode,
		LogLevel:  "warn",
		AllowLAN:  settings.AllowLAN,
	}

	cfg, err := config.Generate(*server, opts)
	if err != nil {
		m.setError(err)
		return fmt.Errorf("generate config: %w", err)
	}

	if startErr := m.runner.Start(cfg); startErr != nil {
		m.setError(startErr)
		return fmt.Errorf("sing-box: %w", startErr)
	}

	// Always set Windows system proxy — in proxy mode it carries all traffic,
	// in TUN mode it gives Windows NCSI a fast path so the tray shows
	// "Connected" in 1-3 s instead of waiting for the next NLA polling cycle.
	SetSystemProxy("127.0.0.1:2080")

	// Warm up the VPN tunnel so Windows NCSI succeeds on its first check.
	// Without this, Windows may show "No Internet" for 10-15 seconds while
	// waiting for its next NLA polling cycle.
	go warmupVPNTunnel()

	m.mu.Lock()
	m.state = StateConnected
	m.connectedAt = time.Now()
	m.mu.Unlock()

	m.store.UpdateSettings(func(s *storage.Settings) {
		s.LastServerID = serverID
	})

	mode := "proxy"
	if settings.TUNMode {
		mode = "tun"
	}
	log.Printf("Connected to %s via %s (%s mode)", server.Name, server.Protocol, mode)
	return nil
}

func (m *Manager) Disconnect() error {
	m.mu.Lock()
	if m.state == StateDisconnected {
		m.mu.Unlock()
		return nil
	}
	m.state = StateDisconnecting
	m.mu.Unlock()

	if err := m.runner.Stop(); err != nil {
		log.Printf("Stop sing-box: %v", err)
	}

	// Always clear system proxy on disconnect
	ClearSystemProxy()

	m.mu.Lock()
	m.state = StateDisconnected
	m.current = nil
	m.connectedAt = time.Time{}
	m.mu.Unlock()

	log.Println("Disconnected")
	return nil
}

func (m *Manager) Stop() {
	if m.cancelPing != nil {
		m.cancelPing()
	}
	if m.subCancel != nil {
		m.subCancel()
	}
	m.Disconnect()
}

func (m *Manager) UpdateSubscription(subURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	settings := m.store.GetSettings()
	if subURL == "" {
		subURL = settings.SubscriptionURL
	}
	if subURL == "" {
		return fmt.Errorf("no subscription URL configured")
	}

	hwid := GetHWIDInfo()
	headers := subscription.HWIDHeaders{
		HWID:  hwid.HWID,
		OS:    hwid.OS,
		OSVer: hwid.OSVer,
		Model: hwid.Model,
	}
	servers, err := subscription.Fetch(ctx, subURL, headers)
	if err != nil {
		return fmt.Errorf("fetch subscription: %w", err)
	}

	m.mu.Lock()
	// Preserve latency from existing servers
	latencyMap := make(map[string]int)
	for _, s := range m.servers {
		latencyMap[s.Address+":"+fmt.Sprint(s.Port)] = s.Latency
	}
	for i := range servers {
		key := servers[i].Address + ":" + fmt.Sprint(servers[i].Port)
		if lat, ok := latencyMap[key]; ok {
			servers[i].Latency = lat
		}
	}
	m.servers = servers
	m.mu.Unlock()

	if subURL != settings.SubscriptionURL {
		m.store.UpdateSettings(func(s *storage.Settings) {
			s.SubscriptionURL = subURL
		})
	}

	log.Printf("Subscription updated: %d servers", len(servers))
	return nil
}

func (m *Manager) PingServer(serverID string) (int, error) {
	m.mu.RLock()
	var target *subscription.Server
	for i := range m.servers {
		if m.servers[i].ID == serverID {
			target = &m.servers[i]
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

	var wg sync.WaitGroup
	results := make([]int, len(servers))

	for i, s := range servers {
		wg.Add(1)
		go func(idx int, srv subscription.Server) {
			defer wg.Done()
			results[idx] = pingTCP(srv.Address, srv.Port)
		}(i, s)
	}
	wg.Wait()

	m.mu.Lock()
	for i := range m.servers {
		for j, s := range servers {
			if m.servers[i].ID == s.ID {
				m.servers[i].Latency = results[j]
				break
			}
		}
	}
	m.mu.Unlock()
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

		// Initial update
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

func (m *Manager) setError(err error) {
	m.mu.Lock()
	m.state = StateDisconnected
	m.current = nil
	m.mu.Unlock()
	log.Printf("Connection error: %v", err)
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

// GetHWID returns the device HWID info for display in UI
func (m *Manager) GetHWID() HWIDInfo {
	return GetHWIDInfo()
}

// warmupVPNTunnel pre-establishes the VPN connection and then sets the
// RustleBoost adapter's Windows network category to Private.
// This changes the tray status from "Без доступа к Интернету" to
// "Подключение установлено" without waiting for Windows NLA re-check.
func warmupVPNTunnel() {
	// Wait until sing-box mixed port is accepting connections (up to 3s)
	for i := 0; i < 30; i++ {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:2080", 100*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	proxyURL, err := url.Parse("http://127.0.0.1:2080")
	if err != nil {
		return
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   8 * time.Second,
	}
	resp, err := client.Head("http://connectivitycheck.gstatic.com/generate_204")
	if err == nil {
		resp.Body.Close()
		log.Println("[warmup] VPN tunnel warmed up successfully")
	} else {
		log.Printf("[warmup] warmup failed (non-critical): %v", err)
	}

	// Mark the RustleBoost adapter as a Private network so Windows NLA
	// shows "Подключение установлено" instead of "Без доступа к Интернету".
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
