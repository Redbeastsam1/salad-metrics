package main

import (
	"log"

	"salad-metrics/internal/api"
	"salad-metrics/internal/config"
	"salad-metrics/internal/db"
	"salad-metrics/internal/discord"
	"salad-metrics/internal/hardware"
	"salad-metrics/internal/tray"
	"salad-metrics/internal/watcher"
	"salad-metrics/internal/ws"
)

func main() {
	// 1. Parse CLI config
	cfg := config.Parse()

	log.Printf("=================================================")
	log.Printf("  🥗 Salad Metrics (Go Edition)")
	log.Printf("  Machine : %s", cfg.MachineName)
	log.Printf("  Port    : %d", cfg.Port)
	log.Printf("  Log Dir : %s", cfg.LogDir)
	log.Printf("  Tray    : %v (hide console: %v)", cfg.TrayEnabled, cfg.HideConsole)
	log.Printf("=================================================")

	// 2. Open SQLite database (WAL mode, indexes, connection pool)
	database, err := db.New(cfg.DBFile)
	if err != nil {
		log.Fatalf("[main] Failed to open database: %v", err)
	}

	// 3. Create WebSocket hub
	hub := ws.NewHub()

	// 4. Start background goroutines
	// Hardware poller: refreshes CPU/GPU/RAM every 2 seconds, cached atomically,
	// and logs power readings to SQLite every 30 seconds
	hardware.StartPoller(database)

	// File watcher: uses OS filesystem events (ReadDirectoryChangesW on Windows)
	// — replaces the old 15-second Python polling loop
	go watcher.Run(cfg.LogDir, database, hub)

	// Discord bot (only if -discord flag is set)
	if cfg.DiscordEnabled {
		go discord.Run(cfg)
	}

	// Windows system tray icon (pure Go, zero CGO)
	if cfg.TrayEnabled {
		tray.Start(cfg.Port, cfg.MachineName, cfg.HideConsole)
		defer tray.Remove()
	}

	// 5. Start HTTP server (blocks main goroutine)
	api.Serve(cfg, database, hub)
}
