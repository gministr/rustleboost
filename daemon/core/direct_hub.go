package core

import (
	"errors"
	"os"
	"sync"
	"sync/atomic"

	"golang.zx2c4.com/wireguard/tun"
)

// Разводка адаптера прямого пути между сменяющимися сессиями ядра.
//
// Адаптер живёт всё подключение: его маршруты и DNS действуют, пока
// подключение не снято. Ядро же перезапускается — сторож поднимает упавшее,
// — и закрывает свой интерфейс при остановке. Отдай ему сам адаптер, и каждый
// перезапуск удалял бы его из системы: маршруты пропадали бы, и трафик на эти
// секунды шёл мимо VPN. Поэтому ядро получает сессию: её закрытие отпускает
// чтение и запись, а адаптер остаётся. Пакеты, пришедшие между сессиями,
// отбрасываются — как в выключенном туннеле, но наружу ничего не уходит.

// directQueue — пакетов в очереди к сессии. Ядро читает их сразу; очередь
// гасит только мгновенные всплески.
const directQueue = 1024

type directHub struct {
	dev     tun.Device
	mtu     int
	name    string
	current atomic.Pointer[directSession]
	closed  atomic.Bool
	done    chan struct{}

	// Байты через адаптер: вверх — от программ к серверу, вниз — обратно.
	up, down atomic.Int64
}

func newDirectHub(dev tun.Device, name string, mtu int) *directHub {
	h := &directHub{dev: dev, mtu: mtu, name: name, done: make(chan struct{})}
	go h.pump()
	return h
}

// pump — единственный читатель адаптера.
func (h *directHub) pump() {
	defer close(h.done)
	buf := make([]byte, 65535)
	bufs := [][]byte{buf}
	sizes := []int{0}
	for {
		n, err := h.dev.Read(bufs, sizes, 0)
		if err != nil {
			if h.closed.Load() || errors.Is(err, os.ErrClosed) {
				return
			}
			continue
		}
		if n == 0 || sizes[0] == 0 {
			continue
		}
		packet := append([]byte(nil), buf[:sizes[0]]...)
		h.up.Add(int64(len(packet)))
		if s := h.current.Load(); s != nil {
			s.deliver(packet)
		}
	}
}

// session заводит новую сессию и делает её текущей; прежняя закрывается.
func (h *directHub) session() *directSession {
	s := &directSession{
		hub:     h,
		packets: make(chan []byte, directQueue),
		done:    make(chan struct{}),
		events:  make(chan tun.Event, 1),
		wake:    make(chan struct{}, 1),
	}
	s.events <- tun.EventUp
	if old := h.current.Swap(s); old != nil {
		_ = old.Close()
	}
	return s
}

// close останавливает разводку и закрывает адаптер.
func (h *directHub) close() {
	if !h.closed.CompareAndSwap(false, true) {
		return
	}
	if s := h.current.Swap(nil); s != nil {
		_ = s.Close()
	}
	_ = h.dev.Close()
	<-h.done
}

// directSession — интерфейс для одного запуска ядра (tun.Device).
type directSession struct {
	hub       *directHub
	packets   chan []byte
	done      chan struct{}
	events    chan tun.Event
	// wake прерывает ожидающий Read (WakeRead): ядру надо отправить пакет
	// своего внутреннего стека — проверку канала, DNS, SOCKS. Без этого такой
	// пакет ждал, пока в адаптер придёт пакет какой-нибудь программы, и
	// проверка канала каждый раз висела все 15 секунд.
	wake      chan struct{}
	closeOnce sync.Once
}

func (s *directSession) deliver(packet []byte) {
	select {
	case s.packets <- packet:
	case <-s.done:
	default:
		// Ядро не успевает: пакет теряется, как на перегруженной линии, а
		// TCP повторит его сам.
	}
}

func (s *directSession) File() *os.File { return nil }

func (s *directSession) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	select {
	case packet := <-s.packets:
		sizes[0] = copy(bufs[0][offset:], packet)
		return 1, nil
	case <-s.wake:
		// Ядро (vkturn/wg nativeDevice) понимает этот ответ как «прервано
		// сроком» и забирает свой пакет из очереди.
		return 0, os.ErrDeadlineExceeded
	case <-s.done:
		return 0, os.ErrClosed
	}
}

// WakeRead прерывает Read — см. поле wake.
func (s *directSession) WakeRead() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *directSession) Write(bufs [][]byte, offset int) (int, error) {
	select {
	case <-s.done:
		return 0, os.ErrClosed
	default:
	}
	if s.hub.closed.Load() {
		return 0, os.ErrClosed
	}
	for _, b := range bufs {
		if len(b) > offset {
			s.hub.down.Add(int64(len(b) - offset))
		}
	}
	return s.hub.dev.Write(bufs, offset)
}

func (s *directSession) MTU() (int, error)        { return s.hub.mtu, nil }
func (s *directSession) Name() (string, error)    { return s.hub.name, nil }
func (s *directSession) Events() <-chan tun.Event { return s.events }
func (s *directSession) BatchSize() int           { return 1 }

// Close отпускает ядро; адаптер остаётся.
func (s *directSession) Close() error {
	s.closeOnce.Do(func() {
		close(s.done)
		close(s.events)
		s.hub.current.CompareAndSwap(s, nil)
	})
	return nil
}
