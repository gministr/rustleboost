package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vpnclient/daemon/zapret"
)

// ZapretRunner owns the winws (DPI-desync) process that lets Discord and
// YouTube reach the network directly, bypassing the tunnel, without the
// censor's DPI seeing enough of the TLS/QUIC handshake to block them.
//
// It only ever makes sense against traffic that never entered the tunnel —
// see package zapret's doc comment. The hybrid route mode is what actually
// sends discord.com et al. to sing-box's "direct" outbound; this runner only
// has to be alive for that un-tunnelled traffic to survive the censor.
type ZapretRunner struct {
	proc    *procRunner
	dataDir string
}

func NewZapretRunner(dataDir string) *ZapretRunner {
	return &ZapretRunner{proc: newProcRunner("winws", dataDir), dataDir: dataDir}
}

func (r *ZapretRunner) IsRunning() bool      { return r.proc.isRunning() }
func (r *ZapretRunner) LogTail(n int) string { return r.proc.logTail(n) }

// Start writes the vendored engine and its assets to disk — once; it skips
// files already the right size, which is the common case on every connect
// after the first — then launches it filtered to exactly zapret.Domains.
func (r *ZapretRunner) Start() error {
	paths, err := r.writeAssets()
	if err != nil {
		return fmt.Errorf("zapret assets: %w", err)
	}

	args := []string{
		"--wf-tcp=443",
		"--wf-udp=443," + zapret.DiscordUDPPortRanges,

		// TLS ClientHello over TCP 443: split it across two segments at the
		// first byte, with real-looking TLS bytes in the overlap, so a
		// censor's DPI reading only the first segment never sees the SNI.
		"--filter-tcp=443",
		"--hostlist=" + paths.hostlist,
		"--dpi-desync=multisplit",
		"--dpi-desync-split-pos=1",
		fmt.Sprintf("--dpi-desync-split-seqovl=%d", len(zapret.TLSPattern)),
		"--dpi-desync-split-seqovl-pattern=" + paths.tlsPattern,
		"--new",

		// QUIC (YouTube over HTTP/3) on UDP 443: no stream to split, so a
		// decoy Initial packet goes out ahead of the real one instead.
		"--filter-udp=443",
		"--hostlist=" + paths.hostlist,
		"--dpi-desync=fake",
		"--dpi-desync-repeats=6",
		"--dpi-desync-fake-quic=" + paths.quicPattern,
		"--new",

		// Discord's voice/video UDP ports carry no hostname at all — this is
		// zapret's own documented fake payload for that path, not SNI-based.
		"--filter-udp=" + zapret.DiscordUDPPortRanges,
		"--filter-l7=discord,stun",
		"--dpi-desync=fake",
		"--dpi-desync-repeats=6",
		"--dpi-desync-fake-discord=" + paths.discordFake,
		"--dpi-desync-fake-stun=" + paths.discordFake,
	}

	return r.proc.startArgs(args...)
}

func (r *ZapretRunner) Stop() error {
	r.proc.stop()
	return nil
}

type zapretPaths struct {
	hostlist    string
	tlsPattern  string
	quicPattern string
	discordFake string
}

// writeAssets materialises the embedded engine next to the files it needs.
// WinDivert.dll/.sys must sit beside winws.exe — it loads the driver by a
// path relative to its own executable, not the process's working directory —
// so all of it goes straight into dataDir, where sing-box and Xray already
// keep their own config and log files.
func (r *ZapretRunner) writeAssets() (zapretPaths, error) {
	write := func(name string, data []byte) (string, error) {
		path := filepath.Join(r.dataDir, name)
		if existing, err := os.ReadFile(path); err == nil && len(existing) == len(data) {
			return path, nil
		}
		return path, os.WriteFile(path, data, 0644)
	}

	if _, err := write("winws.exe", zapret.WinwsEXE); err != nil {
		return zapretPaths{}, err
	}
	if _, err := write("WinDivert.dll", zapret.WinDivertDLL); err != nil {
		return zapretPaths{}, err
	}
	if _, err := write("WinDivert64.sys", zapret.WinDivertSYS); err != nil {
		return zapretPaths{}, err
	}
	if _, err := write("cygwin1.dll", zapret.CygwinDLL); err != nil {
		return zapretPaths{}, err
	}
	tlsPath, err := write("zapret-tls-pattern.bin", zapret.TLSPattern)
	if err != nil {
		return zapretPaths{}, err
	}
	quicPath, err := write("zapret-quic-pattern.bin", zapret.QUICPattern)
	if err != nil {
		return zapretPaths{}, err
	}
	discordPath, err := write("zapret-discord-fake.bin", zapret.DiscordFake)
	if err != nil {
		return zapretPaths{}, err
	}

	hostlistPath := filepath.Join(r.dataDir, "zapret-hostlist.txt")
	if err := os.WriteFile(hostlistPath, []byte(strings.Join(zapret.Domains, "\n")+"\n"), 0644); err != nil {
		return zapretPaths{}, err
	}

	return zapretPaths{
		hostlist:    hostlistPath,
		tlsPattern:  tlsPath,
		quicPattern: quicPath,
		discordFake: discordPath,
	}, nil
}
