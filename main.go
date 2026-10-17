package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"salad-metrics/internal/agent"
	"salad-metrics/internal/api"
	"salad-metrics/internal/config"
	"salad-metrics/internal/db"
	"salad-metrics/internal/discord"
	"salad-metrics/internal/hardware"
	hubPkg "salad-metrics/internal/hub"
	"salad-metrics/internal/tray"
	"salad-metrics/internal/watcher"
	"salad-metrics/internal/ws"
)

func main() {
	// 1. Parse CLI config & environment variables
	cfg := config.Parse()

	// In standalone mode, if an existing instance of Salad Metrics is already running on this port,
	// simply open the dashboard in the default browser and exit cleanly.
	if cfg.Mode == "standalone" {
		client := &http.Client{Timeout: 800 * time.Millisecond}
		if resp, err := client.Get(fmt.Sprintf("http://localhost:%d/api/stats", cfg.Port)); err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			log.Printf("[main] Salad Metrics is already running on port %d. Opening dashboard in browser...", cfg.Port)
			tray.OpenBrowser(cfg.Port)
			time.Sleep(500 * time.Millisecond)
			return
		}
	}

	log.Printf("=================================================")
	log.Printf("  🥗 Salad Metrics (Go Edition)")
	log.Printf("  Mode    : %s", cfg.Mode)
	log.Printf("  Machine : %s", cfg.MachineName)
	if cfg.Mode == "agent" || cfg.Mode == "worker" {
		log.Printf("  Hub URL : %s", cfg.ServerURL)
	} else {
		log.Printf("  Port    : %d", cfg.Port)
	}
	log.Printf("  Log Dir : %s", cfg.LogDir)
	log.Printf("  Tray    : %v (hide console: %v)", cfg.TrayEnabled, cfg.HideConsole)
	log.Printf("=================================================")

	// ── Agent (Worker) Mode ──
	if cfg.Mode == "agent" || cfg.Mode == "worker" {
		agent.Run(cfg)
		return
	}

	// ── Server (Hub) & Standalone Modes ──
	// 2. Open SQLite database (WAL mode, indexes, connection pool)
	database, err := db.New(cfg.DBFile)
	if err != nil {
		log.Fatalf("[main] Failed to open database: %v", err)
	}

	// 3. Create WebSocket hub
	hub := ws.NewHub()

	// 4. Register Discord alert callback and start crash watchdog
	hubPkg.RegisterAlertCallback(discord.SendCrashAlert)
	hubPkg.StartWatchdog(hub, 45*time.Second)

	// 5. Start background routines
	if cfg.Mode != "server" && cfg.Mode != "hub" {
		// Standalone mode: sample local hardware & watch local logs
		hardware.StartPoller(database)
		go watcher.Run(cfg.LogDir, database, hub)
	} else {
		log.Println("[server] Running in Hub Server mode. Waiting for agents to report...")
	}

	// Discord bot (only if -discord flag is set)
	if cfg.DiscordEnabled {
		go discord.Run(cfg)
	}

	// Windows system tray icon (pure Go, zero CGO)
	if cfg.TrayEnabled {
		tray.Start(cfg.Port, cfg.MachineName, cfg.HideConsole)
		defer tray.Remove()
	}

	// In standalone mode, automatically open the dashboard in default browser on launch
	if cfg.Mode == "standalone" {
		go func() {
			time.Sleep(600 * time.Millisecond)
			tray.OpenBrowser(cfg.Port)
		}()
	}

	// 6. Start HTTP server (blocks main goroutine)
	api.Serve(cfg, database, hub)
}
