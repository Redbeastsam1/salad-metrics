package api

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"time"

	"salad-metrics/internal/config"
	"salad-metrics/internal/db"
	"salad-metrics/internal/hardware"
	hubPkg "salad-metrics/internal/hub"
	"salad-metrics/internal/parser"
	"salad-metrics/internal/ws"
)

var (
	sessionStart = time.Now()
	database     *db.DB
	hub          *ws.Hub
	cfg          *config.Config
)

// Serve wires up all HTTP routes and starts the server. Blocks forever.
func Serve(c *config.Config, d *db.DB, h *ws.Hub) {
	cfg = c
	database = d
	hub = h

	mux := http.NewServeMux()

	// Static assets
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// Pages
	mux.HandleFunc("/", serveIndex)

	// WebSocket endpoint
	mux.HandleFunc("/ws", hub.ServeWS)

	// REST API
	mux.HandleFunc("/api/nodes", withCORS(apiNodes))
	mux.HandleFunc("/api/data", withCORS(apiData))
	mux.HandleFunc("/api/stats", withCORS(apiStats))
	mux.HandleFunc("/api/ingest", withCORS(apiIngest))
	mux.HandleFunc("/api/errors", withCORS(apiErrors))
	mux.HandleFunc("/api/dismiss-error", withCORS(apiDismissError))
	mux.HandleFunc("/api/dismiss-all-errors", withCORS(apiDismissAllErrors))
	mux.HandleFunc("/api/system-info", withCORS(apiSystemInfo))
	mux.HandleFunc("/api/hardware-live", withCORS(apiHardwareLive))
	mux.HandleFunc("/api/export", apiExport)

	addr := fmt.Sprintf("0.0.0.0:%d", cfg.Port)
	log.Printf("[server] 🥗 Salad Metrics running at http://localhost:%d", cfg.Port)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("[server] Fatal: %v", err)
	}
}

// ─── Middleware ───────────────────────────────────────────────────────────────

func withCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json")
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	json.NewEncoder(w).Encode(v)
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

func serveIndex(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "templates/index.html")
}

func apiNodes(w http.ResponseWriter, r *http.Request) {
	nodes := hubPkg.GetAllMachines(database)
	if len(nodes) == 0 {
		// Single-node fallback
		workload := database.GetActiveWorkload(cfg.MachineName)
		uptime, sessEarn := database.GetSessionStats(cfg.MachineName)
		hw := hardware.Get()
		sys := parser.GetCachedSystemInfo()
		nodes = []hubPkg.MachineState{
			{
				Name:         cfg.MachineName,
				IsOnline:     true,
				LastSeenSecs: 0,
				Status:       workload.Status,
				Rate:         workload.Rate,
				ContainerID:  workload.ContainerID,
				Uptime:       uptime,
				SessionEarn:  sessEarn,
				Hardware:     &hw,
				SystemInfo:   sys,
			},
		}
	}
	writeJSON(w, nodes)
}

func apiData(w http.ResponseWriter, r *http.Request) {
	hoursStr := r.URL.Query().Get("hours")
	hours := 24
	if hoursStr != "" {
		if h, err := strconv.Atoi(hoursStr); err == nil {
			hours = h
		}
	}
	machine := r.URL.Query().Get("machine")
	if machine == "" || machine == "all" {
		machine = cfg.MachineName
	}
	writeJSON(w, database.GetData(hours, machine))
}

func apiStats(w http.ResponseWriter, r *http.Request) {
	machine := r.URL.Query().Get("machine")
	if machine == "" || machine == "all" {
		machine = cfg.MachineName
	}

	workload := database.GetActiveWorkload(machine)
	uptimeStr, sessionEarnings := database.GetSessionStats(machine)
	latestWallet := database.GetLatestWallet(machine)
	var walletBal float64
	if latestWallet != nil {
		walletBal = latestWallet.Current
	}

	writeJSON(w, map[string]any{
		"uptime":              uptimeStr,
		"session_earnings":    sessionEarnings,
		"wallet_balance":      walletBal,
		"machine_name":        machine,
		"status":              workload.Status,
		"current_rate":        workload.Rate,
		"active_container_id": workload.ContainerID,
	})
}

func apiIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload hubPkg.IngestPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, fmt.Sprintf("Bad request: %v", err), http.StatusBadRequest)
		return
	}
	hubPkg.ProcessIngest(database, hub, &payload)
	writeJSON(w, map[string]any{"status": "success"})
}

func apiErrors(w http.ResponseWriter, r *http.Request) {
	machine := r.URL.Query().Get("machine")
	writeJSON(w, database.GetActiveErrors(machine))
}

func apiDismissError(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Timestamp string `json:"timestamp"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	database.DismissError(body.Timestamp)
	writeJSON(w, map[string]any{"status": "success"})
}

func apiDismissAllErrors(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	database.DismissAllErrors()
	writeJSON(w, map[string]any{"status": "success"})
}

func apiSystemInfo(w http.ResponseWriter, r *http.Request) {
	machine := r.URL.Query().Get("machine")
	if machine != "" && machine != "all" {
		if info := hubPkg.GetMachineSysInfo(machine); info != nil {
			writeJSON(w, info)
			return
		}
	}
	if cached := parser.GetCachedSystemInfo(); cached != nil {
		writeJSON(w, cached)
		return
	}
	sysDir := cfg.LogDir + `\systeminformation`
	info := parser.ParseSystemInfo(sysDir)
	writeJSON(w, info)
}

func apiHardwareLive(w http.ResponseWriter, r *http.Request) {
	machine := r.URL.Query().Get("machine")
	if machine != "" && machine != "all" {
		hw := hubPkg.GetMachineHardware(machine)
		if hw != nil {
			writeJSON(w, hw)
			return
		}
	}

	allNodes := hubPkg.GetAllMachines(database)
	if (machine == "all" || machine == "") && len(allNodes) > 1 {
		var totalPower, gpuPower, cpuPower, gpuLimit float64
		var cpuLoadSum, ramLoadSum, gpuLoadSum, gpuTempSum float64
		onlineCount := 0

		for _, n := range allNodes {
			if n.IsOnline && n.Hardware != nil {
				onlineCount++
				totalPower += n.Hardware.TotalPower
				gpuPower += n.Hardware.GPUPower
				cpuPower += n.Hardware.CPUPower
				gpuLimit += n.Hardware.GPULimit
				cpuLoadSum += n.Hardware.CPULoad
				ramLoadSum += n.Hardware.RAMLoad
				gpuLoadSum += n.Hardware.GPULoad
				gpuTempSum += n.Hardware.GPUTemp
			}
		}

		if onlineCount > 0 {
			agg := hardware.Stats{
				CPULoad:     math.Round((cpuLoadSum/float64(onlineCount))*10) / 10,
				RAMLoad:     math.Round((ramLoadSum/float64(onlineCount))*10) / 10,
				GPULoad:     math.Round((gpuLoadSum/float64(onlineCount))*10) / 10,
				GPUTemp:     math.Round((gpuTempSum/float64(onlineCount))*10) / 10,
				CPUPower:    math.Round(cpuPower*10) / 10,
				GPUPower:    math.Round(gpuPower*10) / 10,
				GPULimit:    math.Round(gpuLimit*10) / 10,
				TotalPower:  math.Round(totalPower*10) / 10,
				CPUPowerEst: true,
			}
			writeJSON(w, agg)
			return
		}
	}

	writeJSON(w, hardware.Get())
}

func apiExport(w http.ResponseWriter, r *http.Request) {
	table := r.URL.Query().Get("type")
	machine := r.URL.Query().Get("machine")
	if table == "" {
		table = "earnings"
	}
	validTables := map[string]bool{
		"earnings":  true,
		"wallet":    true,
		"bandwidth": true,
		"power":     true,
		"errors":    true,
	}
	if !validTables[table] {
		http.Error(w, "Invalid export type", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"salad_%s_%s.csv\"", table, time.Now().Format("20060102_150405")))
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if err := database.ExportCSV(w, table, machine); err != nil {
		log.Printf("[export] Error generating CSV for %s: %v", table, err)
	}
}
