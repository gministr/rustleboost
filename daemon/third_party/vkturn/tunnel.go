package vkturn

import (
	"errors"
	"fmt"
	"log"
	"net/netip"
	"sync"
	"time"

	"rustleboost/vkturn/wg"
)

// Сборка туннеля целиком: транспорт через TURN плюс WireGuard над ним.
//
// Порядок продиктован тем, что конфигурацию WireGuard выдаёт сервер, а не
// клиент: пока воркеры не получили креды VK и не построили аллокации, ключей
// и адреса ещё нет. Поэтому сначала поднимается транспорт, затем ожидается
// конфигурация, и только потом встаёт WireGuard.

// TunnelOptions — параметры сборки поверх Config.
type TunnelOptions struct {
	// SocksPort — локальный порт, который займёт SOCKS5. Через него
	// расширение туннеля заводит трафик в WireGuard.
	SocksPort int
	// ConfigTimeout — сколько ждать конфигурацию от сервера.
	ConfigTimeout time.Duration
	// VerboseWireGuard включает подробный лог WireGuard. Годится для разбора
	// проблем с рукопожатием, в обычной работе только засоряет кольцо.
	VerboseWireGuard bool
	// Routing — сборка правил маршрутизации. Пустая означает, что весь
	// трафик идёт через туннель.
	//
	// Применяются они здесь, а не в ядре: у WDTT ядра нет, туннель поднимает
	// сам WireGuard. Формат записей общий с Xray, чтобы сборки правил в
	// приложении работали одинаково для всех протоколов.
	Routing []wg.Rule
	// MaxConnections — предел одновременных соединений через WireGuard.
	// Ноль — значение по умолчанию (под память расширения iOS).
	MaxConnections int
	// DNSServers — запасные адреса на случай, если сервер не назвал свои.
	//
	// Без них сетевой стек не умеет разрешать имена вовсе, и датаграмма с
	// доменом в заголовке молча пропадает. Берутся из настроек туннеля — тех
	// же, что расширение объявляет системе.
	DNSServers []string
}

// Срок ожидания конфигурации по умолчанию.
//
// Система даёт расширению туннеля около минуты на запуск, и выйти за неё
// нельзя: иначе туннель снимут ровно в тот момент, когда он почти поднялся.
// Сорок пять секунд оставляют запас на настройку сети и мост.
const defaultConfigTimeout = 45 * time.Second

var (
	tunnelMu sync.Mutex
	tunnel   *wg.Tunnel
)

// StartTunnel поднимает транспорт и WireGuard над ним.
//
// Возврат происходит, когда SOCKS5 уже слушает порт, — то есть когда туннелем
// можно пользоваться. Рукопожатие WireGuard к этому моменту может быть ещё не
// завершено, но первые же пакеты его и вызовут.
func StartTunnel(cfg Config, options TunnelOptions) error {
	tunnelMu.Lock()
	defer tunnelMu.Unlock()
	// Отмена относится к этому запуску и только к нему: чем бы он ни
	// закончился, следующий начинается с чистого листа. Иначе отмена,
	// прервавшая этот запуск, сорвала бы и подключение к другому серверу,
	// начатое сразу следом.
	defer clearAbort()

	if tunnel != nil {
		return errors.New("vkturn: туннель уже поднят")
	}
	if options.SocksPort == 0 {
		return errors.New("vkturn: не задан порт SOCKS5")
	}
	if options.ConfigTimeout <= 0 {
		options.ConfigTimeout = defaultConfigTimeout
	}

	startedAt := time.Now()
	transportPort, err := Start(cfg)
	if err != nil {
		return err
	}

	// Любой выход по ошибке обязан снять транспорт: воркеры уже держат
	// аллокации на стороне VK, и брошенные они освободятся только по
	// таймауту, отъедая лимит следующей попытки.
	started := false
	defer func() {
		if !started {
			Stop()
		}
	}()

	log.Printf("[ТУННЕЛЬ] Транспорт поднят на порту %d, жду конфигурацию сервера", transportPort)

	waitStarted := time.Now()
	raw := WireGuardConfig(options.ConfigTimeout)
	if raw == "" {
		waited := time.Since(waitStarted).Round(time.Second)
		if waited < options.ConfigTimeout {
			// Воркеры завершились раньше срока — значит, до сервера не дошли
			// вовсе: чаще всего VK отказал в кредах. Причина уже в журнале
			// движка строкой выше.
			return fmt.Errorf("vkturn: воркеры завершились за %s, не получив конфигурацию", waited)
		}
		return fmt.Errorf("vkturn: сервер не прислал конфигурацию за %s", options.ConfigTimeout)
	}

	log.Printf("[ЗАМЕР] Конфигурация сервера получена через %d мс после старта",
		time.Since(startedAt).Milliseconds())

	parsed, err := wg.ParseQuick(raw)
	if err != nil {
		return fmt.Errorf("vkturn: конфигурация сервера: %w", err)
	}
	parsed.ApplyFallbackDNS(options.DNSServers)

	endpoint := netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), uint16(transportPort))
	wg.SetMaxConnections(options.MaxConnections)
	t, err := wg.Start(
		parsed, endpoint, options.SocksPort,
		wg.NewRouter(options.Routing), options.VerboseWireGuard,
	)
	if err != nil {
		return err
	}

	tunnel = t
	started = true
	log.Printf("[ЗАМЕР] Туннель готов через %d мс после старта", time.Since(startedAt).Milliseconds())
	return nil
}

// StopTunnel снимает WireGuard и транспорт.
//
// Порядок обратный запуску: сначала WireGuard перестаёт слать пакеты, затем
// закрываются аллокации. Наоборот — и последние пакеты WireGuard уходили бы в
// уже закрытый сокет, засоряя лог ошибками при штатной остановке.
func StopTunnel() {
	// Если запуск ещё идёт, он держит tunnelMu до конца ожидания
	// конфигурации. Сначала прерываем его — ожидание увидит отмену, запуск
	// вернёт ошибку и отпустит замок за доли секунды.
	Abort()
	defer clearAbort()

	tunnelMu.Lock()
	t := tunnel
	tunnel = nil
	tunnelMu.Unlock()

	t.Close()
	Stop()
}

// TunnelIsRunning сообщает, поднят ли туннель целиком.
func TunnelIsRunning() bool {
	tunnelMu.Lock()
	defer tunnelMu.Unlock()
	return tunnel != nil
}

// CurrentTunnel отдаёт поднятый туннель WDTT, чтобы поверх него можно было
// пустить другой WireGuard (см. wg.StartChained). Nil, если туннель не поднят.
func CurrentTunnel() *wg.Tunnel {
	tunnelMu.Lock()
	defer tunnelMu.Unlock()
	return tunnel
}
