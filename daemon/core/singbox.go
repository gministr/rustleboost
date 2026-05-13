package core

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vpnclient/daemon/config"
)

type TrafficStats struct {
	Upload   int64
	Download int64
}

type SingBoxRunner struct {
	mu          sync.Mutex
	dataDir     string
	cmd         *exec.Cmd
	running     atomic.Bool
	upload      int64
	download    int64
	baseRecv    int64
	baseSent    int64
	statsCancel func()
}

func NewSingBoxRunner(dataDir string) *SingBoxRunner {
	return &SingBoxRunner{dataDir: dataDir}
}

func (r *SingBoxRunner) Start(cfg *config.SingBoxConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running.Load() {
		r.stopLocked()
	}

	cfgPath := filepath.Join(r.dataDir, "sing-box-config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(cfgPath, data, 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	binaryPath := r.findBinary()
	if binaryPath == "" {
		return fmt.Errorf("sing-box binary not found")
	}

	logPath := filepath.Join(r.dataDir, "sing-box.log")
	logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)

	r.cmd = exec.Command(binaryPath, "run", "-c", cfgPath)
	r.cmd.Stdout = logFile
	r.cmd.Stderr = logFile
	setSysProcAttr(r.cmd)

	if err := r.cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("start sing-box: %w", err)
	}

	r.running.Store(true)
	log.Printf("[singbox] started pid=%d", r.cmd.Process.Pid)

	go func() {
		r.cmd.Wait()
		r.running.Store(false)
		log.Println("[singbox] exited")
	}()

	time.Sleep(250 * time.Millisecond)
	logFile.Close()

	if !r.running.Load() {
		return fmt.Errorf("sing-box exited immediately:\n%s", tailFile(logPath, 5))
	}

	// Snapshot network baseline for delta tracking
	recv, sent, _ := readNetStats()
	r.baseRecv = recv
	r.baseSent = sent
	atomic.StoreInt64(&r.upload, 0)
	atomic.StoreInt64(&r.download, 0)

	stopCh := make(chan struct{})
	r.statsCancel = sync.OnceFunc(func() { close(stopCh) })
	go r.pollStats(stopCh)

	return nil
}

// pollStats updates traffic counters every 2 seconds.
// Tries Clash API first (proxy-accurate), falls back to OS adapter delta.
func (r *SingBoxRunner) pollStats(stop <-chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	clashFails := 0

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if !r.running.Load() {
				return
			}

			// 1. Clash API (accurate: counts only proxied bytes)
			if clashFails < 5 {
				if up, down, ok := r.queryClashAPI(); ok {
					atomic.StoreInt64(&r.upload, up)
					atomic.StoreInt64(&r.download, down)
					clashFails = 0
					continue
				}
				clashFails++
			}

			// 2. Fallback: total OS network adapter delta since connect
			recv, sent, err := readNetStats()
			if err == nil {
				dl := recv - r.baseRecv
				ul := sent - r.baseSent
				if dl > 0 {
					atomic.StoreInt64(&r.download, dl)
				}
				if ul > 0 {
					atomic.StoreInt64(&r.upload, ul)
				}
			}
		}
	}
}

func (r *SingBoxRunner) queryClashAPI() (upload, download int64, ok bool) {
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get("http://127.0.0.1:9090/connections")
	if err != nil || resp.StatusCode != 200 {
		return 0, 0, false
	}
	defer resp.Body.Close()
	var data struct {
		DownloadTotal int64 `json:"downloadTotal"`
		UploadTotal   int64 `json:"uploadTotal"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, 0, false
	}
	if data.DownloadTotal == 0 && data.UploadTotal == 0 {
		return 0, 0, false
	}
	return data.UploadTotal, data.DownloadTotal, true
}

func (r *SingBoxRunner) stopLocked() {
	if r.statsCancel != nil {
		r.statsCancel()
		r.statsCancel = nil
	}
	if r.cmd != nil && r.cmd.Process != nil {
		if runtime.GOOS == "windows" {
			r.cmd.Process.Kill()
		} else {
			r.cmd.Process.Signal(os.Interrupt)
			done := make(chan struct{})
			go func() { r.cmd.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(4 * time.Second):
				r.cmd.Process.Kill()
			}
		}
	}
	r.running.Store(false)
	r.cmd = nil
}

func (r *SingBoxRunner) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLocked()
	atomic.StoreInt64(&r.upload, 0)
	atomic.StoreInt64(&r.download, 0)
	return nil
}

func (r *SingBoxRunner) IsRunning() bool { return r.running.Load() }

func (r *SingBoxRunner) GetStats() *TrafficStats {
	if !r.running.Load() {
		return nil
	}
	return &TrafficStats{
		Upload:   atomic.LoadInt64(&r.upload),
		Download: atomic.LoadInt64(&r.download),
	}
}

func (r *SingBoxRunner) findBinary() string {
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), binaryName())
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if _, err := os.Stat(filepath.Join(r.dataDir, binaryName())); err == nil {
		return filepath.Join(r.dataDir, binaryName())
	}
	if path, err := exec.LookPath("sing-box"); err == nil {
		return path
	}
	return ""
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "sing-box.exe"
	}
	return "sing-box"
}

func tailFile(path string, n int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "(no log)"
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
