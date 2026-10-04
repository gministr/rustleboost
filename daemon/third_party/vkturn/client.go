package vkturn

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Точка входа библиотеки — замена main() из Android-клиента.
//
// На Android клиент живёт отдельным процессом: флаги приходят из командной
// строки, команды — через stdin, события — печатью в stdout, а остановка —
// сигналом. На iOS отдельного процесса нет и быть не может: расширение
// туннеля обязано делать всё внутри себя. Поэтому здесь тот же сценарий
// запуска, но собранный из вызовов Start/Stop, а всё, что оригинал читал из
// окружения процесса, передаётся структурой Config.
//
// Сам транспорт не тронут: dispatcher, worker_group, session и обфускация
// используются без изменений, чтобы расхождение с Android-веткой оставалось
// минимальным и правки из неё переносились дословно.

// Config — параметры подключения.
type Config struct {
	// PeerAddr — адрес:порт сервера wdtt-server, к которому TURN relay строит
	// аллокации.
	PeerAddr string
	// VKHashes — хеши звонков VK через запятую. Один хеш даёт ограниченное
	// число аллокаций, поэтому на большое число воркеров нужно несколько.
	VKHashes string
	// Password — пароль подключения. WRAP-ключ выводится из него через HKDF и
	// нигде не хранится в готовом виде.
	Password string
	// DeviceID — идентификатор устройства для привязки пароля на сервере.
	DeviceID string
	// Workers — число воркеров. Приводится к кратному workersPerGroup.
	Workers int
	// ObfsMode — профиль маскировки: audio или video.
	ObfsMode string
	// Fingerprint — браузерный отпечаток TLS: chrome, safari, ios, android, firefox.
	Fingerprint string
	// ClientIDs — идентификаторы клиентов VK через запятую.
	ClientIDs string
	// VKAuthMode — способ получения TURN-кредов: vkcalls или legacy.
	VKAuthMode string
	// CaptchaMode — стратегия обхода капчи: auto, wv или rjs.
	CaptchaMode string
	// TurnHost и TurnPort переопределяют relay, выданный VK. Пусто — обычный режим.
	TurnHost string
	TurnPort string
	// CredentialsDir — где держать креды TURN между запусками. Пусто — только
	// в памяти, и перезапуск процесса снова идёт к VK.
	CredentialsDir string
	// DispatchChunk — пакетов подряд на один воркер. Ноль — по умолчанию.
	DispatchChunk int
}

const (
	// Предел воркеров тот же, что в оригинале: выше VK начинает отбраковывать
	// аллокации, и лишние воркеры только тратят память.
	maxWorkers = 108
	// Число попыток разобрать адрес пира. Имя может не резолвиться первые
	// секунды после подъёма туннеля, пока система не отдала DNS.
	peerResolveAttempts = 15
)

// Состояние единственного запущенного клиента.
//
// Запуск ровно один: расширение туннеля обслуживает одно соединение, а два
// набора воркеров всё равно передрались бы за аллокации одних и тех же
// звонков.
var (
	stateMu   sync.Mutex
	current   *client
	pauseFlag int32
)

type client struct {
	cancel     context.CancelFunc
	stats      *Stats
	listenPort string
	done       chan struct{}

	configMu   sync.Mutex
	wgConfig   string
	configCond chan struct{}
}

// Start поднимает клиент и возвращает локальный UDP-порт, на который нужно
// направить WireGuard.
//
// Возврат происходит сразу после привязки порта, не дожидаясь готовности
// аллокаций: WireGuard должен успеть подняться и начать слать рукопожатия,
// пока воркеры разбирают капчу и получают креды, иначе первые пакеты уходят в
// никуда и соединение стартует на несколько секунд позже.
func Start(cfg Config) (int, error) {
	stateMu.Lock()
	defer stateMu.Unlock()

	if current != nil {
		return 0, errors.New("vkturn: клиент уже запущен")
	}
	setCredentialsDirectory(cfg.CredentialsDir)

	peerAddr := strings.TrimSpace(cfg.PeerAddr)
	if peerAddr == "" {
		return 0, errors.New("vkturn: не задан адрес сервера")
	}
	if cfg.Password == "" {
		return 0, errors.New("vkturn: не задан пароль подключения")
	}

	hashes := ParseHashes(cfg.VKHashes)
	if len(hashes) == 0 {
		return 0, errors.New("vkturn: не задан ни один хеш звонка VK")
	}

	wrapKey, err := deriveWrapKey(cfg.Password)
	if err != nil {
		return 0, fmt.Errorf("vkturn: вывод WRAP-ключа: %w", err)
	}

	setVKAuthMode(cfg.VKAuthMode)
	setCaptchaMode(cfg.CaptchaMode)
	if cfg.Fingerprint != "" {
		SetActiveFingerprint(cfg.Fingerprint)
	}
	if cfg.ClientIDs != "" {
		SetActiveClientIds(cfg.ClientIDs)
	}

	setupGlobalResolver()

	ctx, cancel := context.WithCancel(context.Background())
	if !registerAbort(cancel) {
		cancel()
		return 0, errors.New("vkturn: запуск прерван остановкой")
	}

	peer, err := resolvePeer(ctx, peerAddr)
	if err != nil {
		cancel()
		return 0, err
	}

	// Порт всегда динамический. Фиксированный 9000 из Android-версии здесь не
	// нужен: адрес отдаётся вызывающему возвратом, а расширение туннеля могут
	// перезапустить быстрее, чем система освободит прежний порт.
	localConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		cancel()
		return 0, fmt.Errorf("vkturn: локальный порт: %w", err)
	}
	if uc, ok := localConn.(*net.UDPConn); ok {
		_ = uc.SetReadBuffer(socketBufSize)
		_ = uc.SetWriteBuffer(socketBufSize)
	}
	context.AfterFunc(ctx, func() { _ = localConn.Close() })

	_, localPort, _ := net.SplitHostPort(localConn.LocalAddr().String())
	port, err := net.LookupPort("udp", localPort)
	if err != nil {
		cancel()
		_ = localConn.Close()
		return 0, fmt.Errorf("vkturn: локальный порт: %w", err)
	}

	atomic.StoreInt32(&pauseFlag, 0)

	c := &client{
		cancel:     cancel,
		stats:      NewStats(),
		listenPort: localPort,
		done:       make(chan struct{}),
		configCond: make(chan struct{}),
	}
	current = c

	workers := normalizeWorkerCount(cfg.Workers)
	params := &TurnParams{
		Host:     cfg.TurnHost,
		Port:     cfg.TurnPort,
		Hashes:   hashes,
		WrapKey:  wrapKey,
		ObfsMode: cfg.ObfsMode,
	}

	log.Printf("[КЛИЕНТ] Воркеров: %d | Хешей: %d | Пир: %s | Порт: %s",
		workers, len(hashes), peerAddr, localPort)

	go c.run(ctx, localConn, params, peer, workers, cfg)

	return port, nil
}

// Прерывание запуска.
//
// Запуск держит stateMu, пока резолвит адрес сервера, а StartTunnel держит
// tunnelMu всё время ожидания конфигурации — до сорока пяти секунд. Остановка,
// встающая за этими замками в очередь, ждала бы вместе с запуском: на плохой
// сети человек нажимал «отключить» и смотрел на «Отключение…» полминуты.
// Поэтому сигнал отмены живёт отдельно от них и доставляется сразу.
//
// abortPending — на случай, если отмена пришла раньше, чем запуск успел
// завести свой контекст: тогда он отменяет себя сам, как только заведёт.
var (
	abortMu      sync.Mutex
	abortCancel  context.CancelFunc
	abortPending bool
)

// Abort прерывает идущий запуск, не дожидаясь его замков.
func Abort() {
	abortMu.Lock()
	cancel := abortCancel
	abortPending = true
	abortMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// clearAbort снимает отметку отмены, когда остановка закончена.
func clearAbort() {
	abortMu.Lock()
	abortCancel = nil
	abortPending = false
	abortMu.Unlock()
}

// registerAbort отдаёт контекст запуска под отмену. Возвращает false, если
// отмену уже попросили, — тогда запуск должен закончиться.
func registerAbort(cancel context.CancelFunc) bool {
	abortMu.Lock()
	defer abortMu.Unlock()
	if abortPending {
		return false
	}
	abortCancel = cancel
	return true
}

// Stop останавливает клиент и ждёт завершения воркеров.
//
// Ожидание обязательно: расширение туннеля после Stop сразу перенастраивает
// сеть, и недобитые воркеры продолжали бы слать пакеты в закрытый сокет.
func Stop() {
	stateMu.Lock()
	c := current
	current = nil
	stateMu.Unlock()

	if c == nil {
		return
	}
	c.cancel()

	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		log.Println("[КЛИЕНТ] Воркеры не завершились за 5 с, продолжаю остановку")
	}
}

// IsRunning сообщает, работает ли клиент.
func IsRunning() bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	return current != nil
}

// Pause и Resume приостанавливают отправку, не разрывая аллокации.
//
// Нужны при потере сети: держать аллокации дешевле, чем поднимать их заново
// после каждого переключения между Wi-Fi и сотовой сетью.
func Pause()  { atomic.StoreInt32(&pauseFlag, 1) }
func Resume() { atomic.StoreInt32(&pauseFlag, 0) }

// Statistics возвращает счётчики сессии.
func Statistics() (bytesUp, bytesDown int64, active int32) {
	stateMu.Lock()
	c := current
	stateMu.Unlock()
	if c == nil {
		return 0, 0, 0
	}
	return c.stats.TotalBytesUp.Load(), c.stats.TotalBytesDown.Load(), c.stats.ActiveConnections.Load()
}

// WireGuardConfig отдаёт конфигурацию, присланную сервером, дождавшись её
// появления.
//
// Сервер выдаёт её первой группе воркеров после установления аллокаций,
// поэтому сразу после Start конфигурации ещё нет. Пустая строка означает, что
// за отведённый срок сервер не ответил.
func WireGuardConfig(timeout time.Duration) string {
	stateMu.Lock()
	c := current
	stateMu.Unlock()
	if c == nil {
		return ""
	}

	c.configMu.Lock()
	config := c.wgConfig
	wait := c.configCond
	c.configMu.Unlock()

	if config != "" {
		return config
	}

	// Завершение воркеров — третий выход наравне с ответом и сроком. Без него
	// провал получения кредов (капча, отказ VK) оборачивался полным сроком
	// ожидания впустую: воркеров уже нет, конфигурации не будет, а телефон
	// все эти сорок пять секунд сидит без сети, потому что система уже
	// завернула трафик в неподнятый туннель. На устройстве так и было: воркеры
	// завершились сразу после отказа в кредах, а ожидание длилось до конца.
	select {
	case <-wait:
	case <-c.done:
	case <-time.After(timeout):
		return ""
	}

	c.configMu.Lock()
	defer c.configMu.Unlock()
	return c.wgConfig
}

// run повторяет последовательность запуска из оригинального main().
func (c *client) run(
	ctx context.Context,
	localConn net.PacketConn,
	params *TurnParams,
	peer *net.UDPAddr,
	workers int,
	cfg Config,
) {
	defer close(c.done)

	shutdown := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(shutdown)
	}()
	go c.stats.RunLoop(shutdown)

	disp := NewDispatcher(ctx, localConn, c.stats, cfg.DispatchChunk)
	defer disp.Shutdown()

	configCh := make(chan string, 1)
	configDone := make(chan struct{})
	go func() {
		defer close(configDone)
		select {
		case raw, ok := <-configCh:
			if !ok || raw == "" {
				return
			}
			c.storeConfig(withDefaultMTU(raw))
		case <-ctx.Done():
		}
	}()

	groups := workers / workersPerGroup
	var wg sync.WaitGroup
	workerID := 1

	// Группы поднимаются по цепочке: каждая следующая ждёт, пока предыдущая
	// получит креды и создаст аллокации. Одновременный старт всех групп VK
	// принимает за перебор и отвечает капчей на каждую.
	var prevCreds, prevSpawn <-chan struct{}

	for g := 0; g < groups; g++ {
		isFirst := g == 0

		var waitCreds, waitSpawn <-chan struct{}
		var signalCreds, signalSpawn chan<- struct{}

		if g > 0 {
			waitCreds, waitSpawn = prevCreds, prevSpawn
		}
		if g < groups-1 {
			creds := make(chan struct{})
			spawn := make(chan struct{})
			signalCreds, prevCreds = creds, creds
			signalSpawn, prevSpawn = spawn, spawn
		}

		ids := make([]int, workersPerGroup)
		for i := range ids {
			ids[i] = workerID
			workerID++
		}

		// Конфигурацию сервера запрашивает только первая группа: остальные
		// получили бы ту же самую и лишь нагрузили бы сервер.
		var configChan chan<- string
		if isFirst {
			configChan = configCh
		}

		wg.Add(1)
		go func(groupID, startHash int, first bool, cc chan<- string, ids []int,
			waitC, waitS <-chan struct{}, sigC, sigS chan<- struct{}) {
			defer wg.Done()
			WorkerGroup(ctx, groupID, startHash, params, peer, disp, c.listenPort,
				first, cc, ids, &pauseFlag, cfg.DeviceID, cfg.Password, c.stats,
				waitC, sigC, waitS, sigS)
		}(g+1, g, isFirst, configChan, ids, waitCreds, waitSpawn, signalCreds, signalSpawn)
	}

	wg.Wait()
	close(configCh)
	<-configDone
	log.Println("[КЛИЕНТ] Все воркеры завершены")
}

func (c *client) storeConfig(config string) {
	c.configMu.Lock()
	defer c.configMu.Unlock()
	if c.wgConfig != "" {
		return
	}
	c.wgConfig = config
	close(c.configCond)
}

// withDefaultMTU дописывает MTU, если сервер его не прислал.
//
// Инкапсуляция тройная — WireGuard внутри DTLS внутри TURN, — и при MTU по
// умолчанию пакеты фрагментируются, что TURN relay местами просто теряет.
func withDefaultMTU(config string) string {
	if strings.Contains(config, "MTU") {
		return config
	}
	lines := strings.Split(config, "\n")
	out := make([]string, 0, len(lines)+1)
	for _, line := range lines {
		out = append(out, line)
		if strings.TrimSpace(line) == "[Interface]" {
			out = append(out, "MTU = 1280")
		}
	}
	return strings.Join(out, "\n")
}

func normalizeWorkerCount(n int) int {
	if n > maxWorkers {
		n = maxWorkers
	}
	if n < workersPerGroup {
		n = workersPerGroup
	}
	return (n / workersPerGroup) * workersPerGroup
}

// resolveUDPAddr — то же, что net.ResolveUDPAddr, но с отменой.
//
// Сам net.ResolveUDPAddr контекста не знает и на плохой сети висит на DNS
// секундами. Запрос уходит в горутину, и отмена возвращает управление сразу;
// горутина доработает сама и ничего не держит.
func resolveUDPAddr(ctx context.Context, addr string) (*net.UDPAddr, error) {
	type answer struct {
		peer *net.UDPAddr
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		peer, err := net.ResolveUDPAddr("udp", addr)
		done <- answer{peer, err}
	}()
	select {
	case a := <-done:
		return a.peer, a.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func resolvePeer(ctx context.Context, addr string) (*net.UDPAddr, error) {
	var lastErr error
	for i := 0; i < peerResolveAttempts; i++ {
		peer, err := resolveUDPAddr(ctx, addr)
		if err == nil {
			return peer, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, fmt.Errorf("vkturn: разбор адреса пира %q: %w", addr, lastErr)
}
