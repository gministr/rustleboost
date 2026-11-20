package subscription

// Транспорт WARP: пункт Cloudflare WARP и серверы WDTT из каталога RustleBoost.
//
// WARP на устройстве едет внутри WDTT: прямой UDP до Cloudflare на сетях
// российских операторов не проходит, а туннель WDTT проходит. Поэтому Windows
// берёт WDTT-серверы из каталога, а не из подписки Remnawave: тот же каталог,
// что на iOS и Android.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	// EngineWarp — движок WARP внутри демона; Engine у пункта WARP.
	EngineWarp = "warp"
	// EngineWdtt — серверы WDTT из каталога, служат транспортом для WARP.
	EngineWdtt = "wdtt"

	// CatalogBaseURL — каталог RustleBoost; к нему дописывается короткий ключ подписки.
	CatalogBaseURL = "https://auth.lindavpn.com/catalog?key="
	// CatalogAppKey — ключ приложения, без него каталог не отдаётся.
	CatalogAppKey = "01OPe3R6UJ6FsMEFCjGNJwjU3EOkta-l"
)

// Группы серверов — вкладки интерфейса.
const (
	GroupRegular     = "regular"
	GroupRustleBoost = "rustleboost"
	GroupWarp        = "warp"
)

// WarpServer — синтетический пункт WARP: адрес и ключи выдаёт Cloudflare при
// первом подключении, поэтому реальный адрес здесь только для подписи.
func WarpServer() Server {
	return Server{
		ID:       "warp",
		Name:     "Cloudflare WARP",
		Protocol: "WARP",
		Address:  "engage.cloudflareclient.com",
		Port:     2408,
		Engine:   EngineWarp,
		Group:    GroupWarp,
	}
}

// parseWdtt разбирает ссылку wdtt://<пароль>@<хост>:<порт>?hashes=...#имя.
func parseWdtt(raw string) (Server, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Server{}, err
	}
	password := u.User.Username()
	if password == "" {
		return Server{}, fmt.Errorf("wdtt: нет пароля")
	}
	q := u.Query()
	hashes := splitCSV(q.Get("hashes"))
	if len(hashes) == 0 {
		return Server{}, fmt.Errorf("wdtt: нет хешей звонков")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port == 0 {
		return Server{}, fmt.Errorf("wdtt: порт")
	}
	name := u.Fragment
	if name == "" {
		name = u.Hostname()
	}
	params := map[string]string{
		"password": password,
		"hashes":   strings.Join(hashes, ","),
		"workers":  q.Get("workers"),
		"obfs":     q.Get("obfs"),
		"turnHost": q.Get("turnHost"),
		"turnPort": q.Get("turnPort"),
		// SRTP на том же узле (srtp=порт&srtpkey=ключ&srtptok=токен). Нет —
		// обычный WDTT.
		"srtp":     q.Get("srtp"),
		"srtpkey":  q.Get("srtpkey"),
		"srtptok":  q.Get("srtptok"),
		"srtpname": strings.TrimSpace(q.Get("srtpname")),
		// Запасные релеи ВК для узла (через запятую): ядро подставляет их,
		// когда свежие релеи не принимают учётные данные.
		"relays": q.Get("relays"),
	}
	return Server{
		ID:       newID(),
		Name:     name,
		Protocol: "WDTT",
		Address:  u.Hostname(),
		Port:     port,
		Engine:   EngineWdtt,
		RawURI:   raw,
		Params:   params,
	}, nil
}

// ShortKey — последний сегмент пути ссылки подписки: по нему каталог узнаёт клиента.
func ShortKey(subURL string) string {
	u, err := url.Parse(strings.TrimSpace(subURL))
	if err != nil {
		return ""
	}
	path := strings.TrimSuffix(u.Path, "/")
	return path[strings.LastIndex(path, "/")+1:]
}

// FetchCatalog загружает каталог и возвращает из него серверы WDTT.
// Каталог отдаёт те же ссылки, что и подписка, поэтому разбор общий.
func FetchCatalog(ctx context.Context, subURL string, hwid HWIDHeaders) ([]Server, error) {
	key := ShortKey(subURL)
	if key == "" {
		return nil, fmt.Errorf("catalog: нет ключа подписки")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CatalogBaseURL+key, nil)
	if err != nil {
		return nil, err
	}
	applyHeaders(req, hwid)
	req.Header.Set("X-App-Key", CatalogAppKey)

	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	servers, err := Parse(data)
	if err != nil {
		return nil, err
	}
	var wdtt []Server
	for _, s := range servers {
		if s.Engine == EngineWdtt {
			s.Group = GroupRustleBoost
			wdtt = append(wdtt, s)
		}
	}
	return SplitSRTP(wdtt), nil
}

// SRTPNameSuffix — приписка к названию сервера через SRTP, как на iOS и Android.
const SRTPNameSuffix = " · Новый"

// SplitSRTP — узел с SRTP показывается двумя серверами, как на iOS
// (ShareLinkParser.splittingSRTP): «стабильный» — прежний WDTT с прежним
// названием (и потому прежним идентификатором: выбранный раньше сервер
// остаётся выбранным), и «новый» — SRTP, быстрее, но работает не у всех.
// Новые идут отдельным списком после всех стабильных. Название нового можно
// задать в админке (srtpname).
func SplitSRTP(servers []Server) []Server {
	stable := make([]Server, 0, len(servers))
	var fast []Server
	for _, s := range servers {
		if s.Engine != EngineWdtt || !HasSRTP(s) {
			stable = append(stable, s)
			continue
		}
		fastServer := s
		fastServer.ID = newID()
		fastServer.Params = cloneParams(s.Params)
		fastServer.Protocol = "SRTP"
		if name := s.Params["srtpname"]; name != "" {
			fastServer.Name = name
		} else {
			fastServer.Name = s.Name + SRTPNameSuffix
		}
		fast = append(fast, fastServer)

		plain := s
		plain.Params = cloneParams(s.Params)
		delete(plain.Params, "srtp")
		delete(plain.Params, "srtpkey")
		delete(plain.Params, "srtptok")
		delete(plain.Params, "srtpname")
		stable = append(stable, plain)
	}
	return append(stable, fast...)
}

// HasSRTP — в ссылке узла есть всё для SRTP.
func HasSRTP(s Server) bool {
	return s.Params["srtp"] != "" && s.Params["srtpkey"] != "" && s.Params["srtptok"] != ""
}

func cloneParams(params map[string]string) map[string]string {
	out := make(map[string]string, len(params))
	for k, v := range params {
		out[k] = v
	}
	return out
}
