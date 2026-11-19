package core

// Поддержка и сообщения от сервиса — как в приложениях iOS/Android/Mac:
// обращения (тикеты) с перепиской, сообщения рассылок из админки и счётчик
// непрочитанного. Те же адреса сервера (/v1/tickets, /v1/notices, /v1/inbox)
// и тот же токен устройства, что у регистрации и сердцебиения.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/vpnclient/daemon/subscription"
	"rustleboost/rustlecore"
)

// Ошибки, которые интерфейс показывает своими словами.
var (
	ErrSupportTooMany = errors.New("открытых обращений слишком много — дождитесь ответа по ним")
	ErrSupportClosed  = errors.New("обращение уже закрыто")
)

// SupportMessage — сообщение в обращении.
type SupportMessage struct {
	ID        int64     `json:"id"`
	Author    string    `json:"author"` // "user" | "admin"
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

// SupportTicket — обращение.
type SupportTicket struct {
	ID        int64            `json:"id"`
	Status    string           `json:"status"` // "open" | "closed"
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
	ClosedAt  *time.Time       `json:"closedAt,omitempty"`
	Unread    bool             `json:"unread"`
	Preview   string           `json:"preview"`
	Messages  []SupportMessage `json:"messages,omitempty"`
}

// SupportNotice — сообщение от сервиса (рассылка из админки).
type SupportNotice struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	Read      bool      `json:"read"`
}

// SupportInbox — непрочитанное.
type SupportInbox struct {
	UnreadTickets int `json:"unreadTickets"`
	UnreadNotices int `json:"unreadNotices"`
}

// call — запрос к серверу от имени устройства.
func (d *DeviceClient) call(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, deviceAPIBase+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-App-Key", subscription.CatalogAppKey)
	d.mu.Lock()
	token := d.state.Token
	d.mu.Unlock()
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("нет связи с сервером поддержки")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return ErrSupportTooMany
	case resp.StatusCode == http.StatusConflict:
		return ErrSupportClosed
	case resp.StatusCode/100 != 2:
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("сервер поддержки ответил %d %s", resp.StatusCode, bytes.TrimSpace(msg))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// ensureDevice регистрирует устройство, если этого ещё не случилось: без
// токена сервер поддержки не знает, чьи это обращения.
func (m *Manager) ensureDevice(ctx context.Context) error {
	if m.device.Registered() {
		return nil
	}
	return m.device.Register(ctx, m.subscriptionKeys())
}

func supportContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 20*time.Second)
}

func (m *Manager) SupportInbox() (SupportInbox, error) {
	ctx, cancel := supportContext()
	defer cancel()
	var out SupportInbox
	if err := m.ensureDevice(ctx); err != nil {
		return out, err
	}
	err := m.device.call(ctx, http.MethodGet, "/v1/inbox", nil, &out)
	return out, err
}

func (m *Manager) SupportTickets() ([]SupportTicket, error) {
	ctx, cancel := supportContext()
	defer cancel()
	out := []SupportTicket{}
	if err := m.ensureDevice(ctx); err != nil {
		return out, err
	}
	err := m.device.call(ctx, http.MethodGet, "/v1/tickets", nil, &out)
	return out, err
}

func (m *Manager) SupportTicket(id int64) (SupportTicket, error) {
	ctx, cancel := supportContext()
	defer cancel()
	var out SupportTicket
	if err := m.ensureDevice(ctx); err != nil {
		return out, err
	}
	err := m.device.call(ctx, http.MethodGet, fmt.Sprintf("/v1/tickets/%d", id), nil, &out)
	return out, err
}

func (m *Manager) SupportCreateTicket(text string) (SupportTicket, error) {
	ctx, cancel := supportContext()
	defer cancel()
	var out SupportTicket
	if err := m.ensureDevice(ctx); err != nil {
		return out, err
	}
	err := m.device.call(ctx, http.MethodPost, "/v1/tickets", map[string]string{"body": text}, &out)
	return out, err
}

func (m *Manager) SupportReply(id int64, text string) (SupportTicket, error) {
	ctx, cancel := supportContext()
	defer cancel()
	var out SupportTicket
	if err := m.ensureDevice(ctx); err != nil {
		return out, err
	}
	err := m.device.call(ctx, http.MethodPost, fmt.Sprintf("/v1/tickets/%d/messages", id), map[string]string{"body": text}, &out)
	return out, err
}

func (m *Manager) SupportNotices() ([]SupportNotice, error) {
	ctx, cancel := supportContext()
	defer cancel()
	out := []SupportNotice{}
	if err := m.ensureDevice(ctx); err != nil {
		return out, err
	}
	err := m.device.call(ctx, http.MethodGet, "/v1/notices", nil, &out)
	return out, err
}

// SupportMarkNoticesRead отмечает все сообщения прочитанными.
func (m *Manager) SupportMarkNoticesRead() error {
	ctx, cancel := supportContext()
	defer cancel()
	if err := m.ensureDevice(ctx); err != nil {
		return err
	}
	return m.device.call(ctx, http.MethodPost, "/v1/notices", map[string]string{}, nil)
}

// SupportDeleteNotices стирает одно сообщение (id > 0) или все (id == 0).
func (m *Manager) SupportDeleteNotices(id int64) error {
	ctx, cancel := supportContext()
	defer cancel()
	if err := m.ensureDevice(ctx); err != nil {
		return err
	}
	path := "/v1/notices"
	if id > 0 {
		path = fmt.Sprintf("/v1/notices/%d", id)
	}
	return m.device.call(ctx, http.MethodDelete, path, nil, nil)
}

// subscriptionKeys — короткие ключи подписок для регистрации устройства.
func (m *Manager) subscriptionKeys() []string {
	var keys []string
	if key := subscription.ShortKey(m.store.GetSettings().SubscriptionURL); key != "" {
		keys = append(keys, key)
	}
	return keys
}

// Versions — версии приложения и ядер для экрана «О приложении».
func (m *Manager) Versions() map[string]string {
	return map[string]string{
		"app":       AppVersion,
		"xray":      rustlecore.XrayVersion(),
		"hysteria2": rustlecore.Hysteria2Version(),
	}
}
