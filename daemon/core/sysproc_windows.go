//go:build windows

package core

import (
	"os/exec"
	"syscall"
)

// createNoWindow keeps a spawned process from flashing a console window.
// Every helper this daemon shells out to — wmic, powershell, netsh, tasklist —
// is a console program, and without this flag each one blinks a black window
// over the user's screen on startup and on every connect.
const createNoWindow = 0x08000000

func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
}

// hiddenCommand builds a command that never shows a console window.
// Use it instead of exec.Command anywhere in this package.
func hiddenCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	setSysProcAttr(cmd)
	return cmd
}
