package config

// Конфигурация Xray для Windows — та же, что у приложения iOS/Mac
// (RustleBoostKit/Config/XrayConfigBuilder.swift): маршрутизация, политика,
// DNS, балансировщик групп. Разница одна: на телефоне пакеты в ядро подаёт
// tun2socks через локальный SOCKS, а здесь адаптер Wintun держит сам Xray
// (встроенный вход TUN, proxy/tun). Ядро одно и работает в процессе службы —
// без sing-box и xray.exe, как было раньше.
//
// Петли нет: autoOutboundsInterface привязывает все исходящие соединения ядра
// (прокси, «напрямую», его собственный DNS) к физической сети, как делает
// официальный клиент WireGuard для Windows.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vpnclient/daemon/subscription"
)

const (
	// TunAdapterName — адаптер в «Сетевых подключениях». Тот же, что у
	// прямого пути WDTT/WARP: одно имя для пользователя.
	TunAdapterName = "RustleBoost"
	// TunMTU — пакеты завершает стек gVisor внутри ядра; у самих
	// соединений с сервером свой MTU, поэтому обычные 1500.
	TunMTU = 1500
)

// TunDNS — DNS туннеля, как на iOS (DNSConfiguration.defaultServers):
// запросы идут внутри туннеля, через сервер.
var TunDNS = []string{"1.1.1.1", "8.8.8.8"}

var (
	tunAddresses = []string{"198.18.0.1/30", "fdfe:dcba:9876::1/126"}
	// Две половины вместо маршрута по умолчанию: они точнее и главнее его, а
	// маршрут по умолчанию физической сети остаётся — по нему ядро находит
	// сеть для своих соединений.
	tunRoutes = []string{"0.0.0.0/1", "128.0.0.0/1", "::/1", "8000::/1"}
)

// privateAddressRanges — то же, что XrayConfigBuilder.privateAddressRanges.
var privateAddressRanges = []string{
	"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"169.254.0.0/16", "100.64.0.0/10", "224.0.0.0/4", "240.0.0.0/4",
	"::1/128", "fc00::/7", "fe80::/10",
}

// everyAddress — замыкающее условие правил (см. XrayConfigBuilder.everyAddress):
// именно адрес, а не network: tcp,udp, иначе правила по адресам для соединений
// с именем не проверялись бы никогда.
var everyAddress = []string{"0.0.0.0/0", "::/0"}

// TunXrayInput — всё, из чего собирается конфигурация.
type TunXrayInput struct {
	Server subscription.Server
	Opts   Options
	// LogPath — файл журнала ядра (только предупреждения и ошибки).
	LogPath string
	// UpstreamSocksPort — для серверов, которые держит не Xray (Hysteria2):
	// Xray отправляет трафик в их локальный SOCKS.
	UpstreamSocksPort int
}

// GenerateTunXray собирает конфигурацию в виде JSON для rustlecore.XrayStart.
func GenerateTunXray(in TunXrayInput) ([]byte, error) {
	server := in.Server
	var outbounds []any
	balanced := false

	switch {
	case in.UpstreamSocksPort > 0:
		outbounds = append(outbounds, map[string]any{
			"tag":      subscription.ProxyTag,
			"protocol": "socks",
			"settings": map[string]any{"servers": []any{map[string]any{
				"address": "127.0.0.1", "port": in.UpstreamSocksPort,
			}}},
		})
	case len(server.Outbound) > 0:
		first, err := retagXrayOutbound(server.Outbound, subscription.ProxyTag)
		if err != nil {
			return nil, fmt.Errorf("server %q: %w", server.Name, err)
		}
		outbounds = append(outbounds, json.RawMessage(first))
		// Группа: каналы proxy, proxy-2, … и балансировщик по задержке.
		for i, alternative := range server.Alternatives {
			tag := fmt.Sprintf("%s-%d", subscription.ProxyTag, i+2)
			retagged, err := retagXrayOutbound(alternative, tag)
			if err != nil {
				return nil, fmt.Errorf("server %q: group member %d: %w", server.Name, i+2, err)
			}
			outbounds = append(outbounds, json.RawMessage(retagged))
			balanced = true
		}
	default:
		return nil, fmt.Errorf("сервер %q не поддерживается ядром Xray", server.Name)
	}

	outbounds = append(outbounds,
		map[string]any{"tag": "direct", "protocol": "freedom", "settings": map[string]any{"domainStrategy": "UseIP"}},
		map[string]any{"tag": "block", "protocol": "blackhole"},
	)

	sniffing := map[string]any{
		"enabled":      true,
		"destOverride": []string{"http", "tls", "quic"},
		"routeOnly":    false,
	}
	var inbounds []any
	if in.Opts.TUNMode {
		inbounds = append(inbounds, map[string]any{
			"tag":      "tun-in",
			"port":     0,
			"protocol": "tun",
			"settings": map[string]any{
				"name":                   TunAdapterName,
				"desc":                   TunAdapterName,
				"mtu":                    TunMTU,
				"gateway":                tunAddresses,
				"dns":                    TunDNS,
				"autoSystemRoutingTable": tunRoutes,
				"autoOutboundsInterface": "auto",
			},
			"sniffing": sniffing,
		})
	} else {
		// Режим прокси: системный прокси Windows смотрит в HTTP-вход.
		listen := "127.0.0.1"
		if in.Opts.AllowLAN {
			listen = "0.0.0.0"
		}
		inbounds = append(inbounds, map[string]any{
			"tag": "http-in", "listen": listen, "port": MixedPort, "protocol": "http",
			"settings": map[string]any{}, "sniffing": sniffing,
		})
	}
	// Локальный SOCKS для проверки живости туннеля и замеров — как socks-in на iOS.
	inbounds = append(inbounds, map[string]any{
		"tag": "socks-in", "listen": "127.0.0.1", "port": XraySocksPort, "protocol": "socks",
		"settings": map[string]any{"auth": "noauth", "udp": true},
		"sniffing": sniffing,
	})

	proxyTarget := map[string]any{"outboundTag": subscription.ProxyTag}
	if balanced {
		proxyTarget = map[string]any{"balancerTag": xrayBalancerTag}
	}
	rule := func(fields map[string]any, target map[string]any) map[string]any {
		r := map[string]any{"type": "field"}
		for k, v := range fields {
			r[k] = v
		}
		for k, v := range target {
			r[k] = v
		}
		return r
	}

	rules := []any{
		// Приватные адреса — напрямую (диапазоны явно: geoip.dat в сборке нет).
		rule(map[string]any{"ip": privateAddressRanges}, map[string]any{"outboundTag": "direct"}),
	}
	if suffixes := bypassSuffixes(in.Opts.RouteMode); len(suffixes) > 0 {
		domains := make([]string, 0, len(suffixes))
		for _, suffix := range suffixes {
			domains = append(domains, "domain:"+suffix)
		}
		rules = append(rules, rule(map[string]any{"domain": domains}, map[string]any{"outboundTag": "direct"}))
	}
	// IPv6 без имени (не распознанный трафик) — блокируется, а не идёт
	// мимо: приложения тут же переходят на IPv4. С именем (TLS, HTTP, QUIC)
	// соединение уходит на сервер по имени и работает.
	rules = append(rules, rule(map[string]any{"ip": []string{"::/0"}}, map[string]any{"outboundTag": "block"}))
	if balanced {
		// Замыкающее правило обязательно: иначе трафик ушёл бы в первый канал
		// группы мимо выбора по задержке.
		rules = append(rules, rule(map[string]any{"ip": everyAddress}, proxyTarget))
	}

	routing := map[string]any{
		// Сначала правила по домену, и только если ни одно не сработало —
		// адрес.
		"domainStrategy": "IPIfNonMatch",
		"rules":          rules,
	}
	if balanced {
		routing["balancers"] = []any{map[string]any{
			"tag":      xrayBalancerTag,
			"selector": []string{subscription.ProxyTag},
			"strategy": map[string]any{"type": "leastPing"},
		}}
	}

	// Проверочная версия: подробный журнал (соединения с адаптера и их
	// маршрут) — пока ищем, где рвётся трафик в Windows. Перед выпуском
	// вернуть "warning".
	logSection := map[string]any{"loglevel": "info"}
	if in.LogPath != "" {
		logSection["error"] = in.LogPath
		logSection["access"] = "none"
	}

	cfg := map[string]any{
		"log":       logSection,
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"dns": map[string]any{
			"servers":       TunDNS,
			"queryStrategy": "UseIPv4",
			"disableCache":  false,
		},
		"routing": routing,
		// Сроки жизни соединений: рукопожатие 8 с, как на iOS. Простой —
		// 5 минут: на компьютере нет лимита памяти расширения iOS, а
		// программы держат соединения подолгу.
		"policy": map[string]any{"levels": map[string]any{"0": map[string]any{
			"handshake": 8, "connIdle": 300, "uplinkOnly": 1, "downlinkOnly": 1,
		}}},
	}
	if balanced {
		cfg["observatory"] = map[string]any{
			"subjectSelector":   []string{subscription.ProxyTag},
			"probeUrl":          "https://www.gstatic.com/generate_204",
			"probeInterval":     "30s",
			"enableConcurrency": true,
		}
	}
	return json.Marshal(cfg)
}

// TunDNSAddrs — DNS туннеля в виде строк для брандмауэра.
func TunDNSAddrs() []string { return append([]string(nil), TunDNS...) }

// IsXrayServer — сервер, который целиком обслуживает Xray (готовый исходящий
// из подписки).
func IsXrayServer(server subscription.Server) bool {
	return len(server.Outbound) > 0
}

// IsHysteria2 — сервер Hysteria2: его держит ядро Hysteria2 из rustlecore.
func IsHysteria2(server subscription.Server) bool {
	return strings.EqualFold(server.Protocol, "Hysteria2") || strings.EqualFold(server.Protocol, "hysteria2")
}
