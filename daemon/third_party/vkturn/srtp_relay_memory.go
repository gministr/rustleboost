package vkturn

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Какие релеи сработали для узла, когда выданные звонком не сработали.
//
// Запоминается на сутки, чтобы следующее подключение к этому узлу не теряло
// половину отведённого времени на заведомо молчащий релей. Через сутки снова
// пробуются релеи от ВК: связность у них меняется.
const srtpRelayMemoryTTL = 24 * time.Hour

type srtpRelayRecord struct {
	Relays []string `json:"relays"`
	Until  int64    `json:"until"`
}

type srtpRelayMemory struct {
	dir string
}

func (m srtpRelayMemory) path() string {
	return filepath.Join(m.dir, "srtp-relays.json")
}

func (m srtpRelayMemory) read() map[string]srtpRelayRecord {
	records := map[string]srtpRelayRecord{}
	if m.dir == "" {
		return records
	}
	if data, err := os.ReadFile(m.path()); err == nil {
		_ = json.Unmarshal(data, &records)
	}
	return records
}

// load отдаёт запомненные релеи узла, если срок ещё не вышел.
func (m srtpRelayMemory) load(peer string, now time.Time) []string {
	record, found := m.read()[peer]
	if !found || record.Until <= now.Unix() || len(record.Relays) == 0 {
		return nil
	}
	return record.Relays
}

// save запоминает релеи узла. Пустой список забывает запись: сработали релеи ВК.
func (m srtpRelayMemory) save(peer string, relays []string, now time.Time) {
	if m.dir == "" {
		return
	}
	records := m.read()
	if len(relays) == 0 {
		delete(records, peer)
	} else {
		records[peer] = srtpRelayRecord{Relays: relays, Until: now.Add(srtpRelayMemoryTTL).Unix()}
	}
	data, err := json.Marshal(records)
	if err != nil {
		return
	}
	temporary := m.path() + ".tmp"
	if os.WriteFile(temporary, data, 0o600) == nil {
		_ = os.Rename(temporary, m.path())
	}
}
