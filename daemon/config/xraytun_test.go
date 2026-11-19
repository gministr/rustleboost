package config

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"

	"github.com/vpnclient/daemon/subscription"
)

const testVLESS = "vless://11111111-2222-3333-4444-555555555555@example.org:443?type=tcp&security=reality&pbk=Z84J2IelR9ch3k8VtlVhhs5ycBUlXA7wHBWcBrjqnAw&sni=www.microsoft.com&fp=chrome&sid=6ba85179e30d4fc2&flow=xtls-rprx-vision#Test"
const testXHTTP = "vless://11111111-2222-3333-4444-555555555555@lte.example.org:443?type=xhttp&security=tls&sni=lte.example.org&path=%2Fx&mode=auto#LTE"

func parseShare(t *testing.T, uri string) subscription.Server {
	t.Helper()
	servers, err := subscription.Parse([]byte(uri))
	if err != nil || len(servers) != 1 {
		t.Fatalf("разбор %q: %v (%d)", uri, err, len(servers))
	}
	return servers[0]
}

// Ядро Xray должно принять конфигурацию целиком: разбор и создание всех
// входов и исходящих (без запуска — на Маке адаптер не создать).
func acceptedByXray(t *testing.T, cfg []byte) {
	t.Helper()
	parsed, err := core.LoadConfig("json", bytes.NewReader(cfg))
	if err != nil {
		t.Fatalf("Xray не принял конфигурацию: %v\n%s", err, cfg)
	}
	inst, err := core.New(parsed)
	if err != nil {
		t.Fatalf("Xray не собрал ядро: %v", err)
	}
	_ = inst.Close()
}

func decode(t *testing.T, cfg []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(cfg, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestTunXrayAcceptedByCore(t *testing.T) {
	for _, uri := range []string{testVLESS, testXHTTP} {
		server := parseShare(t, uri)
		for _, tun := range []bool{true, false} {
			cfg, err := GenerateTunXray(TunXrayInput{Server: server, Opts: Options{TUNMode: tun, RouteMode: "ru"}})
			if err != nil {
				t.Fatal(err)
			}
			acceptedByXray(t, cfg)
		}
	}
}

func TestTunXrayMatchesPhoneRules(t *testing.T) {
	server := parseShare(t, testVLESS)
	cfg, err := GenerateTunXray(TunXrayInput{Server: server, Opts: Options{TUNMode: true, RouteMode: "ru"}})
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, cfg)

	inbounds := m["inbounds"].([]any)
	tun := inbounds[0].(map[string]any)
	if tun["protocol"] != "tun" {
		t.Fatalf("первый вход — %v, нужен tun", tun["protocol"])
	}
	settings := tun["settings"].(map[string]any)
	if settings["autoOutboundsInterface"] != "auto" {
		t.Fatal("исходящие ядра не привязаны к физической сети — будет петля")
	}
	routes := settings["autoSystemRoutingTable"].([]any)
	if len(routes) == 0 || routes[0] != "0.0.0.0/1" {
		t.Fatalf("маршруты туннеля: %v", routes)
	}
	if inbounds[1].(map[string]any)["port"].(float64) != XraySocksPort {
		t.Fatal("нет локального SOCKS для проверки живости")
	}

	routing := m["routing"].(map[string]any)
	if routing["domainStrategy"] != "IPIfNonMatch" {
		t.Fatal("стратегия маршрутизации не как на iOS")
	}
	rules := routing["rules"].([]any)
	first := rules[0].(map[string]any)
	if first["outboundTag"] != "direct" || !strings.Contains(string(mustJSON(first["ip"])), "192.168.0.0/16") {
		t.Fatalf("первое правило — не приватные адреса напрямую: %v", first)
	}
	ru := rules[1].(map[string]any)
	if ru["outboundTag"] != "direct" || !strings.Contains(string(mustJSON(ru["domain"])), "domain:ru") {
		t.Fatalf("режим «РФ напрямую» не попал в правила: %v", ru)
	}
	outbounds := m["outbounds"].([]any)
	if outbounds[0].(map[string]any)["tag"] != subscription.ProxyTag {
		t.Fatal("первый исходящий — не сервер")
	}
}

func TestTunXrayGroupHasCatchAllByAddress(t *testing.T) {
	server := parseShare(t, testVLESS)
	second := parseShare(t, testXHTTP)
	server.Alternatives = []json.RawMessage{second.Outbound}
	cfg, err := GenerateTunXray(TunXrayInput{Server: server, Opts: Options{TUNMode: true}})
	if err != nil {
		t.Fatal(err)
	}
	acceptedByXray(t, cfg)
	m := decode(t, cfg)
	rules := m["routing"].(map[string]any)["rules"].([]any)
	last := rules[len(rules)-1].(map[string]any)
	if last["balancerTag"] != xrayBalancerTag || !strings.Contains(string(mustJSON(last["ip"])), "0.0.0.0/0") {
		t.Fatalf("замыкающее правило группы должно быть по адресу (как на iOS): %v", last)
	}
	if _, ok := m["observatory"]; !ok {
		t.Fatal("у группы нет замера задержки")
	}
}

func TestTunXrayUpstreamSocks(t *testing.T) {
	server := subscription.Server{Name: "hy2", Protocol: "Hysteria2", Address: "h.example.org", Port: 443}
	cfg, err := GenerateTunXray(TunXrayInput{Server: server, Opts: Options{TUNMode: true}, UpstreamSocksPort: 21081})
	if err != nil {
		t.Fatal(err)
	}
	acceptedByXray(t, cfg)
	if !strings.Contains(string(cfg), `"port":21081`) {
		t.Fatal("трафик Hysteria2 не направлен в его SOCKS")
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
