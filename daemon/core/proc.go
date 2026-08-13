package core

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// procRunner supervises one external core process (sing-box or Xray).
// Both cores take the same shape of arguments — a config file and a run
// verb — so the lifecycle logic lives here once.
type procRunner struct {
	mu      sync.Mutex
	name    string // "sing-box" | "xray", also the binary stem
	dataDir string
	cmd     *exec.Cmd
	running atomic.Bool
	logPath string
}

func newProcRunner(name, dataDir string) *procRunner {
	return &procRunner{
		name:    name,
		dataDir: dataDir,
		logPath: filepath.Join(dataDir, name+".log"),
	}
}

// start writes cfgJSON to disk and launches the core against it.
// It returns an error containing the tail of the core's log when the process
// dies immediately, which is the usual signal of a malformed config.
func (p *procRunner) start(cfgJSON []byte, cfgName string, args ...string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running.Load() {
		p.stopLocked()
	}

	cfgPath := filepath.Join(p.dataDir, cfgName)
	if err := os.WriteFile(cfgPath, cfgJSON, 0600); err != nil {
		return fmt.Errorf("write %s config: %w", p.name, err)
	}

	binary := p.findBinary()
	if binary == "" {
		return fmt.Errorf("%s binary not found", p.name)
	}

	logFile, _ := os.OpenFile(p.logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)

	fullArgs := append(append([]string{}, args...), "-c", cfgPath)
	p.cmd = exec.Command(binary, fullArgs...)
	p.cmd.Stdout = logFile
	p.cmd.Stderr = logFile
	setSysProcAttr(p.cmd)

	if err := p.cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return fmt.Errorf("start %s: %w", p.name, err)
	}

	p.running.Store(true)
	writePIDFile(p.dataDir, p.name, p.cmd.Process.Pid)
	log.Printf("[%s] started pid=%d", p.name, p.cmd.Process.Pid)

	cmd := p.cmd
	go func() {
		cmd.Wait()
		p.running.Store(false)
		log.Printf("[%s] exited", p.name)
	}()

	time.Sleep(400 * time.Millisecond)
	if logFile != nil {
		logFile.Close()
	}

	if !p.running.Load() {
		return fmt.Errorf("%s exited immediately:\n%s", p.name, tailFile(p.logPath, 8))
	}
	return nil
}

func (p *procRunner) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked()
}

func (p *procRunner) stopLocked() {
	if p.cmd != nil && p.cmd.Process != nil {
		if runtime.GOOS == "windows" {
			p.cmd.Process.Kill()
		} else {
			p.cmd.Process.Signal(os.Interrupt)
			done := make(chan struct{})
			cmd := p.cmd
			go func() { cmd.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(4 * time.Second):
				p.cmd.Process.Kill()
			}
		}
	}
	p.running.Store(false)
	p.cmd = nil
	removePIDFile(p.dataDir, p.name)
}

// ── stale core cleanup ────────────────────────────────────────────────────
//
// A crash or a forced shutdown can leave sing-box or Xray running. The
// orphan keeps the TUN adapter and the local ports, so the next connect
// fails. We record each core's PID and kill only those on startup —
// killing every process named sing-box.exe would take down an unrelated
// VPN client the user happens to run.

func pidFilePath(dataDir, name string) string {
	return filepath.Join(dataDir, name+".pid")
}

func writePIDFile(dataDir, name string, pid int) {
	os.WriteFile(pidFilePath(dataDir, name), []byte(strconv.Itoa(pid)), 0600)
}

func removePIDFile(dataDir, name string) {
	os.Remove(pidFilePath(dataDir, name))
}

// CleanupStaleCores terminates cores left behind by a previous run.
func CleanupStaleCores(dataDir string) {
	for _, name := range []string{"sing-box", "xray"} {
		path := pidFilePath(dataDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		os.Remove(path)

		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || pid <= 0 {
			continue
		}
		if killStaleProcess(pid, name) {
			log.Printf("[cleanup] terminated stale %s (pid=%d)", name, pid)
		}
	}
}

// killStaleProcess kills pid only when it is still the core we started —
// PIDs get recycled, and killing a stranger's process would be worse than
// leaving ours running.
func killStaleProcess(pid int, name string) bool {
	if runtime.GOOS != "windows" {
		proc, err := os.FindProcess(pid)
		if err != nil {
			return false
		}
		return proc.Kill() == nil
	}

	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
	if err != nil || !strings.Contains(strings.ToLower(string(out)), name+".exe") {
		return false
	}
	return exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run() == nil
}

func (p *procRunner) isRunning() bool { return p.running.Load() }

// logTail returns the last n lines of the core's log, for surfacing the real
// reason a connection failed instead of a generic message.
func (p *procRunner) logTail(n int) string { return tailFile(p.logPath, n) }

// findBinary looks beside the daemon executable first — that is where the
// installer places the bundled cores — then falls back to the data dir and PATH.
func (p *procRunner) findBinary() string {
	names := []string{p.name}
	if runtime.GOOS == "windows" {
		// The installer drops plain names beside the app; a dev tree keeps
		// the Tauri sidecar names instead.
		names = []string{p.name + ".exe", p.name + "-x86_64-pc-windows-msvc.exe"}
	}

	var dirs []string
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	dirs = append(dirs, p.dataDir)

	for _, dir := range dirs {
		for _, name := range names {
			candidate := filepath.Join(dir, name)
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	if path, err := exec.LookPath(p.name); err == nil {
		return path
	}
	return ""
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
