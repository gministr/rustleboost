package core

import (
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fastTiming runs the watchdog in milliseconds.
func fastTiming() guardTiming {
	return guardTiming{
		tick: 5 * time.Millisecond, probeEvery: 10 * time.Millisecond, warmUp: 0,
		maxRecoveries: 3, recoveryWindow: time.Minute,
		failuresBeforeRestart: 2, maxRestartsWithoutProof: 3,
		pauseAfterUseless: time.Minute, sleepGap: time.Hour, afterResume: time.Millisecond,
	}
}

type fakeConn struct {
	singboxAlive, chainAlive, probeOK atomic.Bool
	restartsChain, restartsSingbox    atomic.Int32
	probes                            atomic.Int32
	gaveUp                            atomic.Value // string
	stopped                           atomic.Bool
	chainRestartErr                   atomic.Value // error
	clockSkew                         atomic.Int64
}

func newFake() *fakeConn {
	f := &fakeConn{}
	f.singboxAlive.Store(true)
	f.chainAlive.Store(true)
	f.probeOK.Store(true)
	return f
}

func (f *fakeConn) hooks(withChain, withProbe bool) guardHooks {
	h := guardHooks{
		stale:        func() bool { return f.stopped.Load() || f.gaveUp.Load() != nil },
		singboxAlive: f.singboxAlive.Load,
		restartSingbox: func() error {
			f.restartsSingbox.Add(1)
			f.singboxAlive.Store(true)
			return nil
		},
		giveUp: func(reason string) { f.gaveUp.Store(reason) },
		logf:   func(string, ...any) {},
		now:    func() time.Time { return time.Now().Add(time.Duration(f.clockSkew.Load())) },
		sleep:  time.Sleep,
	}
	if withChain {
		h.chainAlive = f.chainAlive.Load
		h.restartChain = func() error {
			f.restartsChain.Add(1)
			if err, _ := f.chainRestartErr.Load().(error); err != nil {
				return err
			}
			f.chainAlive.Store(true)
			return nil
		}
	}
	if withProbe {
		h.probe = func() bool { f.probes.Add(1); return f.probeOK.Load() }
	}
	return h
}

func runInBackground(t *testing.T, timing guardTiming, h guardHooks) (stop func()) {
	t.Helper()
	done := make(chan struct{})
	var stopped atomic.Bool
	orig := h.stale
	h.stale = func() bool { return stopped.Load() || orig() }
	go func() { runGuard(timing, h); close(done) }()
	return func() {
		stopped.Store(true)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("сторож не завершился")
		}
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("не дождался: %s", what)
}

func TestHealthyChannelIsLeftAlone(t *testing.T) {
	f := newFake()
	stop := runInBackground(t, fastTiming(), f.hooks(true, true))
	waitFor(t, "несколько проб", func() bool { return f.probes.Load() >= 4 })
	stop()
	if f.restartsChain.Load() != 0 || f.restartsSingbox.Load() != 0 || f.gaveUp.Load() != nil {
		t.Fatal("здоровый канал не должен трогаться")
	}
}

func TestDeadChainCoreIsRestartedInsteadOfDroppingTheConnection(t *testing.T) {
	f := newFake()
	f.chainAlive.Store(false)
	stop := runInBackground(t, fastTiming(), f.hooks(true, false))
	waitFor(t, "перезапуск прокси-ядра", func() bool { return f.restartsChain.Load() == 1 })
	time.Sleep(30 * time.Millisecond)
	stop()
	if f.gaveUp.Load() != nil {
		t.Fatalf("соединение снято, хотя ядро поднялось: %v", f.gaveUp.Load())
	}
}

func TestDeadSingboxIsRestartedToo(t *testing.T) {
	f := newFake()
	f.singboxAlive.Store(false)
	stop := runInBackground(t, fastTiming(), f.hooks(true, false))
	waitFor(t, "перезапуск sing-box", func() bool { return f.restartsSingbox.Load() == 1 })
	stop()
}

func TestGivesUpAfterRepeatedDeaths(t *testing.T) {
	f := newFake()
	h := f.hooks(true, false)
	// Ядро «поднимается», но тут же снова умирает.
	h.restartChain = func() error { f.restartsChain.Add(1); f.chainAlive.Store(false); return nil }
	f.chainAlive.Store(false)
	stop := runInBackground(t, fastTiming(), h)
	waitFor(t, "сдаться", func() bool { return f.gaveUp.Load() != nil })
	stop()
	if f.restartsChain.Load() != 3 {
		t.Fatalf("перезапусков %d, ожидалось ровно 3 до отказа", f.restartsChain.Load())
	}
	if !strings.Contains(f.gaveUp.Load().(string), "не помогает") {
		t.Fatalf("причина без объяснения: %v", f.gaveUp.Load())
	}
}

func TestFailedRestartIsRetriedUntilTheLimit(t *testing.T) {
	f := newFake()
	f.chainAlive.Store(false)
	f.chainRestartErr.Store(errors.New("порт занят"))
	stop := runInBackground(t, fastTiming(), f.hooks(true, false))
	waitFor(t, "сдаться", func() bool { return f.gaveUp.Load() != nil })
	stop()
	if f.restartsChain.Load() != 3 {
		t.Fatalf("попыток %d, ожидалось 3", f.restartsChain.Load())
	}
}

func TestEngineThatCannotBeRestartedIsReportedAsBefore(t *testing.T) {
	f := newFake()
	h := f.hooks(true, false)
	h.restartChain = nil // WDTT/WARP
	f.chainAlive.Store(false)
	stop := runInBackground(t, fastTiming(), h)
	waitFor(t, "отчёт о гибели", func() bool { return f.gaveUp.Load() != nil })
	stop()
}

func TestSilentChannelIsRestartedAfterTwoFailedProbes(t *testing.T) {
	f := newFake()
	f.probeOK.Store(false)
	h := f.hooks(true, true)
	orig := h.restartChain
	h.restartChain = func() error { f.probeOK.Store(true); return orig() }
	stop := runInBackground(t, fastTiming(), h)
	waitFor(t, "перезапуск по пробе", func() bool { return f.restartsChain.Load() == 1 })
	stop()
}

func TestOneFailedProbeIsNotEnough(t *testing.T) {
	f := newFake()
	var n atomic.Int32
	h := f.hooks(true, true)
	h.probe = func() bool { f.probes.Add(1); return n.Add(1) != 1 } // только первая не прошла
	stop := runInBackground(t, fastTiming(), h)
	waitFor(t, "пробы", func() bool { return f.probes.Load() >= 5 })
	stop()
	if f.restartsChain.Load() != 0 {
		t.Fatal("одиночная неудача не должна перезапускать ядро")
	}
}

// Проба, которая бьётся в недоступную цель, не должна превращать живой
// туннель в цикл перезапусков.
func TestUselessRestartsStopInsteadOfLooping(t *testing.T) {
	f := newFake()
	f.probeOK.Store(false) // проба не отвечает никогда, перезапуски не помогают
	stop := runInBackground(t, fastTiming(), f.hooks(true, true))
	waitFor(t, "три перезапуска", func() bool { return f.restartsChain.Load() >= 3 })
	time.Sleep(150 * time.Millisecond)
	stop()
	if got := f.restartsChain.Load(); got != 3 {
		t.Fatalf("перезапусков %d — после трёх бесполезных сторож должен остановиться", got)
	}
	if f.gaveUp.Load() != nil {
		t.Fatal("работающий туннель снимать нельзя")
	}
}

func TestNoProbeRestartWithoutARestartableChain(t *testing.T) {
	f := newFake()
	f.probeOK.Store(false)
	stop := runInBackground(t, fastTiming(), f.hooks(false, true))
	waitFor(t, "пробы", func() bool { return f.probes.Load() >= 6 })
	stop()
	if f.restartsSingbox.Load() != 0 || f.gaveUp.Load() != nil {
		t.Fatal("без перезапускаемого ядра проба только пишет в журнал")
	}
}

func TestWarmUpKeepsProbesSilent(t *testing.T) {
	f := newFake()
	f.probeOK.Store(false)
	timing := fastTiming()
	timing.warmUp = 300 * time.Millisecond
	stop := runInBackground(t, timing, f.hooks(true, true))
	time.Sleep(200 * time.Millisecond)
	stop()
	if f.probes.Load() != 0 {
		t.Fatalf("в прогреве проб быть не должно, было %d", f.probes.Load())
	}
}

func TestWakingFromSleepTriggersAnImmediateProbe(t *testing.T) {
	f := newFake()
	timing := fastTiming()
	timing.probeEvery = time.Hour // плановой пробы не будет
	timing.sleepGap = 20 * time.Millisecond
	timing.afterResume = time.Millisecond
	stop := runInBackground(t, timing, f.hooks(true, true))
	time.Sleep(30 * time.Millisecond)
	if f.probes.Load() != 0 {
		t.Fatal("без сна проб быть не должно")
	}
	f.clockSkew.Add(int64(time.Second)) // часы прыгнули: машина «спала»
	waitFor(t, "проба после пробуждения", func() bool { return f.probes.Load() >= 1 })
	stop()
}

// ── проба через SOCKS5 против настоящего (поддельного) сервера ─────────────

func fakeSocks(t *testing.T, behave func(net.Conn, string)) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 3)
				if _, err := io.ReadFull(c, buf); err != nil {
					return
				}
				c.Write([]byte{5, 0})
				head := make([]byte, 5)
				if _, err := io.ReadFull(c, head); err != nil {
					return
				}
				name := make([]byte, int(head[4]))
				io.ReadFull(c, name)
				io.ReadFull(c, make([]byte, 2))
				c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
				behave(c, string(name))
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func TestProbeAnswersMeanAlive(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	port := fakeSocks(t, func(c net.Conn, name string) {
		mu.Lock()
		asked = append(asked, name)
		mu.Unlock()
		buf := make([]byte, 1)
		c.Read(buf)
		c.Write([]byte("HTTP/1.1 204 No Content\r\n\r\n"))
	})
	if !livenessProbe(port, time.Second) {
		t.Fatal("ответивший сервер должен считаться живым")
	}
	if len(asked) != 1 || asked[0] != "www.gstatic.com" {
		t.Fatalf("спрашивали %v", asked)
	}
}

func TestProbeRedirectToCaptivePortalStillMeansAlive(t *testing.T) {
	port := fakeSocks(t, func(c net.Conn, _ string) {
		c.Read(make([]byte, 1))
		c.Write([]byte("HTTP/1.1 302 Found\r\n\r\n"))
	})
	if !livenessProbe(port, time.Second) {
		t.Fatal("перенаправление — тоже ответ")
	}
}

func TestProbeFallsBackToTheSecondTarget(t *testing.T) {
	port := fakeSocks(t, func(c net.Conn, name string) {
		c.Read(make([]byte, 1))
		if name == "cp.cloudflare.com" {
			c.Write([]byte("HTTP/1.1 204 No Content\r\n\r\n"))
		}
		// первую цель «режет сервер» — молчание
	})
	start := time.Now()
	if !livenessProbe(port, 2*time.Second) {
		t.Fatal("вторая цель отвечает — канал жив")
	}
	if time.Since(start) > 1500*time.Millisecond {
		t.Fatal("запасная цель должна получить свою очередь, а не остатки времени")
	}
}

func TestProbeSilenceMeansDeadWithinTheDeadline(t *testing.T) {
	port := fakeSocks(t, func(c net.Conn, _ string) { time.Sleep(3 * time.Second) })
	start := time.Now()
	if livenessProbe(port, 600*time.Millisecond) {
		t.Fatal("молчащий канал не жив")
	}
	if time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("проба не уложилась в срок: %s", time.Since(start))
	}
}

func TestProbeRefusedConnectIsDead(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	if livenessProbe(port, 500*time.Millisecond) {
		t.Fatal("закрытый порт — мёртвый канал")
	}
}

func TestProbeGarbageIsDead(t *testing.T) {
	port := fakeSocks(t, func(c net.Conn, _ string) {
		c.Read(make([]byte, 1))
		c.Write([]byte("not http at all!!"))
	})
	if livenessProbe(port, time.Second) {
		t.Fatal("мусор вместо HTTP — не ответ")
	}
}
