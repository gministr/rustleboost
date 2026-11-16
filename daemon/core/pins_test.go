package core

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeRoutes struct {
	gw      pinGateway
	up      bool
	table   map[netip.Addr]pinGateway
	foreign map[netip.Addr]bool
	fail    error
	adds    int
}

func newFakeRoutes() *fakeRoutes {
	return &fakeRoutes{
		gw:      pinGateway{Iface: 7, NextHop: netip.MustParseAddr("192.168.1.1")},
		up:      true,
		table:   map[netip.Addr]pinGateway{},
		foreign: map[netip.Addr]bool{},
	}
}

func (f *fakeRoutes) gateway() (pinGateway, bool) { return f.gw, f.up }

func (f *fakeRoutes) add(ip netip.Addr, gw pinGateway) error {
	if f.fail != nil {
		return f.fail
	}
	if f.foreign[ip] {
		return errPinExists
	}
	f.adds++
	f.table[ip] = gw
	return nil
}

func (f *fakeRoutes) remove(ip netip.Addr, gw pinGateway) error {
	if f.table[ip] == gw {
		delete(f.table, ip)
	}
	return nil
}

func testPinner(t *testing.T, routes *fakeRoutes) (*pinner, *time.Time) {
	t.Helper()
	journal := filepath.Join(t.TempDir(), "pins.json")
	p := newPinner(routes, journal, []netip.Prefix{netip.MustParsePrefix("1.1.1.1/32")})
	now := time.Unix(1_000_000, 0)
	p.now = func() time.Time { return now }
	p.logf = func(string, ...any) {}
	return p, &now
}

func TestPinnerPinsRelaysOnce(t *testing.T) {
	routes := newFakeRoutes()
	p, _ := testPinner(t, routes)

	for i := 0; i < 3; i++ {
		if err := p.hook("udp4", "155.212.1.10:3478"); err != nil {
			t.Fatal(err)
		}
	}
	if routes.adds != 1 || p.count() != 1 {
		t.Fatalf("адрес закреплён %d раз, учтено %d — нужен один маршрут", routes.adds, p.count())
	}
	if routes.table[netip.MustParseAddr("155.212.1.10")] != routes.gw {
		t.Fatal("маршрут ведёт не через физический шлюз")
	}
}

func TestPinnerSkipsLocalTunnelDNSAndIPv6(t *testing.T) {
	routes := newFakeRoutes()
	p, _ := testPinner(t, routes)
	for _, address := range []string{
		"127.0.0.1:20000",    // петля
		"192.168.1.1:53",     // роутер: своя сеть
		"10.8.0.1:443",       // частная сеть
		"1.1.1.1:53",         // DNS туннеля
		"[2a00:1450::1]:443", // IPv6 в туннель не направлен
		"vk.com:443",         // не адрес — не наше дело
	} {
		if err := p.hook("tcp", address); err != nil {
			t.Fatalf("%s: %v", address, err)
		}
	}
	if routes.adds != 0 {
		t.Fatalf("закреплено %d адресов, которые закреплять нельзя", routes.adds)
	}
}

func TestPinnerLeavesForeignRoutes(t *testing.T) {
	routes := newFakeRoutes()
	p, _ := testPinner(t, routes)
	ip := netip.MustParseAddr("87.240.1.1")
	routes.foreign[ip] = true
	if err := p.ensure(ip); err != nil {
		t.Fatal(err)
	}
	if p.count() != 0 {
		t.Fatal("чужой маршрут записан как свой: при отключении его бы удалили")
	}
}

func TestPinnerRefusesDialWhenRouteFails(t *testing.T) {
	routes := newFakeRoutes()
	routes.fail = errors.New("доступ запрещён")
	p, _ := testPinner(t, routes)
	if err := p.hook("udp4", "95.163.1.1:3478"); err == nil {
		t.Fatal("маршрут не встал, а соединение разрешено: оно ушло бы в собственный туннель")
	}
}

func TestPinnerFollowsNetworkChange(t *testing.T) {
	routes := newFakeRoutes()
	p, now := testPinner(t, routes)
	a, b := netip.MustParseAddr("91.231.1.1"), netip.MustParseAddr("90.156.2.2")
	_ = p.ensure(a)
	_ = p.ensure(b)

	cable := pinGateway{Iface: 12, NextHop: netip.MustParseAddr("10.0.0.1")}
	routes.gw = cable
	*now = now.Add(time.Second) // кэш шлюза ещё действует: follow обязан спросить заново
	p.follow()

	for _, ip := range []netip.Addr{a, b} {
		if routes.table[ip] != cable {
			t.Fatalf("%s остался на старом шлюзе после смены сети", ip)
		}
	}
	if p.count() != 2 {
		t.Fatalf("учтено %d маршрутов, ожидалось 2", p.count())
	}
}

func TestPinnerKeepsRoutesWithoutNetwork(t *testing.T) {
	routes := newFakeRoutes()
	p, _ := testPinner(t, routes)
	ip := netip.MustParseAddr("91.231.1.1")
	_ = p.ensure(ip)
	routes.up = false
	p.follow()
	if _, ok := routes.table[ip]; !ok || p.count() != 1 {
		t.Fatal("без сети маршрут снят: вернётся та же сеть — закреплять заново")
	}
}

func TestPinnerClearAndCrashCleanup(t *testing.T) {
	routes := newFakeRoutes()
	p, _ := testPinner(t, routes)
	_ = p.ensure(netip.MustParseAddr("91.231.1.1"))
	_ = p.ensure(netip.MustParseAddr("95.163.1.1"))

	// Демон упал: журнал остался. Следующий запуск убирает его маршруты.
	if _, err := os.Stat(p.journal); err != nil {
		t.Fatal("журнал маршрутов не записан")
	}
	if n := cleanupPins(routes, p.journal); n != 2 || len(routes.table) != 0 {
		t.Fatalf("после сбоя убрано %d маршрутов, осталось %d", n, len(routes.table))
	}

	_ = p.ensure(netip.MustParseAddr("87.240.2.2"))
	p.clear()
	if len(routes.table) != 0 || p.count() != 0 {
		t.Fatal("отключение оставило свои маршруты")
	}
	if _, err := os.Stat(p.journal); !os.IsNotExist(err) {
		t.Fatal("журнал не удалён после отключения")
	}
}
