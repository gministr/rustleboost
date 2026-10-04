package core

// Движок WARP внутри демона.
//
// Встроенное ядро (third_party/warpcore) поднимает WDTT и поверх него WARP и
// открывает SOCKS5 на порту, который ждёт sing-box, — то же место, где раньше
// сидел Xray. Здесь только сборка конфигурации и жизненный цикл.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/vpnclient/daemon/subscription"
	"github.com/vpnclient/daemon/third_party/warpcore"
)

const (
	warpRegisterURL = "https://auth.lindavpn.com/v1/warp/register"
	// warpMTU — внутренний MTU; см. wg.ChainedMTU в общем ядре.
	warpMTU = 1200
	// wdttMaxConnections — предел соединений; на ПК памяти хватает с запасом.
	wdttMaxConnections = 512
	// wdttDefaultWorkers и wdttDefaultObfs — то, что приложение берёт по умолчанию.
	wdttDefaultWorkers = 24
	wdttDefaultObfs    = "audio"
)

// WarpRunner держит запущенный WARP. Один на демон: сокет SOCKS один.
type WarpRunner struct {
	mu      sync.Mutex
	running bool
}

// Start поднимает WARP через WDTT-сервер transport на порту socksPort.
// Блокирует поток: регистрация и поднятие WDTT занимают секунды.
func (r *WarpRunner) Start(dataDir string, transport subscription.Server, socksPort int, deviceID string, verbose bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return fmt.Errorf("warp: уже запущен")
	}

	cfg, err := warpConfigJSON(dataDir, transport, socksPort, deviceID, verbose)
	if err != nil {
		return err
	}
	if err := warpcore.WarpStart(cfg); err != nil {
		return err
	}
	r.running = true
	return nil
}

// Stop снимает WARP и WDTT. Безопасно вызывать и без запуска.
func (r *WarpRunner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running && !warpcore.WarpIsRunning() {
		return
	}
	warpcore.WarpStop()
	r.running = false
}

// IsRunning — поднят ли WARP. Сторож опрашивает его, как Xray.
func (r *WarpRunner) IsRunning() bool {
	return warpcore.WarpIsRunning()
}

// warpConfigJSON собирает конфигурацию для ядра: WARP и вложенный WDTT.
func warpConfigJSON(dataDir string, transport subscription.Server, socksPort int, deviceID string, verbose bool) (string, error) {
	if transport.Engine != subscription.EngineWdtt {
		return "", fmt.Errorf("warp: транспорт не WDTT")
	}
	wdtt, err := wdttTransportConfig(dataDir, transport, deviceID, verbose)
	if err != nil {
		return "", err
	}
	cfg := map[string]any{
		"socksPort":   socksPort,
		"stateDir":    filepath.Join(dataDir, "warp"),
		"routing":     []any{},
		"mtu":         warpMTU,
		"verbose":     verbose,
		"registerUrl": warpRegisterURL,
		"registerKey": subscription.CatalogAppKey,
		"transport":   wdtt,
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// wdttTransportConfig — конфигурация WDTT в формате ядра (vkturn.Config).
// Капча: rjs — решение через Go без окна; при неудаче ядро откатывается на
// WebView-капчу, которой на ПК нет, и тогда подключение честно сообщит об ошибке.
func wdttTransportConfig(dataDir string, transport subscription.Server, deviceID string, verbose bool) (map[string]any, error) {
	p := transport.Params
	password := p["password"]
	hashes := p["hashes"]
	if password == "" || hashes == "" {
		return nil, fmt.Errorf("wdtt: в ссылке нет пароля или хешей")
	}
	workers := wdttDefaultWorkers
	if w, err := strconv.Atoi(p["workers"]); err == nil && w > 0 {
		workers = w
	}
	obfs := p["obfs"]
	if obfs == "" {
		obfs = wdttDefaultObfs
	}
	cfg := map[string]any{
		"peerAddress":    fmt.Sprintf("%s:%d", transport.Address, transport.Port),
		"vkHashes":       hashes,
		"password":       password,
		"deviceId":       deviceID,
		"socksPort":      0, // подменяется ядром на свободный порт
		"workers":        workers,
		"obfsMode":       obfs,
		"captchaMode":    "rjs",
		"credentialsDir": filepath.Join(dataDir, "wdtt-credentials"),
		"maxConnections": wdttMaxConnections,
		"verbose":        verbose,
	}
	if v := p["turnHost"]; v != "" {
		cfg["turnHost"] = v
	}
	if v := p["turnPort"]; v != "" {
		cfg["turnPort"] = v
	}
	return cfg, nil
}
