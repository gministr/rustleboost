package core

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/xtls/xray-core/core"

	"github.com/vpnclient/daemon/config"
	// Регистрирует протоколы и транспорты Xray (rustlecore делает то же).
	_ "github.com/xtls/xray-core/main/distro/all"
)

// TrafficStats — счётчики трафика подключения.
type TrafficStats struct {
	Upload   int64
	Download int64
}

// XrayRunner — отдельный экземпляр Xray для замера задержки серверов.
//
// Раньше для этого запускался второй xray.exe с конфигурацией на диске (а в
// ней учётные данные серверов). Теперь экземпляр живёт в процессе службы и
// получает конфигурацию из памяти. Основное ядро подключения — rustlecore.
type XrayRunner struct {
	mu   sync.Mutex
	inst *core.Instance
}

// NewXrayProbeRunner — экземпляр для замеров.
func NewXrayProbeRunner(string) *XrayRunner { return &XrayRunner{} }

func (r *XrayRunner) StartProbe(cfg *config.XrayConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal probe config: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inst != nil {
		_ = r.inst.Close()
		r.inst = nil
	}
	inst, err := core.StartInstance("json", data)
	if err != nil {
		return err
	}
	r.inst = inst
	return nil
}

func (r *XrayRunner) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inst != nil {
		_ = r.inst.Close()
		r.inst = nil
	}
	return nil
}
