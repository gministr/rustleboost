package vkturn

import (
	"encoding/binary"
	"testing"
	"time"
)

func wgPacket(idx uint32, ctr uint64) []byte {
	p := getPktBuf(64)
	for i := range p {
		p[i] = 0
	}
	p[0] = 4
	binary.LittleEndian.PutUint32(p[4:8], idx)
	binary.LittleEndian.PutUint64(p[8:16], ctr)
	return p
}

func collect(out *[]uint64) func([]byte) {
	return func(p []byte) {
		if _, c, ok := wgDataHeader(p); ok {
			*out = append(*out, c)
		} else {
			*out = append(*out, 1<<63)
		}
	}
}

func TestReorderRestoresOrder(t *testing.T) {
	var got []uint64
	r := newReorderer(collect(&got))
	now := time.Now()
	for _, c := range []uint64{10, 12, 13, 11, 15, 14} {
		r.push(wgPacket(1, c), now)
	}
	want := []uint64{10, 11, 12, 13, 14, 15}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestReorderGivesUpOnLostPacket(t *testing.T) {
	var got []uint64
	r := newReorderer(collect(&got))
	now := time.Now()
	r.push(wgPacket(1, 1), now)
	r.push(wgPacket(1, 3), now)
	r.push(wgPacket(1, 4), now)
	if len(got) != 1 {
		t.Fatalf("до срока ушло лишнее: %v", got)
	}
	if _, ok := r.nextDeadline(now); !ok {
		t.Fatal("нет срока ожидания")
	}
	r.expire(now.Add(reorderHold))
	if len(got) != 3 || got[1] != 3 || got[2] != 4 {
		t.Fatalf("после срока: %v", got)
	}
	// Опоздавший отдаётся сразу.
	r.push(wgPacket(1, 2), now)
	if len(got) != 4 || got[3] != 2 {
		t.Fatalf("опоздавший: %v", got)
	}
}

func TestReorderPassesNonDataAndNewSessions(t *testing.T) {
	var got []uint64
	r := newReorderer(collect(&got))
	now := time.Now()
	h := getPktBuf(32)
	for i := range h {
		h[i] = 0
	}
	h[0] = 2
	r.push(h, now)
	r.push(wgPacket(1, 100), now)
	r.push(wgPacket(2, 0), now) // новая сессия после смены ключей
	r.push(wgPacket(1, 1_000_000), now)
	if len(got) != 4 {
		t.Fatalf("got %v", got)
	}
}

func TestReorderBoundsHeldPackets(t *testing.T) {
	var got []uint64
	r := newReorderer(collect(&got))
	now := time.Now()
	r.push(wgPacket(1, 0), now)
	for c := uint64(2); c < 2+reorderMaxHeld+10; c++ {
		r.push(wgPacket(1, c), now)
	}
	if r.held > reorderMaxHeld {
		t.Fatalf("держим %d", r.held)
	}
}
