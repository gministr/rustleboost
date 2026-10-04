package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vpnclient/daemon/subscription"
)

// Пункт WARP идёт в sing-box через SOCKS на порту, который держит движок
// WARP, и демон обходит TUN: иначе транспорт WARP уйдёт сам в себя.
func TestWarpRoutesThroughSocksAndBypassesTunnel(t *testing.T) {
	cfg, err := Generate(subscription.WarpServer(), Options{TUNMode: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, `"server_port":21080`) {
		t.Fatalf("SOCKS-выход WARP не на порту %d: %s", XraySocksPort, text)
	}
	if !strings.Contains(text, `"daemon.exe"`) {
		t.Fatal("демон не вынесен из TUN: транспорт WARP уйдёт в петлю")
	}
}

func TestWarpIsNotXrayEngine(t *testing.T) {
	if NeedsXray(subscription.WarpServer()) {
		t.Fatal("пункт WARP не должен запускать отдельный Xray")
	}
	if !NeedsWarp(subscription.WarpServer()) {
		t.Fatal("пункт WARP не распознан")
	}
}
