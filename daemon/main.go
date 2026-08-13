package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/vpnclient/daemon/api"
	"github.com/vpnclient/daemon/core"
	"github.com/vpnclient/daemon/storage"
)

func main() {
	port := flag.String("port", "7777", "API server port")
	dataDir := flag.String("data", defaultDataDir(), "Data directory")
	flag.Parse()

	if err := os.MkdirAll(*dataDir, 0755); err != nil {
		log.Fatalf("Failed to create data dir: %v", err)
	}

	// A previous run may have been killed before it could stop its cores.
	core.CleanupStaleCores(*dataDir)

	store := storage.New(filepath.Join(*dataDir, "settings.json"))
	if err := store.Load(); err != nil {
		log.Printf("No existing settings, using defaults: %v", err)
	}

	manager := core.NewManager(*dataDir, store)

	settings := store.GetSettings()
	if settings.AutoConnect && settings.LastServerID != "" {
		go manager.Connect(settings.LastServerID)
	}

	server := api.NewServer(manager, store, "localhost:"+*port)

	log.Printf("VPN daemon starting on port %s", *port)

	// Monitor parent process (linda-vpn.exe) вЂ” exit cleanly when it closes.
	// This ensures daemon.exe is not locked when the NSIS installer overwrites it.
	go core.WatchParentProcess(func() {
		manager.Stop()
		os.Exit(0)
	})

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := server.Start(); err != nil {
			log.Printf("API server error: %v", err)
		}
	}()

	<-sig
	log.Println("Shutting down...")
	manager.Stop()
}

func defaultDataDir() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, _ := os.UserHomeDir()
		appData = home
	}
	return filepath.Join(appData, "RustleBoost")
}

