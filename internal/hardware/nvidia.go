package hardware

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"
)

// NvidiaGPUStats holds parsed metrics from nvidia-smi.
type NvidiaGPUStats struct {
	Load  float64
	Temp  float64
	Power float64
	Limit float64
}

// readNvidiaGPU queries nvidia-smi for GPU utilization, temperature, power draw, and power limit.
// Returns NvidiaGPUStats. Returns zeroed struct if nvidia-smi is unavailable.
func readNvidiaGPU() NvidiaGPUStats {
	cmd := exec.Command("nvidia-smi",
		"--query-gpu=utilization.gpu,temperature.gpu,power.draw,power.limit",
		"--format=csv,noheader,nounits",
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return NvidiaGPUStats{}
	}

	line := strings.TrimSpace(out.String())
	parts := strings.Split(line, ",")
	if len(parts) < 4 {
		return NvidiaGPUStats{}
	}

	load, _ := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	temp, _ := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	power, _ := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	limit, _ := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)

	return NvidiaGPUStats{
		Load:  load,
		Temp:  temp,
		Power: power,
		Limit: limit,
	}
}
