package subscription

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ProxyTag is the outbound tag every generated Xray config uses.
const ProxyTag = "proxy"

// fetchXrayJSON pulls Remnawave's /v2ray-json endpoint, which answers with an
// array of complete Xray configs — one per node. We keep only each config's
// "proxy" outbound; inbounds and routing are ours to decide.
func fetchXrayJSON(ctx context.Context, subURL string, hwid HWIDHeaders) ([]Server, error) {
	endpoint, err := joinPath(subURL, "v2ray-json")
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	applyHeaders(req, hwid)

	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	var entries []struct {
		Remarks   string            `json:"remarks"`
		Outbounds []json.RawMessage `json:"outbounds"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, err
	}

	var servers []Server
	for _, entry := range entries {
		for _, raw := range entry.Outbounds {
			srv, ok := serverFromOutbound(raw, entry.Remarks)
			if !ok {
				continue
			}
			servers = append(servers, srv)
			break
		}
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("no proxy outbounds in v2ray-json")
	}
	return servers, nil
}

// parseJSONPayload reads a subscription body that is already JSON rather than
// a URI list: either an array of Xray configs, or a single config object whose
// outbounds are Xray- or sing-box-shaped.
func parseJSONPayload(data []byte) ([]Server, error) {
	type configEnvelope struct {
		Remarks   string            `json:"remarks"`
		Outbounds []json.RawMessage `json:"outbounds"`
	}

	// An array means one complete config per node.
	var entries []configEnvelope
	if err := json.Unmarshal(data, &entries); err == nil && len(entries) > 0 {
		var servers []Server
		for _, entry := range entries {
			for _, raw := range entry.Outbounds {
				if srv, ok := serverFromOutbound(raw, entry.Remarks); ok {
					servers = append(servers, srv)
					break
				}
			}
		}
		if len(servers) > 0 {
			return servers, nil
		}
	}

	// A single object holds every node among its outbounds.
	var single configEnvelope
	if err := json.Unmarshal(data, &single); err == nil && len(single.Outbounds) > 0 {
		var servers []Server
		for _, raw := range single.Outbounds {
			if srv, ok := serverFromOutbound(raw, single.Remarks); ok {
				servers = append(servers, srv)
				continue
			}
			if srv, ok := serverFromSingBoxOutbound(raw); ok {
				servers = append(servers, srv)
			}
		}
		if len(servers) > 0 {
			return servers, nil
		}
	}

	return nil, fmt.Errorf("no servers found in json subscription")
}

// serverFromSingBoxOutbound handles subscriptions published as a sing-box
// config. The outbound is kept verbatim and handed back to sing-box at connect
// time — rebuilding it from parsed fields is how options get silently dropped.
func serverFromSingBoxOutbound(raw json.RawMessage) (Server, bool) {
	var ob struct {
		Type   string `json:"type"`
		Tag    string `json:"tag"`
		Server string `json:"server"`
		Port   int    `json:"server_port"`
	}
	if err := json.Unmarshal(raw, &ob); err != nil || ob.Server == "" || ob.Port <= 0 {
		return Server{}, false
	}
	switch strings.ToLower(ob.Type) {
	case "direct", "block", "dns", "selector", "urltest", "":
		return Server{}, false
	}

	outbound, err := retagOutbound(raw)
	if err != nil {
		return Server{}, false
	}

	name := ob.Tag
	if name == "" {
		name = ob.Server
	}
	country, flag := guessCountry(name, ob.Server)

	return Server{
		ID:        newID(),
		Name:      name,
		Country:   country,
		Flag:      flag,
		Protocol:  protocolLabel(ob.Type),
		Transport: "tcp",
		Security:  "tls",
		Address:   ob.Server,
		Port:      ob.Port,
		Latency:   -1,
		Engine:    EngineSingBox,
		Outbound:  outbound,
	}, true
}

// joinPath appends a path segment to a subscription URL, preserving its query.
func joinPath(raw, segment string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + segment
	return u.String(), nil
}

// serverFromOutbound turns one Xray outbound into a Server. It reports false
// for the non-proxy outbounds every config carries (freedom, blackhole, dns).
//
// The outbound is kept in two forms. Params is a flat, protocol-agnostic map
// good enough for sing-box to build a native outbound directly — no local
// core in between — for every transport sing-box implements itself. Outbound
// is the original Xray JSON, used only as a fallback for transports sing-box
// does not have (XHTTP, mKCP): those still route through Xray via a local
// SOCKS bridge. Routing a node through Xray when sing-box could carry it
// alone is not free — it is one more process that can be blocked by a
// firewall or fail to resolve DNS on a given machine — so every node that
// can skip it does.
func serverFromOutbound(raw json.RawMessage, remarks string) (Server, bool) {
	var ob struct {
		Tag      string `json:"tag"`
		Protocol string `json:"protocol"`
		Settings struct {
			VNext []struct {
				Address string `json:"address"`
				Port    int    `json:"port"`
				Users   []struct {
					ID         string `json:"id"`
					Flow       string `json:"flow"`
					Encryption string `json:"encryption"`
				} `json:"users"`
			} `json:"vnext"`
			Servers []struct {
				Address  string `json:"address"`
				Port     int    `json:"port"`
				Password string `json:"password"`
				Method   string `json:"method"`
			} `json:"servers"`
		} `json:"settings"`
		StreamSettings streamSettings `json:"streamSettings"`
	}
	if err := json.Unmarshal(raw, &ob); err != nil {
		return Server{}, false
	}

	protocol := strings.ToLower(ob.Protocol)
	switch protocol {
	case "vless", "vmess", "trojan", "shadowsocks":
	default:
		return Server{}, false
	}

	address, port := "", 0
	params := map[string]string{}

	if len(ob.Settings.VNext) > 0 {
		v := ob.Settings.VNext[0]
		address, port = v.Address, v.Port
		if len(v.Users) > 0 {
			params["uuid"] = v.Users[0].ID
			params["flow"] = v.Users[0].Flow
			params["encryption"] = v.Users[0].Encryption
		}
	} else if len(ob.Settings.Servers) > 0 {
		s := ob.Settings.Servers[0]
		address, port = s.Address, s.Port
		params["password"] = s.Password
		params["method"] = s.Method
	}
	if address == "" {
		return Server{}, false
	}

	outbound, err := retagOutbound(raw)
	if err != nil {
		return Server{}, false
	}

	network := firstNonEmpty(ob.StreamSettings.Network, "tcp")
	security := firstNonEmpty(ob.StreamSettings.Security, "none")
	extractStreamParams(params, network, security, ob.StreamSettings, address)

	engine := EngineXray
	// Only VLESS/Trojan/Shadowsocks have a native builder below; VMess keeps
	// the Xray path unconditionally rather than risk an under-tested cipher
	// negotiation.
	if protocol != "vmess" && singBoxNativeTransport(network) {
		engine = EngineSingBox
	}

	name := remarks
	if name == "" {
		name = address
	}
	country, flag := guessCountry(name, address)

	return Server{
		ID:        newID(),
		Name:      name,
		Country:   country,
		Flag:      flag,
		Protocol:  protocolLabel(ob.Protocol),
		Transport: network,
		Security:  security,
		Address:   address,
		Port:      port,
		Latency:   -1,
		Engine:    engine,
		Params:    params,
		Outbound:  outbound,
	}, true
}

// singBoxNativeTransport reports whether sing-box's own VLESS/Trojan/
// Shadowsocks outbound implements this transport. Verified directly against
// the bundled binary with `sing-box check`, not assumed from documentation:
// XHTTP and mKCP are rejected with "unknown transport type"; everything
// listed here is accepted.
func singBoxNativeTransport(network string) bool {
	switch network {
	case "", "tcp", "ws", "grpc", "httpupgrade", "http", "h2", "quic":
		return true
	default:
		return false
	}
}

// extractStreamParams flattens the decoded Xray streamSettings into the same
// param keys buildStream (URI path) produces, so the sing-box native builder
// in the config package can consume either source uniformly.
func extractStreamParams(params map[string]string, network, security string, s streamSettings, fallbackSNI string) {
	sni := fallbackSNI
	fp := ""

	switch security {
	case "reality":
		if s.RealitySettings != nil {
			sni = firstNonEmpty(s.RealitySettings.ServerName, sni)
			fp = s.RealitySettings.Fingerprint
			params["pbk"] = s.RealitySettings.PublicKey
			params["sid"] = s.RealitySettings.ShortID
			params["spx"] = s.RealitySettings.SpiderX
		}
	case "tls":
		if s.TLSSettings != nil {
			sni = firstNonEmpty(s.TLSSettings.ServerName, sni)
			fp = s.TLSSettings.Fingerprint
			params["alpn"] = strings.Join(s.TLSSettings.ALPN, ",")
			if s.TLSSettings.AllowInsecure {
				params["insecure"] = "1"
			}
		}
	}
	params["sni"] = sni
	params["fp"] = fp

	switch network {
	case "ws":
		if s.WSSettings != nil {
			params["path"] = s.WSSettings.Path
			params["host"] = firstNonEmpty(s.WSSettings.Headers["Host"], s.WSSettings.Host)
		}
	case "grpc":
		if s.GRPCSettings != nil {
			params["serviceName"] = s.GRPCSettings.ServiceName
		}
	case "httpupgrade":
		if s.HTTPUpgradeSettings != nil {
			params["path"] = s.HTTPUpgradeSettings.Path
			params["host"] = s.HTTPUpgradeSettings.Host
		}
	}
}

// retagOutbound rewrites an outbound's tag to ProxyTag so the routing rules in
// our generated config always find it under a known name.
func retagOutbound(raw json.RawMessage) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	tag, _ := json.Marshal(ProxyTag)
	m["tag"] = tag
	return json.Marshal(m)
}

func protocolLabel(p string) string {
	switch strings.ToLower(p) {
	case "vless":
		return "VLESS"
	case "vmess":
		return "VMess"
	case "trojan":
		return "Trojan"
	case "shadowsocks", "ss":
		return "Shadowsocks"
	case "hysteria2":
		return "Hysteria2"
	case "tuic":
		return "TUIC"
	case "naive":
		return "NaiveProxy"
	}
	return strings.ToUpper(p)
}

// ── URI → Xray outbound ───────────────────────────────────────────────────
//
// Used when a subscription has no /v2ray-json endpoint. Every transport Xray
// supports is covered here, because a dropped option (xhttp "extra", reality
// shortId, alpn) is the difference between a node that works and one that
// times out with no visible error.

type streamSettings struct {
	Network  string `json:"network"`
	Security string `json:"security,omitempty"`

	TLSSettings     *tlsSettings     `json:"tlsSettings,omitempty"`
	RealitySettings *realitySettings `json:"realitySettings,omitempty"`

	TCPSettings         *tcpSettings         `json:"tcpSettings,omitempty"`
	WSSettings          *wsSettings          `json:"wsSettings,omitempty"`
	GRPCSettings        *grpcSettings        `json:"grpcSettings,omitempty"`
	XHTTPSettings       *xhttpSettings       `json:"xhttpSettings,omitempty"`
	HTTPUpgradeSettings *httpUpgradeSettings `json:"httpupgradeSettings,omitempty"`
	KCPSettings         *kcpSettings         `json:"kcpSettings,omitempty"`
}

type tlsSettings struct {
	ServerName    string   `json:"serverName,omitempty"`
	Fingerprint   string   `json:"fingerprint,omitempty"`
	ALPN          []string `json:"alpn,omitempty"`
	AllowInsecure bool     `json:"allowInsecure,omitempty"`
}

type realitySettings struct {
	ServerName  string `json:"serverName,omitempty"`
	PublicKey   string `json:"publicKey"`
	ShortID     string `json:"shortId,omitempty"`
	SpiderX     string `json:"spiderX,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

type tcpSettings struct {
	Header json.RawMessage `json:"header,omitempty"`
}

type wsSettings struct {
	Path    string            `json:"path,omitempty"`
	Host    string            `json:"host,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type grpcSettings struct {
	ServiceName string `json:"serviceName"`
	MultiMode   bool   `json:"multiMode,omitempty"`
	Authority   string `json:"authority,omitempty"`
}

type xhttpSettings struct {
	Mode  string          `json:"mode,omitempty"`
	Host  string          `json:"host,omitempty"`
	Path  string          `json:"path,omitempty"`
	Extra json.RawMessage `json:"extra,omitempty"`
}

type httpUpgradeSettings struct {
	Path string `json:"path,omitempty"`
	Host string `json:"host,omitempty"`
}

type kcpSettings struct {
	Seed   string          `json:"seed,omitempty"`
	Header json.RawMessage `json:"header,omitempty"`
}

// buildStream maps URI query parameters onto Xray stream settings.
func buildStream(q url.Values, fallbackSNI string) *streamSettings {
	network := firstNonEmpty(q.Get("type"), "tcp")
	// Panels disagree on the name for Xray's "h2" network.
	if network == "http" {
		network = "h2"
	}

	security := firstNonEmpty(q.Get("security"), "none")
	st := &streamSettings{Network: network, Security: security}

	sni := firstNonEmpty(q.Get("sni"), q.Get("peer"), fallbackSNI)
	fp := firstNonEmpty(q.Get("fp"), "chrome")

	switch security {
	case "reality":
		st.RealitySettings = &realitySettings{
			ServerName:  sni,
			PublicKey:   q.Get("pbk"),
			ShortID:     q.Get("sid"),
			SpiderX:     q.Get("spx"),
			Fingerprint: fp,
		}
	case "tls", "xtls":
		st.Security = "tls"
		st.TLSSettings = &tlsSettings{
			ServerName:    sni,
			Fingerprint:   fp,
			ALPN:          splitCSV(q.Get("alpn")),
			AllowInsecure: isTruthy(q.Get("allowInsecure")) || isTruthy(q.Get("insecure")),
		}
	}

	host := firstNonEmpty(q.Get("host"), sni)
	path := firstNonEmpty(q.Get("path"), "/")

	switch network {
	case "grpc":
		st.GRPCSettings = &grpcSettings{
			// Panels put the gRPC service name in either field; Xray refuses
			// the connection when it ends up empty.
			ServiceName: firstNonEmpty(q.Get("serviceName"), q.Get("path")),
			MultiMode:   q.Get("mode") == "multi",
			Authority:   q.Get("authority"),
		}
	case "ws":
		ws := &wsSettings{Path: path, Host: host}
		if host != "" {
			ws.Headers = map[string]string{"Host": host}
		}
		st.WSSettings = ws
	case "xhttp", "splithttp":
		st.Network = "xhttp"
		x := &xhttpSettings{
			Mode: firstNonEmpty(q.Get("mode"), "auto"),
			Host: host,
			Path: path,
		}
		// "extra" carries an inline JSON object of padding/pacing knobs. It is
		// passed through untouched — the server rejects streams whose padding
		// parameters do not match exactly.
		if extra := strings.TrimSpace(q.Get("extra")); extra != "" && json.Valid([]byte(extra)) {
			x.Extra = json.RawMessage(extra)
		}
		st.XHTTPSettings = x
	case "httpupgrade":
		st.HTTPUpgradeSettings = &httpUpgradeSettings{Path: path, Host: host}
	case "kcp", "mkcp":
		st.Network = "kcp"
		k := &kcpSettings{Seed: q.Get("seed")}
		if ht := q.Get("headerType"); ht != "" {
			k.Header = json.RawMessage(fmt.Sprintf(`{"type":%q}`, ht))
		}
		st.KCPSettings = k
	case "tcp":
		if q.Get("headerType") == "http" {
			hosts, _ := json.Marshal(splitCSV(host))
			st.TCPSettings = &tcpSettings{
				Header: json.RawMessage(fmt.Sprintf(
					`{"type":"http","request":{"path":[%q],"headers":{"Host":%s}}}`, path, hosts)),
			}
		}
	}

	return st
}

func parseVLESS(raw string) (Server, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Server{}, err
	}
	port, _ := strconv.Atoi(u.Port())
	q := u.Query()
	q.Set("uuid", u.User.Username())

	outbound := map[string]any{
		"tag":      ProxyTag,
		"protocol": "vless",
		"settings": map[string]any{
			"vnext": []any{map[string]any{
				"address": u.Hostname(),
				"port":    port,
				"users": []any{map[string]any{
					"id":         u.User.Username(),
					"encryption": firstNonEmpty(q.Get("encryption"), "none"),
					"flow":       q.Get("flow"),
				}},
			}},
		},
		"streamSettings": buildStream(q, u.Hostname()),
	}
	return finishServer(raw, u, port, q, "vless", outbound)
}

func parseVMess(raw string) (Server, error) {
	// vmess:// is normally a base64-encoded JSON blob rather than a URL.
	payload := strings.TrimPrefix(raw, "vmess://")
	decoded, err := base64Decode(payload)
	if err != nil {
		return Server{}, err
	}
	var v struct {
		Add  string `json:"add"`
		Port any    `json:"port"`
		ID   string `json:"id"`
		Aid  any    `json:"aid"`
		Net  string `json:"net"`
		Type string `json:"type"`
		Host string `json:"host"`
		Path string `json:"path"`
		TLS  string `json:"tls"`
		SNI  string `json:"sni"`
		ALPN string `json:"alpn"`
		FP   string `json:"fp"`
		Scy  string `json:"scy"`
		PS   string `json:"ps"`
	}
	if err := json.Unmarshal([]byte(decoded), &v); err != nil {
		return Server{}, err
	}

	port := toInt(v.Port)
	q := url.Values{}
	q.Set("type", firstNonEmpty(v.Net, "tcp"))
	q.Set("security", firstNonEmpty(v.TLS, "none"))
	q.Set("sni", firstNonEmpty(v.SNI, v.Host, v.Add))
	q.Set("host", v.Host)
	q.Set("path", v.Path)
	q.Set("alpn", v.ALPN)
	q.Set("fp", v.FP)
	q.Set("headerType", v.Type)
	q.Set("serviceName", v.Path)

	outbound := map[string]any{
		"tag":      ProxyTag,
		"protocol": "vmess",
		"settings": map[string]any{
			"vnext": []any{map[string]any{
				"address": v.Add,
				"port":    port,
				"users": []any{map[string]any{
					"id":       v.ID,
					"alterId":  toInt(v.Aid),
					"security": firstNonEmpty(v.Scy, "auto"),
				}},
			}},
		},
		"streamSettings": buildStream(q, v.Add),
	}

	name := firstNonEmpty(v.PS, v.Add)
	return assembleServer(raw, name, v.Add, port, "vmess", q, outbound)
}

func parseTrojan(raw string) (Server, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Server{}, err
	}
	port, _ := strconv.Atoi(u.Port())
	q := u.Query()
	if q.Get("security") == "" {
		q.Set("security", "tls")
	}
	q.Set("password", u.User.Username())

	outbound := map[string]any{
		"tag":      ProxyTag,
		"protocol": "trojan",
		"settings": map[string]any{
			"servers": []any{map[string]any{
				"address":  u.Hostname(),
				"port":     port,
				"password": u.User.Username(),
			}},
		},
		"streamSettings": buildStream(q, u.Hostname()),
	}
	return finishServer(raw, u, port, q, "trojan", outbound)
}

func parseShadowsocks(raw string) (Server, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Server{}, err
	}

	method, password := "", ""
	if pw, ok := u.User.Password(); ok {
		method, password = u.User.Username(), pw
	} else if decoded, err := base64Decode(u.User.Username()); err == nil {
		method, password, _ = strings.Cut(decoded, ":")
	}
	if method == "" {
		return Server{}, fmt.Errorf("shadowsocks: no cipher")
	}

	port, _ := strconv.Atoi(u.Port())
	q := u.Query()
	q.Set("method", method)
	q.Set("password", password)

	outbound := map[string]any{
		"tag":      ProxyTag,
		"protocol": "shadowsocks",
		"settings": map[string]any{
			"servers": []any{map[string]any{
				"address":  u.Hostname(),
				"port":     port,
				"method":   method,
				"password": password,
			}},
		},
		"streamSettings": buildStream(q, u.Hostname()),
	}
	return finishServer(raw, u, port, q, "shadowsocks", outbound)
}

// finishServer completes a URL-shaped URI into a Server.
func finishServer(raw string, u *url.URL, port int, q url.Values, protocol string, outbound map[string]any) (Server, error) {
	name, _ := url.QueryUnescape(u.Fragment)
	if name == "" {
		name = u.Hostname()
	}
	return assembleServer(raw, name, u.Hostname(), port, protocol, q, outbound)
}

// assembleServer is the shared exit point for every URI-shaped proxy scheme.
// Engine and Params follow the same rule as the /v2ray-json path in
// serverFromOutbound: sing-box carries the node natively whenever it
// implements the transport, and Xray is used only where sing-box has no
// equivalent (XHTTP, mKCP) or for VMess, whose cipher negotiation is not
// exercised by the native path.
func assembleServer(raw, name, address string, port int, protocol string, q url.Values, outbound map[string]any) (Server, error) {
	if address == "" || port <= 0 {
		return Server{}, fmt.Errorf("%s: missing address or port", protocol)
	}

	encoded, err := json.Marshal(outbound)
	if err != nil {
		return Server{}, err
	}

	country, flag := guessCountry(name, address)
	transport := firstNonEmpty(q.Get("type"), "tcp")
	if transport == "splithttp" {
		transport = "xhttp"
	}

	engine := EngineXray
	if protocol != "vmess" && singBoxNativeTransport(transport) {
		engine = EngineSingBox
	}

	params := map[string]string{
		"uuid": q.Get("uuid"), "password": q.Get("password"), "method": q.Get("method"),
		"flow": q.Get("flow"), "security": q.Get("security"),
		"sni": q.Get("sni"), "fp": q.Get("fp"),
		"pbk": q.Get("pbk"), "sid": q.Get("sid"), "alpn": q.Get("alpn"),
		"path": q.Get("path"), "host": q.Get("host"), "serviceName": q.Get("serviceName"),
		"insecure": q.Get("insecure"),
	}

	return Server{
		ID:        newID(),
		Name:      name,
		Country:   country,
		Flag:      flag,
		Protocol:  protocolLabel(protocol),
		Transport: transport,
		Security:  firstNonEmpty(q.Get("security"), "none"),
		Address:   address,
		Port:      port,
		Latency:   -1,
		RawURI:    raw,
		Engine:    engine,
		Params:    params,
		Outbound:  encoded,
	}, nil
}

// ── sing-box-only protocols ───────────────────────────────────────────────
//
// Xray implements none of these, so they keep the sing-box path and carry
// their options as flat params for the sing-box config generator.

func parseHysteria2(raw string) (Server, error) {
	normalized := strings.Replace(raw, "hy2://", "hysteria2://", 1)
	u, err := url.Parse(normalized)
	if err != nil {
		return Server{}, err
	}
	port, _ := strconv.Atoi(u.Port())
	q := u.Query()

	password := u.User.Username()
	if p, ok := u.User.Password(); ok && p != "" {
		password = p
	}

	return singBoxServer(raw, u, port, "hysteria2", map[string]string{
		"password":      password,
		"sni":           firstNonEmpty(q.Get("sni"), u.Hostname()),
		"insecure":      q.Get("insecure"),
		"obfs":          q.Get("obfs"),
		"obfs-password": q.Get("obfs-password"),
	})
}

func parseTUIC(raw string) (Server, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Server{}, err
	}
	port, _ := strconv.Atoi(u.Port())
	q := u.Query()
	password, _ := u.User.Password()

	return singBoxServer(raw, u, port, "tuic", map[string]string{
		"uuid":              u.User.Username(),
		"password":          password,
		"sni":               firstNonEmpty(q.Get("sni"), u.Hostname()),
		"alpn":              q.Get("alpn"),
		"congestion_control": q.Get("congestion_control"),
		"udp_relay_mode":    q.Get("udp_relay_mode"),
		"insecure":          q.Get("allow_insecure"),
	})
}

func parseNaive(raw string) (Server, error) {
	u, err := url.Parse(strings.TrimPrefix(raw, "naive+"))
	if err != nil {
		return Server{}, err
	}
	port, _ := strconv.Atoi(u.Port())
	password, _ := u.User.Password()

	return singBoxServer(raw, u, port, "naive", map[string]string{
		"username": u.User.Username(),
		"password": password,
		"scheme":   u.Scheme,
	})
}

func singBoxServer(raw string, u *url.URL, port int, protocol string, params map[string]string) (Server, error) {
	if u.Hostname() == "" || port <= 0 {
		return Server{}, fmt.Errorf("%s: missing address or port", protocol)
	}
	name, _ := url.QueryUnescape(u.Fragment)
	if name == "" {
		name = u.Hostname()
	}
	country, flag := guessCountry(name, u.Hostname())

	return Server{
		ID:        newID(),
		Name:      name,
		Country:   country,
		Flag:      flag,
		Protocol:  protocolLabel(protocol),
		Transport: "udp",
		Security:  "tls",
		Address:   u.Hostname(),
		Port:      port,
		Latency:   -1,
		RawURI:    raw,
		Engine:    EngineSingBox,
		Params:    params,
	}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────

func firstNonEmpty(vals ...string) string {
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

func isTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}
