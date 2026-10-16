# Salad Metrics — Go Rewrite + UI Revamp Plan

## Goals

| Goal | Current State | Target State |
|---|---|---|
| Log detection latency | ~15 seconds (polling) | **<100ms** (OS file events via `fsnotify`) |
| Hardware polling | 100ms blocking per request | **Non-blocking** (background goroutine, cached) |
| DB connections | New connection per call | **Persistent pool** with WAL mode |
| Deployment | Python + venv + 6 packages | **Single `.exe`**, zero dependencies |
| UI | Bootstrap 5, basic cards | **Premium dark dashboard** (custom design system) |
| Discord bot | Separate `discord_bot.py` | **Compiled into the same binary**, optional flag |

---

## New Project Structure

```
salad-metrics/
│
├── main.go                   # Entry point: CLI flags, wires everything together
│
├── internal/
│   ├── config/
│   │   └── config.go         # CLI args (port, machine name, log dir)
│   │
│   ├── parser/
│   │   └── parser.go         # All regex patterns + log line parsing functions
│   │
│   ├── watcher/
│   │   └── watcher.go        # fsnotify file watcher → sends events to a channel
│   │
│   ├── db/
│   │   └── db.go             # SQLite pool, WAL mode, all queries
│   │
│   ├── api/
│   │   └── routes.go         # All HTTP REST handlers (/api/data, /api/stats, etc.)
│   │
│   ├── ws/
│   │   └── hub.go            # WebSocket hub: register/unregister clients, broadcast
│   │
│   └── discord/
│       └── bot.go            # discordgo bot (optional, enabled via CLI flag)
│
├── static/
│   ├── css/
│   │   └── style.css         # Brand new custom CSS design system
│   └── js/
│       └── app.js            # Revamped frontend JS
│
└── templates/
    └── index.html            # New premium HTML template
```

---

## Phase 1 — Go Project Foundation (~30 min)

**Tasks:**
- [ ] `go mod init salad-metrics`
- [ ] Install dependencies:
  - `github.com/fsnotify/fsnotify` — file system watcher
  - `github.com/gorilla/websocket` — WebSocket hub
  - `github.com/mattn/go-sqlite3` — SQLite3 CGo driver (or `modernc.org/sqlite` for pure Go)
  - `github.com/bwmarrin/discordgo` — Discord bot
  - `github.com/rs/cors` — CORS middleware
- [ ] Create the directory structure above
- [ ] Write `main.go` with CLI flag parsing (`-port`, `-machine-name`, `-discord`)

**Key `main.go` shape:**
```go
func main() {
    cfg := config.Parse()       // CLI flags
    database := db.New(cfg)     // Open SQLite with WAL + indexes
    hub := ws.NewHub()          // WebSocket broadcast hub
    go hub.Run()                // Goroutine: manage WS clients
    go watcher.Run(cfg, database, hub) // Goroutine: fsnotify → parse → DB → push WS
    go hardwarePoller(database) // Goroutine: cache CPU/GPU/RAM every 2s
    if cfg.DiscordEnabled {
        go discord.Run(cfg)     // Goroutine: discord bot
    }
    api.Serve(cfg, database, hub) // Main thread: HTTP server (blocking)
}
```

---

## Phase 2 — Database Layer (`internal/db/db.go`) (~45 min)

**Key design decisions:**
- Use `database/sql` with a connection pool (`db.SetMaxOpenConns(5)`)
- Enable WAL mode on open: `PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL;`
- Add proper indexes on first run

```go
// Schema (same tables, better setup)
CREATE TABLE IF NOT EXISTS wallet    (timestamp TEXT PRIMARY KEY, current REAL);
CREATE TABLE IF NOT EXISTS earnings  (timestamp TEXT PRIMARY KEY, container_id TEXT, rate REAL);
CREATE TABLE IF NOT EXISTS bandwidth (timestamp TEXT PRIMARY KEY, throughput REAL);
CREATE TABLE IF NOT EXISTS errors    (timestamp TEXT PRIMARY KEY, message TEXT, dismissed INTEGER DEFAULT 0);

CREATE INDEX IF NOT EXISTS idx_wallet_ts    ON wallet(timestamp);
CREATE INDEX IF NOT EXISTS idx_earnings_ts  ON earnings(timestamp);
CREATE INDEX IF NOT EXISTS idx_bandwidth_ts ON bandwidth(timestamp);
```

**Functions to implement:**
- `InsertWallet`, `InsertEarnings`, `InsertBandwidth`, `InsertErrors` (bulk upsert)
- `GetData(hours int)` — returns all tables for a time window
- `GetActiveErrors()`, `DismissError(ts)`, `DismissAllErrors()`
- `GetLatestWallet()` — for session earnings calc

---

## Phase 3 — Log Parser (`internal/parser/parser.go`) (~30 min)

Direct port of the Python regex patterns to Go's `regexp` package.
All patterns are pre-compiled at package init (same as Python — no runtime recompilation).

```go
var (
    earningsRe  = regexp.MustCompile(`(\d{4}-\d{2}-\d{2}...) \[INF\] Predicted Earnings Report: ([\d.]+) from \(([^)]+)\)`)
    walletRe    = regexp.MustCompile(`... \[INF\] Wallet: Current\(([\d.]+)\)...`)
    bandwidthRe = regexp.MustCompile(`... \[INF\] \{.*?"BidirThroughput":([\d.]+)`)
    errorRe     = regexp.MustCompile(`... \[(WRN|ERR)\] (.*)`)
    sysInfoRe   = regexp.MustCompile(`\[INF\] StaticData: (\{.*\})`)
)

// ParseFile reads only NEW lines (from offset), returns structured data
func ParseFile(path string, offset int64) (ParsedData, int64)
```

> **Go advantage:** We use **byte offsets** (`io.Seeker`) instead of line counts. This means we never re-read any part of the file — we seek directly to where we left off.

---

## Phase 4 — File Watcher (`internal/watcher/watcher.go`) (~45 min)

This is the **most important upgrade** over the Python version.

```go
func Run(cfg *config.Config, db *db.DB, hub *ws.Hub) {
    watcher, _ := fsnotify.NewWatcher()
    
    // Watch the entire Salad log directory recursively
    watchDirRecursive(watcher, cfg.LogDir)
    
    for event := range watcher.Events {
        if event.Op&fsnotify.Write == fsnotify.Write {
            // A log file was written to — parse only the new bytes
            processFile(event.Name, db, hub)
        }
        if event.Op&fsnotify.Create == fsnotify.Create {
            // A new log file was created — add it to the watch list
            watcher.Add(event.Name)
        }
    }
}
```

**Result:** Log data hits the DB and the WebSocket push fires in **<100ms** of the Salad process writing a line. No polling at all.

---

## Phase 5 — WebSocket Hub (`internal/ws/hub.go`) (~30 min)

A classic Go WebSocket hub pattern:

```go
type Hub struct {
    clients    map[*Client]bool
    broadcast  chan []byte
    register   chan *Client
    unregister chan *Client
}

// Broadcast(msg) is called by the watcher when new data is available
// All connected browser tabs receive the push instantly
func (h *Hub) Broadcast(event string, data any)
```

The frontend's `socket.on('data_updated', ...)` already handles this — **no frontend JS changes needed** for the WebSocket logic.

---

## Phase 6 — REST API (`internal/api/routes.go`) (~45 min)

Using Go's standard `net/http` (no framework needed — the API is simple):

| Route | Handler |
|---|---|
| `GET /` | Serve `index.html` |
| `GET /api/data?hours=N` | Query DB, return JSON |
| `GET /api/stats` | Uptime + session earnings |
| `GET /api/errors` | Active errors |
| `POST /api/dismiss-error` | Dismiss by timestamp |
| `POST /api/dismiss-all-errors` | Dismiss all |
| `GET /api/system-info` | Parse system info log |
| `GET /api/hardware-live` | Return cached CPU/GPU/RAM (updated every 2s in background) |
| `GET /ws` | WebSocket upgrade endpoint |

**Hardware polling goroutine** (replaces the blocking `cpu_percent(interval=0.1)`):
```go
// Runs in background, updates a shared atomic struct every 2s
func hardwarePoller() {
    for {
        hwCache.Store(readHardware()) // Non-blocking: reads already-computed values
        time.Sleep(2 * time.Second)
    }
}
// /api/hardware-live just reads the cache — responds in microseconds
```

---

## Phase 7 — Discord Bot (`internal/discord/bot.go`) (~30 min)

Direct port of `discord_bot.py` using `bwmarrin/discordgo`:
- Same `?status` and `?errors` commands
- Calls the local REST API via `net/http` (same as before)
- Launched as a goroutine in `main.go` when `-discord` flag is passed

---

## Phase 8 — UI Revamp (~2–3 hours)

This is a **complete redesign** of the frontend. Same data, dramatically better look.

### Design Direction: **"Terminal meets Premium Dark Dashboard"**

| Aspect | Current | New |
|---|---|---|
| Color scheme | Bootstrap default dark | Custom deep-navy/slate palette with neon-green accents |
| Typography | Bootstrap default | `JetBrains Mono` for values, `Inter` for labels |
| Cards | Flat Bootstrap cards | Glassmorphism panels with subtle border glow |
| Charts | Default Chart.js | Custom-themed with gradient fills and smooth animations |
| Hardware bars | Basic Bootstrap progress bars | Animated neon progress bars with glow effects |
| Status indicator | Text label | Animated pulsing status dot |
| Layout | Static rows | CSS Grid with sidebar that auto-shows on errors |
| Animations | None | Subtle number count-up on load, smooth data transitions |
| Earnings display | Plain text | Large hero number with ticker animation on update |

### New Color Palette

```css
--bg-primary:     #0a0e1a;   /* Deep navy */
--bg-secondary:   #0f1629;   /* Panel background */
--bg-card:        #151c35;   /* Card surface */
--accent-green:   #00ff88;   /* Earnings/wallet (neon green) */
--accent-blue:    #4d9fff;   /* CPU / primary */
--accent-purple:  #a855f7;   /* GPU */
--accent-cyan:    #22d3ee;   /* Bandwidth */
--accent-red:     #ff4757;   /* Errors */
--text-primary:   #e2e8f0;
--text-muted:     #64748b;
--border-glow:    rgba(0, 255, 136, 0.15);
```

---

## Phase 9 — Build, Test & Replace (~30 min)

```powershell
# Build the single binary
go build -o salad-metrics.exe .

# New start.bat (replaces the old one)
@echo off
salad-metrics.exe -port 8000 -machine-name "Beast" -discord
pause
```

---

## Summary Timeline

| Phase | Task | Est. Time |
|---|---|---|
| 1 | Go project setup + `main.go` | 30 min |
| 2 | Database layer | 45 min |
| 3 | Log parser | 30 min |
| 4 | File watcher (fsnotify) | 45 min |
| 5 | WebSocket hub | 30 min |
| 6 | REST API + hardware cache | 45 min |
| 7 | Discord bot | 30 min |
| 8 | UI revamp | 2–3 hours |
| 9 | Build, test, replace | 30 min |
| **Total** | | **~7–8 hours** |

---

> [!IMPORTANT]
> The existing `salad_metrics.db` SQLite database is **100% compatible** with the Go version — same schema, same file. No data migration needed.

> [!TIP]
> Ready to start? Say **"proceed"** and I'll implement phase by phase, starting with the Go foundation and database layer.
