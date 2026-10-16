# Salad Metrics — Codebase Analysis & Rewrite Recommendation

## What the App Does

A local monitoring dashboard for a [Salad.io](https://salad.io) node. It:
1. **Tail-watches** Windows log files from `C:\ProgramData\Salad\logs` using regex
2. **Persists** parsed data (wallet, earnings, bandwidth, errors) to a local SQLite DB
3. **Serves** a Flask + Socket.IO web dashboard with live charts (Chart.js)
4. **Pushes** real-time `data_updated` events via WebSocket when new log data is found
5. **Reports** to Discord via a `discord.py` bot that calls the local REST API

---

## Current Stack

| Layer | Technology | File |
|---|---|---|
| Web Server | Python · Flask + Flask-SocketIO | `server.py` |
| Database | Python · SQLite3 (bare) | `database.py` |
| Discord Bot | Python · discord.py + aiohttp | `discord_bot.py` |
| Frontend | HTML + Vanilla JS + Bootstrap 5 + Chart.js | `templates/index.html`, `static/` |

---

## Current Bottlenecks & Pain Points

### 🐌 Performance Problems

| # | Problem | Impact |
|---|---|---|
| 1 | **15-second polling loop** — `socketio.sleep(15)` is the *only* log refresh trigger | 15s minimum lag between an event happening and the UI updating |
| 2 | **New DB connection per call** — `sqlite3.connect()` is called fresh in *every* `insert_data()`, `get_data()`, `get_active_errors()`, `dismiss_error()` | Constant open/close overhead on every API hit |
| 3 | **`readlines()[start_line:]` in memory** — Entire file slice loaded into RAM, then joined into one string for regex | Wasteful for large log files |
| 4 | **`/api/hardware-live` blocks for 100ms** — `psutil.cpu_percent(interval=0.1)` is a blocking I/O call on the main Flask thread | Every hardware poll ties up a request thread for 100ms |
| 5 | **No DB indexing** — Timestamps are stored as `TEXT` with no index beyond `UNIQUE` constraint | Slow `WHERE timestamp >= ?` range queries as DB grows |
| 6 | **Flask's dev WSGI server** — Even with eventlet/gevent under Socket.IO it isn't optimized for high concurrency | Not a problem at single-user scale but worth noting |

### 🧱 Structural Problems

| # | Problem |
|---|---|
| 1 | **No connection pooling** — database.py opens/closes a connection for every single operation |
| 2 | **No file-watching (inotify/ReadDirectoryChanges)** — Uses a timer loop instead of OS filesystem events |
| 3 | **Hardcoded log path** — `C:\ProgramData\Salad\logs` is baked into `server.py` |
| 4 | **Single-file architecture** — No separation between routes, services, or data access layers |
| 5 | **GIL contention** — The background log monitor task competes with request handling threads under Python's GIL |

---

## Language / Stack Comparison

### Option A — **Stay in Python, but optimize it** ✅ (Recommended for low effort)

> **Just fix the real bottlenecks without a rewrite.**

Changes needed:
- Replace 15s polling with `watchdog` (uses Win32 `ReadDirectoryChanges`) — sub-second log change detection
- Add a persistent SQLite connection with WAL mode + a `timestamp` index
- Move `cpu_percent()` to a background thread and cache the result
- Use `uvicorn` + `FastAPI` instead of Flask for async-native routing

**Effort**: ~2–4 hours  
**Speedup**: Log detection latency **15s → <1s**, hardware API **100ms → <10ms**

---

### Option B — **Rewrite the backend in Go** 🚀 (Best real-time performance)

> **Go is the ideal language for this specific workload.**

| Why Go fits perfectly | Details |
|---|---|
| **True concurrency without GIL** | `goroutines` are green threads — log watcher, HTTP server, and DB writer run in parallel with zero GIL contention |
| **`fsnotify` built-in** | The standard go library ecosystem has `fsnotify` — a cross-platform file watcher using `ReadDirectoryChangesW` on Windows. Zero polling needed. |
| **`gorilla/websocket` or `nhooyr.io/websocket`** | Fast, production-grade WebSocket push |
| **`modernc.org/sqlite` or `mattn/go-sqlite3`** | SQLite with WAL mode and proper connection pool |
| **Single binary deployment** | The entire server compiles to a single `.exe`. No Python, no venv, no dependencies to install. `start.bat` becomes trivial. |
| **~5–10x lower memory footprint** | Go's runtime is much lighter than CPython + Flask + eventlet |

**Effort**: ~1–2 days (the logic is straightforward to port)  
**Speedup**: Log detection **<100ms**, hardware polling **non-blocking async**, WebSocket push **native goroutine**

---

### Option C — **Rewrite the backend in Rust** ⚡ (Maximum performance, maximum effort)

> Rust (`axum` + `tokio` + `notify`) would be the *fastest* possible implementation but is overkill for a local metrics dashboard.

**Effort**: 3–5 days  
**Verdict**: Unnecessary for this use case. Go achieves 95% of the same gains with 20% of the complexity.

---

### Option D — **Node.js (TypeScript)** 🟡 (Middle ground)

> `chokidar` for file watching, `ws` for WebSockets, `better-sqlite3` for SQLite. Shares the JavaScript language with the frontend.

**Pros**: Full TypeScript stack, easy to understand for frontend devs, `chokidar` uses native Win32 watching  
**Cons**: Still single-threaded event loop (though non-blocking), heavier runtime than Go  
**Verdict**: A solid option if you want one language for both front and backend, but Go is still better for the pure server role.

---

## Verdict & Recommendation

```
📌 Short-term fix:  Python + watchdog + FastAPI  (2–4 hours, most impactful change)
📌 Long-term ideal: Go backend rewrite            (1–2 days, best performance + deployment)
```

The single biggest win **right now** regardless of language is replacing the **15-second polling loop** with an **OS filesystem watcher**. That alone takes log-to-dashboard latency from ~15 seconds down to under 1 second.

---

## Specific Go Architecture (if you want to rewrite)

```
salad-metrics/
├── main.go              # Entry point, CLI flags, server start
├── watcher/
│   └── watcher.go       # fsnotify-based log file watcher
├── parser/
│   └── parser.go        # Regex log parsing (same patterns, just in Go)
├── db/
│   └── db.go            # SQLite pool, insert/query helpers
├── api/
│   └── routes.go        # HTTP handlers (net/http or chi/gin)
├── ws/
│   └── hub.go           # WebSocket hub, broadcast to connected clients
├── discord/
│   └── bot.go           # discordgo bot
└── static/              # Same HTML/CSS/JS frontend (no changes needed)
    templates/
```

The frontend (`index.html`, `app.js`, `style.css`) would require **zero changes** — it already speaks standard HTTP + Socket.IO-compatible WebSockets.

---

## Quick Win Checklist (Python, no rewrite)

- [ ] `pip install watchdog` — replace `socketio.sleep(15)` loop with `watchdog.Observer`
- [ ] Add `PRAGMA journal_mode=WAL` to `init_db()` — concurrent reads during writes
- [ ] Add `CREATE INDEX IF NOT EXISTS idx_wallet_ts ON wallet(timestamp)` to `init_db()`
- [ ] Move `cpu_percent(interval=0.1)` to a background thread, cache result every 2s
- [ ] Use a module-level `sqlite3.connect()` with `check_same_thread=False` instead of per-call connections
