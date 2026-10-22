# 🐳 Salad Metrics Hub | Docker Linux Deployment & Multi-Node Guide

This guide details how to deploy the **Salad Metrics Hub** on any Linux server (Ubuntu, Debian, Fedora, Proxmox VM, Raspberry Pi, etc.) using Docker & Docker Compose, connect multiple Windows mining rigs, and take advantage of backend chart downsampling.

---

## 🏗️ Architecture

```
 ┌──────────────────────────────────────────────┐
 │             24/7 Linux Server                │
 │   ┌──────────────────────────────────────┐   │
 │   │      Docker: salad-metrics-hub       │   │
 │   │  • Web Dashboard (:8000)             │   │
 │   │  • Persistent SQLite Database        │   │
 │   │  • Multi-Node Ingest & Watchdog      │   │
 │   │  • Discord Alerts (24/7 online)      │   │
 │   │  • Real-Time Downsampling Engine     │   │
 │   └──────────────────▲───────────────────┘   │
 └──────────────────────┼───────────────────────┘
                        │ HTTP /api/ingest
         ┌──────────────┴──────────────┐
         │                             │
┌────────┴────────┐           ┌────────┴────────┐
│  Mining Rig 1   │           │  Mining Rig 2   │
│  "Beast"        │           │  "Rig-4090"     │
│  (Windows 11)   │           │  (Windows 10)   │
│  • Salad Logs   │           │  • Salad Logs   │
│  • RTX Telemetry│           │  • RTX Telemetry│
│  • Agent Mode   │           │  • Agent Mode   │
└─────────────────┘           └─────────────────┘
```

---

## 🚀 Quick Start (Docker Compose)

### 1. Prerequisites on Linux Server
Ensure Docker and Docker Compose (v2) are installed:
```bash
sudo apt update && sudo apt install -y docker.io docker-compose-v2
# Enable Docker without sudo (optional):
sudo usermod -aG docker $USER && newgrp docker
```

### 2. Copy Project Files to Your Linux Server
From your Windows development machine (PowerShell):
```powershell
# Create destination directory on the server
ssh user@<SERVER_IP> "mkdir -p ~/salad-metrics"

# Copy required deployment files
scp Dockerfile docker-compose.yml go.mod go.sum main.go user@<SERVER_IP>:~/salad-metrics/
scp -r internal templates static user@<SERVER_IP>:~/salad-metrics/
```

*(Alternatively, git clone your repository directly on the Linux server).*

### 3. (Optional) Existing Data Migration
If you already have historical data in `salad_metrics.db` on your Windows PC and want to bring it to the Linux Hub:
```powershell
ssh user@<SERVER_IP> "mkdir -p ~/salad-metrics/data"
scp salad_metrics.db user@<SERVER_IP>:~/salad-metrics/data/salad_metrics.db
```
The server will automatically run the schema migration on boot (adding multi-node `machine` columns and indices) without losing any historical data.

### 4. Configure & Start
SSH into your Linux server:
```bash
cd ~/salad-metrics

# Create data directory if not present
mkdir -p data

# (Optional) Setup environment variables in .env
cat << 'EOF' > .env
TZ=Asia/Kolkata
SALAD_MODE=server
SALAD_DB_FILE=/app/data/salad_metrics.db
# Uncomment if you want 24/7 Discord crash alerts:
# DISCORD_BOT_TOKEN=your_bot_token_here
# DISCORD_ALERT_CHANNEL=your_channel_id_here
EOF

# Build & launch the Hub in background
docker compose up -d --build
```

### 5. Verify Hub Status
```bash
# Check container status
docker compose ps

# Follow container logs
docker compose logs -f
```

The Web UI is now live at:
**`http://<SERVER_IP>:8000`**

---

## 💻 Connecting Windows Mining Rigs (Agent Mode)

Each Windows mining PC runs `salad-metrics` in lightweight **Agent Mode**.

### Method A: Using `.env` and `start.bat` (Recommended)
1. In the `salad-metrics` directory on the Windows rig, edit or create `.env`:
   ```env
   SERVER_URL=http://<SERVER_IP>:8000
   MACHINE_NAME=Beast
   ```
2. Run `start.bat` (or double-click it).
   - If running in background/tray: `start.bat -silent`

### Method B: Command Line Direct
```cmd
salad-metrics.exe -mode agent -server http://<SERVER_IP>:8000 -machine-name "Beast" -silent
```

### For Multiple Rigs
On your second rig, set a unique `MACHINE_NAME`:
```cmd
salad-metrics.exe -mode agent -server http://<SERVER_IP>:8000 -machine-name "Rig-4090" -silent
```

---

## 🖥️ Multi-Node Dashboard Features

1. **Node Switcher Dropdown (Header)**:
   - Switch between **"All Nodes (Fleet)"** and individual nodes (**"Beast"**, **"Rig-4090"**, etc.).
   - Persists your selection in local storage across browser refreshes.

2. **Fleet Nodes Overview Panel**:
   - Live status cards for every active and offline node.
   - Shows live GPU model, temperature, power draw, container workload, and current earning rate.
   - Click any node card to instantly isolate that node's detailed telemetry.

3. **Fleet Aggregation Mode**:
   - In "All Nodes (Fleet)" mode, total fleet power and earnings are aggregated, while individual nodes remain distinguishable.

4. **Crash Watchdog**:
   - If any rig drops offline (e.g. BSOD, driver crash, power cut), its status card highlights in red with a "Last seen Xm ago" timer.
   - If Discord bot is configured, an emergency alert is dispatched to Discord.

---

## ⚡ Performance Downsampling & Free X-Axes

1. **Adaptive Go-Level Downsampling**:
   - Querying 50,000+ data points over long timeframes (1W, 1M, 6M, ALL) can cause browser UI lag in Chart.js.
   - Salad Metrics uses Go-level adaptive downsampling:
     - **Bandwidth & Power**: Time-bucketed max/average downsampling (~350 high-fidelity points) preserving peak throughput spikes and container power draws.
     - **Wallet Balance**: Decimated time-window sampling preserving boundary changes.
     - **Earnings (Containers)**: Run-length deduplication keeping transition edges (start, finish, rate changes) while eliminating redundant heartbeats.
   - Database queries return in under **50ms**.

2. **Free, Auto-Scaling X-Axes**:
   - The X-axes on all charts remain free and dynamic:
   - Data scales automatically from the earliest recorded event to the latest.
   - No hard-coded 24-hour or weekly clamping.

---

## 🛠️ Management & Maintenance

### Update to Latest Version on Linux
```bash
cd ~/salad-metrics
# If pulling from git:
git pull
# Rebuild container:
docker compose up -d --build
```

### View Live Database Data
```bash
# Run sqlite3 directly inside the container
docker compose exec salad-metrics-hub sqlite3 /app/data/salad_metrics.db "SELECT machine, COUNT(*) FROM earnings GROUP BY machine;"
```

### Stop / Restart Hub
```bash
docker compose restart
docker compose down
```
