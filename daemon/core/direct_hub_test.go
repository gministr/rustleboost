package core

import (
	"os"
	"sync"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/tun"
)

// fakeAdapter — адаптер в памяти: in — пакеты «от программ», out — записанные ядром.
type fakeAdapter struct {
	in     chan []byte
	out    chan []byte
	closed chan struct{}
	once   sync.Once
}

func newFakeAdapter() *fakeAdapter {
	return &fakeAdapter{in: make(chan []byte, 16), out: make(chan []byte, 16), closed: make(chan struct{})}
}

func (f *fakeAdapter) File() *os.File { return nil }
func (f *fakeAdapter) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	select {
	case p := <-f.in:
		sizes[0] = copy(bufs[0][offset:], p)
		return 1, nil
	case <-f.closed:
		return 0, os.ErrClosed
	}
}
func (f *fakeAdapter) Write(bufs [][]byte, offset int) (int, error) {
	for _, b := range bufs {
		f.out <- append([]byte(nil), b[offset:]...)
	}
	return len(bufs), nil
}
func (f *fakeAdapter) MTU() (int, error)        { return 1280, nil }
func (f *fakeAdapter) Name() (string, error)    { return "fake", nil }
func (f *fakeAdapter) Events() <-chan tun.Event { return nil }
func (f *fakeAdapter) BatchSize() int           { return 1 }
func (f *fakeAdapter) Close() error             { f.once.Do(func() { close(f.closed) }); return nil }

func readOne(t *testing.T, dev tun.Device) []byte {
	t.Helper()
	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 2000)
		sizes := []int{0}
		if _, err := dev.Read([][]byte{buf}, sizes, 16); err == nil {
			got <- append([]byte(nil), buf[16:16+sizes[0]]...)
		}
	}()
	select {
	case p := <-got:
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("пакет не дошёл до ядра")
		return nil
	}
}

func TestDirectHubSurvivesCoreRestart(t *testing.T) {
	adapter := newFakeAdapter()
	hub := newDirectHub(adapter, "RustleBoost", 1280)
	defer hub.close()

	first := hub.session()
	if ev := <-first.Events(); ev != tun.EventUp {
		t.Fatal("сессия не сообщила, что интерфейс поднят")
	}
	adapter.in <- []byte("one")
	if string(readOne(t, first)) != "one" {
		t.Fatal("пакет искажён")
	}

	// Ядро остановилось: закрытие отпускает его чтение, адаптер жив.
	done := make(chan error, 1)
	go func() {
		_, err := first.Read([][]byte{make([]byte, 100)}, []int{0}, 0)
		done <- err
	}()
	_ = first.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("чтение закрытой сессии вернуло пакет")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("закрытие сессии не отпустило чтение: остановка ядра зависла бы")
	}
	select {
	case <-adapter.closed:
		t.Fatal("остановка ядра закрыла адаптер: маршруты пропали бы")
	default:
	}

	// Перезапуск ядра: новая сессия получает пакеты того же адаптера.
	second := hub.session()
	adapter.in <- []byte("two")
	if string(readOne(t, second)) != "two" {
		t.Fatal("после перезапуска ядра пакеты не идут")
	}
	if _, err := second.Write([][]byte{append(make([]byte, 16), []byte("reply")...)}, 16); err != nil {
		t.Fatal(err)
	}
	if string(<-adapter.out) != "reply" {
		t.Fatal("ответ не записан в адаптер")
	}
	if _, err := first.Write([][]byte{[]byte("late")}, 0); err == nil {
		t.Fatal("закрытая сессия пишет в адаптер")
	}
	if hub.up.Load() != 6 || hub.down.Load() != 5 {
		t.Fatalf("счётчики: вверх %d, вниз %d", hub.up.Load(), hub.down.Load())
	}
}

func TestDirectHubNewSessionReplacesOld(t *testing.T) {
	adapter := newFakeAdapter()
	hub := newDirectHub(adapter, "RustleBoost", 1280)
	old := hub.session()
	_ = hub.session()
	if _, err := old.Read([][]byte{make([]byte, 10)}, []int{0}, 0); err == nil {
		t.Fatal("прежняя сессия осталась открытой рядом с новой")
	}
	hub.close()
	select {
	case <-adapter.closed:
	default:
		t.Fatal("отключение не закрыло адаптер")
	}
}
