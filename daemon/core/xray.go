package core

import (
	"encoding/json"
	"fmt"

	"github.com/vpnclient/daemon/config"
)

// XrayRunner owns the Xray-core process.
//
// Xray exists in this client for one reason: transports sing-box does not
// implement. XHTTP in particular is an Xray-only transport, and it is what the
// LTE-facing nodes use — without this core those locations cannot connect at
// all. Xray carries only the proxy leg; TUN, DNS and routing stay in sing-box,
// which reaches Xray over a local SOCKS inbound.
type XrayRunner struct {
	proc *procRunner
}

func NewXrayRunner(dataDir string) *XrayRunner {
	return &XrayRunner{proc: newProcRunner("xray", dataDir)}
}

// NewXrayProbeRunner returns a second Xray used only for latency measurement.
// It writes its own config, log and pid files so a probe never disturbs the
// instance carrying the user's traffic.
func NewXrayProbeRunner(dataDir string) *XrayRunner {
	return &XrayRunner{proc: newProcRunnerNamed("xray", "xray-probe", dataDir)}
}

func (r *XrayRunner) StartProbe(cfg *config.XrayConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal probe config: %w", err)
	}
	return r.proc.start(data, "xray-probe-config.json", "run")
}

func (r *XrayRunner) Start(cfg *config.XrayConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal xray config: %w", err)
	}
	return r.proc.start(data, "xray-config.json", "run")
}

func (r *XrayRunner) Stop() error {
	r.proc.stop()
	return nil
}

func (r *XrayRunner) IsRunning() bool        { return r.proc.isRunning() }
func (r *XrayRunner) LogTail(n int) string   { return r.proc.logTail(n) }
