package core

// Закрепление адресов транспорта за физическим шлюзом.
//
// На прямом пути маршрут по умолчанию уходит в адаптер туннеля для всего
// компьютера, включая сам демон. Его собственные соединения — релеи ВК, API ВК
// за учётками, решатель капчи, регистрация WARP, прямой выход по правилам —
// ушли бы в туннель, который они же и несут. На iOS и Android система выпускает
// сокеты расширения мимо туннеля сама; на компьютере так не бывает.
//
// Решение то же, что в консольном клиенте авторов транспорта SRTP
// (tools/vk-turn-proxy-console, pins.go): перед первым пакетом каждого
// соединения ядро спрашивает демон (vkturn.SetDialHook), и демон ставит
// маршрут /32 через физический шлюз. Закрепление именно в момент соединения,
// потому что заранее список не составить: DNS ВК меняет адреса, учётки и капча
// ходят на десятки хостов. Раньше это делало правило sing-box «процесс
// daemon.exe — напрямую», а сопоставление соединения с процессом на Windows
// срабатывает не всегда, особенно для UDP: промах отправлял транспорт в
// собственный туннель, и связь пропадала.
//
// Не закрепляются: петля, локальные и частные адреса (до них и так ведёт
// маршрут своей сети), адрес самого туннеля и DNS туннеля — иначе мимо туннеля
// пошёл бы DNS всех программ. IPv6 не трогается: в туннель он не направлен.
// Маршрут, существовавший до нас, остаётся как был: убираем только свои.

import (
	"encoding/json"
	"errors"
	"log"
	"net/netip"
	"os"
	"sync"
	"time"
)

// pinGateway — физический шлюз: интерфейс и следующий узел.
type pinGateway struct {
	Iface   uint64     `json:"iface"`
	NextHop netip.Addr `json:"nextHop"`
}

// errPinExists — маршрут к адресу уже есть, и он не наш.
var errPinExists = errors.New("маршрут уже есть")

// pinRoutes — таблица маршрутов системы (Windows: winipcfg; тесты: подделка).
type pinRoutes interface {
	// gateway — текущий физический маршрут по умолчанию; false — сети нет.
	gateway() (pinGateway, bool)
	add(ip netip.Addr, gw pinGateway) error
	remove(ip netip.Addr, gw pinGateway) error
}

// gatewayTTL — как долго верим найденному шлюзу без нового запроса таблицы.
const gatewayTTL = 2 * time.Second

type pinner struct {
	mu      sync.Mutex
	routes  pinRoutes
	skip    []netip.Prefix
	pins    map[netip.Addr]pinGateway // поставленные нами
	gw      pinGateway
	gwOK    bool
	gwAt    time.Time
	journal string // файл со списком наших маршрутов: убрать после сбоя
	now     func() time.Time
	logf    func(string, ...any)
}

func newPinner(routes pinRoutes, journal string, skip []netip.Prefix) *pinner {
	base := []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("224.0.0.0/3"),
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("100.64.0.0/10"),
	}
	return &pinner{
		routes:  routes,
		skip:    append(base, skip...),
		pins:    make(map[netip.Addr]pinGateway),
		journal: journal,
		now:     time.Now,
		logf:    log.Printf,
	}
}

// hook — функция для vkturn.SetDialHook: адрес вида «ip:порт».
func (p *pinner) hook(_ string, address string) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return nil
	}
	return p.ensure(ap.Addr())
}

func (p *pinner) skipped(ip netip.Addr) bool {
	if !ip.Is4() {
		return true
	}
	for _, prefix := range p.skip {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// ensure закрепляет адрес, если нужно. Ошибка возвращается соединению: лучше
// не соединиться, чем уйти в собственный туннель.
func (p *pinner) ensure(ip netip.Addr) error {
	ip = ip.Unmap()
	if p.skipped(ip) {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	gw, ok := p.gatewayLocked()
	if !ok {
		// Сети нет: соединение не состоится и так, а маршрут ставить некуда.
		return nil
	}
	if current, pinned := p.pins[ip]; pinned {
		if current == gw {
			return nil
		}
		_ = p.routes.remove(ip, current)
		delete(p.pins, ip)
	}
	switch err := p.routes.add(ip, gw); {
	case errors.Is(err, errPinExists):
		return nil
	case err != nil:
		return err
	}
	p.pins[ip] = gw
	p.saveLocked()
	return nil
}

func (p *pinner) gatewayLocked() (pinGateway, bool) {
	if !p.gwAt.IsZero() && p.now().Sub(p.gwAt) < gatewayTTL {
		return p.gw, p.gwOK
	}
	p.gw, p.gwOK = p.routes.gateway()
	p.gwAt = p.now()
	return p.gw, p.gwOK
}

// follow переводит закреплённые адреса на новый шлюз после смены сети
// (Wi-Fi на кабель, другой роутер). Без сети маршруты остаются как были:
// вернётся та же сеть — они снова верны.
func (p *pinner) follow() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gwAt = time.Time{}
	gw, ok := p.gatewayLocked()
	if !ok {
		return
	}
	moved := 0
	for ip, current := range p.pins {
		if current == gw {
			continue
		}
		_ = p.routes.remove(ip, current)
		delete(p.pins, ip)
		if err := p.routes.add(ip, gw); err == nil {
			p.pins[ip] = gw
			moved++
		}
	}
	if moved > 0 {
		p.logf("[pins] сеть сменилась: %d адрес(ов) переведены на шлюз %s", moved, gw.NextHop)
		p.saveLocked()
	}
}

func (p *pinner) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pins)
}

// clear убирает все свои маршруты.
func (p *pinner) clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for ip, gw := range p.pins {
		_ = p.routes.remove(ip, gw)
	}
	p.pins = make(map[netip.Addr]pinGateway)
	if p.journal != "" {
		_ = os.Remove(p.journal)
	}
}

type pinRecord struct {
	IP netip.Addr `json:"ip"`
	pinGateway
}

func (p *pinner) saveLocked() {
	if p.journal == "" {
		return
	}
	records := make([]pinRecord, 0, len(p.pins))
	for ip, gw := range p.pins {
		records = append(records, pinRecord{IP: ip, pinGateway: gw})
	}
	data, err := json.Marshal(records)
	if err != nil {
		return
	}
	tmp := p.journal + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, p.journal)
	}
}

// cleanupPins убирает маршруты, оставшиеся после аварийного завершения демона.
// Они безвредны — ведут туда же, куда и маршрут по умолчанию, — но копились бы.
func cleanupPins(routes pinRoutes, journal string) int {
	data, err := os.ReadFile(journal)
	if err != nil {
		return 0
	}
	var records []pinRecord
	_ = json.Unmarshal(data, &records)
	for _, r := range records {
		_ = routes.remove(r.IP, r.pinGateway)
	}
	_ = os.Remove(journal)
	return len(records)
}
