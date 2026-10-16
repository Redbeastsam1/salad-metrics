package discord

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/bwmarrin/discordgo"

	"salad-metrics/internal/config"
)

var (
	cfg    *config.Config
	apiURL string
)

// Run starts the Discord bot. Blocks until the process is killed.
func Run(c *config.Config) {
	cfg = c
	apiURL = fmt.Sprintf("http://127.0.0.1:%d/api", cfg.Port)

	token := cfg.DiscordToken
	if token == "" {
		token = os.Getenv("DISCORD_BOT_TOKEN")
	}
	if token == "" {
		log.Println("[discord] No token found. Bot disabled.")
		return
	}

	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Printf("[discord] Failed to create session: %v", err)
		return
	}

	dg.AddHandler(onReady)
	dg.AddHandler(onMessage)
	dg.Identify.Intents = discordgo.IntentsGuildMessages

	if err := dg.Open(); err != nil {
		log.Printf("[discord] Failed to open connection: %v", err)
		return
	}

	log.Printf("[discord] Bot running. Monitoring %s.", cfg.MachineName)
	select {} // Block forever
}

func onReady(s *discordgo.Session, e *discordgo.Ready) {
	log.Printf("[discord] Logged in as %s.", e.User.Username)
}

func onMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	switch m.Content {
	case "?status":
		handleStatus(s, m)
	case "?errors":
		handleErrors(s, m)
	}
}

// ─── Commands ─────────────────────────────────────────────────────────────────

func handleStatus(s *discordgo.Session, m *discordgo.MessageCreate) {
	data, err := fetchJSON(apiURL + "/data?hours=1")
	if err != nil {
		s.ChannelMessageSend(m.ChannelID, "⚠️ **Could not connect to Salad Metrics API.**")
		return
	}
	stats, _ := fetchJSON(apiURL + "/stats")
	hw, _ := fetchJSON(apiURL + "/hardware-live")

	// Wallet balance
	currentBalance := 0.0
	if wallets, ok := data["wallet"].([]any); ok && len(wallets) > 0 {
		if last, ok := wallets[len(wallets)-1].(map[string]any); ok {
			currentBalance, _ = last["current"].(float64)
		}
	}

	// Active container
	activeContainer := "None"
	rate := 0.0
	if earnings, ok := data["earnings"].([]any); ok && len(earnings) > 0 {
		if last, ok := earnings[len(earnings)-1].(map[string]any); ok {
			if id, ok := last["container_id"].(string); ok && len(id) >= 8 {
				activeContainer = fmt.Sprintf("`%s...`", id[:8])
			}
			rate, _ = last["rate"].(float64)
		}
	}

	status := "🟡 Sitting Idle"
	color := 0xFFD700 // gold
	if rate > 0 {
		status = "🟢 Running"
		color = 0x00FF88 // neon green
	}

	embed := &discordgo.MessageEmbed{
		Title: fmt.Sprintf("🥗 %s | Live Status", cfg.MachineName),
		Color: color,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "💰 Wallet Balance", Value: fmt.Sprintf("$%.4f", currentBalance), Inline: true},
			{Name: "🖥️ Status", Value: status, Inline: true},
			{Name: "⏱️ Session Uptime", Value: safeStr(stats, "uptime", "0h 0m"), Inline: true},
			{Name: "📦 Active Container", Value: activeContainer, Inline: false},
			{Name: "🧠 CPU Load", Value: fmt.Sprintf("%.1f%%", safeFloat(hw, "cpu_load")), Inline: true},
			{Name: "🎮 GPU Load", Value: fmt.Sprintf("%.1f%% (%.0f°C)", safeFloat(hw, "gpu_load"), safeFloat(hw, "gpu_temp")), Inline: true},
			{Name: "💾 RAM Usage", Value: fmt.Sprintf("%.1f%%", safeFloat(hw, "ram_load")), Inline: true},
			{Name: "⚡ Power Draw", Value: fmt.Sprintf("GPU: **%.0fW** | CPU: **~%.0fW** | Total: **~%.0fW**", safeFloat(hw, "gpu_power"), safeFloat(hw, "cpu_power"), safeFloat(hw, "total_power")), Inline: false},
		},
	}
	s.ChannelMessageSendEmbed(m.ChannelID, embed)
}

func handleErrors(s *discordgo.Session, m *discordgo.MessageCreate) {
	errors, err := fetchJSONArray(apiURL + "/errors")
	if err != nil {
		s.ChannelMessageSend(m.ChannelID, "⚠️ **Could not connect to Salad Metrics API.**")
		return
	}

	if len(errors) == 0 {
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("✅ **No active errors!** `%s` is running smoothly.", cfg.MachineName))
		return
	}

	embed := &discordgo.MessageEmbed{
		Title: fmt.Sprintf("⚠️ %s | Active Errors", cfg.MachineName),
		Color: 0xFF4757,
	}

	limit := 5
	if len(errors) < limit {
		limit = len(errors)
	}
	for _, e := range errors[:limit] {
		if errMap, ok := e.(map[string]any); ok {
			ts, _ := errMap["timestamp"].(string)
			msg, _ := errMap["message"].(string)
			if len(msg) > 200 {
				msg = msg[:200]
			}
			embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
				Name:  fmt.Sprintf("Time: %s", ts),
				Value: fmt.Sprintf("```\n%s\n```", msg),
			})
		}
	}

	if len(errors) > 5 {
		embed.Footer = &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("...and %d more. Check the local dashboard.", len(errors)-5),
		}
	}
	s.ChannelMessageSendEmbed(m.ChannelID, embed)
}

// ─── HTTP Helpers ─────────────────────────────────────────────────────────────

func fetchJSON(url string) (map[string]any, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	json.Unmarshal(body, &result)
	return result, nil
}

func fetchJSONArray(url string) ([]any, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result []any
	json.Unmarshal(body, &result)
	return result, nil
}

func safeStr(m map[string]any, key, fallback string) string {
	if m == nil {
		return fallback
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return fallback
}

func safeFloat(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}
