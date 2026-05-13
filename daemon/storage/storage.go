package storage

import (
	"encoding/json"
	"os"
	"sync"
)

type Settings struct {
	SubscriptionURL string `json:"subscription_url"`
	LastServerID    string `json:"last_server_id"`
	AutoConnect     bool   `json:"auto_connect"`
	AutoUpdate      bool   `json:"auto_update"`
	UpdateInterval  int    `json:"update_interval"`
	DNSMode         string `json:"dns_mode"`
	KillSwitch      bool   `json:"kill_switch"`
	AllowLAN        bool   `json:"allow_lan"`
	Language        string `json:"language"`
	TUNMode         bool   `json:"tun_mode"`
	RouteMode       string `json:"route_mode"` // "all" | "ru" | "cn"
}

type Store struct {
	mu       sync.RWMutex
	path     string
	settings Settings
}

func New(path string) *Store {
	return &Store{
		path: path,
		settings: Settings{
			AutoUpdate:     true,
			UpdateInterval: 12,
			DNSMode:        "local",
			Language:       "ru",
			TUNMode:        true,
			RouteMode:      "all",
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
