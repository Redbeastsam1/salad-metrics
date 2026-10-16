package config

import "flag"

// Config holds all runtime configuration parsed from CLI flags.
type Config struct {
	Port           int
	MachineName    string
	LogDir         string
	DBFile         string
	DiscordEnabled bool
	DiscordToken   string
}

// Parse reads CLI flags and returns a populated Config.
func Parse() *Config {
	cfg := &Config{}
	flag.IntVar(&cfg.Port, "port", 8000, "HTTP server port")
	flag.StringVar(&cfg.MachineName, "machine-name", "Beast", "Display name for this machine")
	flag.StringVar(&cfg.LogDir, "log-dir", `C:\ProgramData\Salad\logs`, "Path to Salad log directory")
	flag.StringVar(&cfg.DBFile, "db", "salad_metrics.db", "Path to SQLite database file")
	flag.BoolVar(&cfg.DiscordEnabled, "discord", false, "Enable the Discord bot")
	flag.StringVar(&cfg.DiscordToken, "discord-token", "", "Discord bot token (or set DISCORD_BOT_TOKEN env var)")
	flag.Parse()
	return cfg
}
