package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Settings struct {
	SubscriptionURL string `json:"subscription_url"`
	LastServerID    string `json:"last_server_id"`
	// LastServerKey survives a subscription refresh, which reissues every
	// server ID; auto-connect falls back to it when the ID no longer exists.
	LastServerKey  string `json:"last_server_key"`
	AutoConnect    bool   `json:"auto_connect"`
	AutoUpdate     bool   `json:"auto_update"`
	UpdateInterval int    `json:"update_interval"`
	DNSMode        string `json:"dns_mode"`
	KillSwitch     bool   `json:"kill_switch"`
	AllowLAN       bool   `json:"allow_lan"`
	Language       string `json:"language"`
	TUNMode        bool   `json:"tun_mode"`
	RouteMode      string `json:"route_mode"` // "all" | "ru" | "cn"
	// RouterMode picks which core carries proxy traffic. Which one gets
	// through varies by network — a censor can fingerprint and block one
	// implementation's handshake while letting the other through — so this
	// is a user choice, not something the app should decide on its own.
	RouterMode string `json:"router_mode"` // "auto" | "singbox" | "xray"
}

type Store struct {
	mu        sync.RWMutex
	path      string
	cachePath string
	settings  Settings
}

func New(path string) *Store {
	return &Store{
		path:      path,
		cachePath: filepath.Join(filepath.Dir(path), "subscription-cache.json"),
		settings: Settings{
			AutoUpdate:     true,
			UpdateInterval: 12,
			DNSMode:        "local",
			Language:       "ru",
			TUNMode:        true,
			// Russian sites go direct by default: they are the ones that get
			// slower or refuse foreign addresses outright when tunnelled.
			RouteMode: "ru",
			// Xray by default. It is the path that has actually connected on
			// every machine tested so far; sing-box works for some people and
			// not others, and there is no way to tell which from here. The
			// switch in Settings is there for anyone it doesn't suit.
			RouterMode: "xray",
		},
	}
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &s.settings)
}

func (s *Store) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := json.MarshalIndent(s.settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0600)
}

func (s *Store) GetSettings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

func (s *Store) UpdateSettings(fn func(*Settings)) error {
	s.mu.Lock()
	fn(&s.settings)
	s.mu.Unlock()
	return s.Save()
}

// SaveCache persists the last successful subscription refresh so the app can
// show its server list and traffic figures immediately on the next launch,
// rather than an empty screen until the panel answers.
func (s *Store) SaveCache(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.WriteFile(s.cachePath, data, 0600)
}

func (s *Store) LoadCache() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return os.ReadFile(s.cachePath)
}
