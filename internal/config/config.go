package config

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// Config holds all runtime configuration parsed from CLI flags and environment variables.
type Config struct {
	Mode           string // "standalone", "server" (hub), "agent" (worker)
	ServerURL      string // e.g. "http://192.168.1.100:8000" (agent mode)
	Port           int
	MachineName    string
	LogDir         string
	DBFile         string
	DiscordEnabled bool
	DiscordToken   string
	TrayEnabled    bool
	HideConsole    bool
}

// Parse reads CLI flags and environment variables and returns a populated Config.
func Parse() *Config {
	cfg := &Config{}
	flag.StringVar(&cfg.Mode, "mode", "standalone", "Run mode: standalone, server, or agent")
	flag.StringVar(&cfg.ServerURL, "server", "", "Hub server URL for agent mode (e.g. http://192.168.1.100:8000)")
	flag.IntVar(&cfg.Port, "port", 8000, "HTTP server port")
	flag.StringVar(&cfg.MachineName, "machine-name", "Beast", "Display name for this machine")
	flag.StringVar(&cfg.LogDir, "log-dir", `C:\ProgramData\Salad\logs`, "Path to Salad log directory")
	flag.StringVar(&cfg.DBFile, "db", "salad_metrics.db", "Path to SQLite database file")
	flag.BoolVar(&cfg.DiscordEnabled, "discord", false, "Enable the Discord bot")
	flag.StringVar(&cfg.DiscordToken, "discord-token", "", "Discord bot token (or set DISCORD_BOT_TOKEN env var)")
	flag.BoolVar(&cfg.TrayEnabled, "tray", true, "Enable Windows system tray icon")
	flag.BoolVar(&cfg.HideConsole, "hide-console", false, "Hide the console window on startup (runs silently in tray)")
	flag.BoolVar(&cfg.HideConsole, "silent", false, "Alias for -hide-console")
	flag.Parse()

	// Environment variable overrides / fallbacks
	if envMode := os.Getenv("SALAD_MODE"); envMode != "" && cfg.Mode == "standalone" {
		cfg.Mode = envMode
	}
	if envServer := os.Getenv("SERVER_URL"); envServer != "" && cfg.ServerURL == "" {
		cfg.ServerURL = envServer
	}
	if envMachine := os.Getenv("MACHINE_NAME"); envMachine != "" && cfg.MachineName == "Beast" {
		cfg.MachineName = envMachine
	}
	if envDB := os.Getenv("SALAD_DB_FILE"); envDB != "" && cfg.DBFile == "salad_metrics.db" {
		cfg.DBFile = envDB
	}
	if envPort := os.Getenv("SALAD_PORT"); envPort != "" {
		var p int
		if _, err := fmt.Sscanf(envPort, "%d", &p); err == nil && p > 0 {
			cfg.Port = p
		}
	} else if envPort := os.Getenv("PORT"); envPort != "" {
		var p int
		if _, err := fmt.Sscanf(envPort, "%d", &p); err == nil && p > 0 {
			cfg.Port = p
		}
	}
	if envToken := os.Getenv("DISCORD_BOT_TOKEN"); envToken != "" && cfg.DiscordToken == "" {
		cfg.DiscordToken = envToken
		cfg.DiscordEnabled = true
	}

	cfg.Mode = strings.ToLower(strings.TrimSpace(cfg.Mode))
	if cfg.ServerURL != "" {
		cfg.ServerURL = strings.TrimRight(cfg.ServerURL, "/")
		if cfg.Mode == "standalone" {
			cfg.Mode = "agent"
		}
	}

	return cfg
}
