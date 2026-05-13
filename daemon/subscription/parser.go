package subscription

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// HWIDHeaders groups device identification headers for Remnawave.
type HWIDHeaders struct {
	HWID  string // x-hwid  (max 36 chars, required)
	OS    string // x-device-os
	OSVer string // x-ver-os
	Model string // x-device-model
}

type Server struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Country  string            `json:"country"`
	Flag     string            `json:"flag"`
	Protocol string            `json:"protocol"`
	Address  string            `json:"address"`
	Port     int               `json:"port"`
	Latency  int               `json:"latency"`
	RawURI   string            `json:"raw_uri"`
	Params   map[string]string `json:"params"`
}

func Fetch(ctx context.Context, subURL string, hwid HWIDHeaders) ([]Server, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, subURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// Remnawave-required headers
	req.Header.Set("User-Agent", "RustleBoost/1.0 (Windows; sing-box compatible)")
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

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch subscription: %w", err)
	}
	defer resp.Body.Close()

	// Check Remnawave-specific response headers
	if resp.Header.Get("x-hwid-not-supported") != "" {
		return nil, ErrHWIDNotSupported
	}
	if resp.Header.Get("x-hwid-max-devices-reached") != "" {
		return nil, ErrHWIDMaxDevices
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrHWIDRejected
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return Parse(body)
}

func Parse(data []byte) ([]Server, error) {
	trimmed := strings.TrimSpace(string(data))

	if strings.HasPrefix(trimmed, "{") {
		servers, err := parseSingBoxJSON([]byte(trimmed))
		if err == nil && len(servers) > 0 {
			return servers, nil
		}
	}

	decoded, err := base64Decode(trimmed)
	if err == nil {
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
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
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
	for _, prefix := range []string{"vless://", "vmess://", "hysteria2://", "hy2://", "naive+", "trojan://"} {
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
	case strings.HasPrefix(raw, "hysteria2://"), strings.HasPrefix(raw, "hy2://"):
		return parseHysteria2(raw)
	case strings.HasPrefix(raw, "naive+"):
		return parseNaive(raw)
	case strings.HasPrefix(raw, "trojan://"):
		return parseTrojan(raw)
	default:
		return Server{}, fmt.Errorf("unsupported protocol")
	}
}

func parseVLESS(raw string) (Server, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Server{}, err
	}
	port, _ := strconv.Atoi(u.Port())
	name, _ := url.QueryUnescape(u.Fragment)
	if name == "" {
		name = u.Hostname()
	}
	q := u.Query()
	params := map[string]string{
		"uuid": u.User.Username(), "security": q.Get("security"),
		"type": q.Get("type"), "flow": q.Get("flow"),
		"fp": q.Get("fp"), "pbk": q.Get("pbk"),
		"sid": q.Get("sid"), "sni": q.Get("sni"),
		"path": q.Get("path"), "host": q.Get("host"),
	}
	country, flag := guessCountry(name, u.Hostname())
	return Server{ID: uuid.NewString(), Name: name, Country: country, Flag: flag,
		Protocol: "VLESS", Address: u.Hostname(), Port: port, RawURI: raw, Params: params}, nil
}

func parseHysteria2(raw string) (Server, error) {
	normalized := strings.Replace(raw, "hy2://", "hysteria2://", 1)
	u, err := url.Parse(normalized)
	if err != nil {
		return Server{}, err
	}
	port, _ := strconv.Atoi(u.Port())
	name, _ := url.QueryUnescape(u.Fragment)
	if name == "" {
		name = u.Hostname()
	}
	q := u.Query()
	password := u.User.Username()
	if p, _ := u.User.Password(); p != "" {
		password = p
	}
	params := map[string]string{
		"password": password, "sni": q.Get("sni"),
		"insecure": q.Get("insecure"), "obfs": q.Get("obfs"),
		"obfs-password": q.Get("obfs-password"),
	}
	country, flag := guessCountry(name, u.Hostname())
	return Server{ID: uuid.NewString(), Name: name, Country: country, Flag: flag,
		Protocol: "Hysteria2", Address: u.Hostname(), Port: port, RawURI: raw, Params: params}, nil
}

func parseNaive(raw string) (Server, error) {
	stripped := strings.TrimPrefix(raw, "naive+")
	u, err := url.Parse(stripped)
	if err != nil {
		return Server{}, err
	}
	port, _ := strconv.Atoi(u.Port())
	name, _ := url.QueryUnescape(u.Fragment)
	if name == "" {
		name = u.Hostname()
	}
	password, _ := u.User.Password()
	params := map[string]string{
		"username": u.User.Username(), "password": password, "scheme": u.Scheme,
	}
	country, flag := guessCountry(name, u.Hostname())
	return Server{ID: uuid.NewString(), Name: name, Country: country, Flag: flag,
		Protocol: "NaiveProxy", Address: u.Hostname(), Port: port, RawURI: raw, Params: params}, nil
}

func parseTrojan(raw string) (Server, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Server{}, err
	}
	port, _ := strconv.Atoi(u.Port())
	name, _ := url.QueryUnescape(u.Fragment)
	if name == "" {
		name = u.Hostname()
	}
	q := u.Query()
	params := map[string]string{
		"password": u.User.Username(), "security": q.Get("security"),
		"sni": q.Get("sni"), "type": q.Get("type"), "path": q.Get("path"),
	}
	country, flag := guessCountry(name, u.Hostname())
	return Server{ID: uuid.NewString(), Name: name, Country: country, Flag: flag,
		Protocol: "Trojan", Address: u.Hostname(), Port: port, RawURI: raw, Params: params}, nil
}

type singBoxConfig struct {
	Outbounds []json.RawMessage `json:"outbounds"`
}

type sbOutboundBase struct {
	Type   string `json:"type"`
	Tag    string `json:"tag"`
	Server string `json:"server"`
	Port   int    `json:"server_port"`
}

func parseSingBoxJSON(data []byte) ([]Server, error) {
	var cfg singBoxConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	var servers []Server
	for _, raw := range cfg.Outbounds {
		var base sbOutboundBase
		if err := json.Unmarshal(raw, &base); err != nil {
			continue
		}
		if base.Server == "" {
			continue
		}
		protocol := strings.ToUpper(base.Type)
		if base.Type == "hysteria2" {
			protocol = "Hysteria2"
		}
		country, flag := guessCountry(base.Tag, base.Server)
		servers = append(servers, Server{
			ID: uuid.NewString(), Name: base.Tag, Country: country, Flag: flag,
			Protocol: protocol, Address: base.Server, Port: base.Port,
			RawURI: string(raw), Params: map[string]string{"raw_json": string(raw)},
		})
	}
	return servers, nil
}


