package vkturn

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cacggghp/vk-turn-proxy/pkg/proxy"
)

func TestSRTPRelayMemory(t *testing.T) {
	memory := srtpRelayMemory{dir: t.TempDir()}
	now := time.Now()
	if got := memory.load("1.2.3.4:56100", now); got != nil {
		t.Fatalf("пустая память вернула %v", got)
	}
	relays := []string{"91.231.135.85:19302"}
	memory.save("1.2.3.4:56100", relays, now)
	if got := memory.load("1.2.3.4:56100", now); !reflect.DeepEqual(got, relays) {
		t.Fatalf("запись не вернулась: %v", got)
	}
	if got := memory.load("5.6.7.8:56100", now); got != nil {
		t.Fatalf("чужой узел получил %v", got)
	}
	// Связность меняется: через сутки снова пробуются релеи от ВК.
	if got := memory.load("1.2.3.4:56100", now.Add(srtpRelayMemoryTTL+time.Minute)); got != nil {
		t.Fatalf("запись не истекла: %v", got)
	}
	memory.save("1.2.3.4:56100", nil, now)
	if got := memory.load("1.2.3.4:56100", now); got != nil {
		t.Fatalf("запись не забыта: %v", got)
	}
}

func TestRelaysOutsideSkipsFailedSubnet(t *testing.T) {
	out := proxy.RelaysOutside("193.203.43.19")
	if len(out) == 0 {
		t.Fatal("запасных релеев не осталось")
	}
	for _, relay := range out {
		if strings.HasPrefix(relay, "193.203.43.") {
			t.Fatalf("релей из отказавшей подсети: %s", relay)
		}
	}
	if len(proxy.RelaysOutside("")) <= len(out) {
		t.Fatal("без адреса должен вернуться весь список")
	}
}
