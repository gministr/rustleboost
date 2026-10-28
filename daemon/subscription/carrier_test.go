package subscription

import "testing"

// A host that only carries a template (a third-party server with its own
// keys) arrives with a placeholder outbound first and the real server after
// it, reached by a final catch-all rule. These profiles are shaped exactly as
// the panel serves them.

const carrierVLESS = `{"tag":"proxy","protocol":"vless","settings":{"vnext":[{"address":"217.179.221.84","port":443,
 "users":[{"id":"0f8b4c2e-1111-2222-3333-444455556666","flow":"xtls-rprx-vision"}]}]},
 "streamSettings":{"network":"tcp","security":"reality","realitySettings":{"publicKey":"CARRIER","serverName":"www.cloudflare.com","shortId":"aa"}}}`

const realTrojan = `{"tag":"cis02","protocol":"trojan","settings":{"servers":[{"address":"alb.cishosts.org","port":8443,"password":"SECRET"}]},
 "streamSettings":{"network":"tcp","security":"tls","tlsSettings":{"serverName":"alb.cishosts.org","fingerprint":"firefox"}}}`

const realReality = `{"tag":"cis01","protocol":"vless","settings":{"vnext":[{"address":"217.179.221.84","port":443,
 "users":[{"id":"d8baa5cc-abbd-4a10-941c-cd25c978628f","flow":"xtls-rprx-vision"}]}]},
 "streamSettings":{"network":"tcp","security":"reality","realitySettings":{"publicKey":"REAL","serverName":"ve.cishosts.org","shortId":"a1ab5a56bcca","fingerprint":"chrome"}}}`

func profile(rules, outbounds, extra string) string {
	return `[{"remarks":"🇺🇸! США 🟢","routing":{` + extra + `"rules":[` + rules + `]},"outbounds":[` + outbounds +
		`,{"tag":"direct","protocol":"freedom"},{"tag":"block","protocol":"blackhole"}]}]`
}

func parseOne(t *testing.T, body string) Server {
	t.Helper()
	servers, err := parseJSONPayload([]byte(body))
	if err != nil || len(servers) != 1 {
		t.Fatalf("ожидался один сервер, получено %d, ошибка %v", len(servers), err)
	}
	return servers[0]
}

func TestFollowsCatchAllRuleToTheRealServer(t *testing.T) {
	rules := `{"type":"field","protocol":["bittorrent"],"outboundTag":"direct"},
	          {"type":"field","domain":["domain:ru"],"outboundTag":"direct"},
	          {"type":"field","network":"tcp,udp","outboundTag":"cis02"}`
	srv := parseOne(t, profile(rules, carrierVLESS+","+realTrojan, ""))
	if srv.Address != "alb.cishosts.org" || srv.Port != 8443 {
		t.Fatalf("взят носитель вместо настоящего сервера: %s:%d", srv.Address, srv.Port)
	}
	if srv.Protocol != "Trojan" {
		t.Fatalf("протокол %q, ожидался Trojan", srv.Protocol)
	}
}

func TestRealityKeysComeFromTheRealServer(t *testing.T) {
	rules := `{"type":"field","network":"tcp,udp","outboundTag":"cis01"}`
	srv := parseOne(t, profile(rules, carrierVLESS+","+realReality, ""))
	if got := string(srv.Outbound); !contains(got, "REAL") || contains(got, "CARRIER") {
		t.Fatalf("ключи не от настоящего сервера: %s", got)
	}
}

func TestWithoutCatchAllTheFirstOutboundIsUsed(t *testing.T) {
	rules := `{"type":"field","domain":["domain:ru"],"outboundTag":"direct"}`
	srv := parseOne(t, profile(rules, carrierVLESS, ""))
	if srv.Address != "217.179.221.84" {
		t.Fatalf("адрес %q", srv.Address)
	}
}

func TestCatchAllToDirectKeepsTheFirstProxy(t *testing.T) {
	rules := `{"type":"field","domain":["domain:example.com"],"outboundTag":"proxy"},
	          {"type":"field","network":"tcp,udp","outboundTag":"direct"}`
	srv := parseOne(t, profile(rules, carrierVLESS, ""))
	if srv.Address != "217.179.221.84" {
		t.Fatalf("адрес %q", srv.Address)
	}
}

func TestRuleLimitedByPortIsNotCatchAll(t *testing.T) {
	rules := `{"type":"field","network":"tcp,udp","port":"443","outboundTag":"cis02"}`
	srv := parseOne(t, profile(rules, carrierVLESS+","+realTrojan, ""))
	if srv.Address != "217.179.221.84" {
		t.Fatalf("правило с портом не должно считаться «всем трафиком»: %q", srv.Address)
	}
}

func TestRuleWithoutNetworkIsCatchAll(t *testing.T) {
	rules := `{"type":"field","outboundTag":"cis02"}`
	srv := parseOne(t, profile(rules, carrierVLESS+","+realTrojan, ""))
	if srv.Address != "alb.cishosts.org" {
		t.Fatalf("адрес %q", srv.Address)
	}
}

func TestUDPOnlyRuleIsNotCatchAll(t *testing.T) {
	rules := `{"type":"field","network":"udp","outboundTag":"cis02"}`
	srv := parseOne(t, profile(rules, carrierVLESS+","+realTrojan, ""))
	if srv.Address != "217.179.221.84" {
		t.Fatalf("адрес %q", srv.Address)
	}
}

func TestBalancerGroupTakesItsFirstChannelNotTheCarrier(t *testing.T) {
	// Группа: канал-носитель не входит в селектор балансировщика.
	balancers := `"balancers":[{"tag":"auto","selector":["w"],"strategy":{"type":"leastPing"}}],`
	rules := `{"type":"field","network":"tcp,udp","balancerTag":"auto"}`
	first := `{"tag":"w01","protocol":"vless","settings":{"vnext":[{"address":"one.example.com","port":443,"users":[{"id":"0f8b4c2e-1111-2222-3333-444455556666"}]}]},"streamSettings":{"network":"tcp","security":"none"}}`
	second := `{"tag":"w02","protocol":"vless","settings":{"vnext":[{"address":"two.example.com","port":443,"users":[{"id":"0f8b4c2e-1111-2222-3333-444455556666"}]}]},"streamSettings":{"network":"tcp","security":"none"}}`
	srv := parseOne(t, profile(rules, carrierVLESS+","+first+","+second, balancers))
	if srv.Address != "one.example.com" {
		t.Fatalf("взят %q вместо первого канала группы", srv.Address)
	}
}

func TestPlainProfileWithoutRoutingStillWorks(t *testing.T) {
	body := `[{"remarks":"Plain","outbounds":[` + carrierVLESS + `,{"tag":"direct","protocol":"freedom"}]}]`
	srv := parseOne(t, body)
	if srv.Address != "217.179.221.84" {
		t.Fatalf("адрес %q", srv.Address)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
