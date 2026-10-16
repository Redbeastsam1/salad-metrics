package parser

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"

	"salad-metrics/internal/db"
)

// Pre-compiled regex patterns — compiled once at program start.
var (
	earningsRe  = regexp.MustCompile(`(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3} [+-]\d{2}:\d{2}) \[INF\] Predicted Earnings Report: ([\d.]+) from \(([^)]+)\)`)
	walletRe    = regexp.MustCompile(`(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3} [+-]\d{2}:\d{2}) \[INF\] Wallet: Current\(([\d.]+)\)`)
	bandwidthRe = regexp.MustCompile(`(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3} [+-]\d{2}:\d{2}) \[INF\] \{.*?"BidirThroughput":([\d.]+)`)
	errorRe     = regexp.MustCompile(`(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3} [+-]\d{2}:\d{2}) \[(WRN|ERR)\] (.*)`)
	sysInfoRe   = regexp.MustCompile(`\[INF\] StaticData: (\{.*\})`)
)

// ParsedData holds everything extracted from a single parse pass of a file.
type ParsedData struct {
	Wallet    []db.WalletRow
	Earnings  []db.EarningsRow
	Bandwidth []db.BandwidthRow
	Errors    []db.ErrorRow
}

// fileState tracks the byte offset we last read up to for each watched file.
type fileState struct {
	Offset int64
}

var fileStates = make(map[string]*fileState)

// isRealError filters out benign logs and only returns true for genuine node or workload failures.
func isRealError(msg string) bool {
	// Exclude benign/noisy background Salad and containerd lifecycle logs
	if strings.Contains(msg, "FailedPrecondition") ||
		strings.Contains(msg, "container system info") ||
		strings.Contains(msg, "SyncDecryptionKey") ||
		strings.Contains(msg, "MixPanel") ||
		strings.Contains(msg, "virtual hard disk") ||
		strings.Contains(msg, "retrieve process") ||
		strings.Contains(msg, "CpuUtilization") ||
		strings.Contains(msg, "CpuMetrics") ||
		strings.Contains(msg, "WSL") ||
		strings.Contains(msg, "Failed = False") {
		return false
	}

	// Identify real Salad failures
	if strings.Contains(msg, "Node Compatibility") ||
		strings.Contains(msg, "Degraded") ||
		strings.Contains(msg, "TrackWorkloadStatesAsync Failure") ||
		strings.Contains(msg, "Workload Failure") ||
		strings.Contains(msg, "Failed to start container") ||
		strings.Contains(msg, "Failed to pull image") {
		return true
	}

	return false
}

// ParseFile reads only NEW content from the file since the last call (using byte offsets).
// Returns structured data and whether any data was found.
func ParseFile(path string, isBandwidth bool) (ParsedData, bool) {
	var result ParsedData

	f, err := os.Open(path)
	if err != nil {
		return result, false
	}
	defer f.Close()

	state, exists := fileStates[path]
	if !exists {
		state = &fileState{}
		fileStates[path] = state
	}

	// Seek to where we left off
	if state.Offset > 0 {
		if _, err := f.Seek(state.Offset, io.SeekStart); err != nil {
			// File may have been rotated — reset offset
			state.Offset = 0
			f.Seek(0, io.SeekStart)
		}
	}

	content, err := io.ReadAll(f)
	if err != nil || len(content) == 0 {
		return result, false
	}

	// Update the offset for next call
	state.Offset += int64(len(content))

	text := string(content)

	// --- Earnings ---
	for _, m := range earningsRe.FindAllStringSubmatch(text, -1) {
		rate, _ := strconv.ParseFloat(m[2], 64)
		result.Earnings = append(result.Earnings, db.EarningsRow{
			Timestamp:   m[1],
			Rate:        rate,
			ContainerID: m[3],
		})
	}

	// --- Wallet ---
	for _, m := range walletRe.FindAllStringSubmatch(text, -1) {
		bal, _ := strconv.ParseFloat(m[2], 64)
		result.Wallet = append(result.Wallet, db.WalletRow{
			Timestamp: m[1],
			Current:   bal,
		})
	}

	// --- Errors ---
	for _, m := range errorRe.FindAllStringSubmatch(text, -1) {
		msg := strings.TrimSpace(m[3])
		if isRealError(msg) {
			result.Errors = append(result.Errors, db.ErrorRow{
				Timestamp: m[1],
				Message:   msg,
			})
		}
	}

	// --- Bandwidth (only for bandwidth log files) ---
	if isBandwidth {
		for _, m := range bandwidthRe.FindAllStringSubmatch(text, -1) {
			throughput, _ := strconv.ParseFloat(m[2], 64)
			result.Bandwidth = append(result.Bandwidth, db.BandwidthRow{
				Timestamp:  m[1],
				Throughput: throughput / (280000 * 8),
			})
		}
	}

	hasData := len(result.Wallet) > 0 || len(result.Earnings) > 0 ||
		len(result.Bandwidth) > 0 || len(result.Errors) > 0

	return result, hasData
}

// ResetOffset clears the cached byte offset for a file (e.g. on rotation/recreation).
func ResetOffset(path string) {
	delete(fileStates, path)
}

// SysInfo holds parsed static hardware information.
type SysInfo struct {
	CPU string `json:"CPU"`
	GPU string `json:"GPU"`
	RAM string `json:"RAM"`
	OS  string `json:"OS"`
}

// ParseSystemInfo reads the most recent system info log file and extracts hardware details.
func ParseSystemInfo(sysDir string) SysInfo {
	info := SysInfo{CPU: "Unknown", GPU: "Unknown", RAM: "Unknown", OS: "Unknown"}

	entries, err := os.ReadDir(sysDir)
	if err != nil {
		return info
	}

	// Find the newest file
	var newestPath string
	var newestTime int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".txt") && !strings.HasSuffix(name, ".log") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if fi.ModTime().UnixNano() > newestTime {
			newestTime = fi.ModTime().UnixNano()
			newestPath = sysDir + `\` + name
		}
	}

	if newestPath == "" {
		return info
	}

	f, err := os.Open(newestPath)
	if err != nil {
		return info
	}
	defer f.Close()

	content, _ := io.ReadAll(f)
	matches := sysInfoRe.FindSubmatch(content)
	if matches == nil {
		return info
	}

	var data map[string]any
	if err := json.Unmarshal(matches[1], &data); err != nil {
		log.Printf("[parser] sys info json error: %v", err)
		return info
	}

	// CPU
	if cpu, ok := data["cpu"].(map[string]any); ok {
		brand := strings.TrimSpace(fmt.Sprintf("%v", cpu["brand"]))
		brand = strings.ReplaceAll(brand, "Gen Intel", "Intel")
		info.CPU = brand
	}

	// GPU
	if graphics, ok := data["graphics"].(map[string]any); ok {
		if controllers, ok := graphics["controllers"].([]any); ok && len(controllers) > 0 {
			realGPU := controllers[0].(map[string]any)
			for _, c := range controllers {
				gpu := c.(map[string]any)
				name := strings.ToLower(fmt.Sprintf("%v %v", gpu["vendor"], gpu["model"]))
				if strings.Contains(name, "nvidia") || strings.Contains(name, "amd") || strings.Contains(name, "radeon") {
					realGPU = gpu
					break
				}
			}
			vendor := strings.TrimSpace(fmt.Sprintf("%v", realGPU["vendor"]))
			model := strings.TrimSpace(fmt.Sprintf("%v", realGPU["model"]))
			modelLow := strings.ToLower(model)
			if strings.Contains(modelLow, strings.ToLower(vendor)) || strings.Contains(modelLow, "nvidia") || strings.Contains(modelLow, "amd") {
				info.GPU = model
			} else {
				info.GPU = strings.TrimSpace(vendor + " " + model)
			}
		}
	}

	// RAM
	if memLayout, ok := data["memLayout"].([]any); ok {
		var total float64
		for _, m := range memLayout {
			if mem, ok := m.(map[string]any); ok {
				if size, ok := mem["size"].(float64); ok {
					total += size
				}
			}
		}
		info.RAM = fmt.Sprintf("%.0f GB", total/float64(1024*1024*1024))
	}

	// OS
	if osData, ok := data["os"].(map[string]any); ok {
		info.OS = fmt.Sprintf("%v", osData["distro"])
	}

	return info
}


