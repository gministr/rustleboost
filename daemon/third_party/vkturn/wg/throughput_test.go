package wg

import (
	"context"
	"io"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// Стенд пропускной способности: та же цепочка, что в телефоне, но без VK.
//
//	клиент (TCP на петле, как tun2socks) → relay() → gVisor A ⇄ gVisor B → сервер
//
// Два настоящих сетевых стека gVisor соединены напрямую: пакеты из одного
// переписываются в другой. Так замеряется ровно то, что добавляет наш код, —
// копирование, сроки, обёртки, — без потерь и задержек реального релея.

const benchMTU = 1280

func pumpPackets(from, to tun.Device, stop <-chan struct{}) {
	batch := from.BatchSize()
	bufs := make([][]byte, batch)
	for i := range bufs {
		bufs[i] = make([]byte, benchMTU+100)
	}
	sizes := make([]int, batch)
	for {
		select {
		case <-stop:
			return
		default:
		}
		n, err := from.Read(bufs, sizes, 0)
		if err != nil {
			return
		}
		out := make([][]byte, n)
		for i := 0; i < n; i++ {
			out[i] = bufs[i][:sizes[i]]
		}
		if _, err := to.Write(out, 0); err != nil {
			return
		}
	}
}

type benchPath struct {
	a, b     *netstack.Net
	server   netip.AddrPort
	shutdown func()
}

// newBenchPath поднимает два стека и сервер-источник данных на стороне B.
func newBenchPath(tb testing.TB) *benchPath {
	tb.Helper()
	addrA := netip.MustParseAddr("10.9.0.1")
	addrB := netip.MustParseAddr("10.9.0.2")
	tunA, netA, err := netstack.CreateNetTUN([]netip.Addr{addrA}, nil, benchMTU)
	if err != nil {
		tb.Fatal(err)
	}
	tunB, netB, err := netstack.CreateNetTUN([]netip.Addr{addrB}, nil, benchMTU)
	if err != nil {
		tb.Fatal(err)
	}
	stop := make(chan struct{})
	go pumpPackets(tunA, tunB, stop)
	go pumpPackets(tunB, tunA, stop)

	server := netip.AddrPortFrom(addrB, 9000)
	listener, err := netB.ListenTCPAddrPort(server)
	if err != nil {
		tb.Fatal(err)
	}
	go func() {
		chunk := make([]byte, 64*1024)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				for {
					if _, err := conn.Write(chunk); err != nil {
						return
					}
				}
			}()
		}
	}()

	return &benchPath{
		a: netA, b: netB, server: server,
		// Устройства не закрываются: Close ждёт блокировку, которую держит
		// насос пакетов, застрявший в Read, и остановка стенда висела
		// до таймаута теста. Процесс теста и так скоро завершится.
		shutdown: func() {
			close(stop)
			listener.Close()
		},
	}
}

// measure пропускает поток через relay() и возвращает Мбит/с.
func (p *benchPath) measure(tb testing.TB, bytes int64) float64 {
	tb.Helper()
	front, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	defer front.Close()

	go func() {
		client, err := front.Accept()
		if err != nil {
			return
		}
		defer client.Close()
		relay(client, nil, func() (net.Conn, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return p.a.DialContextTCPAddrPort(ctx, p.server)
		})
	}()

	conn, err := net.Dial("tcp", front.Addr().String())
	if err != nil {
		tb.Fatal(err)
	}
	defer conn.Close()

	started := time.Now()
	read, err := io.CopyN(io.Discard, conn, bytes)
	if err != nil {
		tb.Fatalf("прочитано %d из %d: %v", read, bytes, err)
	}
	return float64(read) * 8 / time.Since(started).Seconds() / 1e6
}

// TestRelayThroughputBench печатает пропускную способность relay() на
// стенде. Не проверка, а замер: запускается явно, через -run.
func TestRelayThroughputBench(t *testing.T) {
	if os.Getenv("RB_BENCH") == "" {
		t.Skip("замер: запускается с RB_BENCH=1")
	}
	path := newBenchPath(t)
	defer path.shutdown()

	path.measure(t, 8<<20) // прогрев
	for i := 0; i < 3; i++ {
		t.Logf("relay: %.0f Мбит/с", path.measure(t, 64<<20))
	}
}
