package rustlecore

import (
	"context"
	"strings"
	"testing"

	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/routing"
	routing_session "github.com/xtls/xray-core/features/routing/session"
)

// Группа каналов (JSON-серверы с балансировщиком) обязана подчиняться тем же
// правилам по адресам, что и одиночный сервер. Ядро проверяет правила в два
// прохода: первый без разрешения имени, второй с ним. Замыкающее правило
// `network: tcp,udp` срабатывало уже в первом проходе, и до `geoip:ru` дело не
// доходило никогда — российские сайты с иностранными именами шли за границу.
func pickFor(t *testing.T, catchAll, domain string) string {
	t.Helper()
	config := `{
	  "log": {"loglevel": "none"},
	  "dns": {"hosts": {"site.example": "95.163.0.1", "abroad.example": "8.8.8.8"}, "servers": ["1.1.1.1"]},
	  "outbounds": [
	    {"tag": "proxy", "protocol": "freedom"},
	    {"tag": "direct", "protocol": "freedom"}
	  ],
	  "routing": {
	    "domainStrategy": "IPIfNonMatch",
	    "rules": [
	      {"type": "field", "domain": ["full:listed.example"], "outboundTag": "direct"},
	      {"type": "field", "ip": ["95.163.0.0/16"], "outboundTag": "direct"},
	      {"type": "field", ` + catchAll + `, "balancerTag": "group"}
	    ],
	    "balancers": [{"tag": "group", "selector": ["proxy"]}]
	  }
	}`
	loaded, err := core.LoadConfig("json", strings.NewReader(config))
	if err != nil {
		t.Fatal(err)
	}
	instance, err := core.New(loaded)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	router := instance.GetFeature(routing.RouterType()).(routing.Router)

	ctx := session.ContextWithOutbounds(context.Background(), []*session.Outbound{{
		Target: net.TCPDestination(net.DomainAddress(domain), 443),
	}})
	route, err := router.PickRoute(routing_session.AsRoutingContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	return route.GetOutboundTag()
}

func TestCatchAllByNetworkHidesIPRules(t *testing.T) {
	// Прежнее поведение — зафиксировано, чтобы причина не забылась.
	if got := pickFor(t, `"network": "tcp,udp"`, "site.example"); got != "proxy" {
		t.Fatalf("ожидался обход правила по адресу, ядро выбрало %q", got)
	}
}

func TestCatchAllByAddressKeepsIPRules(t *testing.T) {
	catchAll := `"ip": ["0.0.0.0/0", "::/0"]`
	if got := pickFor(t, catchAll, "site.example"); got != "direct" {
		t.Fatalf("адрес из правила должен идти напрямую, ядро выбрало %q", got)
	}
	if got := pickFor(t, catchAll, "listed.example"); got != "direct" {
		t.Fatalf("имя из правила должно идти напрямую, ядро выбрало %q", got)
	}
	if got := pickFor(t, catchAll, "abroad.example"); got != "proxy" {
		t.Fatalf("остальное — в группу, ядро выбрало %q", got)
	}
}
