//go:build !windows

package core

import "os/exec"

func setSysProcAttr(cmd *exec.Cmd) {}

// hiddenCommand mirrors the Windows helper; elsewhere there is no console
// window to hide, so it is a plain command.
func hiddenCommand(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}
