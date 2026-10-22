package hub

import (
	"log"
	"sync"
	"time"

	"salad-metrics/internal/db"
	"salad-metrics/internal/hardware"
	"salad-metrics/internal/parser"
	"salad-metrics/internal/ws"
)

// IngestPayload is the telemetry and log payload sent by an Agent to the Hub server.
type IngestPayload struct {
	MachineName string            `json:"machine_name"`
	Machine     string            `json:"machine,omitempty"`
	Timestamp   string            `json:"timestamp"`
	Status      string            `json:"status,omitempty"`
	CurrentRate float64           `json:"current_rate,omitempty"`
	ContainerID string            `json:"container_id,omitempty"`
	Hardware    *hardware.Stats   `json:"hardware,omitempty"`
	SystemInfo  *parser.SysInfo   `json:"system_info,omitempty"`
	Wallet      []db.WalletRow    `json:"wallet,omitempty"`
	Earnings    []db.EarningsRow  `json:"earnings,omitempty"`
	Bandwidth   []db.BandwidthRow `json:"bandwidth,omitempty"`
	Errors      []db.ErrorRow     `json:"errors,omitempty"`
	Power       []db.PowerRow     `json:"power,omitempty"`
}

// MachineState tracks the online/offline state and telemetry of an Agent machine.
type MachineState struct {
	Name         string            `json:"name"`
	LastSeen     time.Time         `json:"last_seen"`
	IsOnline     bool              `json:"is_online"`
	LastSeenSecs int64             `json:"last_seen_secs"`
	Hardware     *hardware.Stats   `json:"hardware,omitempty"`
	SystemInfo   *parser.SysInfo   `json:"system_info,omitempty"`
	Status       string            `json:"status"` // "running", "idle", "offline"
	Rate         float64           `json:"current_rate"`
	ContainerID  string            `json:"container_id"`
	Uptime       string            `json:"uptime"`
	SessionEarn  float64           `json:"session_earnings"`
}

var (
	mu        sync.RWMutex
	machines  = make(map[string]*MachineState)
	alertFunc func(machine string, online bool, dur time.Duration)
)

// RegisterAlertCallback sets a callback triggered when a machine's online/offline status changes.
func RegisterAlertCallback(fn func(machine string, online bool, dur time.Duration)) {
	mu.Lock()
	defer mu.Unlock()
	alertFunc = fn
}

// ProcessIngest handles an incoming telemetry/log packet from an Agent.
func ProcessIngest(database *db.DB, wsHub *ws.Hub, p *IngestPayload) {
	if p == nil {
		return
	}
	if p.MachineName == "" && p.Machine != "" {
		p.MachineName = p.Machine
	}
	if p.MachineName == "" {
		return
	}

	mu.Lock()
	state, exists := machines[p.MachineName]
	if !exists {
		state = &MachineState{
			Name:     p.MachineName,
			IsOnline: true,
			LastSeen: time.Now(),
		}
		machines[p.MachineName] = state
		log.Printf("[hub] Registered agent machine: %s", p.MachineName)
	}

	wasOnline := state.IsOnline
	state.LastSeen = time.Now()
	state.IsOnline = true
	if p.Hardware != nil {
		state.Hardware = p.Hardware
	}
	if p.SystemInfo != nil {
		state.SystemInfo = p.SystemInfo
	}
	if p.Status != "" {
		state.Status = p.Status
	}
	if p.CurrentRate > 0 {
		state.Rate = p.CurrentRate
	}
	if p.ContainerID != "" {
		state.ContainerID = p.ContainerID
	}
	fn := alertFunc
	mu.Unlock()

	// Notify if recovered from offline state
	if !wasOnline {
		log.Printf("[watchdog] 🟢 Machine %s is back ONLINE!", p.MachineName)
		wsHub.Broadcast("machine_status", map[string]any{
			"machine": p.MachineName,
			"online":  true,
			"status":  "online",
		})
		if fn != nil {
			go fn(p.MachineName, true, 0)
		}
	}

	// Cache hardware and system info as global fallback
	if p.Hardware != nil {
		hardware.SetCached(p.Hardware)
	}
	if p.SystemInfo != nil {
		parser.SetCachedSystemInfo(*p.SystemInfo)
	}

	// Ingest database records with machine affiliation
	hasNewData := false
	if len(p.Wallet) > 0 {
		database.InsertWalletWithMachine(p.MachineName, p.Wallet)
		hasNewData = true
	}
	if len(p.Earnings) > 0 {
		database.InsertEarningsWithMachine(p.MachineName, p.Earnings)
		hasNewData = true
	}
	if len(p.Bandwidth) > 0 {
		database.InsertBandwidthWithMachine(p.MachineName, p.Bandwidth)
		hasNewData = true
	}
	if len(p.Errors) > 0 {
		database.InsertErrorsWithMachine(p.MachineName, p.Errors)
		hasNewData = true
	}
	if len(p.Power) > 0 {
		database.InsertPowerWithMachine(p.MachineName, p.Power)
		hasNewData = true
	}

	if hasNewData {
		wsHub.Broadcast("data_updated", map[string]any{"source": p.MachineName})
	}
}

// StartWatchdog starts a background ticker that marks machines offline if no heartbeat is received.
func StartWatchdog(wsHub *ws.Hub, timeout time.Duration) {
	if timeout <= 0 {
		timeout = 45 * time.Second
	}

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			mu.Lock()
			fn := alertFunc
			now := time.Now()

			for name, state := range machines {
				if state.IsOnline && now.Sub(state.LastSeen) > timeout {
					state.IsOnline = false
					dur := now.Sub(state.LastSeen)
					log.Printf("[watchdog] ⚠️ Machine %s missed heartbeats (>%v) — marking OFFLINE / CRASHED!", name, dur.Round(time.Second))

					wsHub.Broadcast("machine_status", map[string]any{
						"machine":        name,
						"online":         false,
						"status":         "offline",
						"last_seen_secs": int64(dur.Seconds()),
					})

					if fn != nil {
						go fn(name, false, dur)
					}
				}
			}
			mu.Unlock()
		}
	}()
	log.Printf("[watchdog] Heartbeat watchdog started (timeout: %v).", timeout)
}

// GetMachineStatus returns the current status for a given machine name.
func GetMachineStatus(name string) MachineState {
	mu.RLock()
	defer mu.RUnlock()

	if state, exists := machines[name]; exists {
		st := *state
		st.LastSeenSecs = int64(time.Since(state.LastSeen).Seconds())
		return st
	}

	return MachineState{
		Name:         name,
		IsOnline:     true,
		LastSeen:     time.Now(),
		LastSeenSecs: 0,
	}
}

// GetMachineHardware returns the latest cached hardware stats for a specific machine.
func GetMachineHardware(name string) *hardware.Stats {
	mu.RLock()
	defer mu.RUnlock()

	if state, exists := machines[name]; exists && state.Hardware != nil {
		return state.Hardware
	}
	hw := hardware.Get()
	return &hw
}

// GetMachineSysInfo returns the latest cached system info for a specific machine.
func GetMachineSysInfo(name string) *parser.SysInfo {
	mu.RLock()
	defer mu.RUnlock()

	if state, exists := machines[name]; exists && state.SystemInfo != nil {
		return state.SystemInfo
	}
	return parser.GetCachedSystemInfo()
}

// GetAllMachines returns all currently known agent machines and their live status.
func GetAllMachines(database *db.DB) []MachineState {
	mu.RLock()
	defer mu.RUnlock()

	nameSet := make(map[string]bool)
	for name := range machines {
		nameSet[name] = true
	}
	if database != nil {
		for _, dbName := range database.GetKnownMachines() {
			nameSet[dbName] = true
		}
	}

	var result []MachineState
	now := time.Now()
	for name := range nameSet {
		state, exists := machines[name]
		var st MachineState
		if exists {
			st = *state
			st.LastSeenSecs = int64(now.Sub(state.LastSeen).Seconds())
		} else {
			st = MachineState{
				Name:         name,
				IsOnline:     false,
				LastSeenSecs: 999999,
			}
		}

		if database != nil {
			workload := database.GetActiveWorkload(name)
			uptime, sessEarn := database.GetSessionStats(name)
			if workload.Rate > 0 || st.Rate == 0 {
				st.Rate = workload.Rate
			}
			if workload.ContainerID != "" || st.ContainerID == "" {
				st.ContainerID = workload.ContainerID
			}
			if uptime != "" && uptime != "Idle" {
				st.Uptime = uptime
			} else if st.Uptime == "" {
				st.Uptime = uptime
			}
			if sessEarn > 0 || st.SessionEarn == 0 {
				st.SessionEarn = sessEarn
			}
			if !st.IsOnline {
				st.Status = "offline"
			} else if workload.Status != "" && workload.Status != "idle" {
				st.Status = workload.Status
			} else if st.Status == "" {
				st.Status = workload.Status
			}
		}
		result = append(result, st)
	}
	return result
}
