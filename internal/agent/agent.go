package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"salad-metrics/internal/config"
	"salad-metrics/internal/db"
	"salad-metrics/internal/hardware"
	"salad-metrics/internal/hub"
	"salad-metrics/internal/parser"
	"salad-metrics/internal/tray"
)

// Run starts the Salad Metrics agent worker.
// It monitors local logs and hardware, sending telemetry & heartbeats to the Hub server.
func Run(cfg *config.Config) {
	if cfg.ServerURL == "" {
		log.Fatalf("[agent] Error: -server flag is required in agent mode (e.g. -server http://192.168.1.100:8000)")
	}

	log.Printf("=================================================")
	log.Printf("  🥗 Salad Metrics Agent (Worker Mode)")
	log.Printf("  Machine : %s", cfg.MachineName)
	log.Printf("  Hub URL : %s", cfg.ServerURL)
	log.Printf("  Log Dir : %s", cfg.LogDir)
	log.Printf("=================================================")

	// 1. Parse local static hardware information
	sysDir := filepath.Join(cfg.LogDir, "systeminformation")
	sysInfo := parser.ParseSystemInfo(sysDir)

	// 2. Start hardware poller (no local database; caches live metrics in-memory)
	hardware.StartPoller(nil)

	// 3. Start Windows system tray icon (if enabled)
	if cfg.TrayEnabled {
		// In agent mode, opening the browser will open the Hub dashboard
		tray.Start(cfg.Port, fmt.Sprintf("%s (Agent)", cfg.MachineName), cfg.HideConsole)
		defer tray.Remove()
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	ingestURL := fmt.Sprintf("%s/api/ingest", strings.TrimRight(cfg.ServerURL, "/"))

	var (
		mu           sync.Mutex
		pendingW     []db.WalletRow
		pendingE     []db.EarningsRow
		pendingBW    []db.BandwidthRow
		pendingErr   []db.ErrorRow
		pendingPower []db.PowerRow
	)

	// Background log scanner
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			_ = filepath.Walk(cfg.LogDir, func(path string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return nil
				}
				ext := strings.ToLower(filepath.Ext(path))
				if ext != ".txt" && ext != ".log" {
					return nil
				}
				base := filepath.Base(path)
				if !strings.HasPrefix(base, "log-") && !strings.Contains(filepath.Dir(path), "Bandwidth-SGS-") {
					return nil
				}

				isBandwidth := strings.Contains(filepath.Dir(path), "Bandwidth-SGS-")
				data, hasData := parser.ParseFile(path, isBandwidth)
				if hasData {
					mu.Lock()
					pendingW = append(pendingW, data.Wallet...)
					pendingE = append(pendingE, data.Earnings...)
					pendingBW = append(pendingBW, data.Bandwidth...)
					pendingErr = append(pendingErr, data.Errors...)
					mu.Unlock()
				}
				return nil
			})
		}
	}()

	// Ingest & heartbeat transmit loop
	tickCount := 0
	sendTicker := time.NewTicker(3 * time.Second)
	defer sendTicker.Stop()

	log.Printf("[agent] Streaming telemetry and logs to %s...", ingestURL)

	for range sendTicker.C {
		hw := hardware.Get()

		mu.Lock()
		// Capture power readings every 30 seconds
		if tickCount%10 == 0 && hw.TotalPower > 0 {
			ts := time.Now().Format("2006-01-02 15:04:05")
			pendingPower = append(pendingPower, db.PowerRow{
				Timestamp:  ts,
				GPUPower:   hw.GPUPower,
				CPUPower:   hw.CPUPower,
				TotalPower: hw.TotalPower,
			})
		}
		tickCount++

		wToSend := pendingW
		eToSend := pendingE
		bwToSend := pendingBW
		errToSend := pendingErr
		powerToSend := pendingPower

		payload := hub.IngestPayload{
			MachineName: cfg.MachineName,
			Timestamp:   time.Now().Format(time.RFC3339),
			Hardware:    &hw,
			SystemInfo:  &sysInfo,
			Wallet:      wToSend,
			Earnings:    eToSend,
			Bandwidth:   bwToSend,
			Errors:      errToSend,
			Power:       powerToSend,
		}
		mu.Unlock()

		body, err := json.Marshal(payload)
		if err != nil {
			log.Printf("[agent] JSON marshal error: %v", err)
			continue
		}

		resp, err := client.Post(ingestURL, "application/json", bytes.NewReader(body))
		if err != nil {
			log.Printf("[agent] ⚠️ Hub unreachable (%v) — retrying...", err)
			// Keep pending items in queue for next tick
			continue
		}

		if resp.StatusCode == http.StatusOK {
			// Clear successfully transmitted log batches
			mu.Lock()
			pendingW = pendingW[len(wToSend):]
			pendingE = pendingE[len(eToSend):]
			pendingBW = pendingBW[len(bwToSend):]
			pendingErr = pendingErr[len(errToSend):]
			pendingPower = pendingPower[len(powerToSend):]
			mu.Unlock()
		} else {
			respBody, _ := io.ReadAll(resp.Body)
			log.Printf("[agent] Hub returned status %d: %s", resp.StatusCode, string(respBody))
		}
		resp.Body.Close()
	}
}
