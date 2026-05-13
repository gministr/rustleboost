// Package config generates sing-box 1.13 compatible configurations.
// Tested working format — all deprecated APIs removed:
//   - DNS: uses type/server format (not legacy address string)
//   - Route: requires default_domain_resolver
//   - TUN: gvisor stack, no sniff fields, endpoint_independent_nat
//   - Outbounds: no dns-out (removed in 1.13)
package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/vpnclient/daemon/subscription"
)

type SingBoxConfig struct {
	Log          *LogConfig          `json:"log,omitempty"`
	Experimental *ExperimentalConfig `json:"experimental,omitempty"`
	DNS          *DNSConfig          `json:"dns,omitempty"`
	Inbounds     []interface{}       `json:"inbounds"`
	Outbounds    []interface{}       `json:"outbounds"`
	Route        RouteConfig         `json:"route"`
}

type LogConfig struct {
	Level string `json:"level"`
}

type ExperimentalConfig struct {
	ClashAPI *ClashAPIConfig `json:"clash_api,omitempty"`
}

type ClashAPIConfig struct {
	ExternalController string `json:"external_controller"`
}

// DNS with new sing-box 1.12+ format: type+server instead of address string
type DNSConfig struct {
	Servers []DNSServer `json:"servers"`
	Rules   []DNSRule   `json:"rules,omitempty"`
	Final   string      `json:"final"`
}

// DNSServer uses the new format (type + server + server_port)
type DNSServer struct {
	Tag        string `json:"tag"`
	Type       string `json:"type"`                  // udp | tcp | tls | https | quic | local
	Server     string `json:"server,omitempty"`       // IP or hostname
	ServerPort int    `json:"server_port,omitempty"`  // default: 53/853/443
	Path       string `json:"path,omitempty"`         // for https
}

type DNSRule struct {
	IPIsPrivate  *bool    `json:"ip_is_private,omitempty"`
	Domain       []string `json:"domain,omitempty"`
	DomainSuffix []string `json:"domain_suffix,omitempty"`
	Server       string   `json:"server"`
}


// Inbounds
type MixedInbound struct {
	Type       string `json:"type"`
	Tag        string `json:"tag"`
	Listen     string `json:"listen"`
	ListenPort int    `json:"listen_port"`
}

type TUNPlatformHTTPProxy struct {
	Enabled    bool   `json:"enabled"`
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
}

type TUNPlatform struct {
	HTTPProxy *TUNPlatformHTTPProxy `json:"http_proxy,omitempty"`
}

type TUNInbound struct {
	Type                   string       `json:"type"`
	Tag                    string       `json:"tag"`
	InterfaceName          string       `json:"interface_name"`
	Address                []string     `json:"address"`
	MTU                    int          `json:"mtu"`
	AutoRoute              bool         `json:"auto_route"`
	StrictRoute            bool         `json:"strict_route"`
	Stack                  string       `json:"stack"`
	EndpointIndependentNat bool         `json:"endpoint_independent_nat"`
	Platform               *TUNPlatform `json:"platform,omitempty"`
}

// Outbounds
type DirectOutbound struct {
	Type string `json:"type"`
	Tag  string `json:"tag"`
}

// Route
type RouteConfig struct {
	Rules                 []RouteRule `json:"rules,omitempty"`
	Final                 string      `json:"final"`
	DefaultDomainResolver string      `json:"default_domain_resolver,omitempty"`
	AutoDetectInterface   bool        `json:"auto_detect_interface"`
}

type RouteRule struct {
	IPIsPrivate  *bool    `json:"ip_is_private,omitempty"`
	Domain       []string `json:"domain,omitempty"`
	DomainSuffix []string `json:"domain_suffix,omitempty"`
	Outbound     string   `json:"outbound"`
}

// TLS types
type TLSConfig struct {
	Enabled    bool           `json:"enabled"`
	ServerName string         `json:"server_name,omitempty"`
	UTLS       *UTLSConfig    `json:"utls,omitempty"`
	Reality    *RealityConfig `json:"reality,omitempty"`
	Insecure   bool           `json:"insecure,omitempty"`
}

type UTLSConfig struct {
	Enabled     bool   `json:"enabled"`
	Fingerprint string `json:"fingerprint"`
}

type RealityConfig struct {
	Enabled   bool   `json:"enabled"`
	PublicKey string `json:"public_key"`
	ShortID   string `json:"short_id"`
}

type TransportConfig struct {
	Type string `json:"type"`
	Path string `json:"path,omitempty"`
	Host string `json:"host,omitempty"`
}

type Options struct {
	TUNMode   bool
	RouteMode string // "all" | "ru" | "cn"
	DNSMode   string
	LogLevel  string
	AllowLAN  bool
}

func Generate(server subscription.Server, opts Options) (*SingBoxConfig, error) {
	proxyOut, err := buildProxyOutbound(server)
	if err != nil {
		return nil, fmt.Errorf("build outbound: %w", err)
	}

	level := opts.LogLevel
	if level == "" {
		level = "warn"
	}

	cfg := &SingBoxConfig{
		Log: &LogConfig{Level: level},
		Experimental: &ExperimentalConfig{
			ClashAPI: &ClashAPIConfig{
				ExternalController: "127.0.0.1:9090",
			},
		},
		DNS:      buildDNS(server, opts),
		Inbounds: buildInbounds(opts),
		Outbounds: []interface{}{
			proxyOut,
			DirectOutbound{Type: "direct", Tag: "direct"},
		},
		Route: buildRoute(server, opts),
	}

	return cfg, nil
}

// bypassSuffixes returns domain suffixes that should go direct for a given route mode.
func bypassSuffixes(mode string) []string {
	switch mode {
	case "ru":
		return []string{"ru", "рф", "ру"}
	case "cn":
		return []string{"cn", "com.cn", "net.cn", "org.cn", "gov.cn", "edu.cn"}
	}
	return nil
}

// buildDNS uses the new sing-box 1.12+ DNS server format.
// In TUN mode, the VPN server domain uses local-dns to avoid a bootstrap loop.
// In bypass mode (ru/cn), matching domains also resolve via local-dns.
func buildDNS(server subscription.Server, opts Options) *DNSConfig {
	boolTrue := true
	rules := []DNSRule{
		{IPIsPrivate: &boolTrue, Server: "local-dns"},
	}

	// Bypass domains for ru/cn mode resolve via local DNS (direct, no VPN)
	if suffixes := bypassSuffixes(opts.RouteMode); len(suffixes) > 0 {
		rules = append([]DNSRule{
			{DomainSuffix: suffixes, Server: "local-dns"},
		}, rules...)
	}

	// VPN server hostname resolves via local DNS to avoid a bootstrap loop
	if opts.TUNMode && server.Address != "" {
		rules = append([]DNSRule{
			{Domain: []string{server.Address}, Server: "local-dns"},
		}, rules...)
	}

	return &DNSConfig{
		Servers: []DNSServer{
			{
				Tag:        "proxy-dns",
				Type:       "https",
				Server:     "1.1.1.1",
				ServerPort: 443,
				Path:       "/dns-query",
			},
			{
				Tag:        "local-dns",
				Type:       "udp",
				Server:     "223.5.5.5",
				ServerPort: 53,
			},
		},
		Rules: rules,
		Final: "proxy-dns",
	}
}

func buildRoute(server subscription.Server, opts Options) RouteConfig {
	boolTrue := true
	rules := []RouteRule{
		{IPIsPrivate: &boolTrue, Outbound: "direct"},
	}

	// Domain bypass rules for ru/cn mode
	if suffixes := bypassSuffixes(opts.RouteMode); len(suffixes) > 0 {
		rules = append([]RouteRule{
			{DomainSuffix: suffixes, Outbound: "direct"},
		}, rules...)
	}

	// VPN server traffic goes direct to prevent routing loop through TUN
	if opts.TUNMode && server.Address != "" {
		rules = append([]RouteRule{
			{Domain: []string{server.Address}, Outbound: "direct"},
		}, rules...)
	}

	return RouteConfig{
		Rules:                 rules,
		Final:                 "proxy",
		DefaultDomainResolver: "proxy-dns",
		AutoDetectInterface:   true,
	}
}

func buildInbounds(opts Options) []interface{} {
	listen := "127.0.0.1"
	if opts.AllowLAN {
		listen = "0.0.0.0"
	}
	inbounds := []interface{}{
		MixedInbound{
			Type:       "mixed",
			Tag:        "mixed-in",
			Listen:     listen,
			ListenPort: 2080,
		},
	}
	if opts.TUNMode {
		inbounds = append(inbounds, TUNInbound{
			Type:                   "tun",
			Tag:                    "tun-in",
			InterfaceName:          "RustleBoost",
			Address:                []string{"172.19.0.1/30"},
			MTU:                    1500,
			AutoRoute:              true,
			StrictRoute:            false, // false = system services (NCSI) use physical NIC → NLA shows "Connected"
			Stack:                  "mixed",
			EndpointIndependentNat: true,
		})
	}
	return inbounds
}

// ── Outbound builders ─────────────────────────────────────────────────────

func buildProxyOutbound(server subscription.Server) (interface{}, error) {
	p := server.Params

	switch server.Protocol {
	case "VLESS":
		return buildVLESS(server, p)
	case "Hysteria2":
		return buildHysteria2(server, p)
	case "NaiveProxy":
		return buildNaive(server, p)
	case "Trojan":
		return buildTrojan(server, p), nil
	default:
		if raw, ok := p["raw_json"]; ok {
			var out interface{}
			if err := json.Unmarshal([]byte(raw), &out); err == nil {
				if m, ok := out.(map[string]interface{}); ok {
					m["tag"] = "proxy"
				}
				return out, nil
			}
		}
		return nil, fmt.Errorf("unsupported protocol: %s", server.Protocol)
	}
}

type VLESSOutbound struct {
	Type            string           `json:"type"`
	Tag             string           `json:"tag"`
	Server          string           `json:"server"`
	ServerPort      int              `json:"server_port"`
	UUID            string           `json:"uuid"`
	Flow            string           `json:"flow,omitempty"`
	PacketEncoding  string           `json:"packet_encoding,omitempty"`
	TLS             *TLSConfig       `json:"tls,omitempty"`
	Transport       *TransportConfig `json:"transport,omitempty"`
}

func buildVLESS(server subscription.Server, p map[string]string) (interface{}, error) {
	port, _ := strconv.Atoi(fmt.Sprint(server.Port))
	out := VLESSOutbound{
		Type:           "vless",
		Tag:            "proxy",
		Server:         server.Address,
		ServerPort:     port,
		UUID:           p["uuid"],
		Flow:           p["flow"],
		PacketEncoding: "xudp",
	}

	fp := coalesce(p["fp"], "chrome")
	switch p["security"] {
	case "reality":
		out.TLS = &TLSConfig{
			Enabled:    true,
			ServerName: p["sni"],
			UTLS:       &UTLSConfig{Enabled: true, Fingerprint: fp},
			Reality: &RealityConfig{
				Enabled:   true,
				PublicKey: p["pbk"],
				ShortID:   p["sid"],
			},
		}
	case "tls":
		out.TLS = &TLSConfig{
			Enabled:    true,
			ServerName: p["sni"],
			UTLS:       &UTLSConfig{Enabled: true, Fingerprint: fp},
		}
	}

	switch p["type"] {
	case "ws":
		out.Transport = &TransportConfig{Type: "ws", Path: coalesce(p["path"], "/"), Host: p["host"]}
	case "grpc":
		out.Transport = &TransportConfig{Type: "grpc", Path: p["path"]}
	case "httpupgrade":
		out.Transport = &TransportConfig{Type: "httpupgrade", Path: coalesce(p["path"], "/"), Host: p["host"]}
	}

	return out, nil
}

func buildHysteria2(server subscription.Server, p map[string]string) (interface{}, error) {
	port, _ := strconv.Atoi(fmt.Sprint(server.Port))
	type Obfs struct {
		Type     string `json:"type"`
		Password string `json:"password"`
	}
	type Hy2 struct {
		Type       string     `json:"type"`
		Tag        string     `json:"tag"`
		Server     string     `json:"server"`
		ServerPort int        `json:"server_port"`
		Password   string     `json:"password"`
		TLS        *TLSConfig `json:"tls,omitempty"`
		Obfs       *Obfs      `json:"obfs,omitempty"`
	}
	out := Hy2{
		Type: "hysteria2", Tag: "proxy",
		Server: server.Address, ServerPort: port,
		Password: p["password"],
		TLS: &TLSConfig{
			Enabled:    true,
			ServerName: coalesce(p["sni"], server.Address),
			Insecure:   p["insecure"] == "1",
		},
	}
	if p["obfs"] == "salamander" {
		out.Obfs = &Obfs{Type: "salamander", Password: p["obfs-password"]}
	}
	return out, nil
}

func buildNaive(server subscription.Server, p map[string]string) (interface{}, error) {
	port, _ := strconv.Atoi(fmt.Sprint(server.Port))
	type Naive struct {
		Type       string     `json:"type"`
		Tag        string     `json:"tag"`
		Server     string     `json:"server"`
		ServerPort int        `json:"server_port"`
		Username   string     `json:"username"`
		Password   string     `json:"password"`
		TLS        *TLSConfig `json:"tls,omitempty"`
	}
	return Naive{
		Type: "naive", Tag: "proxy",
		Server: server.Address, ServerPort: port,
		Username: p["username"], Password: p["password"],
		TLS: &TLSConfig{Enabled: true, ServerName: server.Address},
	}, nil
}

func buildTrojan(server subscription.Server, p map[string]string) interface{} {
	port, _ := strconv.Atoi(fmt.Sprint(server.Port))
	type TrojanOut struct {
		Type       string     `json:"type"`
		Tag        string     `json:"tag"`
		Server     string     `json:"server"`
		ServerPort int        `json:"server_port"`
		Password   string     `json:"password"`
		TLS        *TLSConfig `json:"tls,omitempty"`
	}
	return TrojanOut{
		Type: "trojan", Tag: "proxy",
		Server: server.Address, ServerPort: port,
		Password: p["password"],
		TLS:      &TLSConfig{Enabled: true, ServerName: coalesce(p["sni"], server.Address)},
	}
}

func coalesce(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
