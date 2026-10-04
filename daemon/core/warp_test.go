package core

import (
	"encoding/json"
	"testing"

	"github.com/vpnclient/daemon/subscription"
)

func wdttServer() subscription.Server {
	return subscription.Server{
		Address: "151.243.208.197", Port: 56000,
		Engine: subscription.EngineWdtt,
		Params: map[string]string{"password": "p", "hashes": "h1,h2", "workers": "16", "obfs": "audio"},
	}
}

func TestWarpConfigCarriesWdttTransport(t *testing.T) {
	raw, err := warpConfigJSON(t.TempDir(), wdttServer(), 21080, "device-1", false)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["socksPort"].(float64) != 21080 {
		t.Fatalf("порт SOCKS: %v", cfg["socksPort"])
	}
	transport := cfg["transport"].(map[string]any)
	if transport["peerAddress"] != "151.243.208.197:56000" {
		t.Fatalf("адрес узла: %v", transport["peerAddress"])
	}
	if transport["vkHashes"] != "h1,h2" || transport["deviceId"] != "device-1" {
		t.Fatalf("транспорт: %v", transport)
	}
	if transport["captchaMode"] != "rjs" {
		t.Fatalf("капча: %v", transport["captchaMode"])
	}
}

func TestWarpRejectsTransportWithoutCredentials(t *testing.T) {
	bad := wdttServer()
	bad.Params = map[string]string{"hashes": "h1"}
	if _, err := warpConfigJSON(t.TempDir(), bad, 21080, "d", false); err == nil {
		t.Fatal("транспорт без пароля принят")
	}
}

func TestWarpRejectsNonWdttTransport(t *testing.T) {
	if _, err := warpConfigJSON(t.TempDir(), subscription.WarpServer(), 21080, "d", false); err == nil {
		t.Fatal("пункт WARP принят как транспорт")
	}
}
