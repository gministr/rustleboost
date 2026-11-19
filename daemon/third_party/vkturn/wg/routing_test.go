package wg

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

// Маршрутизация — место, где ошибка не падает, а тихо уводит трафик не туда:
// российский сервис уходит в туннель или, наоборот, зарубежный идёт мимо.

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

func TestEmptyRouterSendsEverythingThroughTunnel(t *testing.T) {
	router := NewRouter(nil)
	if got := router.Resolve("example.com", addr("93.184.216.34")); got != ActionProxy {
		t.Errorf("без правил ожидался proxy, получено %q", got)
	}
}

func TestDomainSuffixMatchesSubdomains(t *testing.T) {
	router := NewRouter([]Rule{{Action: ActionDirect, Domains: []string{"domain:ru"}}})

	cases := map[string]Action{
		"ru":          ActionDirect,
		"mail.ru":     ActionDirect,
		"a.b.mail.ru": ActionDirect,
		// Главная ловушка: «похожее» имя не должно подходить, иначе правило
		// «российские сервисы мимо VPN» уводит мимо туннеля чужие домены.
		"peru.com":     ActionProxy,
		"notru":        ActionProxy,
		"ru.other.com": ActionProxy,
	}
	for domain, want := range cases {
		if got := router.Resolve(domain, netip.Addr{}); got != want {
			t.Errorf("%q: ожидалось %q, получено %q", domain, want, got)
		}
	}
}

func TestFullMatchIsExact(t *testing.T) {
	router := NewRouter([]Rule{{Action: ActionDirect, Domains: []string{"full:example.com"}}})

	if got := router.Resolve("example.com", netip.Addr{}); got != ActionDirect {
		t.Errorf("точное имя: получено %q", got)
	}
	if got := router.Resolve("www.example.com", netip.Addr{}); got != ActionProxy {
		t.Errorf("поддомен не должен подходить под full:, получено %q", got)
	}
}

func TestIPRangeMatching(t *testing.T) {
	router := NewRouter([]Rule{{Action: ActionDirect, IPRanges: []string{"10.0.0.0/8", "1.2.3.4"}}})

	if got := router.Resolve("", addr("10.1.2.3")); got != ActionDirect {
		t.Errorf("адрес внутри подсети: получено %q", got)
	}
	if got := router.Resolve("", addr("1.2.3.4")); got != ActionDirect {
		t.Errorf("одиночный адрес: получено %q", got)
	}
	if got := router.Resolve("", addr("8.8.8.8")); got != ActionProxy {
		t.Errorf("адрес вне правил: получено %q", got)
	}
}

// Побеждает первое подошедшее правило — как и в ядре.
func TestFirstMatchWins(t *testing.T) {
	router := NewRouter([]Rule{
		{Action: ActionBlock, Domains: []string{"full:ads.example.com"}},
		{Action: ActionDirect, Domains: []string{"domain:example.com"}},
	})

	if got := router.Resolve("ads.example.com", netip.Addr{}); got != ActionBlock {
		t.Errorf("ожидался block, получено %q", got)
	}
	if got := router.Resolve("www.example.com", netip.Addr{}); got != ActionDirect {
		t.Errorf("ожидался direct, получено %q", got)
	}
}

func TestMatchesAllTrafficIsCatchAll(t *testing.T) {
	router := NewRouter([]Rule{
		{Action: ActionDirect, Domains: []string{"domain:ru"}},
		{Action: ActionBlock, MatchesAllTraffic: true},
	})

	if got := router.Resolve("mail.ru", netip.Addr{}); got != ActionDirect {
		t.Errorf("правило по домену должно сработать раньше замыкающего: %q", got)
	}
	if got := router.Resolve("example.com", addr("93.184.216.34")); got != ActionBlock {
		t.Errorf("замыкающее правило: получено %q", got)
	}
}

// Записи `geosite:`/`geoip:` приложение не использует — базы не помещаются в
// память расширения. Встретив такую запись, правило не должно ни падать, ни
// начинать подходить под всё подряд.
func TestUnknownEntryKindIsIgnored(t *testing.T) {
	router := NewRouter([]Rule{{Action: ActionDirect, Domains: []string{"geosite:category-ads"}}})

	if got := router.Resolve("example.com", addr("93.184.216.34")); got != ActionProxy {
		t.Errorf("неизвестная запись не должна ничего ловить, получено %q", got)
	}
}

func TestDomainComparisonIgnoresCaseAndTrailingDot(t *testing.T) {
	router := NewRouter([]Rule{{Action: ActionDirect, Domains: []string{"domain:example.com"}}})

	for _, domain := range []string{"WWW.Example.COM", "www.example.com."} {
		if got := router.Resolve(domain, netip.Addr{}); got != ActionDirect {
			t.Errorf("%q: получено %q", domain, got)
		}
	}
}

// MARK: - Извлечение имени

// buildClientHello собирает минимальный ClientHello с расширением SNI.
func buildClientHello(serverName string) []byte {
	name := []byte(serverName)

	var sni []byte
	sni = append(sni, 0x00) // тип: имя узла
	sni = binary.BigEndian.AppendUint16(sni, uint16(len(name)))
	sni = append(sni, name...)
	list := binary.BigEndian.AppendUint16(nil, uint16(len(sni)))
	list = append(list, sni...)

	var extension []byte
	extension = binary.BigEndian.AppendUint16(extension, 0x0000) // server_name
	extension = binary.BigEndian.AppendUint16(extension, uint16(len(list)))
	extension = append(extension, list...)

	var hello []byte
	hello = append(hello, 0x03, 0x03)               // версия
	hello = append(hello, make([]byte, 32)...)      // случайные байты
	hello = append(hello, 0x00)                     // идентификатор сессии
	hello = binary.BigEndian.AppendUint16(hello, 2) // наборы шифров
	hello = append(hello, 0x13, 0x01)
	hello = append(hello, 0x01, 0x00) // методы сжатия
	hello = binary.BigEndian.AppendUint16(hello, uint16(len(extension)))
	hello = append(hello, extension...)

	body := []byte{0x01, byte(len(hello) >> 16), byte(len(hello) >> 8), byte(len(hello))}
	body = append(body, hello...)

	record := []byte{0x16, 0x03, 0x01}
	record = binary.BigEndian.AppendUint16(record, uint16(len(body)))
	return append(record, body...)
}

func TestSniffTLSServerName(t *testing.T) {
	if got := sniffHost(buildClientHello("www.example.com")); got != "www.example.com" {
		t.Errorf("имя из SNI = %q", got)
	}
}

func TestSniffTLSIgnoresGarbage(t *testing.T) {
	if got := sniffHost([]byte{0x16, 0x03, 0x01, 0x00, 0x05, 0x01, 0x02}); got != "" {
		t.Errorf("обрезанный ClientHello дал %q", got)
	}
	if got := sniffHost(nil); got != "" {
		t.Errorf("пустой поток дал %q", got)
	}
}

func TestSniffHTTPHost(t *testing.T) {
	request := "GET /index.html HTTP/1.1\r\nUser-Agent: test\r\nHost: Example.COM:8080\r\n\r\n"
	if got := sniffHost([]byte(request)); got != "example.com" {
		t.Errorf("имя из Host = %q", got)
	}
}

// Произвольные байты не должны разбираться как заголовки: иначе случайный
// поток даст «имя» и попадёт не под то правило.
func TestSniffRejectsNonHTTPStream(t *testing.T) {
	if got := sniffHost([]byte("SOME RANDOM\nHost: evil.com\r\n\r\n")); got != "" {
		t.Errorf("не-HTTP поток дал %q", got)
	}
}
