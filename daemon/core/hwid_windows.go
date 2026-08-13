//go:build windows

package core

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// HWIDInfo holds device identification data sent to Remnawave.
type HWIDInfo struct {
	HWID  string // max 36 chars — sent as x-hwid
	OS    string // sent as x-device-os
	OSVer string // sent as x-ver-os
	Model string // sent as x-device-model
}

func GetHWIDInfo() HWIDInfo {
	return HWIDInfo{
		HWID:  machineID(),
		OS:    "Windows",
		OSVer: windowsVersion(),
		Model: machineModel(),
	}
}

// machineID reads MachineGuid from Windows registry — reliable on all Windows versions.
// Falls back to disk serial hash if registry read fails.
func machineID() string {
	// Primary: Windows registry MachineGuid (always present, UUID format, ≤36 chars)
	out, err := hiddenCommand(
		"reg", "query",
		`HKLM\SOFTWARE\Microsoft\Cryptography`,
		"/v", "MachineGuid",
	).Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.Contains(line, "MachineGuid") {
				parts := strings.Fields(line)
				if len(parts) >= 3 {
					guid := strings.TrimSpace(parts[len(parts)-1])
					if len(guid) >= 32 {
						if len(guid) > 36 {
							guid = guid[:36]
						}
						return strings.ToUpper(guid)
					}
				}
			}
		}
	}

	// Fallback: SHA256 of disk serial (32 hex chars)
	return diskSerialHash()
}

func diskSerialHash() string {
	out, err := hiddenCommand(
		"reg", "query",
		`HKLM\HARDWARE\DESCRIPTION\System\BIOS`,
		"/v", "SystemSerialNumber",
	).Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "SystemSerialNumber") {
				parts := strings.Fields(strings.TrimSpace(line))
				if len(parts) >= 3 {
					serial := parts[len(parts)-1]
					if serial != "" && serial != "(null)" {
						h := sha256.Sum256([]byte(serial))
						return fmt.Sprintf("%X", h[:16])
					}
				}
			}
		}
	}

	// Last resort: hostname hash
	hostname, _ := hiddenCommand("hostname").Output()
	h := sha256.Sum256(hostname)
	return fmt.Sprintf("%X", h[:16])
}

func windowsVersion() string {
	// Read from registry — more reliable than cmd /c ver
	out, err := hiddenCommand(
		"reg", "query",
		`HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion`,
		"/v", "DisplayVersion",
	).Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "DisplayVersion") {
				parts := strings.Fields(strings.TrimSpace(line))
				if len(parts) >= 3 {
					return parts[len(parts)-1]
				}
			}
		}
	}
	return "11"
}

func machineModel() string {
	out, err := hiddenCommand(
		"reg", "query",
		`HKLM\HARDWARE\DESCRIPTION\System\BIOS`,
		"/v", "SystemProductName",
	).Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "SystemProductName") {
				parts := strings.Fields(strings.TrimSpace(line))
				if len(parts) >= 3 {
					model := strings.Join(parts[2:], " ")
					model = strings.TrimSpace(model)
					if model != "" && !strings.EqualFold(model, "System Product Name") {
						return model
					}
				}
			}
		}
	}
	return "Windows PC"
}
