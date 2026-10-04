package wg

import (
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Таблица живых соединений SOCKS5 с вытеснением самого давно молчавшего.
//
// Раньше предел держал семафор: при полном наборе приём новых соединений
// просто вставал до освобождения слота. На устройстве это выглядело так:
// через десять секунд после старта все слоты заняты — приложения разом
// восстанавливают соединения, DNS идёт отдельными UDP-связками, — и каждое
// новое соединение, включая пробу живости, ждёт, пока освободится чужое. А
// освобождаются они только по сроку неактивности. Снаружи — подвисания, затем
// «канал не отвечает» и перезапуск ядра, после которого всё повторялось.
//
// Вытеснение решает иначе: новое соединение принимается всегда, а место ему
// освобождает то, по которому дольше всех не шло данных. Обычно это брошенное
// приложением keep-alive соединение — закрыть его ничего не стоит, при
// следующем обращении приложение откроет новое. Живая закачка при этом не
// страдает: у неё данные идут постоянно, и в очередь на вытеснение она не
// попадает.

// session — одно принятое соединение и время последнего обмена по нему.
type session struct {
	conn       net.Conn
	lastActive atomic.Int64
}

func (s *session) touch() { s.lastActive.Store(time.Now().UnixNano()) }

// sessionConn отмечает активность при каждом чтении и записи.
//
// UDP-связка по управляющему соединению данных не гоняет — датаграммы идут
// отдельным сокетом, — поэтому её циклы отмечают активность сами через
// touch(). Иначе живая связка DNS выглядела бы самой молчаливой и вытеснялась
// бы первой.
type sessionConn struct {
	net.Conn
	s *session
}

func (c sessionConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.s.touch()
	}
	return n, err
}

func (c sessionConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.s.touch()
	}
	return n, err
}

func (c sessionConn) touch() { c.s.touch() }

// activityToucher — то, что умеет отметить активность без данных по самому
// соединению. Проверяется приведением: соединение из тестов его не реализует.
type activityToucher interface {
	touch()
}

type sessionTable struct {
	mu      sync.Mutex
	live    map[*session]struct{}
	limit   int
	evicted atomic.Int64
}

func newSessionTable(limit int) *sessionTable {
	return &sessionTable{live: make(map[*session]struct{}, limit), limit: limit}
}

// admit принимает соединение, при полной таблице закрывая самое молчаливое.
func (t *sessionTable) admit(conn net.Conn) *session {
	s := &session{conn: conn}
	s.touch()

	t.mu.Lock()
	var victim *session
	if len(t.live) >= t.limit {
		for candidate := range t.live {
			if victim == nil || candidate.lastActive.Load() < victim.lastActive.Load() {
				victim = candidate
			}
		}
		delete(t.live, victim)
	}
	t.live[s] = struct{}{}
	t.mu.Unlock()

	if victim != nil {
		// Закрытие вне замка: оно будит горутины вытесненного соединения,
		// и держать ради этого таблицу незачем.
		_ = victim.conn.Close()
		count := t.evicted.Add(1)
		if count == 1 || count%100 == 0 {
			idle := time.Since(time.Unix(0, victim.lastActive.Load())).Round(time.Second)
			log.Printf("[СОЕДИНЕНИЯ] Предел %d занят, вытеснено молчавшее %s (всего вытеснено %d)",
				t.limit, idle, count)
		}
	}
	return s
}

func (t *sessionTable) release(s *session) {
	t.mu.Lock()
	delete(t.live, s)
	t.mu.Unlock()
}

func (t *sessionTable) size() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.live)
}
