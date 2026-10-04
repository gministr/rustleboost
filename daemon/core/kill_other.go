//go:build !windows

package core

import "os"

// terminateIfMatches cannot verify the executable behind a pid portably, so
// elsewhere it simply signals it. PID reuse is far less likely outside
// Windows, and this path is only reached in development.
func terminateIfMatches(pid int, _ string) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Kill() == nil
}
