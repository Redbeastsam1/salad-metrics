# 🥗 Salad Metrics (Go Edition)

A high-performance, real-time monitoring dashboard and telemetry system for [Salad.io](https://salad.com) compute nodes. 

Built in pure Go with native WebSockets, SQLite WAL mode, hardware sensor integration, and a responsive dark UI.

---

## ✨ Features

- **⚡ Real-Time Engine (Go)**: Replaces Python/Flask with a single, lightweight, highly concurrent Go binary. Memory footprint < 25MB and sub-millisecond API response times.
- **🔌 Native WebSockets**: Instant push updates for wallet balance changes, container transitions, bandwidth throughput, and hardware stats without polling overhead.
- **🎮 Live GPU Telemetry**: Direct `nvidia-smi` hardware sensor queries tracking GPU utilization %, core temperature, real-time power draw (Watts), and maximum power limits.
- **🧠 CPU Power & Hardware Telemetry**: Dynamic package power draw calculation based on detected CPU architecture (Intel/AMD TDP profiles) and utilization.
- **⚡ Total Node Power Draw**: Live combined rig wattage display (GPU + CPU) allowing precise electricity vs earnings calculations.
- **🛡️ Intelligent Error Classification**: Eliminates noisy containerd/Docker image caching false-positives and highlights real node compatibility issues and workload failures.
- **📊 Interactive Gradient Charts**: Chart.js time-series graphs for Wallet Balance, Container Earnings, and Bandwidth Throughput with time-window filtering (`24H`, `1W`, `1M`, `6M`, `ALL`).
- **🤖 Built-in Discord Bot**: Integrated Discord bot reporting live status, active containers, hardware utilization, and power draw (`?status`, `?errors`).
- **🗃️ SQLite WAL Engine**: Optimized with Write-Ahead Logging (`WAL`), `NORMAL` synchronous mode, connection pooling, and multi-indexed query caching.

---

## 🚀 Quick Start

### 1. Prerequisites
- Windows 10 or 11 (64-bit)
- [Go 1.22+](https://go.dev/) (only needed if compiling from source)
- NVIDIA GPU with drivers installed (`nvidia-smi`)

### 2. Configuration
Copy `.env.example` to `.env` (optional):
```env
MACHINE_NAME=Beast
DISCORD_BOT_TOKEN=your_token_here
```

### 3. Running
Double-click `start.bat` or run via command line:
```powershell
go run .
```
Or build the single binary:
```powershell
go build -o salad-metrics.exe .
./salad-metrics.exe
```

Access the dashboard at **`http://localhost:8000`**.

---

## ⚙️ CLI Options

| Flag | Default | Description |
|---|---|---|
| `-port` | `8000` | HTTP web server port |
| `-machine-name` | `Beast` | Machine display name on dashboard & Discord |
| `-log-dir` | `C:\ProgramData\Salad\logs` | Path to Salad log directory |
| `-db` | `salad_metrics.db` | Path to SQLite database file |
| `-discord` | `false` | Enable the integrated Discord bot |
| `-discord-token` | `""` | Discord bot token (or set via `DISCORD_BOT_TOKEN` in `.env`) |

---

## 📁 Project Structure

```
salad-metrics/
├── main.go               # Program entry point & lifecycle manager
├── go.mod / go.sum       # Go module dependencies
├── start.bat             # One-click Windows launcher
├── .env.example          # Environment variables template
├── internal/
│   ├── api/              # HTTP router, REST endpoints, and middleware
│   ├── config/           # CLI flag and environment configuration
│   ├── db/               # SQLite WAL connection pool, queries, & migrations
│   ├── discord/          # Integrated Discord bot (status embeds & alerts)
│   ├── hardware/         # gopsutil + nvidia-smi live hardware & power telemetry
│   ├── parser/           # Regex log parser with intelligent error filtering
│   ├── watcher/          # Efficient fsnotify log file watcher & incremental reader
│   └── ws/               # Native WebSocket hub & broadcasting engine
├── templates/
│   └── index.html        # Clean semantic HTML dashboard layout
├── static/
│   ├── css/style.css     # Dark glassmorphic design system
│   └── js/app.js         # Native WebSocket client & Chart.js data pipeline
├── docs/                 # Architecture analysis and rewrite documentation
└── legacy_python/        # Original Python Flask implementation (archived)
```

---

## 📜 License
MIT
