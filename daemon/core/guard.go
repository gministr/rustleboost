package core

import (
	"fmt"
	"time"
)

// guardTiming holds every interval of the watchdog in one place so tests can
// run it in milliseconds instead of seconds.
type guardTiming struct {
	tick                    time.Duration // how often process liveness is checked
	probeEvery              time.Duration // how often the channel itself is tested
	warmUp                  time.Duration // silence after a (re)start while the channel comes up
	maxRecoveries           int           // dead-core restarts allowed within recoveryWindow
	recoveryWindow          time.Duration
	failuresBeforeRestart   int           // consecutive failed probes that condemn the channel
	maxRestartsWithoutProof int           // probe-driven restarts in a row with no answer between
	pauseAfterUseless       time.Duration // how long probes stop restarting once that is hit
	sleepGap                time.Duration // a tick this late means the machine slept
	afterResume             time.Duration // how soon to probe after waking
}

func defaultGuardTiming() guardTiming {
	return guardTiming{
		tick:                    2 * time.Second,
		probeEvery:              20 * time.Second,
		warmUp:                  12 * time.Second,
		maxRecoveries:           3,
		recoveryWindow:          2 * time.Minute,
		failuresBeforeRestart:   2,
		maxRestartsWithoutProof: 3,
		pauseAfterUseless:       10 * time.Minute,
		sleepGap:                10 * time.Second,
		afterResume:             4 * time.Second,
	}
}

// guardHooks is everything the watchdog needs from the connection it watches.
type guardHooks struct {
	// stale reports that this connection is over; the watchdog then exits.
	stale func() bool

	singboxAlive func() bool
	// chainAlive is nil for a server sing-box carries on its own.
	chainAlive func() bool

	// probe tests the proxy leg end to end. nil disables probing.
	probe func() bool

	restartSingbox func() error
	// restartChain is nil for engines that cannot be restarted safely; a dead
	// one is then reported rather than revived, as before.
	restartChain func() error

	// giveUp ends the connection with an honest reason.
	giveUp func(reason string)

	logf  func(format string, args ...any)
	now   func() time.Time
	sleep func(time.Duration)
}

// runGuard supervises one connection until it ends.
//
// Before, a core that died took the whole connection down at once, and a
// channel that was alive but no longer carried traffic was never noticed at
// all: the app stayed green over a dead tunnel. Now a dead core is restarted a
// few times before giving up, the channel is probed with a real request, and
// waking from sleep triggers an immediate check.
//
// The restarts are bounded on purpose. A probe that fails because its target
// is unreachable — not because the tunnel is broken — would otherwise restart
// a healthy core forever and tear down every connection it carries.
func runGuard(t guardTiming, h guardHooks) {
	var (
		lastTick      = h.now()
		nextProbe     = h.now().Add(t.probeEvery)
		warmUntil     = h.now().Add(t.warmUp)
		pausedUntil   time.Time
		failures      int
		useless       int // probe-driven restarts since the last answered probe
		recoveryTimes []time.Time
	)

	// recoverDead restarts a core that stopped on its own.
	recoverDead := func(what string, restart func() error) bool {
		if restart == nil {
			h.giveUp(what + " остановился")
			return false
		}
		now := h.now()
		kept := recoveryTimes[:0]
		for _, at := range recoveryTimes {
			if now.Sub(at) < t.recoveryWindow {
				kept = append(kept, at)
			}
		}
		recoveryTimes = kept
		if len(recoveryTimes) >= t.maxRecoveries {
			h.giveUp(fmt.Sprintf("%s останавливался %d раза подряд, перезапуск не помогает", what, len(recoveryTimes)))
			return false
		}
		recoveryTimes = append(recoveryTimes, now)
		h.logf("[guard] %s остановился — перезапускаю (%d из %d)", what, len(recoveryTimes), t.maxRecoveries)
		if err := restart(); err != nil {
			h.logf("[guard] перезапуск не удался: %v", err)
			return true // the next tick tries again, until the limit
		}
		warmUntil = h.now().Add(t.warmUp)
		failures = 0
		return true
	}

	for {
		h.sleep(t.tick)
		if h.stale() {
			return
		}
		now := h.now()

		// A tick that came far too late means the machine was asleep. The
		// network behind the cores has changed under them, so check at once.
		if gap := now.Sub(lastTick); gap > t.sleepGap {
			h.logf("[guard] пробуждение после сна (%s) — проверяю канал", gap.Round(time.Second))
			failures = 0
			warmUntil = now
			nextProbe = now.Add(t.afterResume)
		}
		lastTick = now

		if !h.singboxAlive() {
			if !recoverDead("sing-box", h.restartSingbox) {
				return
			}
			continue
		}
		if h.chainAlive != nil && !h.chainAlive() {
			if !recoverDead("прокси-ядро", h.restartChain) {
				return
			}
			continue
		}

		if h.probe == nil || now.Before(nextProbe) || now.Before(warmUntil) || now.Before(pausedUntil) {
			continue
		}
		nextProbe = now.Add(t.probeEvery)

		if h.probe() {
			if failures > 0 {
				h.logf("[guard] канал снова отвечает")
			}
			failures, useless = 0, 0
			continue
		}
		failures++
		h.logf("[guard] проба не прошла (%d подряд)", failures)
		if failures < t.failuresBeforeRestart {
			continue
		}
		failures = 0

		if h.restartChain == nil {
			continue // nothing safe to restart; the failure is in the log
		}
		if useless >= t.maxRestartsWithoutProof {
			pausedUntil = h.now().Add(t.pauseAfterUseless)
			useless = 0
			h.logf("[guard] перезапуски не помогают (проба молчит после %d подряд) — "+
				"считаю, что дело в самой пробе, и %s по ней не перезапускаю",
				t.maxRestartsWithoutProof, t.pauseAfterUseless.Round(time.Minute))
			continue
		}
		useless++
		h.logf("[guard] канал не отвечает — перезапускаю прокси-ядро")
		if err := h.restartChain(); err != nil {
			h.logf("[guard] перезапуск не удался: %v", err)
			continue
		}
		warmUntil = h.now().Add(t.warmUp)
	}
}
