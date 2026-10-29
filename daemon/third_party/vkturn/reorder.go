package vkturn

import (
	"encoding/binary"
	"time"
)

// Выравнивание порядка встречных пакетов.
//
// Воркеры идут через разные релеи VK с разной задержкой, и сервер раскладывает
// встречный поток по всем сразу: у одной аллокации потолок около 2 Мбит/с.
// Пакеты при этом приходят вперемешку, и TCP внутри туннеля принимает каждую
// перестановку за потерю — повторяет пакеты и сбрасывает скорость. Поэтому
// пакеты данных WireGuard отдаются ему по порядку их счётчика: обогнавший
// ждёт отставших, но не дольше reorderHold.
//
// Счётчик лежит в пакете открыто (байты 8–16), рядом с индексом получателя
// (4–8); у каждой сессии WireGuard своя последовательность.

const (
	reorderHold    = 40 * time.Millisecond
	reorderMaxHeld = 768
	// Скачок вперёд больше этого — не перестановка, а новая последовательность
	// (сервер перезапущен или сессия пересоздана): ждать нечего.
	reorderMaxGap = 4096
)

type heldPacket struct {
	pkt     []byte
	arrived time.Time
}

type reorderStream struct {
	next    uint64
	pending map[uint64]heldPacket
	// oldest — самый ранний по времени прихода из ждущих; нулевой, если ждущих нет.
	oldest time.Time
}

type reorderer struct {
	deliver func([]byte)
	streams map[uint32]*reorderStream
	held    int
}

func newReorderer(deliver func([]byte)) *reorderer {
	return &reorderer{deliver: deliver, streams: make(map[uint32]*reorderStream)}
}

func wgDataHeader(pkt []byte) (idx uint32, counter uint64, ok bool) {
	if len(pkt) < 16 || pkt[0] != 4 || pkt[1] != 0 || pkt[2] != 0 || pkt[3] != 0 {
		return 0, 0, false
	}
	return binary.LittleEndian.Uint32(pkt[4:8]), binary.LittleEndian.Uint64(pkt[8:16]), true
}

func (r *reorderer) push(pkt []byte, now time.Time) {
	idx, ctr, ok := wgDataHeader(pkt)
	if !ok {
		r.deliver(pkt)
		return
	}
	st := r.streams[idx]
	if st == nil {
		// Старые сессии после смены ключей больше не пишут: держать их незачем.
		for k, old := range r.streams {
			if len(old.pending) == 0 {
				delete(r.streams, k)
			}
		}
		st = &reorderStream{next: ctr, pending: make(map[uint64]heldPacket)}
		r.streams[idx] = st
	}
	switch {
	case ctr < st.next:
		// Опоздавший, которого уже перестали ждать: WireGuard сам решит, брать ли.
		r.deliver(pkt)
	case ctr == st.next:
		r.deliver(pkt)
		st.next++
		r.flushReady(st)
	case ctr-st.next > reorderMaxGap:
		r.flushAll(st)
		st.next = ctr + 1
		r.deliver(pkt)
	default:
		if _, dup := st.pending[ctr]; dup {
			putPktBuf(pkt)
			return
		}
		st.pending[ctr] = heldPacket{pkt: pkt, arrived: now}
		r.held++
		if st.oldest.IsZero() {
			st.oldest = now
		}
		if r.held > reorderMaxHeld {
			r.skipGap(st)
		}
	}
}

// flushReady отдаёт подряд идущие пакеты, начиная с ожидаемого.
func (r *reorderer) flushReady(st *reorderStream) {
	for {
		h, ok := st.pending[st.next]
		if !ok {
			break
		}
		delete(st.pending, st.next)
		r.held--
		r.deliver(h.pkt)
		st.next++
	}
	r.recomputeOldest(st)
}

// skipGap перестаёт ждать пропуск: следующим становится самый ранний из ждущих.
func (r *reorderer) skipGap(st *reorderStream) {
	if len(st.pending) == 0 {
		return
	}
	first := uint64(0)
	found := false
	for c := range st.pending {
		if !found || c < first {
			first, found = c, true
		}
	}
	st.next = first
	r.flushReady(st)
}

func (r *reorderer) flushAll(st *reorderStream) {
	for len(st.pending) > 0 {
		r.skipGap(st)
	}
}

func (r *reorderer) recomputeOldest(st *reorderStream) {
	st.oldest = time.Time{}
	for _, h := range st.pending {
		if st.oldest.IsZero() || h.arrived.Before(st.oldest) {
			st.oldest = h.arrived
		}
	}
}

// expire отпускает пакеты, прождавшие дольше reorderHold.
func (r *reorderer) expire(now time.Time) {
	for _, st := range r.streams {
		for !st.oldest.IsZero() && now.Sub(st.oldest) >= reorderHold {
			r.skipGap(st)
		}
	}
}

// nextDeadline — через сколько истечёт ожидание самого старого пакета.
func (r *reorderer) nextDeadline(now time.Time) (time.Duration, bool) {
	var earliest time.Time
	for _, st := range r.streams {
		if !st.oldest.IsZero() && (earliest.IsZero() || st.oldest.Before(earliest)) {
			earliest = st.oldest
		}
	}
	if earliest.IsZero() {
		return 0, false
	}
	wait := earliest.Add(reorderHold).Sub(now)
	if wait < time.Millisecond {
		wait = time.Millisecond
	}
	return wait, true
}
