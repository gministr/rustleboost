package proxy

// Подмена релея, который не принимает свои же креды.
//
// 03.11.2026 ВК начал выдавать в кредах адреса нового кластера релеев
// (155.212.x.x), и этот кластер отвечает на Allocate 401 Unauthorized —
// на свежие креды, с любого адреса, по UDP и по TCP. Старые релеи ВК
// (91.231.135.x, 193.203.43.x, 90.156.23x.x, 95.163.34.x) те же самые
// креды принимают: секрет у кластеров общий, сломан только новый.
//
// Поэтому сразу после выдачи кредов один раз пробуем Allocate на адресе от
// ВК. Принял — список не трогаем, и когда ВК починит кластер, всё пойдёт
// как раньше. Ответил 401 — запоминаем адрес как негодный и подставляем
// старые релеи, проверив хотя бы один. Сетевые ошибки ничего не меняют:
// по ним не отличить сломанный релей от плохой сети у пользователя.

import (
	"errors"
	"log"
	"math/rand/v2"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pion/turn/v5"
)

// FallbackRelays — релеи ВК, через которые неделю шёл трафик пользователей
// и которые 03.11.2026 принимали свежие креды.
var FallbackRelays = []string{
	"91.231.135.85:19302",
	"90.156.234.249:19302",
	"193.203.43.31:19302",
	"91.231.135.137:19302",
	"91.231.135.176:19302",
	"90.156.236.96:19302",
	"91.231.135.183:19302",
	"193.203.43.19:19302",
	"95.163.34.133:19302",
	"91.231.135.90:19302",
	"95.163.34.185:19302",
	"91.231.135.169:19302",
	"90.156.236.80:19302",
	"91.231.135.91:19302",
	"95.163.34.166:19302",
	"91.231.135.168:19302",
}

const (
	relayProbeTimeout = 4 * time.Second
	relayVerdictTTL   = 20 * time.Minute
	// Сколько запасных релеев отдавать: у каждого свой предел аллокаций на
	// одни креды, так что воркеры расходятся по нескольким.
	relayFallbackCount = 4
)

var (
	relayMu      sync.Mutex
	relayBad     = map[string]time.Time{} // хост → когда ответил 401
	relayGood    = map[string]time.Time{} // хост → когда принял креды
	errProbeTime = errors.New("релей не ответил")
)

func relayHost(addr string) string {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return h
}

func relayVerdict(addr string) (bad, good bool) {
	relayMu.Lock()
	defer relayMu.Unlock()
	h := relayHost(addr)
	now := time.Now()
	if t, ok := relayBad[h]; ok && now.Sub(t) < relayVerdictTTL {
		bad = true
	}
	if t, ok := relayGood[h]; ok && now.Sub(t) < relayVerdictTTL {
		good = true
	}
	return
}

func markRelay(addr string, ok bool) {
	relayMu.Lock()
	defer relayMu.Unlock()
	h := relayHost(addr)
	if ok {
		relayGood[h] = time.Now()
		delete(relayBad, h)
	} else {
		relayBad[h] = time.Now()
		delete(relayGood, h)
	}
}

// MarkRelayRejected запоминает, что релей ответил на Allocate 401, — чтобы
// следующие креды сразу шли на запасные.
func MarkRelayRejected(addr string) { markRelay(addr, false) }

// probeRelay делает одну аллокацию и сразу её освобождает.
func probeRelay(addr, user, pass string) error {
	raddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}
	c, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		return err
	}
	cl, err := turn.NewClient(&turn.ClientConfig{
		STUNServerAddr: raddr.String(),
		TURNServerAddr: raddr.String(),
		Conn:           c,
		Username:       user,
		Password:       pass,
	})
	if err != nil {
		c.Close()
		return err
	}
	if err := cl.Listen(); err != nil {
		cl.Close()
		c.Close()
		return err
	}
	done := make(chan error, 1)
	go func() {
		relay, err := cl.Allocate()
		if err == nil {
			relay.Close()
		}
		done <- err
	}()
	var res error
	select {
	case res = <-done:
	case <-time.After(relayProbeTimeout):
		res = errProbeTime
	}
	cl.Close()
	c.Close()
	return res
}

func isRelayAuthReject(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "error 401") || strings.Contains(s, "Unauthorized")
}

// HealRelays возвращает адреса, на которых креды стоит использовать: те же,
// что дал ВК, если релей их принимает, иначе — проверенные запасные.
func HealRelays(user, pass string, addrs []string) []string {
	if len(addrs) == 0 {
		return addrs
	}
	anyBad := false
	for _, a := range addrs {
		bad, good := relayVerdict(a)
		if good {
			return addrs
		}
		anyBad = anyBad || bad
	}
	if !anyBad {
		err := probeRelay(addrs[0], user, pass)
		switch {
		case err == nil:
			markRelay(addrs[0], true)
			return addrs
		case isRelayAuthReject(err):
			for _, a := range addrs {
				markRelay(a, false)
			}
			log.Printf("relay: %s не принял свежие креды (401) — беру запасные релеи ВК", relayHost(addrs[0]))
		default:
			return addrs
		}
	}

	start := rand.IntN(len(FallbackRelays))
	var out []string
	probes := 0
	for i := 0; i < len(FallbackRelays) && len(out) < relayFallbackCount; i++ {
		a := FallbackRelays[(start+i)%len(FallbackRelays)]
		if bad, _ := relayVerdict(a); bad {
			continue
		}
		// Проверяем только первый: остальные из того же исправного кластера,
		// а лишние аллокации тратят время запуска.
		if len(out) == 0 {
			_, good := relayVerdict(a)
			if !good {
				if probes >= 3 {
					break
				}
				probes++
				err := probeRelay(a, user, pass)
				if isRelayAuthReject(err) {
					markRelay(a, false)
					continue
				}
				if err != nil {
					continue
				}
				markRelay(a, true)
			}
		}
		out = append(out, a)
	}
	if len(out) == 0 {
		log.Printf("relay: запасные релеи тоже не подошли — оставляю адреса ВК")
		return addrs
	}
	log.Printf("relay: креды пойдут через %s", strings.Join(out, ", "))
	return out
}
