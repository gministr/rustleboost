package core

// Подключение к обычным серверам (VLESS, VMess, Trojan, Shadowsocks, SOCKS,
// Hysteria2) — как в приложениях iOS/Android/Mac: одно ядро Xray прямо в
// процессе службы, адаптер Wintun держит сам Xray (встроенный вход TUN),
// конфигурация та же, что на телефоне (config.GenerateTunXray).
//
// Раньше здесь работали две отдельные программы — sing-box (адаптер, DNS,
// маршруты, сам протокол для большинства серверов) и xray.exe (только для
// XHTTP) — и два разных конфига. Поведение расходилось с телефонами, а
// подключение ждало запуска двух процессов.

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vpnclient/daemon/config"
	"github.com/vpnclient/daemon/subscription"
	"rustleboost/rustlecore"
)

// hysteriaSocksPort — локальный SOCKS ядра Hysteria2; Xray шлёт туда трафик.
const hysteriaSocksPort = config.XraySocksPort + 1

// xrayLogName — журнал ядра (предупреждения и ошибки) в папке данных.
const xrayLogName = "xray-core.log"

// xraySession — то, что поднято для текущего подключения через Xray.
type xraySession struct {
	cfg      string
	hysteria bool
	tun      bool
}

// connectXray поднимает ядро для сервера и возвращает название движка.
func (m *Manager) connectXray(selected subscription.Server, opts config.Options, killSwitch bool) (string, error) {
	engine := "Xray"
	upstream := 0
	if config.IsHysteria2(selected) {
		if err := m.startHysteria(selected, opts); err != nil {
			return "", err
		}
		upstream = hysteriaSocksPort
		engine = "Hysteria2"
	} else if !config.IsXrayServer(selected) {
		return "", fmt.Errorf("протокол %s пока не поддерживается в приложении для Windows", selected.Protocol)
	}

	logPath := filepath.Join(m.dataDir, xrayLogName)
	_ = os.Remove(logPath)
	cfgJSON, err := config.GenerateTunXray(config.TunXrayInput{
		Server:            selected,
		Opts:              opts,
		LogPath:           logPath,
		UpstreamSocksPort: upstream,
	})
	if err != nil {
		m.stopHysteria()
		return "", fmt.Errorf("настройки сервера: %w", err)
	}

	started := time.Now()
	if err := rustlecore.XrayStart(string(cfgJSON)); err != nil {
		m.stopHysteria()
		return "", fmt.Errorf("ядро Xray: %s", humanizeXrayError(err))
	}
	log.Printf("[xray] ядро запущено за %d мс", time.Since(started).Milliseconds())

	session := &xraySession{cfg: string(cfgJSON), hysteria: upstream > 0, tun: opts.TUNMode}
	if opts.TUNMode {
		if err := enableKillSwitchFirewall(killSwitch, xrayTunDNS()); err != nil {
			// Туннель работает и без блокировки — пишем, но не роняем
			// подключение.
			log.Printf("[firewall] Kill Switch не включился: %v", err)
		}
	} else {
		SetSystemProxy(fmt.Sprintf("127.0.0.1:%d", config.MixedPort))
	}

	m.mu.Lock()
	m.xraySession = session
	m.mu.Unlock()
	return engine, nil
}

// restartXray перезапускает ядро с той же конфигурацией (сторож).
func (m *Manager) restartXray() error {
	m.mu.RLock()
	session := m.xraySession
	m.mu.RUnlock()
	if session == nil {
		return fmt.Errorf("нет активного подключения")
	}
	rustlecore.XrayStop()
	if err := rustlecore.XrayStart(session.cfg); err != nil {
		return fmt.Errorf("ядро Xray: %s", humanizeXrayError(err))
	}
	return nil
}

// stopXray снимает ядро, Hysteria2, системный прокси и защиту DNS.
// keepKillSwitch оставляет блокировку брандмауэра (аварийный Kill Switch).
func (m *Manager) stopXray(keepKillSwitch bool) {
	m.mu.Lock()
	session := m.xraySession
	m.xraySession = nil
	m.mu.Unlock()

	rustlecore.XrayStop()
	m.stopHysteria()
	if session != nil && !session.tun {
		ClearSystemProxy()
	}
	if !keepKillSwitch {
		disableTunFirewall()
	}
}

// xrayAlive — работает ли ядро (и Hysteria2, если сервер его).
func (m *Manager) xrayAlive() bool {
	m.mu.RLock()
	session := m.xraySession
	m.mu.RUnlock()
	if session == nil || !rustlecore.XrayIsRunning() {
		return false
	}
	return !session.hysteria || rustlecore.IsRunning()
}

// xrayLogTail — последние строки журнала ядра.
func (m *Manager) xrayLogTail(lines int) string {
	data, err := os.ReadFile(filepath.Join(m.dataDir, xrayLogName))
	if err != nil {
		return ""
	}
	all := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n")
}

// startHysteria поднимает ядро Hysteria2 из rustlecore с локальным SOCKS.
//
// Своего привязывания к физической сети у Hysteria2 нет, поэтому адреса
// сервера закрепляются за физическим шлюзом до подъёма адаптера — иначе его
// QUIC ушёл бы в собственный туннель.
func (m *Manager) startHysteria(server subscription.Server, opts config.Options) error {
	p := server.Params
	cfg := map[string]any{
		"server":    net.JoinHostPort(server.Address, strconv.Itoa(server.Port)),
		"auth":      p["password"],
		"sni":       p["sni"],
		"insecure":  p["insecure"] == "1" || strings.EqualFold(p["insecure"], "true"),
		"socksPort": hysteriaSocksPort,
	}
	if p["obfs"] != "" {
		cfg["obfsType"] = p["obfs"]
		cfg["obfsPass"] = p["obfs-password"]
	}
	if opts.TUNMode {
		if err := m.pinServer(opts.ServerIPs); err != nil {
			return fmt.Errorf("Hysteria2: маршрут к серверу мимо туннеля: %w", err)
		}
	}
	data, _ := json.Marshal(cfg)
	if err := rustlecore.Start(string(data)); err != nil {
		m.unpinServer()
		return fmt.Errorf("Hysteria2: %w", err)
	}
	if err := waitForPort(hysteriaSocksPort, 5*time.Second); err != nil {
		rustlecore.Stop()
		m.unpinServer()
		return fmt.Errorf("Hysteria2: порт %d не открылся: %w", hysteriaSocksPort, err)
	}
	return nil
}

func (m *Manager) stopHysteria() {
	if rustlecore.IsRunning() {
		rustlecore.Stop()
	}
	m.unpinServer()
}

// humanizeXrayError делает ошибку ядра понятной человеку, оставляя суть.
func humanizeXrayError(err error) string {
	text := err.Error()
	switch {
	case strings.Contains(text, "wintun"), strings.Contains(text, "Wintun"):
		return "не удалось создать сетевой адаптер (Wintun). Перезапустите приложение от имени администратора. " + text
	case strings.Contains(text, "Access is denied"), strings.Contains(text, "Отказано в доступе"):
		return "нет прав администратора для сетевого адаптера. " + text
	}
	return text
}

// xrayTunDNS — DNS адаптера Xray (config.TunDNS) для брандмауэра.
func xrayTunDNS() []netip.Addr {
	var out []netip.Addr
	for _, s := range config.TunDNSAddrs() {
		if addr, err := netip.ParseAddr(s); err == nil {
			out = append(out, addr)
		}
	}
	return out
}

// enableKillSwitchFirewall — правила брандмауэра только для Kill Switch.
//
// Отдельной «защиты DNS» (запрет порта 53 мимо туннеля) в обычном режиме
// нет — так проверено на настоящей Windows (стенд, scripts/lab): запрет
// закрывает и службу DNS самой Windows, через которую ядро находит адреса
// серверов и релеев ВК, когда переподключается, — и переподключение
// упиралось бы в недоступный DNS. Подмена ответов провайдером на обычных
// серверах не страшна: Xray определяет сайт по самому соединению (sniffing)
// и открывает его по имени на стороне сервера.
func enableKillSwitchFirewall(killSwitch bool, dns []netip.Addr) error {
	if !killSwitch {
		return nil
	}
	return enableTunFirewall(true, dns)
}
