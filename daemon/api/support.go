package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/vpnclient/daemon/core"
)

// Поддержка и сообщения от сервиса (core/support.go).

func supportError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	if errors.Is(err, core.ErrSupportTooMany) {
		status = http.StatusTooManyRequests
	} else if errors.Is(err, core.ErrSupportClosed) {
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (s *Server) handleSupportInbox(w http.ResponseWriter, r *http.Request) {
	inbox, err := s.manager.SupportInbox()
	if err != nil {
		supportError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, inbox)
}

func (s *Server) handleSupportTickets(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Body == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "пустое сообщение"})
			return
		}
		ticket, err := s.manager.SupportCreateTicket(body.Body)
		if err != nil {
			supportError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, ticket)
		return
	}
	tickets, err := s.manager.SupportTickets()
	if err != nil {
		supportError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tickets)
}

// /api/support/ticket?id=N — переписка (GET) или ответ (POST {"body"}).
func (s *Server) handleSupportTicket(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "нет номера обращения"})
		return
	}
	if r.Method == http.MethodPost {
		var body struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Body == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "пустое сообщение"})
			return
		}
		ticket, err := s.manager.SupportReply(id, body.Body)
		if err != nil {
			supportError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, ticket)
		return
	}
	ticket, err := s.manager.SupportTicket(id)
	if err != nil {
		supportError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ticket)
}

// /api/support/notices — GET список, POST «прочитано», DELETE ?id=N (или все).
func (s *Server) handleSupportNotices(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		if err := s.manager.SupportMarkNoticesRead(); err != nil {
			supportError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case http.MethodDelete:
		id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if err := s.manager.SupportDeleteNotices(id); err != nil {
			supportError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		notices, err := s.manager.SupportNotices()
		if err != nil {
			supportError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, notices)
	}
}

// /api/version — версия службы и ядер для экрана «О приложении».
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.manager.Versions())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
