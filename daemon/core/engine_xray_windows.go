//go:build windows

package core

import (
	"fmt"
	"net"
	"net/netip"
	"path/filepath"

	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"

	"github.com/vpnclient/daemon/config"
	firewall "github.com/vpnclient/daemon/third_party/wgfirewall"
)

// enableTunFirewall включает правила брандмауэра на время подключения.
//
// Обычный режим — только защита DNS: запросы имён уходят лишь на DNS
// туннеля (см. firewall.EnableDNSProtection). Kill Switch — всё мимо
// туннеля блокируется, как в официальном клиенте WireGuard; при обрыве
// правила остаются, пока человек не нажмёт «Отключить».
func enableTunFirewall(killSwitch bool, dns []netip.Addr) error {
	if firewall.Enabled() {
		firewall.DisableFirewall()
	}
	if !killSwitch {
		return firewall.EnableDNSProtection(dns)
	}
	iface, err := net.InterfaceByName(config.TunAdapterName)
	if err != nil {
		return fmt.Errorf("адаптер %s не найден: %w", config.TunAdapterName, err)
	}
	luid, err := winipcfg.LUIDFromIndex(uint32(iface.Index))
	if err != nil {
		return err
	}
	return firewall.EnableFirewall(uint64(luid), false, dns)
}

func disableTunFirewall() { firewall.DisableFirewall() }

// tunFirewallHolds — держит ли брандмауэр блокировку (Kill Switch).
func tunFirewallHolds() bool { return firewall.Enabled() }

// pinServer закрепляет адреса сервера за физическим шлюзом (Hysteria2).
func (m *Manager) pinServer(ips []string) error {
	m.unpinServer()
	journal := filepath.Join(m.dataDir, "server-pins.json")
	routes := &windowsRoutes{}
	cleanupPins(routes, journal)
	p := newPinner(routes, journal, nil)
	for _, s := range ips {
		addr, err := netip.ParseAddr(s)
		if err != nil || !addr.Is4() {
			continue
		}
		if err := p.ensure(addr); err != nil {
			p.clear()
			return err
		}
	}
	m.mu.Lock()
	m.serverPins = p
	m.mu.Unlock()
	return nil
}

func (m *Manager) unpinServer() {
	m.mu.Lock()
	p := m.serverPins
	m.serverPins = nil
	m.mu.Unlock()
	if p != nil {
		p.clear()
	}
}
