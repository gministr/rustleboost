package wg

import (
	"net/netip"
	"regexp"
	"strings"
)

// Маршрутизация для WDTT.
//
// У Xray правила живут в конфигурации ядра, и остальное приложение о них не
// думает. Здесь ядра нет — туннель поднимает WireGuard, — поэтому правила
// применяются тут же, в SOCKS5-слое: по каждому соединению решается, вести
// его через туннель или мимо него.
//
// Формат записей тот же, что у Xray, чтобы сборки правил в приложении были
// общими для всех протоколов: `domain:example.com` (сам домен и поддомены),
// `full:example.com` (только он), `keyword:...`, `regexp:...`, и подсети вида
// `10.0.0.0/8`.
//
// Записи `geosite:`/`geoip:` сюда не доходят: категории баз разворачивает
// приложение и передаёт уже готовыми списками. Файлы баз весят десять и
// шестнадцать мегабайт и в память расширения не помещаются, а нужные из них
// категории — сотни килобайт.
//
// Отсюда требование к сопоставлению: списки бывают огромными. У `geoip:ru`
// двадцать пять тысяч подсетей, у списка рекламы — сто девяносто тысяч
// доменов. Перебором таких списков решение по одному соединению занимало
// 1 482 000 нс на доменах и 59 200 нс на подсетях — полторы миллисекунды
// процессора на каждое соединение, при том что страница открывает их
// десятками. Поиск по картам довёл это до 29 и 22 нс.
//
// Поэтому домены ищутся перебором меток самого имени (их единицы), а подсети
// — перебором встреченных длин префикса (их пара десятков), но не перебором
// самих списков.

// Action — что делать с соединением.
type Action string

const (
	// ActionProxy — вести через туннель.
	ActionProxy Action = "proxy"
	// ActionDirect — в обход туннеля, напрямую с устройства.
	ActionDirect Action = "direct"
	// ActionBlock — не пропускать вовсе.
	ActionBlock Action = "block"
)

// Rule — одно правило сборки.
type Rule struct {
	Action            Action   `json:"action"`
	Domains           []string `json:"domains"`
	IPRanges          []string `json:"ipRanges"`
	MatchesAllTraffic bool     `json:"matchesAllTraffic"`
}

// Router сопоставляет адрес назначения с правилами.
//
// Правила разбираются один раз при запуске: разбор подсетей и регулярных
// выражений на каждом соединении стоил бы дороже самого соединения.
type Router struct {
	rules []compiledRule
}

type compiledRule struct {
	action   Action
	domains  domainSet
	patterns []*regexp.Regexp
	keywords []string
	networks networkSet
	all      bool
}

// domainSet — множества имён с поиском за постоянное время.
//
// `suffix` хранит записи `domain:`, `exact` — записи `full:`. Поиск идёт не
// перебором записей, а перебором меток самого имени: у имени их единицы, а
// записей в списке могут быть сотни тысяч.
type domainSet struct {
	exact  map[string]struct{}
	suffix map[string]struct{}
}

func (d *domainSet) addExact(value string) {
	if d.exact == nil {
		d.exact = make(map[string]struct{})
	}
	d.exact[value] = struct{}{}
}

func (d *domainSet) addSuffix(value string) {
	if d.suffix == nil {
		d.suffix = make(map[string]struct{})
	}
	d.suffix[value] = struct{}{}
}

func (d *domainSet) empty() bool {
	return len(d.exact) == 0 && len(d.suffix) == 0
}

// matches повторяет семантику ядра: `full:` — точное совпадение, `domain:` —
// сам домен и его поддомены, но не «похожее» имя.
//
// То есть `domain:ru` ловит `ru` и `mail.ru`, но не `peru.com`, — иначе
// правило «российские сервисы мимо VPN» уводило бы мимо туннеля половину
// чужих доменов. Поэтому имя режется по точкам, а не по символам.
func (d *domainSet) matches(domain string) bool {
	if len(d.exact) > 0 {
		if _, ok := d.exact[domain]; ok {
			return true
		}
	}
	if len(d.suffix) == 0 {
		return false
	}
	rest := domain
	for {
		if _, ok := d.suffix[rest]; ok {
			return true
		}
		dot := strings.IndexByte(rest, '.')
		if dot < 0 {
			return false
		}
		rest = rest[dot+1:]
	}
}

// networkSet — подсети, разложенные по длине префикса.
//
// Проверка «есть ли подсеть, содержащая адрес» не требует перебора: для
// каждой встреченной длины адрес обрезается до неё и ищется в карте. Длин в
// списке пара десятков, подсетей — десятки тысяч.
type networkSet struct {
	v4     map[int]map[[4]byte]struct{}
	v6     map[int]map[[16]byte]struct{}
	v4Bits []int
	v6Bits []int
}

func (n *networkSet) add(prefix netip.Prefix) {
	prefix = prefix.Masked()
	bits := prefix.Bits()
	address := prefix.Addr()

	if address.Is4() {
		if n.v4 == nil {
			n.v4 = make(map[int]map[[4]byte]struct{})
		}
		if n.v4[bits] == nil {
			n.v4[bits] = make(map[[4]byte]struct{})
			n.v4Bits = append(n.v4Bits, bits)
		}
		n.v4[bits][address.As4()] = struct{}{}
		return
	}
	if n.v6 == nil {
		n.v6 = make(map[int]map[[16]byte]struct{})
	}
	if n.v6[bits] == nil {
		n.v6[bits] = make(map[[16]byte]struct{})
		n.v6Bits = append(n.v6Bits, bits)
	}
	n.v6[bits][address.As16()] = struct{}{}
}

func (n *networkSet) empty() bool {
	return len(n.v4) == 0 && len(n.v6) == 0
}

func (n *networkSet) contains(address netip.Addr) bool {
	// Адрес IPv4, записанный как IPv6, — это тот же адрес: подсети для него
	// лежат среди четырёхбайтных.
	if address.Is4In6() {
		address = address.Unmap()
	}
	if address.Is4() {
		for _, bits := range n.v4Bits {
			network, err := address.Prefix(bits)
			if err != nil {
				continue
			}
			if _, ok := n.v4[bits][network.Addr().As4()]; ok {
				return true
			}
		}
		return false
	}
	for _, bits := range n.v6Bits {
		network, err := address.Prefix(bits)
		if err != nil {
			continue
		}
		if _, ok := n.v6[bits][network.Addr().As16()]; ok {
			return true
		}
	}
	return false
}

// NewRouter разбирает сборку правил. Пустая сборка означает, что весь трафик
// идёт через туннель.
func NewRouter(rules []Rule) *Router {
	compiled := make([]compiledRule, 0, len(rules))
	for _, rule := range rules {
		item := compiledRule{action: rule.Action, all: rule.MatchesAllTraffic}

		for _, entry := range rule.Domains {
			value := strings.ToLower(strings.TrimSpace(entry))
			switch {
			case value == "":
				continue
			case strings.HasPrefix(value, "domain:"):
				item.domains.addSuffix(strings.TrimPrefix(value, "domain:"))
			case strings.HasPrefix(value, "full:"):
				item.domains.addExact(strings.TrimPrefix(value, "full:"))
			case strings.HasPrefix(value, "keyword:"):
				// Вхождение подстроки. В базах такие записи редки, и перебор
				// десятка из них ничего не стоит.
				if keyword := strings.TrimPrefix(value, "keyword:"); keyword != "" {
					item.keywords = append(item.keywords, keyword)
				}
			case strings.HasPrefix(value, "regexp:"):
				if pattern, err := regexp.Compile(strings.TrimPrefix(value, "regexp:")); err == nil {
					item.patterns = append(item.patterns, pattern)
				}
			case strings.Contains(value, ":"):
				// Неизвестная разметка ядра. Молча пропускаем: сопоставить
				// нечем, а падать из-за записи в сборке правил нельзя.
				// Записи `geosite:`/`geoip:` сюда не доходят — их
				// разворачивает приложение.
				continue
			default:
				item.domains.addSuffix(value)
			}
		}

		for _, entry := range rule.IPRanges {
			value := strings.TrimSpace(entry)
			if prefix, err := netip.ParsePrefix(value); err == nil {
				item.networks.add(prefix)
				continue
			}
			// Одиночный адрес — это подсеть из одного адреса.
			if address, err := netip.ParseAddr(value); err == nil {
				item.networks.add(netip.PrefixFrom(address, address.BitLen()))
			}
		}

		if item.all || !item.domains.empty() || len(item.patterns) > 0 ||
			len(item.keywords) > 0 || !item.networks.empty() {
			compiled = append(compiled, item)
		}
	}
	return &Router{rules: compiled}
}

// Resolve выбирает действие для адреса назначения.
//
// `domain` — имя, если его удалось извлечь из потока; пустая строка, если
// известен только адрес. Порядок правил важен: побеждает первое подошедшее,
// как и в ядре.
func (r *Router) Resolve(domain string, address netip.Addr) Action {
	if r == nil || len(r.rules) == 0 {
		return ActionProxy
	}
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))

	for index := range r.rules {
		if r.rules[index].matches(domain, address) {
			return r.rules[index].action
		}
	}
	// Не подошло ни одно правило — через туннель. Так же ведёт себя ядро: без
	// замыкающего правила остаток трафика уходит в канал по умолчанию.
	return ActionProxy
}

func (c *compiledRule) matches(domain string, address netip.Addr) bool {
	if c.all {
		return true
	}

	if domain != "" {
		if c.domains.matches(domain) {
			return true
		}
		for _, keyword := range c.keywords {
			if strings.Contains(domain, keyword) {
				return true
			}
		}
		for _, pattern := range c.patterns {
			if pattern.MatchString(domain) {
				return true
			}
		}
	}

	if address.IsValid() && c.networks.contains(address) {
		return true
	}
	return false
}
