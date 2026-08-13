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
	ClashAPI  *ClashAPIConfig  `json:"clash_api,omitempty"`
	CacheFile *CacheFileConfig `json:"cache_file,omitempty"`
}

type ClashAPIConfig struct {
	ExternalController string `json:"external_controller"`
}

// CacheFileConfig persists the FakeIP mapping. Without it every reconnect
// hands out fresh placeholder addresses while applications still hold the
// previous ones, which strands their open connections.
type CacheFileConfig struct {
	Enabled     bool   `json:"enabled"`
	Path        string `json:"path,omitempty"`
	StoreFakeIP bool   `json:"store_fakeip,omitempty"`
}

type DNSConfig struct {
	Servers          []DNSServer `json:"servers"`
	Rules            []DNSRule   `json:"rules,omitempty"`
	Final            string      `json:"final"`
	IndependentCache bool        `json:"independent_cache,omitempty"`
}

type DNSServer struct {
	Tag        string `json:"tag"`
	Type       string `json:"type"` // udp | tcp | tls | https | quic | local | fakeip
	Server     string `json:"server,omitempty"`
	ServerPort int    `json:"server_port,omitempty"`
	Path       string `json:"path,omitempty"`
	// Detour names the outbound used to reach this server. "direct" makes
	// sing-box dial it through its own direct outbound, which binds to the
	// physical interface — the query then never enters the tunnel and cannot
	// be caught by the hijack-dns rules.
	Detour string `json:"detour,omitempty"`
	// FakeIP pools. sing-box 1.12 replaced the old top-level "fakeip" block
	// with these fields on a server of type "fakeip".
	Inet4Range string `json:"inet4_range,omitempty"`
	Inet6Range string `json:"inet6_range,omitempty"`
}

type DNSRule struct {
	IPIsPrivate  *bool    `json:"ip_is_private,omitempty"`
	Domain       []string `json:"domain,omitempty"`
	DomainSuffix []string `json:"domain_suffix,omitempty"`
	ProcessName  []string `json:"process_name,omitempty"`
	QueryType    []string `json:"query_type,omitempty"`
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
	Protocol     string   `json:"protocol,omitempty"`
	Port         []int    `json:"port,omitempty"`
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

// TransportConfig covers the transports sing-box implements natively.
// Field names and shapes were verified directly against the bundled binary
// (`sing-box check`) rather than assumed from Xray's schema, which differs:
// ws needs Host under "headers", not a bare "host" field, and grpc's field is
// "service_name", not "serviceName" — both fail as "unknown field" otherwise,
// because sing-box's decoder rejects unrecognized JSON keys outright.
type TransportConfig struct {
	Type        string            `json:"type"`
	Path        string            `json:"path,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	ServiceName string            `json:"service_name,omitempty"`
}

type Options struct {
	TUNMode    bool
	RouteMode  string // "all" | "ru" | "cn"
	DNSMode    string
	LogLevel   string
	AllowLAN   bool
	RouterMode string // "auto" | "singbox" | "xray"
	// ServerIPs are the node's addresses resolved before the tunnel came up.
	// They become direct routes so the proxy leg cannot be swallowed by the
	// tunnel it is carrying, without relying on process matching.
	ServerIPs []string
	// SystemDNS are the machine's own resolvers, read before the tunnel came
	// up. On a censored network the ISP's resolver is the address most likely
	// to answer, so it is preferred over any fixed public one.
	SystemDNS []string
}

// Router mode values, mirrored from storage.Settings.RouterMode.
const (
	RouterAuto    = "auto"
	RouterSingBox = "singbox"
	RouterXray    = "xray"
)

// ── Xray ──────────────────────────────────────────────────────────────────

type XrayConfig struct {
	Log       *XrayLog          `json:"log,omitempty"`
	Inbounds  []interface{}     `json:"inbounds"`
	Outbounds []json.RawMessage `json:"outbounds"`
	Routing   *XrayRouting      `json:"routing,omitempty"`
}

type XrayRouting struct {
	Rules []XrayRule `json:"rules"`
}

type XrayRule struct {
	Type        string   `json:"type"`
	InboundTag  []string `json:"inboundTag"`
	OutboundTag string   `json:"outboundTag"`
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

// ProbeTarget pairs a server with the local port its probe traffic uses.
type ProbeTarget struct {
	ServerID string
	Outbound json.RawMessage
	Port     int
}

// GenerateXrayProbe builds a measurement config: one SOCKS inbound per node,
// each routed to that node's own outbound.
//
// Measuring latency by opening a TCP connection to the node only times the
// first hop — it says nothing about whether the proxy actually carries
// traffic, and for CDN-fronted nodes every location returns the same few
// milliseconds. Sending a real request through each outbound is the only
// measurement that reflects what the user will experience, and one process
// with N inbounds does all of them at once.
func GenerateXrayProbe(targets []ProbeTarget) (*XrayConfig, error) {
	if len(targets) == 0 {
		return nil, fmt.Errorf("no probe targets")
	}

	cfg := &XrayConfig{
		Log:     &XrayLog{LogLevel: "none"},
		Routing: &XrayRouting{},
	}

	for i, target := range targets {
		inTag := fmt.Sprintf("in-%d", i)
		outTag := fmt.Sprintf("out-%d", i)

		cfg.Inbounds = append(cfg.Inbounds, XraySocksInbound{
			Tag:      inTag,
			Listen:   "127.0.0.1",
			Port:     target.Port,
			Protocol: "socks",
			Settings: XraySocksSettings{Auth: "noauth", UDP: false},
		})

		outbound, err := retagXrayOutbound(target.Outbound, outTag)
		if err != nil {
			return nil, fmt.Errorf("probe outbound %s: %w", target.ServerID, err)
		}
		cfg.Outbounds = append(cfg.Outbounds, outbound)

		cfg.Routing.Rules = append(cfg.Routing.Rules, XrayRule{
			Type:        "field",
			InboundTag:  []string{inTag},
			OutboundTag: outTag,
		})
	}

	return cfg, nil
}

func retagXrayOutbound(raw json.RawMessage, tag string) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(tag)
	if err != nil {
		return nil, err
	}
	m["tag"] = encoded
	return json.Marshal(m)
}

// ResolveEngine decides which core actually carries a node's traffic,
// honouring the user's router-mode override.
//
// Automatic selection sounds strictly better than a manual switch — sing-box
// natively is one fewer process, one fewer thing that can fail. It is not:
// which implementation's handshake gets through a given network is not a
// property of the node or the hardware, it is a property of what that
// network's own filtering happens to fingerprint on that day, and it can
// differ between two people on the same subscription. A choice that only
// the user's own trial and error can make correctly must stay theirs.
//
// Only one kind of node has no choice regardless of the setting:
// Hysteria2/TUIC/NaiveProxy have no Xray path in this client at all, so even
// "Xray" mode falls back to sing-box for them. XHTTP/mKCP nodes are the
// opposite case — sing-box has no such transport — but forcing "sing-box"
// mode is still honoured literally here: it resolves to sing-box, and it is
// left to the caller (buildProxyOutbound) to refuse with a clear reason
// rather than have ResolveEngine quietly substitute Xray back in, which
// would defeat the point of forcing sing-box in the first place — the user
// asked specifically to rule it out.
func ResolveEngine(server subscription.Server, routerMode string) string {
	singBoxOnly := server.Engine == subscription.EngineSingBox && len(server.Outbound) == 0

	switch routerMode {
	case RouterXray:
		if singBoxOnly {
			return subscription.EngineSingBox
		}
		return subscription.EngineXray
	case RouterSingBox:
		return subscription.EngineSingBox
	default: // RouterAuto, or unset
		if server.Engine == subscription.EngineXray {
			return subscription.EngineXray
		}
		return subscription.EngineSingBox
	}
}

// NeedsXray reports whether a server would use Xray under automatic engine
// selection. It has no router-mode override; use it only where one does not
// apply (latency measurement, tests) — a real connect must go through
// ResolveEngine with the user's actual setting.
func NeedsXray(server subscription.Server) bool {
	return ResolveEngine(server, RouterAuto) == subscription.EngineXray
}

// Generate builds the sing-box side.
func Generate(server subscription.Server, opts Options) (*SingBoxConfig, error) {
	proxyOut, err := buildProxyOutbound(server, opts.RouterMode)
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
			ClashAPI:  &ClashAPIConfig{ExternalController: "127.0.0.1:9090"},
			CacheFile: &CacheFileConfig{Enabled: true, Path: "cache.db", StoreFakeIP: true},
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

// GenerateBlocking builds a tunnel that accepts traffic and drops it.
//
// This is the kill switch. When a core dies unexpectedly the TUN adapter
// would otherwise disappear and Windows would silently fall back to the
// physical interface, sending in the clear exactly the traffic the user
// turned a VPN on to protect. Keeping the adapter up with a reject rule means
// connections fail instead of leaking.
func GenerateBlocking(opts Options) *SingBoxConfig {
	boolTrue := true

	return &SingBoxConfig{
		Log:      &LogConfig{Level: "warn"},
		Inbounds: buildInbounds(opts),
		Outbounds: []interface{}{
			DirectOutbound{Type: "direct", Tag: "direct"},
		},
		Route: RouteConfig{
			Rules: []RouteRule{
				// Local traffic keeps working, so the app itself and the
				// user's LAN devices stay reachable.
				{IPIsPrivate: &boolTrue, Outbound: "direct"},
				{Action: "reject"},
			},
			Final:               "direct",
			AutoDetectInterface: true,
		},
	}
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

// buildDNS resolves names through FakeIP rather than a real upstream.
//
// The previous design sent every lookup to DNS-over-HTTPS on 1.1.1.1 and
// routed it through the tunnel. On a network where that endpoint is blocked
// — common enough for Russian ISPs — every single query died on a ten second
// timeout, so no name resolved and nothing loaded, while the tunnel itself
// was demonstrably up and carrying the readiness probe. Depending on one
// hardcoded public resolver put a single, easily blocked point of failure in
// front of the entire connection.
//
// FakeIP removes that dependency completely: an A/AAAA query is answered
// instantly from a reserved range with no network traffic at all, the
// original domain travels to the proxy, and the node on the far side does
// the real resolution. This is what the panel's own sing-box template does.
//
// Real resolution is still needed in three places, all of which must bypass
// FakeIP or the connection cannot be established at all:
//   - the node's own hostname, or the tunnel could never come up;
//   - Xray's lookups, since it dials the node itself and a fake address
//     would send it back into the tunnel it is supposed to be carrying;
//   - domains routed direct, which need an address the direct outbound can
//     actually connect to.
func buildDNS(server subscription.Server, opts Options) *DNSConfig {
	boolTrue := true
	var rules []DNSRule

	if opts.TUNMode {
		rules = append(rules, DNSRule{
			ProcessName: []string{"xray.exe", "xray"},
			Server:      "local-dns",
		})
	}
	if server.Address != "" && net.ParseIP(server.Address) == nil {
		rules = append(rules, DNSRule{Domain: []string{server.Address}, Server: "local-dns"})
	}
	if suffixes := bypassSuffixes(opts.RouteMode); len(suffixes) > 0 {
		rules = append(rules, DNSRule{DomainSuffix: suffixes, Server: "local-dns"})
	}
	rules = append(rules,
		DNSRule{IPIsPrivate: &boolTrue, Server: "local-dns"},
		DNSRule{QueryType: []string{"A", "AAAA"}, Server: "fakeip-dns"},
	)

	servers := []DNSServer{{
		Tag:        "fakeip-dns",
		Type:       "fakeip",
		Inet4Range: "198.18.0.0/15",
		Inet6Range: "fc00::/18",
	}}
	servers = append(servers, localResolvers(opts)...)

	return &DNSConfig{
		Servers: servers,
		Rules:   rules,
		// Anything that is not an address lookup (PTR, HTTPS records, SRV)
		// has no FakeIP equivalent and goes to the real resolver.
		Final:            "local-dns",
		IndependentCache: true,
	}
}

// localResolvers builds the server that answers the lookups FakeIP cannot.
//
// It must never be sing-box's "local" type. That resolves through the OS,
// whose packets follow the system routing table straight into the tunnel,
// where the hijack-dns rules catch them and hand them back to sing-box —
// which asks the OS again. The query loops until it times out, and because
// this resolver serves the node's own hostname and every direct-routed
// domain, the whole connection dies with it. Observed as repeated
// "read udp <lan-ip>:x-><router-ip>:53: i/o timeout" against a router that
// was reachable the entire time.
//
// Naming the addresses explicitly and dialling them through the direct
// outbound keeps the query on the physical interface, out of the tunnel's
// reach.
func localResolvers(opts Options) []DNSServer {
	addresses := opts.SystemDNS
	if len(addresses) == 0 {
		// Only if the machine's own resolvers could not be read. These are
		// a poor substitute — they are exactly the addresses most likely to
		// be blocked on the networks this client exists for — but a fixed
		// fallback beats no resolver at all.
		addresses = []string{"8.8.8.8", "1.1.1.1"}
	}

	servers := make([]DNSServer, 0, len(addresses))
	for i, address := range addresses {
		tag := "local-dns"
		if i > 0 {
			tag = fmt.Sprintf("local-dns-%d", i+1)
		}
		// No detour: sing-box refuses one pointing at a plain direct outbound
		// ("detour to an empty direct outbound makes no sense") and fails to
		// start. Its own DNS dials do not pass through the inbound route
		// rules anyway, so they are never caught by hijack-dns.
		servers = append(servers, DNSServer{
			Tag:    tag,
			Type:   "udp",
			Server: address,
		})
	}
	return servers
}

func buildRoute(server subscription.Server, opts Options) RouteConfig {
	boolTrue := true
	rules := []RouteRule{
		// Sniffing must run first: domain rules below match on the sniffed
		// hostname, not on the raw destination IP.
		{Action: "sniff"},
	}

	if opts.TUNMode {
		// Answer DNS inside sing-box instead of letting queries leak to
		// whatever resolver the physical adapter uses. Windows' connectivity
		// check resolves dns.msftncsi.com before it will call an interface
		// online, so this is what gets the tray out of "No Internet".
		rules = append(rules,
			RouteRule{Action: "hijack-dns", Protocol: "dns"},
			RouteRule{Action: "hijack-dns", Port: []int{53}},
		)

		// Everything the cores themselves send must bypass the tunnel,
		// otherwise the proxy leg is routed back into TUN and deadlocks.
		rules = append(rules, RouteRule{
			ProcessName: []string{"xray.exe", "sing-box.exe"},
			Outbound:    "direct",
		})

		// Process matching is not guaranteed to succeed — it depends on
		// sing-box being able to attribute a connection to an owning process,
		// which does not always work. When it silently fails, Xray's own
		// connection to the node is captured by the tunnel it is carrying,
		// and every request dies with an EOF that names no cause. Matching
		// the node's addresses directly does not depend on that mechanism, so
		// both rules are present and either one alone is sufficient.
		var cidrs []string
		if server.Address != "" && net.ParseIP(server.Address) != nil {
			cidrs = append(cidrs, hostRoute(server.Address))
		}
		for _, ip := range opts.ServerIPs {
			if parsed := net.ParseIP(ip); parsed != nil {
				cidrs = append(cidrs, hostRoute(ip))
			}
		}
		if len(cidrs) > 0 {
			rules = append(rules, RouteRule{IPCIDR: cidrs, Outbound: "direct"})
		}
		if server.Address != "" && net.ParseIP(server.Address) == nil {
			rules = append(rules, RouteRule{
				Domain:   []string{server.Address},
				Outbound: "direct",
			})
		}
	}

	rules = append(rules, RouteRule{IPIsPrivate: &boolTrue, Outbound: "direct"})

	if suffixes := bypassSuffixes(opts.RouteMode); len(suffixes) > 0 {
		rules = append(rules, RouteRule{DomainSuffix: suffixes, Outbound: "direct"})
	}

	return RouteConfig{
		Rules:                 rules,
		Final:                 "proxy",
		// Outbounds resolve through the real resolver. Handing them a FakeIP
		// address would mean dialling a placeholder that routes nowhere.
		DefaultDomainResolver: "local-dns",
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
			AutoRoute: true,
			// Off deliberately. It was switched on to make Windows report the
			// adapter as online, by forcing the connectivity probe through
			// the tunnel. On Windows it installs filters that drop traffic
			// outside the tunnel — including the reply to a DNS query sent
			// from the physical interface, which is how one machine ended up
			// with every lookup timing out ("read response: i/o timeout"
			// against a router that was answering everyone else). FakeIP now
			// resolves the probe's hostname without a real lookup and carries
			// the connection through the tunnel, so the original reason for
			// turning this on no longer applies.
			StrictRoute:            false,
			Stack:                  "mixed",
			EndpointIndependentNat: true,
		})
	}
	return inbounds
}

// ── Outbound builders ─────────────────────────────────────────────────────

func buildProxyOutbound(server subscription.Server, routerMode string) (interface{}, error) {
	engine := ResolveEngine(server, routerMode)

	if engine == subscription.EngineXray {
		if len(server.Outbound) == 0 {
			return nil, fmt.Errorf("узел %q не поддерживает режим Xray", server.Name)
		}
		return SocksOutbound{
			Type:       "socks",
			Tag:        "proxy",
			Server:     "127.0.0.1",
			ServerPort: XraySocksPort,
			Version:    "5",
		}, nil
	}

	p := server.Params

	// A subscription published AS a sing-box config supplies its outbound
	// ready-made — pass it through rather than rebuilding it field by field.
	// That path never populates Params, which is how it is told apart from a
	// Remnawave /v2ray-json node: those carry an Xray-shaped Outbound (wrong
	// field names for sing-box) alongside Params extracted for exactly this
	// native path.
	if len(p) == 0 && len(server.Outbound) > 0 {
		var raw interface{}
		if err := json.Unmarshal(server.Outbound, &raw); err != nil {
			return nil, fmt.Errorf("decode outbound for %q: %w", server.Name, err)
		}
		return raw, nil
	}

	// Reaching here with a node whose engine can only be Xray (an XHTTP/mKCP
	// transport) means the user forced "только sing-box" against a node that
	// genuinely cannot run there. Building anyway would silently emit a
	// wrong-transport outbound — e.g. XHTTP treated as plain TCP — that fails
	// to connect with no clue why. Say so instead.
	if server.Engine == subscription.EngineXray {
		return nil, fmt.Errorf(
			"узел %q работает только через Xray (%s) — переключите режим ядра на «Xray» или «Авто» в настройках",
			server.Name, server.Transport)
	}

	switch server.Protocol {
	case "VLESS":
		return buildVLESS(server, p), nil
	case "Trojan":
		return buildTrojan(server, p), nil
	case "Shadowsocks":
		return buildShadowsocks(server, p), nil
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

func buildTLS(p map[string]string) *TLSConfig {
	switch p["security"] {
	case "reality":
		return &TLSConfig{
			Enabled:    true,
			ServerName: p["sni"],
			UTLS:       &UTLSConfig{Enabled: true, Fingerprint: coalesce(p["fp"], "chrome")},
			Reality: &RealityConfig{
				Enabled:   true,
				PublicKey: p["pbk"],
				ShortID:   p["sid"],
			},
		}
	case "tls":
		return &TLSConfig{
			Enabled:    true,
			ServerName: p["sni"],
			ALPN:       splitCSV(p["alpn"]),
			UTLS:       &UTLSConfig{Enabled: true, Fingerprint: coalesce(p["fp"], "chrome")},
			Insecure:   p["insecure"] == "1",
		}
	default:
		return nil
	}
}

func buildTransport(network string, p map[string]string) *TransportConfig {
	switch network {
	case "ws":
		t := &TransportConfig{Type: "ws", Path: coalesce(p["path"], "/")}
		if host := p["host"]; host != "" {
			t.Headers = map[string]string{"Host": host}
		}
		return t
	case "grpc":
		return &TransportConfig{Type: "grpc", ServiceName: p["serviceName"]}
	case "httpupgrade":
		return &TransportConfig{Type: "httpupgrade", Path: coalesce(p["path"], "/")}
	case "http", "h2":
		t := &TransportConfig{Type: "http", Path: coalesce(p["path"], "/")}
		if host := p["host"]; host != "" {
			t.Headers = map[string]string{"Host": host}
		}
		return t
	default:
		return nil
	}
}

type VLESSOutbound struct {
	Type       string           `json:"type"`
	Tag        string           `json:"tag"`
	Server     string           `json:"server"`
	ServerPort int              `json:"server_port"`
	UUID       string           `json:"uuid"`
	Flow       string           `json:"flow,omitempty"`
	TLS        *TLSConfig       `json:"tls,omitempty"`
	Transport  *TransportConfig `json:"transport,omitempty"`
}

func buildVLESS(server subscription.Server, p map[string]string) interface{} {
	return VLESSOutbound{
		Type: "vless", Tag: "proxy",
		Server: server.Address, ServerPort: server.Port,
		UUID:      p["uuid"],
		Flow:      p["flow"],
		TLS:       buildTLS(p),
		Transport: buildTransport(server.Transport, p),
	}
}

type TrojanOutbound struct {
	Type       string           `json:"type"`
	Tag        string           `json:"tag"`
	Server     string           `json:"server"`
	ServerPort int              `json:"server_port"`
	Password   string           `json:"password"`
	TLS        *TLSConfig       `json:"tls,omitempty"`
	Transport  *TransportConfig `json:"transport,omitempty"`
}

func buildTrojan(server subscription.Server, p map[string]string) interface{} {
	tls := buildTLS(p)
	if tls == nil {
		// Trojan is defined on top of TLS; a node with no explicit tls/reality
		// block still means "plain TLS to this host", not "no TLS at all".
		tls = &TLSConfig{Enabled: true, ServerName: coalesce(p["sni"], server.Address)}
	}
	return TrojanOutbound{
		Type: "trojan", Tag: "proxy",
		Server: server.Address, ServerPort: server.Port,
		Password:  p["password"],
		TLS:       tls,
		Transport: buildTransport(server.Transport, p),
	}
}

type ShadowsocksOutbound struct {
	Type       string `json:"type"`
	Tag        string `json:"tag"`
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
	Method     string `json:"method"`
	Password   string `json:"password"`
}

func buildShadowsocks(server subscription.Server, p map[string]string) interface{} {
	return ShadowsocksOutbound{
		Type: "shadowsocks", Tag: "proxy",
		Server: server.Address, ServerPort: server.Port,
		Method:   p["method"],
		Password: p["password"],
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
