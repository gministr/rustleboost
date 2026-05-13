//go:build !windows

package core

import (
	"crypto/sha256"
	"fmt"
	"os"
	"runtime"
	"strings"
)

type HWIDInfo struct {
	HWID  string
	OS    string
	OSVer string
	Model string
}

func GetHWIDInfo() HWIDInfo {
	return HWIDInfo{
		HWID:  machineID(),
		OS:    runtime.GOOS,
		OSVer: "",
		Model: "",
	}
}

func machineID() string {
	for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		data, err := os.ReadFile(path)
		if err == nil {
			id := strings.TrimSpace(string(data))
			if len(id) > 36 {
				id = id[:36]
			}
			return strings.ToUpper(id)
		}
	}
	hostname, _ := os.Hostname()
	h := sha256.Sum256([]byte(hostname))
	return fmt.Sprintf("%X", h[:16])
}
