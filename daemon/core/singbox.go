package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vpnclient/daemon/config"
)

// ClashAPIAddr is the controller sing-box exposes for traffic accounting.
const ClashAPIAddr = "127.0.0.1:9090"

type TrafficStats struct {
	Upload   int64
	Download int64
}

// SingBoxRunner owns the sing-box process. sing-box always runs while
// connected: it provides the TUN interface, DNS and routing, and it is the
// single point every byte passes through — which makes its Clash API the one
// honest source of session traffic regardless of which core carries the proxy.
type SingBoxRunner struct {
	proc *procRunner

	upload      int64
	download    int64
	statsOnce   sync.Mutex
	statsCancel func()
}

func NewSingBoxRunner(dataDir string) *SingBoxRunner {
	return &SingBoxRunner{proc: newProcRunner("sing-box", dataDir)}
}

func (r *SingBoxRunner) Start(cfg *config.SingBoxConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := r.proc.start(data, "sing-box-config.json", "run"); err != nil {
		return err
	}

	atomic.StoreInt64(&r.upload, 0)
	atomic.StoreInt64(&r.download, 0)

	r.statsOnce.Lock()
	stop := make(chan struct{})
	r.statsCancel = sync.OnceFunc(func() { close(stop) })
	r.statsOnce.Unlock()
	go r.pollStats(stop)

	return nil
}

func (r *SingBoxRunner) Stop() error {
	r.statsOnce.Lock()
	if r.statsCancel != nil {
		r.statsCancel()
		r.statsCancel = nil
	}
	r.statsOnce.Unlock()

	r.proc.stop()
	atomic.StoreInt64(&r.upload, 0)
	atomic.StoreInt64(&r.download, 0)
	return nil
}

func (r *SingBoxRunner) IsRunning() bool  { return r.proc.isRunning() }
func (r *SingBoxRunner) LogTail(n int) string { return r.proc.logTail(n) }

func (r *SingBoxRunner) GetStats() *TrafficStats {
	if !r.proc.isRunning() {
		return nil
	}
	return &TrafficStats{
		Upload:   atomic.LoadInt64(&r.upload),
		Download: atomic.LoadInt64(&r.download),
	}
}

// pollStats reads cumulative session counters from the Clash API.
//
// There is deliberately no fallback to OS adapter counters here: those measure
// every byte the machine sends, VPN or not, so they overstate usage badly on a
// busy connection. Reporting nothing is better than reporting a wrong number.
func (r *SingBoxRunner) pollStats(stop <-chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	client := &http.Client{Timeout: 2 * time.Second}

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if !r.proc.isRunning() {
				return
			}
			if up, down, ok := queryClashTotals(client); ok {
				atomic.StoreInt64(&r.upload, up)
				atomic.StoreInt64(&r.download, down)
			}
		}
	}
}

func queryClashTotals(client *http.Client) (upload, download int64, ok bool) {
	resp, err := client.Get("http://" + ClashAPIAddr + "/connections")
	if err != nil {
		return 0, 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, false
	}

	var data struct {
		DownloadTotal int64 `json:"downloadTotal"`
		UploadTotal   int64 `json:"uploadTotal"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, 0, false
	}
	return data.UploadTotal, data.DownloadTotal, true
}
