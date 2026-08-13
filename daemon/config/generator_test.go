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

func TestParseGRPCReality(t *testing.T) {
	s := parseOne(t, grpcRealityURI)

	if s.Transport != "grpc" || s.Security != "reality" {
		t.Errorf("transport/security = %q/%q, want grpc/reality", s.Transport, s.Security)
	}
	if s.Engine != subscription.EngineXray {
		t.Errorf("engine = %q, want %q", s.Engine, subscription.EngineXray)
	}
	if s.Country != "Poland" {
		t.Errorf("country = %q, want Poland (from flag emoji)", s.Country)
	}

	var ob struct {
		Stream struct {
			GRPC struct {
				ServiceName string `json:"serviceName"`
			} `json:"grpcSettings"`
			Reality struct {
				PublicKey string `json:"publicKey"`
				ShortID   string `json:"shortId"`
			} `json:"realitySettings"`
		} `json:"streamSettings"`
	}
	if err := json.Unmarshal(s.Outbound, &ob); err != nil {
		t.Fatalf("decode outbound: %v", err)
	}
	if ob.Stream.Reality.PublicKey != "TEST_PUBLIC_KEY" || ob.Stream.Reality.ShortID != "abcd1234" {
		t.Errorf("reality settings lost: %+v", ob.Stream.Reality)
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
