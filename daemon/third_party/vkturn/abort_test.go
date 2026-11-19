package vkturn

import (
	"context"
	"testing"
	"time"
)

// Отмена, пришедшая раньше, чем запуск завёл контекст, не должна теряться:
// иначе запуск, начатый после нажатия «отключить», прошёл бы целиком.
func TestAbortBeforeRegistrationStopsStart(t *testing.T) {
	defer clearAbort()
	Abort()
	if registerAbort(func() {}) {
		t.Fatal("запуск зарегистрировался, хотя отмену уже попросили")
	}
}

// Отмена после регистрации гасит контекст запуска сразу.
func TestAbortCancelsRegisteredStart(t *testing.T) {
	defer clearAbort()
	ctx, cancel := context.WithCancel(context.Background())
	if !registerAbort(cancel) {
		t.Fatal("регистрация не прошла без отмены")
	}
	Abort()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("контекст запуска не отменился")
	}
}

// После остановки отметка снимается — следующий запуск идёт как обычно.
func TestClearAbortAllowsNextStart(t *testing.T) {
	Abort()
	clearAbort()
	defer clearAbort()
	if !registerAbort(func() {}) {
		t.Fatal("следующий запуск отменился из-за прошлой остановки")
	}
}

// Резолв с отменой возвращает управление сразу, не дожидаясь DNS.
func TestResolveReturnsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	// Имя из зарезервированной зоны: настоящий ответ не важен, важно, что
	// отменённый запрос не ждёт его.
	_, err := resolveUDPAddr(ctx, "never-resolves.invalid:56000")
	if err == nil {
		t.Fatal("ожидалась ошибка отмены")
	}
	if time.Since(started) > 200*time.Millisecond {
		t.Fatalf("отменённый резолв занял %s", time.Since(started))
	}
}
