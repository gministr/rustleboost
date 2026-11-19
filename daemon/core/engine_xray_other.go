//go:build !windows

package core

import "net/netip"

// Брандмауэр и закрепление маршрутов есть только на Windows; на других
// системах служба собирается для разработки и тестов.

func enableTunFirewall(bool, []netip.Addr) error { return nil }
func disableTunFirewall()          {}
func tunFirewallHolds() bool       { return false }

func (m *Manager) pinServer([]string) error { return nil }
func (m *Manager) unpinServer()             {}
