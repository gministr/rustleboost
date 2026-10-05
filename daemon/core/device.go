package core

// Регистрация устройства на сервере RustleBoost — чтобы клиент Windows
// появлялся в админ-панели рядом с Android и iOS.
//
// Контракт тот же, что у приложений: регистрация, обновление ключей подписок
// и heartbeat раз в минуту с состоянием подключения и счётчиками трафика.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/vpnclient/daemon/subscription"
)

const (
	deviceAPIBase    = "https://auth.lindavpn.com"
	devicePlatform   = "windows"
	deviceAppVersion = "1.5.0"
	heartbeatEvery   = time.Minute
)

type deviceState struct {
	DeviceID string `json:"deviceId"`
	Token    string `json:"token"`
}

// DeviceClient держит идентификатор устройства и связь с сервером.
type DeviceClient struct {
	mu     sync.Mutex
	path   string
	state  deviceState
	hwid   HWIDInfo
	client *http.Client
}

func NewDeviceClient(dataDir string) *DeviceClient {
	d := &DeviceClient{
		path:   filepath.Join(dataDir, "device.json"),
		hwid:   GetHWIDInfo(),
		client: &http.Client{Timeout: 20 * time.Second},
	}
	if data, err := os.ReadFile(d.path); err == nil {
		_ = json.Unmarshal(data, &d.state)
	}
	return d
}

// post отправляет JSON на сервер и возвращает разобранный ответ.
func (d *DeviceClient) post(ctx context.Context, path string, body any, out any, auth bool) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deviceAPIBase+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-App-Key", subscription.CatalogAppKey)
	if auth {
		d.mu.Lock()
		token := d.state.Token
		d.mu.Unlock()
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s: %d %s", path, resp.StatusCode, string(msg))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Register заводит устройство, если оно ещё не зарегистрировано, и передаёт
// ключи подписок, чтобы панель видела, каким подпискам принадлежит устройство.
func (d *DeviceClient) Register(ctx context.Context, subscriptions []string) error {
	d.mu.Lock()
	registered := d.state.Token != ""
	d.mu.Unlock()
	if registered {
		return d.UpdateSubscriptions(ctx, subscriptions)
	}

	var answer struct {
		DeviceID string `json:"deviceId"`
		Token    string `json:"token"`
	}
	err := d.post(ctx, "/v1/device/register", map[string]any{
		"hwid":          d.hwid.HWID,
		"platform":      devicePlatform,
		"model":         d.hwid.Model,
		"osVersion":     d.hwid.OSVer,
		"appVersion":    deviceAppVersion,
		"subscriptions": subscriptions,
	}, &answer, false)
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.state = deviceState{DeviceID: answer.DeviceID, Token: answer.Token}
	data, _ := json.Marshal(d.state)
	d.mu.Unlock()
	return os.WriteFile(d.path, data, 0o600)
}

// UpdateSubscriptions сообщает серверу актуальный набор ключей подписок.
func (d *DeviceClient) UpdateSubscriptions(ctx context.Context, subscriptions []string) error {
	return d.post(ctx, "/v1/device/update", map[string]any{
		"appVersion":    deviceAppVersion,
		"subscriptions": subscriptions,
	}, nil, true)
}

// Heartbeat отправляет состояние подключения и счётчики трафика.
func (d *DeviceClient) Heartbeat(ctx context.Context, sessionID string, connected bool, server string, up, down int64) error {
	return d.post(ctx, "/v1/device/heartbeat", map[string]any{
		"sessionId": sessionID,
		"connected": connected,
		"server":    server,
		"bytesUp":   up,
		"bytesDown": down,
	}, nil, true)
}

// Registered — есть ли у устройства токен.
func (d *DeviceClient) Registered() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state.Token != ""
}
