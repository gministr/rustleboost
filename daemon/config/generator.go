// Package config generates the core configurations this client runs.
//
// Two cores cooperate. sing-box always runs and owns the TUN interface, DNS
// and routing. Xray runs alongside it whenever the selected node uses a
// transport sing-box does not implement — XHTTP above all, which is what the
// LTE nodes use. In that arrangement sing-box's "proxy" outbound is a SOCKS
// client pointed at Xray, and Xray alone talks to the VPN server.
//
// sing-box 1.13 compatibility notes — all deprecated APIs avoided:
//   - DNS uses the type/server form, not the legacy address string
//   - route requires default_domain_resolver
//   - TUN takes no sniff fields; sniffing is a route action
//   - dns-out was removed; DNS is answered by the dns section
package config

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/vpnclient/daemon/subscription"
)

// Local ports. These are deliberately off the well-trodden 10808/10809 pair,
// which other VPN clients on the same machine tend to occupy.
const (
	MixedPort     = 2080  // sing-box mixed inbound (system proxy)
	XraySocksPort = 21080 // Xray SOCKS inbound that sing-box forwards to
)

// ── sing-box ──────────────────────────────────────────────────────────────

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

type DNSConfig struct {
	Servers []DNSServer `json:"servers"`
	Rules   []DNSRule   `json:"rules,omitempty"`
	Final   string      `json:"final"`
}

type DNSServer struct {
	Tag        string `json:"tag"`
	Type       string `json:"type"` // udp | tcp | tls | https | quic | local
	Server     string `json:"server,omitempty"`
	ServerPort int    `json:"server_port,omitempty"`
	Path       string `json:"path,omitempty"`
}

type DNSRule struct {
	IPIsPrivate  *bool    `json:"ip_is_private,omitempty"`
	Domain       []string `json:"domain,omitempty"`
	DomainSuffix []string `json:"domain_suffix,omitempty"`
	Server       string   `json:"server"`
}

type MixedInbound struct {
	Type       string `json:"type"`
	Tag        string `json:"tag"`
	Listen     string `json:"listen"`
	ListenPort int    `json:"listen_port"`
}

type TUNInbound struct {
	Type                   string   `json:"type"`
	Tag                    string   `json:"tag"`
	InterfaceName          string   `json:"interface_name"`
	Address                []string `json:"address"`
	MTU                    int      `json:"mtu"`
	AutoRoute              bool     `json:"auto_route"`
	StrictRoute            bool     `json:"strict_route"`
	Stack                  string   `json:"stack"`
	EndpointIndependentNat bool     `json:"endpoint_independent_nat"`
}

type DirectOutbound struct {
	Type string `json:"type"`
	Tag  string `json:"tag"`
}

// SocksOutbound is how sing-box hands traffic to Xray.
type SocksOutbound struct {
	Type       string `json:"type"`
	Tag        string `json:"tag"`
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
	Version    string `json:"version"`
}

type RouteConfig struct {
	Rules                 []RouteRule `json:"rules,omitempty"`
	Final                 string      `json:"final"`
	DefaultDomainResolver string      `json:"default_domain_resolver,omitempty"`
	AutoDetectInterface   bool        `json:"auto_detect_interface"`
}

type RouteRule struct {
	Action       string   `json:"action,omitempty"`
	IPIsPrivate  *bool    `json:"ip_is_private,omitempty"`
	Domain       []string `json:"domain,omitempty"`
	DomainSuffix []string `json:"domain_suffix,omitempty"`
	IPCIDR       []string `json:"ip_cidr,omitempty"`
	ProcessName  []string `json:"process_name,omitempty"`
	Outbound     string   `json:"outbound,omitempty"`
}

type TLSConfig struct {
	Enabled    bool           `json:"enabled"`
	ServerName string         `json:"server_name,omitempty"`
	ALPN       []string       `json:"alpn,omitempty"`
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

type Options struct {
	TUNMode   bool
	RouteMode string // "all" | "ru" | "cn"
	DNSMode   string
	LogLevel  string
	AllowLAN  bool
}

// ── Xray ──────────────────────────────────────────────────────────────────

type XrayConfig struct {
	Log       *XrayLog          `json:"log,omitempty"`
	Inbounds  []interface{}     `json:"inbounds"`
	Outbounds []json.RawMessage `json:"outbounds"`
}

type XrayLog struct {
	LogLevel string `json:"loglevel"`
}

type XraySocksInbound struct {
	Tag      string            `json:"tag"`
	Listen   string            `json:"listen"`
	Port     int               `json:"port"`
	Protocol string            `json:"protocol"`
	Settings XraySocksSettings `json:"settings"`
	Sniffing *XraySniffing     `json:"sniffing,omitempty"`
}

type XraySocksSettings struct {
	Auth string `json:"auth"`
	UDP  bool   `json:"udp"`
}

type XraySniffing struct {
	Enabled      bool     `json:"enabled"`
	DestOverride []string `json:"destOverride"`
	RouteOnly    bool     `json:"routeOnly"`
}

// GenerateXray builds the Xray side: a local SOCKS inbound feeding the node's
// outbound, which the subscription already supplied fully formed.
func GenerateXray(server subscription.Server, opts Options) (*XrayConfig, error) {
	if len(server.Outbound) == 0 {
		return nil, fmt.Errorf("server %q has no xray outbound", server.Name)
	}

	level := opts.LogLevel
	if level == "" {
		level = "warning"
	}

	direct, _ := json.Marshal(map[string]string{"tag": "direct", "protocol": "freedom"})
	block, _ := json.Marshal(map[string]string{"tag": "block", "protocol": "blackhole"})

	return &XrayConfig{
		Log: &XrayLog{LogLevel: level},
		Inbounds: []interface{}{
			XraySocksInbound{
				Tag:      "socks-in",
				Listen:   "127.0.0.1",
				Port:     XraySocksPort,
				Protocol: "socks",
				Settings: XraySocksSettings{Auth: "noauth", UDP: true},
				// sing-box has already sniffed and passes hostnames through,
				// so Xray only needs sniffing for its own destination override.
				Sniffing: &XraySniffing{
					Enabled:      true,
					DestOverride: []string{"http", "tls", "quic"},
				},
			},
		},
		Outbounds: []json.RawMessage{server.Outbound, direct, block},
	}, nil
}

// NeedsXray reports whether a server must be carried by Xray-core.
func NeedsXray(server subscription.Server) bool {
	return server.Engine == subscription.EngineXray && len(server.Outbound) > 0
}

// Generate builds the sing-box side.
func Generate(server subscription.Server, opts Options) (*SingBoxConfig, error) {
	proxyOut, err := buildProxyOutbound(server)
	if err != nil {
		return nil, fmt.Errorf("build outbound: %w", err)
	}

	level := opts.LogLevel
	if level == "" {
		level = "warn"
	}

	return &SingBoxConfig{
		Log: &LogConfig{Level: level},
		Experimental: &ExperimentalConfig{
			ClashAPI: &ClashAPIConfig{ExternalController: "127.0.0.1:9090"},
		},
		DNS:      buildDNS(server, opts),
		Inbounds: buildInbounds(opts),
		Outbounds: []interface{}{
			proxyOut,
			DirectOutbound{Type: "direct", Tag: "direct"},
		},
		Route: buildRoute(server, opts),
	}, nil
}

func bypassSuffixes(mode string) []string {
	switch mode {
	case "ru":
		return []string{"ru", "рф", "ру"}
	case "cn":
		return []string{"cn", "com.cn", "net.cn", "org.cn", "gov.cn", "edu.cn"}
	}
	return nil
}

func buildDNS(server subscription.Server, opts Options) *DNSConfig {
	boolTrue := true
	var rules []DNSRule

	// The node's own hostname must resolve without the tunnel, or the tunnel
	// can never come up in the first place.
	if server.Address != "" && net.ParseIP(server.Address) == nil {
		rules = append(rules, DNSRule{Domain: []string{server.Address}, Server: "local-dns"})
	}
	if suffixes := bypassSuffixes(opts.RouteMode); len(suffixes) > 0 {
		rules = append(rules, DNSRule{DomainSuffix: suffixes, Server: "local-dns"})
	}
	rules = append(rules, DNSRule{IPIsPrivate: &boolTrue, Server: "local-dns"})

	return &DNSConfig{
		Servers: []DNSServer{
			{Tag: "proxy-dns", Type: "https", Server: "1.1.1.1", ServerPort: 443, Path: "/dns-query"},
			// The system resolver bootstraps the node's hostname before the
			// tunnel exists. A hardcoded public resolver is the wrong choice
			// here: it is exactly the kind of address that gets throttled on
			// the networks this client is meant to work on.
			{Tag: "local-dns", Type: "local"},
		},
		Rules: rules,
		Final: "proxy-dns",
	}
}

func buildRoute(server subscription.Server, opts Options) RouteConfig {
	boolTrue := true
	rules := []RouteRule{
		// Sniffing must run first: domain rules below match on the sniffed
		// hostname, not on the raw destination IP.
		{Action: "sniff"},
	}

	if opts.TUNMode {
		// Everything the cores themselves send must bypass the tunnel,
		// otherwise the proxy leg is routed back into TUN and deadlocks. This
		// covers nodes addressed by bare IP, which a domain rule cannot match.
		rules = append(rules, RouteRule{
			ProcessName: []string{"xray.exe", "sing-box.exe"},
			Outbound:    "direct",
		})

		if server.Address != "" {
			if net.ParseIP(server.Address) != nil {
				rules = append(rules, RouteRule{
					IPCIDR:   []string{hostRoute(server.Address)},
					Outbound: "direct",
				})
			} else {
				rules = append(rules, RouteRule{
					Domain:   []string{server.Address},
					Outbound: "direct",
				})
			}
		}
	}

	rules = append(rules, RouteRule{IPIsPrivate: &boolTrue, Outbound: "direct"})

	if suffixes := bypassSuffixes(opts.RouteMode); len(suffixes) > 0 {
		rules = append(rules, RouteRule{DomainSuffix: suffixes, Outbound: "direct"})
	}

	return RouteConfig{
		Rules:                 rules,
		Final:                 "proxy",
		DefaultDomainResolver: "proxy-dns",
		AutoDetectInterface:   true,
	}
}

// hostRoute turns a bare IP into the single-host CIDR sing-box expects.
func hostRoute(ip string) string {
	if strings.Contains(ip, ":") {
		return ip + "/128"
	}
	return ip + "/32"
}

func buildInbounds(opts Options) []interface{} {
	listen := "127.0.0.1"
	if opts.AllowLAN {
		listen = "0.0.0.0"
	}

	inbounds := []interface{}{
		MixedInbound{Type: "mixed", Tag: "mixed-in", Listen: listen, ListenPort: MixedPort},
	}

	if opts.TUNMode {
		inbounds = append(inbounds, TUNInbound{
			Type:          "tun",
			Tag:           "tun-in",
			InterfaceName: "RustleBoost",
			Address:       []string{"172.19.0.1/30"},
			MTU:           1500,
			AutoRoute:     true,
			// false keeps Windows' own connectivity probes on the physical
			// NIC, so the tray reports "Connected" instead of "No Internet".
			StrictRoute:            false,
			Stack:                  "mixed",
			EndpointIndependentNat: true,
		})
	}
	return inbounds
}

// ── Outbound builders ─────────────────────────────────────────────────────

func buildProxyOutbound(server subscription.Server) (interface{}, error) {
	if NeedsXray(server) {
		return SocksOutbound{
			Type:       "socks",
			Tag:        "proxy",
			Server:     "127.0.0.1",
			ServerPort: XraySocksPort,
			Version:    "5",
		}, nil
	}

	// A subscription published as a sing-box config supplies its outbound
	// ready-made; pass it through rather than rebuilding it field by field.
	if len(server.Outbound) > 0 {
		var raw interface{}
		if err := json.Unmarshal(server.Outbound, &raw); err != nil {
			return nil, fmt.Errorf("decode outbound for %q: %w", server.Name, err)
		}
		return raw, nil
	}

	p := server.Params
	switch server.Protocol {
	case "Hysteria2":
		return buildHysteria2(server, p), nil
	case "TUIC":
		return buildTUIC(server, p), nil
	case "NaiveProxy":
		return buildNaive(server, p), nil
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", server.Protocol)
	}
}

func buildHysteria2(server subscription.Server, p map[string]string) interface{} {
	type obfs struct {
		Type     string `json:"type"`
		Password string `json:"password"`
	}
	type hy2 struct {
		Type       string     `json:"type"`
		Tag        string     `json:"tag"`
		Server     string     `json:"server"`
		ServerPort int        `json:"server_port"`
		Password   string     `json:"password"`
		TLS        *TLSConfig `json:"tls,omitempty"`
		Obfs       *obfs      `json:"obfs,omitempty"`
	}

	out := hy2{
		Type: "hysteria2", Tag: "proxy",
		Server: server.Address, ServerPort: server.Port,
		Password: p["password"],
		TLS: &TLSConfig{
			Enabled:    true,
			ServerName: coalesce(p["sni"], server.Address),
			Insecure:   p["insecure"] == "1",
		},
	}
	if p["obfs"] == "salamander" {
		out.Obfs = &obfs{Type: "salamander", Password: p["obfs-password"]}
	}
	return out
}

func buildTUIC(server subscription.Server, p map[string]string) interface{} {
	type tuic struct {
		Type              string     `json:"type"`
		Tag               string     `json:"tag"`
		Server            string     `json:"server"`
		ServerPort        int        `json:"server_port"`
		UUID              string     `json:"uuid"`
		Password          string     `json:"password"`
		CongestionControl string     `json:"congestion_control,omitempty"`
		UDPRelayMode      string     `json:"udp_relay_mode,omitempty"`
		TLS               *TLSConfig `json:"tls,omitempty"`
	}

	return tuic{
		Type: "tuic", Tag: "proxy",
		Server: server.Address, ServerPort: server.Port,
		UUID:              p["uuid"],
		Password:          p["password"],
		CongestionControl: p["congestion_control"],
		UDPRelayMode:      p["udp_relay_mode"],
		TLS: &TLSConfig{
			Enabled:    true,
			ServerName: coalesce(p["sni"], server.Address),
			ALPN:       splitCSV(p["alpn"]),
			Insecure:   p["insecure"] == "1",
		},
	}
}

func buildNaive(server subscription.Server, p map[string]string) interface{} {
	type naive struct {
		Type       string     `json:"type"`
		Tag        string     `json:"tag"`
		Server     string     `json:"server"`
		ServerPort int        `json:"server_port"`
		Username   string     `json:"username"`
		Password   string     `json:"password"`
		TLS        *TLSConfig `json:"tls,omitempty"`
	}

	return naive{
		Type: "naive", Tag: "proxy",
		Server: server.Address, ServerPort: server.Port,
		Username: p["username"], Password: p["password"],
		TLS: &TLSConfig{Enabled: true, ServerName: server.Address},
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

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
