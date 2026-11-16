package subscription

import (
	"encoding/json"
	"testing"
)

// Пункт подписки на группе (балансировщик шаблона панели) хранит все её узлы:
// первый узел группы — самый старый набор ключей и часто уже не работает.
func TestGroupKeepsEveryMember(t *testing.T) {
	trojan := func(tag, pw string) map[string]any {
		return map[string]any{"tag": tag, "protocol": "trojan",
			"settings": map[string]any{"servers": []any{map[string]any{"address": "alb.example.org", "password": pw, "port": 8443}}},
			"streamSettings": map[string]any{"network": "tcp", "security": "tls",
				"tlsSettings": map[string]any{"serverName": "alb.example.org"}}}
	}
	profile := map[string]any{
		"remarks":   "США 🟢",
		"outbounds": []any{trojan("cis02", "old"), trojan("cis02b", "b"), trojan("cis02l", "new"), map[string]any{"tag": "direct", "protocol": "freedom"}},
		"routing": map[string]any{
			"rules":     []any{map[string]any{"type": "field", "network": "tcp,udp", "balancerTag": "b1"}},
			"balancers": []any{map[string]any{"tag": "b1", "selector": []string{"cis02"}}},
		},
	}
	data, _ := json.Marshal([]any{profile})
	servers, err := parseJSONPayload(data)
	if err != nil || len(servers) != 1 {
		t.Fatalf("servers=%d err=%v", len(servers), err)
	}
	s := servers[0]
	if len(s.Alternatives) != 2 {
		t.Fatalf("у группы %d запасных узлов, ожидалось 2", len(s.Alternatives))
	}
	if s.Engine != EngineXray {
		t.Fatalf("группа идёт через %s: балансировщик есть только в Xray", s.Engine)
	}
	var last struct {
		Tag      string `json:"tag"`
		Settings struct {
			Servers []struct{ Password string } `json:"servers"`
		} `json:"settings"`
	}
	_ = json.Unmarshal(s.Alternatives[1], &last)
	if last.Settings.Servers[0].Password != "new" || last.Tag != ProxyTag {
		t.Fatalf("последний узел группы потерян или не переименован: %+v", last)
	}
}
