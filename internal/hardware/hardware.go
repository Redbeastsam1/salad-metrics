package hardware

import (
	"log"
	"math"
	"strings"
	"sync/atomic"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

// Stats holds a snapshot of live hardware usage and power metrics.
type Stats struct {
	CPULoad     float64 `json:"cpu_load"`
	CPUPower    float64 `json:"cpu_power"`     // in Watts (e.g. 38.5)
	CPUPowerEst bool    `json:"cpu_power_est"` // true if estimated from CPU profile
	RAMLoad     float64 `json:"ram_load"`
	GPULoad     float64 `json:"gpu_load"`
	GPUTemp     float64 `json:"gpu_temp"`
	GPUPower    float64 `json:"gpu_power"`     // in Watts (direct from nvidia-smi)
	GPULimit    float64 `json:"gpu_limit"`     // in Watts (power limit from nvidia-smi)
	TotalPower  float64 `json:"total_power"`   // in Watts (GPU + CPU combined)
}

var (
	cached       atomic.Pointer[Stats]
	cpuIdleWatts = 18.0
	cpuMaxWatts  = 250.0
)

func init() {
	// Detect CPU architecture for realistic TDP profile
	infos, err := cpu.Info()
	if err == nil && len(infos) > 0 {
		model := strings.ToLower(infos[0].ModelName)
		switch {
		case strings.Contains(model, "13900") || strings.Contains(model, "14900") || strings.Contains(model, "7950"):
			cpuIdleWatts = 18.0
			cpuMaxWatts = 253.0
		case strings.Contains(model, "13700") || strings.Contains(model, "14700") || strings.Contains(model, "7900") || strings.Contains(model, "7700"):
			cpuIdleWatts = 15.0
			cpuMaxWatts = 210.0
		case strings.Contains(model, "13600") || strings.Contains(model, "14600") || strings.Contains(model, "7600"):
			cpuIdleWatts = 12.0
			cpuMaxWatts = 180.0
		case strings.Contains(model, "i3") || strings.Contains(model, "ryzen 3"):
			cpuIdleWatts = 8.0
			cpuMaxWatts = 90.0
		default:
			cpuIdleWatts = 15.0
			cpuMaxWatts = 125.0
		}
	}
}

// estimateCPUPower calculates estimated package power draw in Watts given current load %.
func estimateCPUPower(load float64) float64 {
	if load <= 0 {
		return cpuIdleWatts
	}
	if load >= 100 {
		return cpuMaxWatts
	}
	ratio := math.Pow(load/100.0, 1.35)
	watts := cpuIdleWatts + (cpuMaxWatts-cpuIdleWatts)*ratio
	return math.Round(watts*10) / 10
}

// Get returns the most recently cached hardware stats (non-blocking, microseconds).
func Get() Stats {
	p := cached.Load()
	if p == nil {
		return Stats{}
	}
	return *p
}

// StartPoller launches a background goroutine that refreshes hardware stats every 2 seconds.
func StartPoller() {
	go func() {
		for {
			s := poll()
			cached.Store(&s)
			time.Sleep(2 * time.Second)
		}
	}()
	log.Println("[hardware] Poller started (2s interval).")
}

func poll() Stats {
	s := Stats{
		CPUPowerEst: true,
	}

	// CPU — 200ms measurement window
	if percents, err := cpu.Percent(200*time.Millisecond, false); err == nil && len(percents) > 0 {
		s.CPULoad = percents[0]
	}
	s.CPUPower = estimateCPUPower(s.CPULoad)

	// RAM
	if vm, err := mem.VirtualMemory(); err == nil {
		s.RAMLoad = vm.UsedPercent
	}

	// GPU via nvidia-smi
	gpuStats := readNvidiaGPU()
	s.GPULoad = gpuStats.Load
	s.GPUTemp = gpuStats.Temp
	s.GPUPower = gpuStats.Power
	s.GPULimit = gpuStats.Limit

	// Total combined power draw
	s.TotalPower = math.Round((s.GPUPower+s.CPUPower)*10) / 10

	return s
}
