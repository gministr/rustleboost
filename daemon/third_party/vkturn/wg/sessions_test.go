package wg

import (
	"io"
	"net"
	"testing"
	"time"
)

// На устройстве все слоты заполнялись за десять секунд после старта, и приём
// новых соединений вставал. Теперь новое соединение принимается всегда, а
// место освобождает самое давно молчавшее. Проверяется, что уходит именно
// оно, а не живое.

func pipeConn(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}

func isClosed(conn net.Conn) bool {
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	_, err := conn.Read(make([]byte, 1))
	_ = conn.SetReadDeadline(time.Time{})
	return err == io.ErrClosedPipe || err == io.EOF
}

func TestFullTableAcceptsNewConnection(t *testing.T) {
	table := newSessionTable(2)
	first, _ := pipeConn(t)
	second, _ := pipeConn(t)
	third, _ := pipeConn(t)

	table.admit(first)
	time.Sleep(time.Millisecond)
	table.admit(second)
	time.Sleep(time.Millisecond)
	table.admit(third)

	if table.size() != 2 {
		t.Fatalf("в таблице %d соединений, ожидалось 2", table.size())
	}
	if !isClosed(first) {
		t.Error("самое старое молчащее соединение не закрыто")
	}
	if isClosed(third) {
		t.Error("новое соединение закрыто вместо старого")
	}
}

// Вытесняется не самое старое, а самое молчаливое: живая закачка, открытая
// давно, остаётся, а брошенное недавно — уходит.
func TestEvictionPrefersIdleOverOld(t *testing.T) {
	table := newSessionTable(2)
	oldButBusy, oldPeer := pipeConn(t)
	newButIdle, _ := pipeConn(t)
	incoming, _ := pipeConn(t)

	busy := table.admit(oldButBusy)
	time.Sleep(2 * time.Millisecond)
	table.admit(newButIdle)
	time.Sleep(2 * time.Millisecond)

	// По старому соединению пошли данные.
	go func() { _, _ = oldPeer.Write([]byte("x")) }()
	buffer := make([]byte, 1)
	if _, err := (sessionConn{oldButBusy, busy}).Read(buffer); err != nil {
		t.Fatal(err)
	}

	table.admit(incoming)

	if isClosed(oldButBusy) {
		t.Error("вытеснено живое соединение")
	}
	if !isClosed(newButIdle) {
		t.Error("молчащее соединение осталось")
	}
}

func TestReleaseFreesPlace(t *testing.T) {
	table := newSessionTable(1)
	first, _ := pipeConn(t)
	second, _ := pipeConn(t)

	s := table.admit(first)
	table.release(s)
	table.admit(second)

	if isClosed(second) {
		t.Error("после освобождения места новое соединение закрыто")
	}
	if table.evicted.Load() != 0 {
		t.Errorf("вытеснено %d, хотя место было свободно", table.evicted.Load())
	}
}

// UDP-связка данных по управляющему соединению не гоняет. Её активность
// отмечается через touch(), иначе живая связка DNS уходила бы первой.
func TestSessionConnExposesTouch(t *testing.T) {
	conn, _ := pipeConn(t)
	s := &session{conn: conn}
	s.lastActive.Store(0)

	var wrapped net.Conn = sessionConn{conn, s}
	toucher, ok := wrapped.(activityToucher)
	if !ok {
		t.Fatal("соединение из таблицы не умеет отмечать активность")
	}
	toucher.touch()
	if s.lastActive.Load() == 0 {
		t.Fatal("отметка активности не сработала")
	}
}
