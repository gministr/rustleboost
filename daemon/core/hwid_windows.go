//go:build windows

package core

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
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

// These values are read through the registry API instead of by running
// reg.exe and parsing its console output. Beyond being more robust — no
// locale-dependent text to scrape — it removes a burst of child processes
// that a hidden, unsigned executable fires off at startup, which is a shape
// behavioural scanners treat as reconnaissance.
func readRegistryString(root registry.Key, path, name string) (string, bool) {
	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer key.Close()

	value, _, err := key.GetStringValue(name)
	if err != nil {
		return "", false
	}
	value = strings.TrimSpace(value)
	return value, value != ""
}

// machineID reads MachineGuid, which is present on every Windows install and
// already in UUID form. Falls back to a hash of hardware identifiers.
func machineID() string {
	if guid, ok := readRegistryString(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Cryptography`, "MachineGuid"); ok && len(guid) >= 32 {
		if len(guid) > 36 {
			guid = guid[:36]
		}
		return strings.ToUpper(guid)
	}
	return fallbackID()
}

func fallbackID() string {
	if serial, ok := readRegistryString(registry.LOCAL_MACHINE,
		`HARDWARE\DESCRIPTION\System\BIOS`, "SystemSerialNumber"); ok && serial != "(null)" {
		h := sha256.Sum256([]byte(serial))
		return fmt.Sprintf("%X", h[:16])
	}

	hostname, _ := os.Hostname()
	h := sha256.Sum256([]byte(hostname))
	return fmt.Sprintf("%X", h[:16])
}

func windowsVersion() string {
	if version, ok := readRegistryString(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "DisplayVersion"); ok {
		return version
	}
	return "11"
}

func machineModel() string {
	model, ok := readRegistryString(registry.LOCAL_MACHINE,
		`HARDWARE\DESCRIPTION\System\BIOS`, "SystemProductName")
	if ok && !strings.EqualFold(model, "System Product Name") {
		return model
	}
	return "Windows PC"
}
