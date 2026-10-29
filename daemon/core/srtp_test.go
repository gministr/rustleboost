package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vpnclient/daemon/subscription"
)

// Ссылка WDTT с параметрами SRTP даёт конфиг SRTP с постоянным ключом
// устройства; без них — обычный WDTT.
func TestWdttConfigCarriesSRTP(t *testing.T) {
	dir := t.TempDir()
	link := "wdtt://pass@82.26.94.108:56000?hashes=h1,h2&srtp=56100&srtpkey=f%2F3RObwQ8V6swKkKOZiclGAb6nQpRHQy1W2cHvG%2B3Ao%3D&srtptok=dG9r#TR"
	servers, err := subscription.Parse([]byte(link))
	if err != nil || len(servers) != 1 {
		t.Fatalf("разбор: %v %d", err, len(servers))
	}
	cfg, err := wdttTransportConfig(dir, servers[0], "dev", false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg["srtpPeer"] != "82.26.94.108:56100" || cfg["srtpPublicKey"] != "f/3RObwQ8V6swKkKOZiclGAb6nQpRHQy1W2cHvG+3Ao=" ||
		cfg["srtpConns"] != srtpConnections || cfg["srtpMaxProcs"] != -1 {
		t.Fatalf("конфиг: %v", cfg)
	}
	key := cfg["devicePrivateKey"]
	again, _ := wdttTransportConfig(dir, servers[0], "dev", false)
	if key == "" || again["devicePrivateKey"] != key {
		t.Fatal("ключ устройства не постоянный")
	}
	if _, err := os.Stat(filepath.Join(dir, "srtp-device.key")); err != nil {
		t.Fatal(err)
	}
	plain, _ := subscription.Parse([]byte("wdtt://pass@h:56000?hashes=a#X"))
	c2, _ := wdttTransportConfig(dir, plain[0], "dev", false)
	if _, ok := c2["srtpPeer"]; ok {
		t.Fatal("SRTP без параметров")
	}
}
