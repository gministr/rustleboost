package warpcore

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"rustleboost/vkturn"
	"rustleboost/vkturn/wg"
)

// Мост к движку WDTT — WireGuard поверх TURN-релеев VK.
//
// Третье ядро в том же пакете и по той же причине, что и первые два: каждая
// сборка gomobile несёт собственный рантайм Go, а два рантайма в одном
// процессе не уживаются. Поэтому Xray, Hysteria2 и WDTT линкуются в один
// фреймворк, и наружу все трое выглядят одинаково — поднимают SOCKS5 на
// петлевом интерфейсе, а трафик в него заводит tun2socks.
//
// Отличие от первых двух в том, где берётся конфигурация. Xray и Hysteria2
// получают всё из подписки, а здесь ключи и адрес WireGuard выдаёт сам сервер
// после того, как поднялись аллокации. Ожидание спрятано внутри VKTurnStart:
// вызов возвращается, когда туннелем уже можно пользоваться.

// vkTurnConfig — то, что приходит из Swift. Имена полей совпадают с
// TunnelConfiguration на стороне приложения.
type vkTurnConfig struct {
	PeerAddress string    `json:"peerAddress"` // адрес:порт сервера wdtt-server
	VKHashes    string    `json:"vkHashes"`    // хеши звонков VK через запятую
	Password    string    `json:"password"`    // пароль подключения, из него выводится WRAP-ключ
	DeviceID    string    `json:"deviceId"`
	SocksPort   int       `json:"socksPort"`
	Workers     int       `json:"workers"`
	ObfsMode    string    `json:"obfsMode"`    // audio или video
	Fingerprint string    `json:"fingerprint"` // отпечаток TLS
	ClientIDs   string    `json:"clientIds"`
	VKAuthMode  string    `json:"vkAuthMode"`  // vkcalls или legacy
	CaptchaMode string    `json:"captchaMode"` // auto, wv или rjs
	TurnHost    string    `json:"turnHost"`    // переопределение relay, обычно пусто
	TurnPort    string    `json:"turnPort"`
	DNSServers  string    `json:"dnsServers"` // запасные DNS через запятую
	Routing     []wg.Rule `json:"routing"`    // сборка правил маршрутизации
	TimeoutSec  int       `json:"timeoutSec"` // ожидание конфигурации сервера
	// Каталог для кредов TURN между запусками процесса. Пусто — только память.
	CredentialsDir string `json:"credentialsDir"`
	// Пакетов подряд на один воркер. Ноль — по умолчанию движка.
	DispatchChunk int `json:"dispatchChunk"`
	// Предел одновременных соединений. Ноль — по умолчанию (iOS).
	MaxConnections int  `json:"maxConnections"`
	Verbose        bool `json:"verbose"`
}

// Число воркеров по умолчанию.
//
// На Android стоит 24, но там процесс живёт без потолка памяти. У расширения
// туннеля лимит около 50 МБ на всё сразу — сетевой стек, WireGuard и
// аллокации, — поэтому здесь берётся вдвое меньше. Значение из подписки его
// перекрывает, и поднимать его стоит по результатам замера памяти, а не
// заранее.
const vkTurnDefaultWorkers = 12

// VKTurnStart поднимает туннель WDTT и локальный SOCKS5.
//
// Возвращает управление, когда SOCKS5 уже слушает порт. Ошибка означает, что
// ни транспорт, ни WireGuard не остались запущенными — снимать ничего не надо.
func VKTurnStart(configJSON string) error {
	var config vkTurnConfig
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return fmt.Errorf("vkturn: разбор конфигурации: %w", err)
	}

	workers := config.Workers
	if workers <= 0 {
		workers = vkTurnDefaultWorkers
	}

	return vkturn.StartTunnel(
		vkturn.Config{
			PeerAddr:    config.PeerAddress,
			VKHashes:    config.VKHashes,
			Password:    config.Password,
			DeviceID:    config.DeviceID,
			Workers:     workers,
			ObfsMode:    config.ObfsMode,
			Fingerprint: config.Fingerprint,
			ClientIDs:   config.ClientIDs,
			VKAuthMode:  config.VKAuthMode,
			CaptchaMode: config.CaptchaMode,
			TurnHost:    config.TurnHost,
			TurnPort:    config.TurnPort,

			CredentialsDir: config.CredentialsDir,
			DispatchChunk:  config.DispatchChunk,
		},
		vkturn.TunnelOptions{
			SocksPort:        config.SocksPort,
			DNSServers:       splitList(config.DNSServers),
			Routing:          config.Routing,
			ConfigTimeout:    time.Duration(config.TimeoutSec) * time.Second,
			MaxConnections:   config.MaxConnections,
			VerboseWireGuard: config.Verbose,
		},
	)
}

// splitList разбивает список, записанный через запятую.
func splitList(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// VKTurnStop снимает туннель. Повторный вызов безопасен.
func VKTurnStop() {
	vkturn.StopTunnel()
}

// VKTurnAbort прерывает идущий запуск и сразу возвращает управление.
//
// Нужен остановке туннеля: VKTurnStart блокирует вызвавший поток на всё время
// запуска — до сорока пяти секунд ожидания конфигурации на плохой сети, — и
// VKTurnStop, вставший за ним в очередь, ждал бы столько же. Этот вызов
// замков запуска не берёт.
func VKTurnAbort() {
	vkturn.Abort()
}

// VKTurnIsRunning сообщает, поднят ли туннель.
func VKTurnIsRunning() bool {
	return vkturn.TunnelIsRunning()
}

// VKTurnStatsJSON отдаёт счётчики сессии.
//
// Отдельно от статистики Xray: у WDTT считаются байты транспорта, а не
// соединения ядра, и смешивать их в одном типе означало бы врать в одном из
// двух случаев.
func VKTurnStatsJSON() string {
	up, down, active := vkturn.Statistics()
	payload, err := json.Marshal(map[string]any{
		"bytesUp":   up,
		"bytesDown": down,
		"active":    active,
	})
	if err != nil {
		return "{}"
	}
	return string(payload)
}

// VKTurnReadLog отдаёт накопленный лог клиента.
func VKTurnReadLog() string {
	return vkturn.ReadLog()
}

// VKTurnPause и VKTurnResume приостанавливают отправку, не разрывая
// аллокации. Нужны при смене сети: поднять аллокации заново дороже, чем
// переждать переключение.
func VKTurnPause()  { vkturn.Pause() }
func VKTurnResume() { vkturn.Resume() }

// VKTurnSubmitCaptcha передаёт разгаданную капчу ожидающему воркеру.
//
// Строки "error:timeout" и "error:cancelled" сообщают, что пользователь не
// справился или закрыл окно, — воркер тогда не ждёт до конца таймаута.
func VKTurnSubmitCaptcha(result string) {
	vkturn.SubmitCaptcha(result)
}
