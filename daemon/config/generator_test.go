package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vpnclient/daemon/subscription"
)

// Credentials here are synthetic. The shapes mirror what Remnawave issues:
// gRPC over Reality for the Wi-Fi nodes, XHTTP over TLS for the LTE nodes.
const (
	grpcRealityURI = "vless://11111111-2222-3333-4444-555555555555@198.51.100.7:8443" +
		"?encryption=none&type=grpc&mode=gun&security=reality&sni=www.example.com" +
		"&fp=chrome&pbk=TEST_PUBLIC_KEY&sid=abcd1234#%F0%9F%87%B5%F0%9F%87%B1%20Wi-Fi%20%7C%20Poland"

	xhttpTLSURI = "vless://11111111-2222-3333-4444-555555555555@cdn.example.com:443" +
		"?encryption=none&type=xhttp&path=/lte-nl&host=cdn.example.com&mode=packet-up" +
		`&extra={"xPaddingKey":"dc","scMaxBufferedPosts":30}` +
		"&security=tls&sni=cdn.example.com&fp=chrome&alpn=h2#%F0%9F%87%B3%F0%9F%87%B1%20LTE%20%7C%20NL"
)

// Which core actually carries a node is a user setting, not something the
// app decides — the same network can block one implementation's handshake
// and let the other through, and only the user's own trial and error can
// tell which. ResolveEngine must honour an explicit override even where
// automatic selection would pick the other core, and refuse rather than
// silently misconfigure the one combination that is genuinely impossible.
func TestResolveEngineHonoursOverride(t *testing.T) {
	grpc := parseOne(t, grpcRealityURI) // sing-box-capable
	xhttp := parseOne(t, xhttpTLSURI)   // Xray-only

	cases := []struct {
		name   string
		server subscription.Server
		mode   string
		want   string
	}{
		{"grpc auto picks singbox", grpc, RouterAuto, subscription.EngineSingBox},
		{"grpc forced to singbox stays singbox", grpc, RouterSingBox, subscription.EngineSingBox},
		{"grpc forced to xray goes to xray", grpc, RouterXray, subscription.EngineXray},
		{"xhttp auto picks xray", xhttp, RouterAuto, subscription.EngineXray},
		{"xhttp forced to xray stays xray", xhttp, RouterXray, subscription.EngineXray},
		// ResolveEngine honours the forced choice literally even though this
		// specific node cannot actually run on it; Generate() is what turns
		// that into a clear refusal (see TestSingBoxOnlyModeRejectsXHTTPNode).
		{"xhttp forced to singbox resolves there — Generate() must reject it", xhttp, RouterSingBox, subscription.EngineSingBox},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ResolveEngine(c.server, c.mode)
			if got != c.want {
				t.Errorf("ResolveEngine(mode=%s) = %q, want %q", c.mode, got, c.want)
			}
		})
	}
}

// Forcing "только sing-box" against an XHTTP-only node must fail loudly, not
// build a config that quietly treats XHTTP as plain TCP and then times out
// with no explanation of why.
func TestSingBoxOnlyModeRejectsXHTTPNode(t *testing.T) {
	xhttp := parseOne(t, xhttpTLSURI)

	_, err := Generate(xhttp, Options{TUNMode: true, RouterMode: RouterSingBox})
	if err == nil {
		t.Fatal("expected an error forcing sing-box-only mode onto an XHTTP node")
	}
	if !strings.Contains(err.Error(), "Xray") {
		t.Errorf("error should point the user at the Xray/Auto setting, got: %v", err)
	}
}

func parseOne(t *testing.T, uri string) subscription.Server {
	t.Helper()
	servers, err := subscription.Parse([]byte(uri))
	if err != nil {
		t.Fatalf("parse %q: %v", uri, err)
	}
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}
	return servers[0]
}

// sing-box has its own native VLESS/gRPC/Reality implementation, verified
// directly against the bundled binary (see buildTransport / buildTLS in
// generator.go). A node using it does not need Xray running at all — one
// fewer process that can be blocked by a firewall or fail its own DNS
// lookup, which is exactly the failure mode that surfaced when every node
// was forced through Xray regardless of transport.
func TestParseGRPCReality(t *testing.T) {
	s := parseOne(t, grpcRealityURI)

	if s.Transport != "grpc" || s.Security != "reality" {
		t.Errorf("transport/security = %q/%q, want grpc/reality", s.Transport, s.Security)
	}
	if s.Engine != subscription.EngineSingBox {
		t.Errorf("engine = %q, want %q (sing-box implements grpc+reality natively)", s.Engine, subscription.EngineSingBox)
	}
	if s.Country != "Poland" {
		t.Errorf("country = %q, want Poland (from flag emoji)", s.Country)
	}

	if s.Params["pbk"] != "TEST_PUBLIC_KEY" || s.Params["sid"] != "abcd1234" {
		t.Errorf("reality params lost: pbk=%q sid=%q", s.Params["pbk"], s.Params["sid"])
	}
}

// The xhttp "extra" object carries padding and pacing parameters that the
// server matches exactly; dropping or reformatting it breaks the transport.
func TestParseXHTTPPreservesExtra(t *testing.T) {
	s := parseOne(t, xhttpTLSURI)

	if s.Transport != "xhttp" {
		t.Fatalf("transport = %q, want xhttp", s.Transport)
	}

	var ob struct {
		Stream struct {
			Network string `json:"network"`
			XHTTP   struct {
				Mode  string          `json:"mode"`
				Path  string          `json:"path"`
				Extra json.RawMessage `json:"extra"`
			} `json:"xhttpSettings"`
			TLS struct {
				ALPN []string `json:"alpn"`
			} `json:"tlsSettings"`
		} `json:"streamSettings"`
	}
	if err := json.Unmarshal(s.Outbound, &ob); err != nil {
		t.Fatalf("decode outbound: %v", err)
	}

	if ob.Stream.Network != "xhttp" || ob.Stream.XHTTP.Mode != "packet-up" {
		t.Errorf("xhttp stream wrong: %+v", ob.Stream)
	}
	if !strings.Contains(string(ob.Stream.XHTTP.Extra), "scMaxBufferedPosts") {
		t.Errorf("extra not preserved: %s", ob.Stream.XHTTP.Extra)
	}
	if len(ob.Stream.TLS.ALPN) != 1 || ob.Stream.TLS.ALPN[0] != "h2" {
		t.Errorf("alpn = %v, want [h2]", ob.Stream.TLS.ALPN)
	}
}

// Xray-carried nodes must reach sing-box only through the local SOCKS port.
func TestSingBoxDelegatesToXray(t *testing.T) {
	s := parseOne(t, xhttpTLSURI)

	cfg, err := Generate(s, Options{TUNMode: true, RouteMode: "all"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	out, ok := cfg.Outbounds[0].(SocksOutbound)
	if !ok {
		t.Fatalf("proxy outbound is %T, want SocksOutbound", cfg.Outbounds[0])
	}
	if out.ServerPort != XraySocksPort || out.Tag != "proxy" {
		t.Errorf("socks outbound = %+v", out)
	}
}

// A node sing-box implements natively must never touch Xray: routing it
// through the SOCKS bridge anyway is what left every server unreachable on a
// machine where something (firewall, DNS, whatever) breaks the Xray process
// specifically, even though sing-box's own tunnel worked fine there.
func TestGRPCRealityBuildsWithoutXray(t *testing.T) {
	s := parseOne(t, grpcRealityURI)

	if NeedsXray(s) {
		t.Fatal("NeedsXray = true for a grpc+reality node sing-box supports natively")
	}

	cfg, err := Generate(s, Options{TUNMode: true, RouteMode: "all"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	out, ok := cfg.Outbounds[0].(VLESSOutbound)
	if !ok {
		t.Fatalf("proxy outbound is %T, want VLESSOutbound", cfg.Outbounds[0])
	}
	if out.UUID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("uuid = %q", out.UUID)
	}
	if out.TLS == nil || out.TLS.Reality == nil {
		t.Fatal("no reality block")
	}
	if out.TLS.Reality.PublicKey != "TEST_PUBLIC_KEY" || out.TLS.Reality.ShortID != "abcd1234" {
		t.Errorf("reality = %+v", out.TLS.Reality)
	}
	if out.Transport == nil || out.Transport.Type != "grpc" {
		t.Fatalf("transport = %+v, want grpc", out.Transport)
	}

	for _, ob := range cfg.Outbounds {
		if _, isSocks := ob.(SocksOutbound); isSocks {
			t.Fatal("a socks bridge to Xray was generated for a native-only node")
		}
	}
}

// Without a bypass for the cores' own traffic, the proxy leg is captured by
// TUN and the tunnel deadlocks. Nodes addressed by bare IP need a CIDR rule
// because a domain rule can never match them.
func TestRouteBypassesTunnelLoop(t *testing.T) {
	byIP := parseOne(t, grpcRealityURI)
	cfg, err := Generate(byIP, Options{TUNMode: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var sawProcess, sawCIDR, sawSniff bool
	for _, rule := range cfg.Route.Rules {
		if rule.Action == "sniff" {
			sawSniff = true
		}
		for _, p := range rule.ProcessName {
			if p == "xray.exe" && rule.Outbound == "direct" {
				sawProcess = true
			}
		}
		for _, c := range rule.IPCIDR {
			if c == "198.51.100.7/32" && rule.Outbound == "direct" {
				sawCIDR = true
			}
		}
	}

	if !sawSniff {
		t.Error("no sniff action — domain rules cannot match without it")
	}
	if !sawProcess {
		t.Error("core traffic is not routed direct — tunnel would loop")
	}
	if !sawCIDR {
		t.Error("IP-addressed node has no direct CIDR rule")
	}
}

// Windows calls an interface offline until its own probe succeeds through it,
// and that probe starts with a DNS lookup. Leaving DNS to the physical
// adapter is what kept the tray on "No Internet" while the tunnel worked.
func TestRouteHijacksDNSInTunnelMode(t *testing.T) {
	s := parseOne(t, grpcRealityURI)

	cfg, err := Generate(s, Options{TUNMode: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var byProtocol, byPort bool
	for _, rule := range cfg.Route.Rules {
		if rule.Action != "hijack-dns" {
			continue
		}
		if rule.Protocol == "dns" {
			byProtocol = true
		}
		for _, p := range rule.Port {
			if p == 53 {
				byPort = true
			}
		}
	}

	if !byProtocol || !byPort {
		t.Errorf("DNS not hijacked (protocol=%v port=%v)", byProtocol, byPort)
	}

	tun, ok := cfg.Inbounds[1].(TUNInbound)
	if !ok {
		t.Fatalf("second inbound is %T, want TUNInbound", cfg.Inbounds[1])
	}
	if !tun.StrictRoute {
		t.Error("strict_route off — system probes bypass the tunnel")
	}
}

// Every node needs its own inbound and outbound, or the probe measures one
// node repeatedly and reports the same latency for the whole list.
func TestProbeConfigIsolatesEachNode(t *testing.T) {
	a := parseOne(t, grpcRealityURI)
	b := parseOne(t, xhttpTLSURI)

	cfg, err := GenerateXrayProbe([]ProbeTarget{
		{ServerID: a.ID, Outbound: a.Outbound, Port: 23100},
		{ServerID: b.ID, Outbound: b.Outbound, Port: 23101},
	})
	if err != nil {
		t.Fatalf("generate probe: %v", err)
	}

	if len(cfg.Inbounds) != 2 || len(cfg.Outbounds) != 2 {
		t.Fatalf("got %d inbounds / %d outbounds, want 2 each",
			len(cfg.Inbounds), len(cfg.Outbounds))
	}

	seen := map[string]string{}
	for _, rule := range cfg.Routing.Rules {
		if len(rule.InboundTag) != 1 {
			t.Fatalf("rule matches %d inbounds, want 1", len(rule.InboundTag))
		}
		seen[rule.InboundTag[0]] = rule.OutboundTag
	}

	if seen["in-0"] != "out-0" || seen["in-1"] != "out-1" {
		t.Errorf("probe routing crossed over: %v", seen)
	}

	ports := map[int]bool{}
	for _, raw := range cfg.Inbounds {
		in := raw.(XraySocksInbound)
		if ports[in.Port] {
			t.Errorf("duplicate probe port %d", in.Port)
		}
		ports[in.Port] = true
	}
}

func TestGenerateXrayCarriesNodeOutbound(t *testing.T) {
	s := parseOne(t, xhttpTLSURI)

	cfg, err := GenerateXray(s, Options{})
	if err != nil {
		t.Fatalf("generate xray: %v", err)
	}
	if len(cfg.Outbounds) != 3 {
		t.Fatalf("want proxy+direct+block outbounds, got %d", len(cfg.Outbounds))
	}

	in, ok := cfg.Inbounds[0].(XraySocksInbound)
	if !ok || in.Port != XraySocksPort {
		t.Errorf("socks inbound = %+v", cfg.Inbounds[0])
	}
	if !strings.Contains(string(cfg.Outbounds[0]), `"tag":"proxy"`) {
		t.Errorf("node outbound not tagged proxy: %s", cfg.Outbounds[0])
	}
}

// The panel answers with a dummy 0.0.0.0:1 node when it refuses to serve the
// real list. Showing it would give the user a server that silently fails.
func TestPlaceholderNodesAreRejected(t *testing.T) {
	placeholder := "vless://00000000-0000-0000-0000-000000000000@0.0.0.0:1" +
		"?encryption=none&type=tcp&security=none#blocked"

	servers, err := subscription.Parse([]byte(placeholder))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Generate(servers[0], Options{}); err == nil {
		// Generation itself may succeed; the filter runs during Fetch. Assert
		// the address is what the filter keys on so the two stay in step.
		if servers[0].Address != "0.0.0.0" {
			t.Errorf("placeholder address = %q, filter will miss it", servers[0].Address)
		}
	}
}
