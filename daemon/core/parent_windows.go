//go:build windows

package core

import (
	"log"
	"os"
	"syscall"
)

const (
	synchronize     = 0x00100000
	waitObject0     = 0x00000000
	waitTimeout     = 0x00000102
	infinite        = 0xFFFFFFFF
)

// WatchParentProcess monitors the parent process (linda-vpn.exe) and
// shuts down the daemon when the parent exits. This ensures daemon.exe
// is not locked when the NSIS installer tries to overwrite it on update.
func WatchParentProcess(onExit func()) {
	ppid := os.Getppid()
	if ppid == 0 || ppid == 1 {
		return
	}

	handle, err := syscall.OpenProcess(synchronize, false, uint32(ppid))
	if err != nil {
		log.Printf("[parent-watch] cannot open parent PID %d: %v — skipping watch", ppid, err)
		return
	}

	log.Printf("[parent-watch] watching parent PID %d", ppid)

	// Block until the parent process exits
	syscall.WaitForSingleObject(handle, infinite)
	syscall.CloseHandle(handle)

	log.Println("[parent-watch] parent process exited, shutting down daemon")
	onExit()
}
