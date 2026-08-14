//go:build windows

package core

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// terminateIfMatches ends the process with the given pid, but only when its
// executable is still the core we recorded. Windows recycles PIDs, so a stale
// pid file can easily point at something the user cares about by the time we
// read it.
func terminateIfMatches(pid int, name string) bool {
	const access = windows.PROCESS_QUERY_LIMITED_INFORMATION | windows.PROCESS_TERMINATE

	handle, err := windows.OpenProcess(access, false, uint32(pid))
	if err != nil {
		return false // already gone, or not ours to touch
	}
	defer windows.CloseHandle(handle)

	if !processImageIs(handle, name) {
		return false
	}
	return windows.TerminateProcess(handle, 1) == nil
}

func processImageIs(handle windows.Handle, name string) bool {
	buffer := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buffer))

	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return false
	}

	image := filepath.Base(windows.UTF16ToString(buffer[:size]))
	return strings.EqualFold(image, name+".exe")
}
