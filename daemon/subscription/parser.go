package subscription

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrHWIDNotSupported is returned when the server rejects the request because
// no x-hwid was sent (x-hwid-not-supported header present).
var ErrHWIDNotSupported = fmt.Errorf("server requires HWID but none was sent")

// ErrHWIDMaxDevices is returned when the user's device limit is reached.
var ErrHWIDMaxDevices = fmt.Errorf("device limit reached for this subscription")

// ErrHWIDRejected is returned on HTTP 404 when HWID feature is active.
var ErrHWIDRejected = fmt.Errorf("subscription not found or HWID not registered (HTTP 404)")

// ErrPlaceholderOnly is returned when the panel answered with its
// "unsupported app" / "too many devices" placeholder instead of real nodes.
var ErrPlaceholderOnly = fmt.Errorf("subscription returned no usable servers")

// UserAgent identifies the client to the panel. Remnawave keys its per-app
// templates off this string; the "sing-box" hint keeps panels that gate on
// known clients from serving us a Clash-only payload.
const UserAgent = "RustleBoost/2.0 (Windows; sing-box compatible)"

// Engine selects which core carries a server's traffic.
const (
	EngineXray    = "xray"    // VLESS/VMess/Trojan/Shadowsocks — every Xray transport
	EngineSingBox = "singbox" // Hysteria2/TUIC/Naive — protocols Xray does not implement
)

// HWIDHeaders groups device identification headers for Remnawave.
type HWIDHeaders struct {
	HWID  string // x-hwid  (max 36 chars, required)
	OS    string // x-device-os
	OSVer string // x-ver-os
	Model string // x-device-model
}

type Server struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Country   string            `json:"country"`
	Flag      string            `json:"flag"`
	Protocol  string            `json:"protocol"`  // VLESS | Trojan | Hysteria2 | ...
	Transport string            `json:"transport"` // tcp | grpc | xhttp | ws | httpupgrade | ...
	Security  string            `json:"security"`  // reality | tls | none
	Address   string            `json:"address"`
	Port      int               `json:"port"`
	Latency   int               `json:"latency"`
	RawURI    string            `json:"raw_uri"`
	Engine    string            `json:"engine"`
	Params    map[string]string `json:"params,omitempty"`

	// Outbound is a ready-to-use Xray outbound object (engine "xray").
	// It is serialised so the on-disk cache can reconnect after a restart
	// without waiting for the panel; the UI simply ignores it.
	Outbound json.RawMessage `json:"outbound,omitempty"`
}

// Info mirrors the metadata headers Remnawave attaches to every
// subscription response — this is what the UI shows as traffic and expiry.
type Info struct {
	Upload     int64  `json:"upload"`
	Download   int64  `json:"download"`
	Total      int64  `json:"total"`  // 0 = unlimited
	Expire     int64  `json:"expire"` // unix seconds, 0 = never
	Title      string `json:"title"`
	Announce   string `json:"announce"`
	SupportURL string `json:"support_url"`
	WebPageURL string `json:"web_page_url"`
	UpdatedAt  int64  `json:"updated_at"`
}

// Result is the outcome of a subscription refresh.
type Result struct {
	Servers []Server `json:"servers"`
	Info    Info     `json:"info"`
}

func newHTTPClient() *http.Client {
	return &http.Client{Timeout: 25 * time.Second}
}

func applyHeaders(req *http.Request, hwid HWIDHeaders) {
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")

	if hwid.HWID != "" {
		h := hwid.HWID
		if len(h) > 36 {
			h = h[:36]
		}
		req.Header.Set("x-hwid", h)
	}
	if hwid.OS != "" {
		req.Header.Set("x-device-os", hwid.OS)
	}
	if hwid.OSVer != "" {
		req.Header.Set("x-ver-os", hwid.OSVer)
	}
	if hwid.Model != "" {
		req.Header.Set("x-device-model", hwid.Model)
	}
}

// checkResponse maps Remnawave's signalling headers onto typed errors.
func checkResponse(resp *http.Response) error {
	if resp.Header.Get("x-hwid-max-devices-reached") != "" {
		return ErrHWIDMaxDevices
	}
	if resp.Header.Get("x-hwid-not-supported") != "" {
		return ErrHWIDNotSupported
	}
	if resp.StatusCode == http.StatusNotFound {
		return ErrHWIDRejected
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// Fetch refreshes a subscription.
//
// Remnawave exposes a /v2ray-json endpoint that returns fully-formed Xray
// configs — one per node, with every exotic transport option already filled
// in. We prefer it because converting URIs ourselves risks dropping options
// (xhttp "extra", reality shortIds, alpn) that decide whether a node works at
// all. The plain endpoint is still fetched for its metadata headers, and its
// URI list serves as the fallback for non-Remnawave subscriptions.
func Fetch(ctx context.Context, subURL string, hwid HWIDHeaders) (*Result, error) {
	info, body, err := fetchBase(ctx, subURL, hwid)
	if err != nil {
		return nil, err
	}

	servers, xerr := fetchXrayJSON(ctx, subURL, hwid)
	if xerr != nil || len(servers) == 0 {
		servers, err = Parse(body)
		if err != nil {
			return nil, err
		}
	}

	servers = dropPlaceholders(servers)
	if len(servers) == 0 {
		return nil, ErrPlaceholderOnly
	}

	info.UpdatedAt = time.Now().Unix()
	return &Result{Servers: servers, Info: info}, nil
}

func fetchBase(ctx context.Context, subURL string, hwid HWIDHeaders) (Info, []byte, error) {
	var info Info

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, subURL, nil)
	if err != nil {
		return info, nil, fmt.Errorf("create request: %w", err)
	}
	applyHeaders(req, hwid)

	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return info, nil, fmt.Errorf("fetch subscription: %w", err)
	}
	defer resp.Body.Close()

	info = parseInfoHeaders(resp.Header)

	if err := checkResponse(resp); err != nil {
		return info, nil, err
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return info, nil, fmt.Errorf("read body: %w", err)
	}
	return info, body, nil
}

func parseInfoHeaders(h http.Header) Info {
	var info Info

	// subscription-userinfo: upload=0; download=123; total=0; expire=1807540328
	for _, part := range strings.Split(h.Get("subscription-userinfo"), ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "upload":
			info.Upload = n
		case "download":
			info.Download = n
		case "total":
			info.Total = n
		case "expire":
			info.Expire = n
		}
	}

	info.Title = decodeHeaderText(h.Get("profile-title"))
	info.Announce = decodeHeaderText(h.Get("announce"))
	info.SupportURL = h.Get("support-url")
	info.WebPageURL = h.Get("profile-web-page-url")
	return info
}

// decodeHeaderText unwraps Remnawave's "base64:<payload>" header encoding,
// which it uses so non-ASCII titles survive HTTP header transport.
func decodeHeaderText(raw string) string {
	raw = strings.TrimSpace(raw)
	payload, ok := strings.CutPrefix(raw, "base64:")
	if !ok {
		return raw
	}
	decoded, err := base64Decode(payload)
	if err != nil {
		return ""
	}
	return decoded
}

// dropPlaceholders removes the dummy node Remnawave substitutes when it
// refuses to serve the real list ("🚫 Приложение не поддерживается",
// "Слишком много устройств"). Keeping it would show the user a server that
// silently fails; removing it lets the caller report the real reason.
func dropPlaceholders(servers []Server) []Server {
	kept := make([]Server, 0, len(servers))
	for _, s := range servers {
		if s.Address == "" || s.Address == "0.0.0.0" || s.Port <= 0 {
			continue
		}
		kept = append(kept, s)
	}
	return kept
}

func Parse(data []byte) ([]Server, error) {
	trimmed := strings.TrimSpace(string(data))

	if strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "{") {
		if servers, err := parseJSONPayload([]byte(trimmed)); err == nil && len(servers) > 0 {
			return servers, nil
		}
	}

	if decoded, err := base64Decode(trimmed); err == nil {
		lines := splitLines(decoded)
		if len(lines) > 0 && looksLikeProxyURI(lines[0]) {
			return parseURIList(lines)
		}
	}

	lines := splitLines(trimmed)
	if len(lines) > 0 && looksLikeProxyURI(lines[0]) {
		return parseURIList(lines)
	}

	return nil, fmt.Errorf("unrecognized subscription format")
}

func base64Decode(s string) (string, error) {
	s = strings.NewReplacer("\n", "", "\r", "", " ", "").Replace(s)
	for len(s)%4 != 0 {
		s += "="
	}
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		data, err = base64.URLEncoding.DecodeString(s)
		if err != nil {
			return "", err
		}
	}
	return string(data), nil
}

func splitLines(s string) []string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func looksLikeProxyURI(s string) bool {
	for _, prefix := range []string{
		"vless://", "vmess://", "trojan://", "ss://",
		"hysteria2://", "hy2://", "tuic://", "naive+",
	} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func parseURIList(lines []string) ([]Server, error) {
	var servers []Server
	for _, line := range lines {
		srv, err := parseURI(line)
		if err != nil {
			continue
		}
		servers = append(servers, srv)
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("no valid servers found")
	}
	return servers, nil
}

func parseURI(raw string) (Server, error) {
	switch {
	case strings.HasPrefix(raw, "vless://"):
		return parseVLESS(raw)
	case strings.HasPrefix(raw, "vmess://"):
		return parseVMess(raw)
	case strings.HasPrefix(raw, "trojan://"):
		return parseTrojan(raw)
	case strings.HasPrefix(raw, "ss://"):
		return parseShadowsocks(raw)
	case strings.HasPrefix(raw, "hysteria2://"), strings.HasPrefix(raw, "hy2://"):
		return parseHysteria2(raw)
	case strings.HasPrefix(raw, "tuic://"):
		return parseTUIC(raw)
	case strings.HasPrefix(raw, "naive+"):
		return parseNaive(raw)
	default:
		return Server{}, fmt.Errorf("unsupported protocol")
	}
}

func newID() string { return uuid.NewString() }
