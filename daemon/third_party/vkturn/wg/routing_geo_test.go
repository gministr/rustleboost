package wg

import (
	"fmt"
	"net/netip"
	"testing"
)

// Списки из баз правил огромны: у `geoip:ru` двадцать пять тысяч подсетей, у
// списка рекламы — сто девяносто тысяч доменов. Перебор таких списков на
// каждом соединении съел бы больше, чем само соединение, поэтому проверяется
// не только правильность, но и время.

func hugeDomainRule(count int) Rule {
	domains := make([]string, 0, count+1)
	for i := 0; i < count; i++ {
		domains = append(domains, fmt.Sprintf("domain:ads%d.example.com", i))
	}
	domains = append(domains, "domain:nalog.ru")
	return Rule{Action: ActionDirect, Domains: domains}
}

func hugeNetworkRule(count int) Rule {
	ranges := make([]string, 0, count+1)
	for i := 0; i < count; i++ {
		ranges = append(ranges, fmt.Sprintf("%d.%d.%d.0/24", 10+i/65536, (i/256)%256, i%256))
	}
	ranges = append(ranges, "5.45.192.0/18")
	return Rule{Action: ActionDirect, IPRanges: ranges}
}

func TestHugeDomainListStillMatches(t *testing.T) {
	router := NewRouter([]Rule{hugeDomainRule(190000)})

	if got := router.Resolve("nalog.ru", netip.Addr{}); got != ActionDirect {
		t.Fatalf("домен из конца списка не найден: %s", got)
	}
	if got := router.Resolve("lk.nalog.ru", netip.Addr{}); got != ActionDirect {
		t.Fatalf("поддомен не найден: %s", got)
	}
	if got := router.Resolve("ads7.example.com", netip.Addr{}); got != ActionDirect {
		t.Fatalf("домен из середины списка не найден: %s", got)
	}
	if got := router.Resolve("youtube.com", netip.Addr{}); got != ActionProxy {
		t.Fatalf("посторонний домен подошёл под правило: %s", got)
	}
}

// Поиск идёт по меткам имени, а не по записям списка. Значит `domain:ru`
// обязан по-прежнему не ловить `peru.com` — иначе правило «российское мимо
// VPN» уводило бы мимо туннеля чужие домены.
func TestLabelSearchDoesNotMatchLookalikes(t *testing.T) {
	router := NewRouter([]Rule{{Action: ActionDirect, Domains: []string{"domain:ru"}}})

	cases := map[string]Action{
		"ru":       ActionDirect,
		"mail.ru":  ActionDirect,
		"a.b.ru":   ActionDirect,
		"peru.com": ActionProxy,
		"notru":    ActionProxy,
		"ru.com":   ActionProxy,
	}
	for domain, want := range cases {
		if got := router.Resolve(domain, netip.Addr{}); got != want {
			t.Errorf("%s: ожидалось %s, получено %s", domain, want, got)
		}
	}
}

func TestHugeNetworkListStillMatches(t *testing.T) {
	router := NewRouter([]Rule{hugeNetworkRule(25000)})

	inside := netip.MustParseAddr("5.45.200.1")
	if got := router.Resolve("", inside); got != ActionDirect {
		t.Fatalf("адрес внутри подсети не найден: %s", got)
	}
	if got := router.Resolve("", netip.MustParseAddr("10.0.0.5")); got != ActionDirect {
		t.Fatalf("адрес из начала списка не найден: %s", got)
	}
	if got := router.Resolve("", netip.MustParseAddr("8.8.8.8")); got != ActionProxy {
		t.Fatalf("посторонний адрес подошёл под правило: %s", got)
	}
}

// IPv6 из баз приходит полной записью, а соединения могут приходить с
// адресом IPv4, записанным как IPv6. Это один и тот же адрес, и правило
// обязано срабатывать одинаково.
func TestIPv6AndMappedAddresses(t *testing.T) {
	router := NewRouter([]Rule{{
		Action:   ActionDirect,
		IPRanges: []string{"2a02:6b8:0:0:0:0:0:0/32", "5.45.192.0/18"},
	}})

	if got := router.Resolve("", netip.MustParseAddr("2a02:6b8::1:1")); got != ActionDirect {
		t.Errorf("подсеть IPv6 не сработала: %s", got)
	}
	if got := router.Resolve("", netip.MustParseAddr("::ffff:5.45.200.1")); got != ActionDirect {
		t.Errorf("адрес IPv4 в записи IPv6 не сработал: %s", got)
	}
	if got := router.Resolve("", netip.MustParseAddr("2001:db8::1")); got != ActionProxy {
		t.Errorf("посторонний адрес IPv6 подошёл: %s", got)
	}
}

func TestKeywordMatchesSubstring(t *testing.T) {
	router := NewRouter([]Rule{{Action: ActionBlock, Domains: []string{"keyword:doubleclick"}}})

	if got := router.Resolve("static.doubleclick.net", netip.Addr{}); got != ActionBlock {
		t.Errorf("вхождение не найдено: %s", got)
	}
	if got := router.Resolve("example.com", netip.Addr{}); got != ActionProxy {
		t.Errorf("посторонний домен подошёл: %s", got)
	}
}

// Замеры. Эти списки — настоящий размер `category-ads-all` и `geoip:ru`.
func BenchmarkResolveHugeDomainList(b *testing.B) {
	router := NewRouter([]Rule{hugeDomainRule(190000)})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		router.Resolve("lk.nalog.ru", netip.Addr{})
	}
}

func BenchmarkResolveHugeDomainListMiss(b *testing.B) {
	router := NewRouter([]Rule{hugeDomainRule(190000)})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		router.Resolve("www.youtube.com", netip.Addr{})
	}
}

func BenchmarkResolveHugeNetworkList(b *testing.B) {
	router := NewRouter([]Rule{hugeNetworkRule(25000)})
	address := netip.MustParseAddr("5.45.200.1")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		router.Resolve("", address)
	}
}

func BenchmarkResolveHugeNetworkListMiss(b *testing.B) {
	router := NewRouter([]Rule{hugeNetworkRule(25000)})
	address := netip.MustParseAddr("8.8.8.8")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		router.Resolve("", address)
	}
}
