package proxy

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Несколько включений подряд оставляют весь пул «ждущим» после загрузки с диска.
// Телефон не должен ждать десять минут: свежая учётка — новая личность со своей
// квотой, а получить её можно за секунду.
func TestPendingPoolGivesWayToFreshCredential(t *testing.T) {
	expiry := time.Now().Add(8 * time.Hour).Unix()
	creds := func(id string) *TURNCreds {
		return &TURNCreds{
			Username:  fmt.Sprintf("%d:%s", expiry, id),
			Password:  "p",
			Address:   "198.51.100.1:19302",
			Addresses: []string{"198.51.100.1:19302"},
		}
	}
	fresh := creds("fresh")
	minted := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cp := newCredPool(ctx, 3, time.Minute, "", func(bool, int) (string, *TURNCreds, error) {
		minted++
		return fresh.Address, fresh, nil
	})

	// Слот 1 ждёт дольше всех — ему и уступать, а слот с соединением неприкосновенен.
	now := time.Now()
	cp.mu.Lock()
	for slot, wait := range []time.Duration{2 * time.Minute, 9 * time.Minute, 5 * time.Minute} {
		cp.pool[slot] = credPoolEntry{
			addr:        "198.51.100.1:19302",
			addrs:       []string{"198.51.100.1:19302"},
			creds:       creds(fmt.Sprintf("old%d", slot)),
			ts:          now,
			lastUsedAt:  now,
			availableAt: now.Add(wait),
		}
	}
	cp.mu.Unlock()

	_, got, slot, err := cp.get(0, false)
	if err != nil {
		t.Fatalf("пул не выдал учётку: %v", err)
	}
	if minted != 1 || got.Username != fresh.Username {
		t.Fatalf("ожидалась свежая учётка, выпущено %d, получена %q", minted, got.Username)
	}
	if slot != 1 {
		t.Fatalf("уступить должен слот, ждущий дольше всех, а уступил %d", slot)
	}
}
