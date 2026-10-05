package core

// Ядро WDTT как отдельный режим подключения: серверы группы RustleBoost.
//
// Ядро поднимает SOCKS5 на порту, который ждёт sing-box, — так же, как Xray или
// WARP. Общий код транспорта — в third_party/warpcore (копия из RustleBoost).

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/vpnclient/daemon/subscription"
	"github.com/vpnclient/daemon/third_party/warpcore"
)

// WdttRunner держит запущенный WDTT. Один на демон: SOCKS один.
type WdttRunner struct {
	mu      sync.Mutex
	running bool
}

// Start поднимает WDTT к серверу server на порту socksPort.
// Блокирует поток: получение конфигурации от сервера занимает секунды.
func (r *WdttRunner) Start(dataDir string, server subscription.Server, socksPort int, deviceID string, verbose bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return fmt.Errorf("wdtt: уже запущен")
	}
	if server.Engine != subscription.EngineWdtt {
		return fmt.Errorf("wdtt: сервер не WDTT")
	}
	cfg, err := wdttTransportConfig(dataDir, server, deviceID, verbose)
	if err != nil {
		return err
	}
	cfg["socksPort"] = socksPort
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := warpcore.VKTurnStart(string(data)); err != nil {
		return err
	}
	r.running = true
	return nil
}

// Stop снимает WDTT. Безопасно вызывать и без запуска.
func (r *WdttRunner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running && !warpcore.VKTurnIsRunning() {
		return
	}
	warpcore.VKTurnStop()
	r.running = false
}

// IsRunning — поднят ли WDTT. Сторож опрашивает его, как Xray.
func (r *WdttRunner) IsRunning() bool {
	return warpcore.VKTurnIsRunning()
}
