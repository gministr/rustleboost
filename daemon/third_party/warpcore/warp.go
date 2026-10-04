package warpcore

// Cloudflare WARP — третий способ подключения, рядом с Xray, Hysteria2 и WDTT.
//
// Зачем он нужен. Собственные серверы стоят в дата-центрах, и крупные сервисы
// относятся к их адресам строже: просят подтвердить, что вы не робот, режут
// качество видео, придираются к входу в учётную запись. WARP выпускает трафик
// с адресов Cloudflare, которыми пользуются обычные люди со своих телефонов,
// и такого отношения там нет.
//
// Важно, чем это отличается от WARP на сервере. Регистрация своя у каждого
// устройства, и выходной адрес у каждого свой. Один общий выход на сервере,
// наоборот, быстро накапливает дурную славу: проверено на узле — поиск Google
// начинал требовать капчу. Поэтому WARP живёт только здесь, на устройстве.
//
// От глушения связи WARP не защищает: его адреса известны и закрываются
// наравне с прочими. Это способ получить чистый доступ там, где интернет
// работает, а не обойти блокировку.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/curve25519"
	"rustleboost/vkturn"
	"rustleboost/vkturn/wg"
)

type warpConfig struct {
	SocksPort int       `json:"socksPort"`
	StateDir  string    `json:"stateDir"` // где хранить регистрацию между запусками
	Routing   []wg.Rule `json:"routing"`
	MTU       int       `json:"mtu"`
	Verbose   bool      `json:"verbose"`
	// Куда идти за регистрацией. Пусто — прямо в Cloudflare; на сетях
	// российских операторов это не работает (рукопожатие TLS с
	// api.cloudflareclient.com не завершается — проверено на устройстве),
	// поэтому приложение подставляет сюда адрес нашего сервера.
	// Транспорт WDTT, если WARP едет внутри него: полная конфигурация
	// VKTurnStart. Пусто — прямой UDP до Cloudflare (как было раньше).
	Transport   json.RawMessage `json:"transport,omitempty"`
	RegisterURL string          `json:"registerUrl"`
	RegisterKey string          `json:"registerKey"` // ключ приложения для нашего сервера
}

// Учётные данные, выданные Cloudflare. Лежат на устройстве и переживают
// перезапуск: повторная регистрация на каждое подключение выглядела бы для
// Cloudflare как наплыв новых устройств.
type warpCredentials struct {
	PrivateKey    string `json:"privateKey"`
	Address       string `json:"address"`
	PeerPublicKey string `json:"peerPublicKey"`
	Endpoint      string `json:"endpoint"`
	RegisteredAt  int64  `json:"registeredAt"`
	// Запасные адреса, которые отдал наш сервер. Перебираются по порядку,
	// см. warpChooseEndpoint; удачный переезжает в Endpoint, чтобы следующий
	// запуск начинал с него.
	Endpoints []string `json:"endpoints,omitempty"`
	// Настоящий адрес Cloudflare. Endpoint может быть подменён ретранслятором,
	// а внутри WDTT WARP должен идти именно сюда.
	Upstream string `json:"upstream,omitempty"`
}

var (
	warpMu      sync.Mutex
	warpTunnel  *wg.Tunnel
	warpChained bool // внешний туннель WDTT поднят ради WARP и гасится вместе с ним
)

// WarpStart поднимает WARP и локальный SOCKS5 на указанном порту.
func WarpStart(configJSON string) error {
	warpStopping.Wait()
	var cfg warpConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("warp: конфигурация: %w", err)
	}
	if cfg.SocksPort == 0 {
		return errors.New("warp: не задан порт SOCKS5")
	}

	warpMu.Lock()
	defer warpMu.Unlock()
	if warpTunnel != nil {
		return errors.New("warp: уже запущен")
	}

	// Без запроса к нашему серверу: сетевые настройки туннеля к этому моменту
	// уже применены, и собственные запросы расширения уходят в ещё не
	// построенный туннель — ответа не будет, а подключение задержится на
	// время ожидания. Адрес уже спрошен и сохранён на предыдущем шаге,
	// см. WarpEnsureRegistered.
	if len(cfg.Transport) > 0 {
		return warpStartChained(cfg)
	}

	creds, err := warpEnsureCredentials(cfg.StateDir, cfg.RegisterURL, cfg.RegisterKey, false)
	if err != nil {
		return err
	}

	endpoint, err := netip.ParseAddrPort(creds.Endpoint)
	if err != nil {
		return fmt.Errorf("warp: адрес Cloudflare %q: %w", creds.Endpoint, err)
	}

	mtu := cfg.MTU
	if mtu <= 0 {
		// Столько же использует собственное приложение Cloudflare. Больше
		// брать нельзя: часть сетей режет пакеты, и крупные ответы молча
		// пропадали бы.
		mtu = 1280
	}
	quick := strings.Join([]string{
		"[Interface]",
		"PrivateKey = " + creds.PrivateKey,
		"Address = " + creds.Address,
		"DNS = 1.1.1.1, 1.0.0.1",
		fmt.Sprintf("MTU = %d", mtu),
		"[Peer]",
		"PublicKey = " + creds.PeerPublicKey,
		"AllowedIPs = 0.0.0.0/0",
		"Endpoint = " + creds.Endpoint,
	}, "\n")

	parsed, err := wg.ParseQuick(quick)
	if err != nil {
		return fmt.Errorf("warp: разбор конфигурации: %w", err)
	}

	tunnel, err := wg.Start(parsed, endpoint, cfg.SocksPort, wg.NewRouter(cfg.Routing), cfg.Verbose)
	if err != nil {
		return fmt.Errorf("warp: запуск: %w", err)
	}
	warpTunnel = tunnel

	if working, ok := warpChooseEndpoint(tunnel, creds); ok && working != creds.Endpoint {
		// Удачный адрес — первым на следующий запуск, чтобы перебор не
		// повторялся каждый раз.
		creds.Endpoint = working
		warpSaveCredentials(cfg.StateDir, creds)
	}
	return nil
}

// Куда стучимся, проверяя, что данные через туннель действительно ходят.
// Собственный адрес Cloudflare: он отвечает всегда и доступен изнутри WARP.
var warpProbeTarget = netip.MustParseAddrPort("1.1.1.1:80")

// Сколько ждём ответа от одного адреса. Внутрь укладываются рукопожатие и
// установка соединения; больше ждать незачем, а на полном переборе каждая
// секунда складывается в задержку подключения.
const warpProbeTimeout = 5 * time.Second

// warpChooseEndpoint перебирает адреса и оставляет тот, по которому пошли
// данные.
//
// Зачем перебор. Одного рукопожатия мало: на сети оператора оно проходило, а
// пакеты с данными не доходили — замерено на устройстве, узел отправил 22
// ответа Cloudflare, телефон принял из них только 92-байтные рукопожатия.
// Какой порт у оператора живой, заранее не угадать, и меняться это может без
// предупреждения. Поэтому ядро проверяет адреса настоящим соединением наружу
// и само выбирает работающий.
func warpChooseEndpoint(tunnel *wg.Tunnel, creds warpCredentials) (string, bool) {
	candidates := make([]string, 0, len(creds.Endpoints)+1)
	candidates = append(candidates, creds.Endpoint)
	for _, candidate := range creds.Endpoints {
		if candidate != creds.Endpoint {
			candidates = append(candidates, candidate)
		}
	}

	for _, candidate := range candidates {
		address, err := netip.ParseAddrPort(candidate)
		if err != nil {
			continue
		}
		if err := tunnel.SetEndpoint(address); err != nil {
			log.Printf("[WARP] %s: не получилось назначить: %v", candidate, err)
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), warpProbeTimeout)
		err = tunnel.Probe(ctx, warpProbeTarget)
		cancel()

		stats := tunnel.PeerStats()
		if err == nil {
			log.Printf("[WARP] работает через %s (входящих %d байт)", candidate, stats.Received)
			return candidate, true
		}
		// Разделяем две разные беды. Нет рукопожатия — пакеты не доходят до
		// Cloudflare или не возвращаются вовсе. Рукопожатие есть, а ответа
		// нет — доходят только мелкие: ровно это и видно на устройстве.
		if stats.Handshaken {
			log.Printf("[WARP] %s: рукопожатие прошло, данные не идут — %v (входящих %d байт)",
				candidate, err, stats.Received)
		} else {
			log.Printf("[WARP] %s: рукопожатия нет — %v (отправлено %d байт)",
				candidate, err, stats.Sent)
		}
	}

	// Ни один не ответил. Возвращаем пира на первый адрес: оставить его на
	// последнем перебранном означало бы молча посадить подключение на самый
	// неудачный из списка.
	if address, err := netip.ParseAddrPort(candidates[0]); err == nil {
		_ = tunnel.SetEndpoint(address)
	}
	// Туннель оставляем поднятым: сторож живости всё равно переспросит, а
	// приложение покажет отсутствие связи честнее, чем отказ запуска с
	// невнятной причиной.
	log.Printf("[WARP] ни один из %d адресов не отозвался", len(candidates))
	return "", false
}

func warpSaveCredentials(stateDir string, creds warpCredentials) {
	if stateDir == "" {
		return
	}
	data, err := json.Marshal(creds)
	if err != nil {
		return
	}
	_ = os.MkdirAll(stateDir, 0o700)
	_ = os.WriteFile(filepath.Join(stateDir, warpCredentialsFile), data, 0o600)
}

// WarpEnsureRegistered получает и сохраняет регистрацию, не поднимая туннель.
//
// Вызывается до того, как расширение применит сетевые настройки. После их
// применения трафик расширения уходит в ещё не построенный туннель, и запрос к
// Cloudflare умирает по таймауту рукопожатия — так и было на устройстве:
// «Шаг 2» в журнале, а через десять секунд отказ.
func WarpEnsureRegistered(configJSON string) error {
	var cfg warpConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("warp: конфигурация: %w", err)
	}
	// Через WDTT адрес берётся настоящий, ретранслятор ни при чём — его не
	// переспрашиваем.
	refresh := len(cfg.Transport) == 0
	_, err := warpEnsureCredentials(cfg.StateDir, cfg.RegisterURL, cfg.RegisterKey, refresh)
	return err
}

// warpStopping ждёт фонового закрытия туннеля. Новый запуск не должен
// начинаться, пока старое закрытие не закончилось: оба трогают один и тот же
// транспорт WDTT.
var warpStopping sync.WaitGroup

// WarpStop отпускает туннель сразу, а закрытие делает в фоне.
//
// Раньше остановка ждала закрытия всех соединений и на телефоне занимала
// несколько секунд: система отменяла остановку по таймауту, и пользователь
// видел «отключение» вместо мгновенного «не подключено». Снятие интерфейса
// важно для системы, а освобождение сокетов можно доделать после.
func WarpStop() {
	warpMu.Lock()
	t := warpTunnel
	chained := warpChained
	warpTunnel = nil
	warpChained = false
	warpMu.Unlock()
	if t == nil && !chained {
		return
	}
	warpStopping.Add(1)
	go func() {
		defer warpStopping.Done()
		t.Close()
		if chained {
			vkturn.StopTunnel()
		}
	}()
}

func WarpIsRunning() bool {
	warpMu.Lock()
	defer warpMu.Unlock()
	return warpTunnel != nil
}

// WarpReset забывает регистрацию. Нужен, если Cloudflare перестала принимать
// выданные нам данные: следующий запуск зарегистрируется заново.
func WarpReset(stateDir string) {
	if stateDir != "" {
		_ = os.Remove(filepath.Join(stateDir, warpCredentialsFile))
	}
}

const warpCredentialsFile = "warp.json"

// warpLoadCredentials читает сохранённую регистрацию. Неполная запись не
// годится: Cloudflare откажет, а по её отказу причину не понять — лучше
// зарегистрироваться заново.
func warpLoadCredentials(path string) (warpCredentials, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return warpCredentials{}, false
	}
	var creds warpCredentials
	if json.Unmarshal(data, &creds) != nil {
		return warpCredentials{}, false
	}
	complete := creds.PrivateKey != "" && creds.PeerPublicKey != "" &&
		creds.Address != "" && creds.Endpoint != "" && creds.Upstream != ""
	return creds, complete
}

// warpEnsureCredentials отдаёт сохранённую регистрацию или получает новую.
//
// refresh — спросить ли у нашего сервера свежий адрес узла. Спрашивать можно
// только пока сеть обычная: после применения сетевых настроек туннеля запрос
// уйдёт в никуда.
func warpEnsureCredentials(stateDir, registerURL, registerKey string, refresh bool) (warpCredentials, error) {
	path := ""
	creds := warpCredentials{}
	saved := false
	if stateDir != "" {
		path = filepath.Join(stateDir, warpCredentialsFile)
		creds, saved = warpLoadCredentials(path)
	}

	changed := false
	if !saved {
		fresh, err := warpRegister(registerURL, registerKey)
		if err != nil {
			return warpCredentials{}, err
		}
		creds = fresh
		changed = true
	}

	// Куда слать пакеты — переспрашиваем, см. warpRefreshEndpoint.
	if refresh {
		if endpoints, found := warpRefreshEndpoint(registerURL, registerKey); found {
			if !slices.Equal(endpoints, creds.Endpoints) {
				creds.Endpoints = endpoints
				changed = true
			}
			// Сохранённый адрес держим первым, если он всё ещё в списке: он
			// уже проверен на этой сети, и перебор начнётся с удачного.
			if !slices.Contains(endpoints, creds.Endpoint) {
				creds.Endpoint = endpoints[0]
				changed = true
			}
		}
	}

	if changed && path != "" {
		warpSaveCredentials(stateDir, creds)
	}
	return creds, nil
}

// warpRefreshEndpoint спрашивает у нашего сервера, куда сейчас слать пакеты
// WireGuard.
//
// Выданный Cloudflare адрес годится не всегда: с сетей российских операторов
// её узлы недоступны, и тогда сервер отдаёт вместо них наш ретранслятор — он
// пересылает пакеты в Cloudflare, не расшифровывая их, поэтому ключи
// по-прежнему знают только устройство и Cloudflare.
//
// Адресов может быть несколько: какой порт у оператора живой, заранее не
// известно, и ядро выбирает работающий само (см. warpChooseEndpoint).
//
// Спрашивается при каждом запуске, чтобы менять путь без обновления
// приложения. Сохранённый адрес при этом остаётся запасным: если наш сервер
// не ответил, подключение всё равно состоится по прежнему пути.
func warpRefreshEndpoint(registerURL, appKey string) ([]string, bool) {
	if registerURL == "" {
		return nil, false
	}
	url := strings.TrimSuffix(registerURL, "/register") + "/endpoint"
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, false
	}
	if appKey != "" {
		request.Header.Set("X-App-Key", appKey)
	}

	// Коротко: запрос стоит на пути подключения, и недоступный сервер не
	// должен задерживать его надолго.
	client := &http.Client{Timeout: 6 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, false
	}

	var answer struct {
		Endpoint  string   `json:"endpoint"`
		Endpoints []string `json:"endpoints"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&answer) != nil {
		return nil, false
	}

	// Одиночный адрес тоже принимаем: так отвечал сервер прежней сборки.
	raw := answer.Endpoints
	if len(raw) == 0 && answer.Endpoint != "" {
		raw = []string{answer.Endpoint}
	}
	endpoints := make([]string, 0, len(raw))
	for _, candidate := range raw {
		if _, err := netip.ParseAddrPort(candidate); err == nil {
			endpoints = append(endpoints, candidate)
		}
	}
	if len(endpoints) == 0 {
		return nil, false
	}
	return endpoints, true
}

// warpRegister заводит устройство в Cloudflare и получает параметры туннеля.
func warpRegister(registerURL, registerKey string) (warpCredentials, error) {
	private, public, err := warpKeyPair()
	if err != nil {
		return warpCredentials{}, err
	}

	// Через свой сервер: закрытый ключ остаётся здесь, наружу уходит только
	// открытая часть.
	if registerURL != "" {
		creds, err := warpRegisterVia(registerURL, registerKey, public)
		if err != nil {
			return warpCredentials{}, err
		}
		creds.PrivateKey = private
		creds.RegisteredAt = time.Now().Unix()
		return creds, nil
	}

	body, _ := json.Marshal(map[string]string{
		"key":           public,
		"install_id":    "",
		"fcm_token":     "",
		"tos":           time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"model":         "PC",
		"serial_number": "",
		"locale":        "en_US",
	})
	request, err := http.NewRequest("POST", "https://api.cloudflareclient.com/v0a2158/reg", strings.NewReader(string(body)))
	if err != nil {
		return warpCredentials{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("CF-Client-Version", "a-6.30-3596")

	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return warpCredentials{}, fmt.Errorf("warp: регистрация: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return warpCredentials{}, fmt.Errorf("warp: Cloudflare ответила %d", response.StatusCode)
	}

	var answer struct {
		Config struct {
			Interface struct {
				Addresses struct {
					V4 string `json:"v4"`
				} `json:"addresses"`
			} `json:"interface"`
			Peers []struct {
				PublicKey string `json:"public_key"`
				Endpoint  struct {
					V4 string `json:"v4"`
				} `json:"endpoint"`
			} `json:"peers"`
		} `json:"config"`
	}
	if err := json.NewDecoder(response.Body).Decode(&answer); err != nil {
		return warpCredentials{}, fmt.Errorf("warp: ответ Cloudflare: %w", err)
	}
	if len(answer.Config.Peers) == 0 || answer.Config.Peers[0].PublicKey == "" {
		return warpCredentials{}, errors.New("warp: Cloudflare не прислала параметры узла")
	}

	address := answer.Config.Interface.Addresses.V4
	if address == "" {
		return warpCredentials{}, errors.New("warp: Cloudflare не выдала адрес")
	}

	// В ответе порт бывает нулевым — тогда берём тот, на котором WARP слушает
	// всегда.
	endpoint := answer.Config.Peers[0].Endpoint.V4
	if host, port, found := strings.Cut(endpoint, ":"); !found || port == "0" || port == "" {
		endpoint = host + ":2408"
	}

	return warpCredentials{
		PrivateKey:    private,
		Address:       address + "/32",
		PeerPublicKey: answer.Config.Peers[0].PublicKey,
		Endpoint:      endpoint,
		Upstream:      endpoint,
		RegisteredAt:  time.Now().Unix(),
	}, nil
}

// warpRegisterVia просит наш сервер зарегистрировать устройство в Cloudflare.
func warpRegisterVia(url, appKey, publicKey string) (warpCredentials, error) {
	payload, _ := json.Marshal(map[string]string{"publicKey": publicKey})
	request, err := http.NewRequest("POST", url, strings.NewReader(string(payload)))
	if err != nil {
		return warpCredentials{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	if appKey != "" {
		request.Header.Set("X-App-Key", appKey)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return warpCredentials{}, fmt.Errorf("warp: регистрация через сервер: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return warpCredentials{}, fmt.Errorf("warp: сервер ответил %d", response.StatusCode)
	}

	var answer struct {
		PeerPublicKey string `json:"peerPublicKey"`
		Address       string `json:"address"`
		Endpoint      string `json:"endpoint"`
		Upstream      string `json:"upstream"`
	}
	if err := json.NewDecoder(response.Body).Decode(&answer); err != nil {
		return warpCredentials{}, fmt.Errorf("warp: ответ сервера: %w", err)
	}
	if answer.PeerPublicKey == "" || answer.Address == "" || answer.Endpoint == "" {
		return warpCredentials{}, errors.New("warp: сервер прислал неполные данные")
	}
	upstream := answer.Upstream
	if upstream == "" {
		// Сервер прежней сборки настоящего адреса не присылает: тогда
		// Endpoint и есть адрес Cloudflare.
		upstream = answer.Endpoint
	}
	return warpCredentials{
		Address:       answer.Address,
		PeerPublicKey: answer.PeerPublicKey,
		Endpoint:      answer.Endpoint,
		Upstream:      upstream,
	}, nil
}

// warpKeyPair создаёт пару ключей WireGuard: закрытый и открытый, оба в base64.
func warpKeyPair() (private, public string, err error) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return "", "", err
	}
	// Приведение по требованиям curve25519.
	key[0] &= 248
	key[31] &= 127
	key[31] |= 64

	pub, err := curve25519.X25519(key[:], curve25519.Basepoint)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(key[:]),
		base64.StdEncoding.EncodeToString(pub), nil
}

// WarpStats описывает состояние канала с Cloudflare человеческими словами.
//
// Без этого самая частая неполадка неразличима: приложение показывает
// «подключено», а ничего не грузится. Интерфейс WireGuard внутри процесса
// поднимается всегда, дошли пакеты до Cloudflare или нет, поэтому отличить
// «Cloudflare недоступна у этого оператора» от ошибки в нашем стеке можно
// только по рукопожатию.
func WarpStats() string {
	warpMu.Lock()
	t := warpTunnel
	warpMu.Unlock()
	if t == nil {
		return "WARP не запущен"
	}
	stats := t.PeerStats()
	if !stats.Handshaken {
		return fmt.Sprintf(
			"рукопожатия с Cloudflare не было, отправлено %d байт — ответа нет",
			stats.Sent,
		)
	}
	return fmt.Sprintf(
		"рукопожатие %s назад, ↓%d ↑%d байт",
		stats.HandshakeAgo.Round(time.Second), stats.Received, stats.Sent,
	)
}

// warpStartChained поднимает WARP внутри WDTT.
//
// Порядок важен. Сначала поднимается WDTT на свободном локальном порту — тот
// самый туннель, который приложение уже умеет держать. Поверх его стека
// заводится внутренний WireGuard с ключами WARP и адресом Cloudflare, и уже он
// слушает порт SOCKS5, который видит приложение.
func warpStartChained(cfg warpConfig) error {
	creds, err := warpEnsureCredentials(cfg.StateDir, cfg.RegisterURL, cfg.RegisterKey, false)
	if err != nil {
		return err
	}
	remote, err := netip.ParseAddrPort(creds.Upstream)
	if err != nil {
		return fmt.Errorf("warp: адрес Cloudflare %q: %w", creds.Upstream, err)
	}

	outerPort, err := freeLocalPort()
	if err != nil {
		return err
	}
	outerConfig, err := withSocksPort(cfg.Transport, outerPort)
	if err != nil {
		return err
	}
	if err := VKTurnStart(outerConfig); err != nil {
		return fmt.Errorf("warp: транспорт WDTT: %w", err)
	}
	outer := vkturn.CurrentTunnel()
	if outer == nil {
		vkturn.StopTunnel()
		return errors.New("warp: транспорт WDTT не поднялся")
	}

	mtu := wg.ChainedMTU
	parsed, err := wg.ParseQuick(warpQuickConfig(creds, creds.Upstream, mtu))
	if err != nil {
		vkturn.StopTunnel()
		return fmt.Errorf("warp: разбор конфигурации: %w", err)
	}

	tunnel, err := wg.StartChained(outer, parsed, remote, cfg.SocksPort, wg.NewRouter(cfg.Routing), cfg.Verbose)
	if err != nil {
		vkturn.StopTunnel()
		return fmt.Errorf("warp: запуск поверх WDTT: %w", err)
	}
	warpTunnel = tunnel
	warpChained = true
	return nil
}

// warpQuickConfig собирает конфигурацию WireGuard для WARP.
func warpQuickConfig(creds warpCredentials, endpoint string, mtu int) string {
	return strings.Join([]string{
		"[Interface]",
		"PrivateKey = " + creds.PrivateKey,
		"Address = " + creds.Address,
		"DNS = 1.1.1.1, 1.0.0.1",
		fmt.Sprintf("MTU = %d", mtu),
		"[Peer]",
		"PublicKey = " + creds.PeerPublicKey,
		"AllowedIPs = 0.0.0.0/0",
		"Endpoint = " + endpoint,
	}, "\n")
}

// withSocksPort подменяет порт SOCKS5 во вложенной конфигурации WDTT.
func withSocksPort(transport json.RawMessage, port int) (string, error) {
	var fields map[string]any
	if err := json.Unmarshal(transport, &fields); err != nil {
		return "", fmt.Errorf("warp: конфигурация транспорта: %w", err)
	}
	fields["socksPort"] = port
	data, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// freeLocalPort отдаёт свободный порт на петле. Гонка возможна, но для
// служебного порта внешнего туннеля её хватает.
func freeLocalPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("warp: свободный порт: %w", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}
