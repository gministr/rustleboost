package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/vpnclient/daemon/core"
	"github.com/vpnclient/daemon/storage"
)

type Server struct {
	manager *core.Manager
	store   *storage.Store
	addr    string
	mux     *http.ServeMux
}

func NewServer(manager *core.Manager, store *storage.Store, addr string) *Server {
	s := &Server{
		manager: manager,
		store:   store,
		addr:    addr,
		mux:     http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

func (s *Server) Start() error {
	log.Printf("API listening on http://%s", s.addr)
	return http.ListenAndServe(s.addr, corsMiddleware(s.mux))
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/api/status", s.handleStatus)
	s.mux.HandleFunc("/api/connect", s.handleConnect)
	s.mux.HandleFunc("/api/disconnect", s.handleDisconnect)
	s.mux.HandleFunc("/api/servers", s.handleServers)
	s.mux.HandleFunc("/api/subscription", s.handleSubscription)
	s.mux.HandleFunc("/api/ping", s.handlePing)
	s.mux.HandleFunc("/api/ping-all", s.handlePingAll)
	s.mux.HandleFunc("/api/settings", s.handleSettings)
	s.mux.HandleFunc("/api/hwid", s.handleHWID)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	jsonResponse(w, s.manager.GetStatus())
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ServerID string `json:"server_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ServerID == "" {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := s.manager.Connect(req.ServerID); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]string{"status": "connected"})
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := s.manager.Disconnect(); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]string{"status": "disconnected"})
}

func (s *Server) handleServers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	jsonResponse(w, s.manager.GetServers())
}

func (s *Server) handleSubscription(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings := s.store.GetSettings()
		jsonResponse(w, map[string]interface{}{
			"url":     settings.SubscriptionURL,
			"servers": len(s.manager.GetServers()),
			"info":    s.manager.GetInfo(),
		})

	case http.MethodPost:
		var req struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		if err := s.manager.UpdateSubscription(req.URL); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		jsonResponse(w, map[string]interface{}{
			"status":  "updated",
			"servers": len(s.manager.GetServers()),
			"info":    s.manager.GetInfo(),
		})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ServerID string `json:"server_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	latency, err := s.manager.PingServer(req.ServerID)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}

	jsonResponse(w, map[string]int{"latency": latency})
}

func (s *Server) handlePingAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	go s.manager.PingAll()
	jsonResponse(w, map[string]string{"status": "pinging"})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonResponse(w, s.store.GetSettings())

	case http.MethodPost:
		var incoming storage.Settings
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		s.store.UpdateSettings(func(s *storage.Settings) {
			if incoming.DNSMode != "" {
				s.DNSMode = incoming.DNSMode
			}
			if incoming.Language != "" {
				s.Language = incoming.Language
			}
			if incoming.UpdateInterval > 0 {
				s.UpdateInterval = incoming.UpdateInterval
			}
			s.AutoConnect = incoming.AutoConnect
			s.AutoUpdate = incoming.AutoUpdate
			s.KillSwitch = incoming.KillSwitch
			s.AllowLAN = incoming.AllowLAN
			s.TUNMode = incoming.TUNMode
			if incoming.RouteMode != "" {
				s.RouteMode = incoming.RouteMode
			}
		})

		jsonResponse(w, s.store.GetSettings())

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func jsonResponse(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *Server) handleHWID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	info := s.manager.GetHWID()
	jsonResponse(w, map[string]string{
		"hwid":  info.HWID,
		"os":    info.OS,
		"ver":   info.OSVer,
		"model": info.Model,
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if strings.HasPrefix(origin, "tauri://") || strings.HasPrefix(origin, "https://tauri.localhost") || origin == "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
