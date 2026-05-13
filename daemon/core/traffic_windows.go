//go:build windows

package core

import (
	"os/exec"
	"strconv"
	"strings"
)

// readNetStats returns total bytes (received, sent) across all active adapters
// by parsing `netstat -e` output. Used to compute traffic delta during VPN session.
func readNetStats() (recv int64, sent int64, err error) {
	out, err := exec.Command("netstat", "-e").Output()
	if err != nil {
		return 0, 0, err
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Bytes") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				recv, _ = strconv.ParseInt(fields[1], 10, 64)
				sent, _ = strconv.ParseInt(fields[2], 10, 64)
				return recv, sent, nil
			}
		}
	}
	return 0, 0, nil
}
