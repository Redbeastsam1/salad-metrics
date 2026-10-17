package db

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps a sql.DB connection pool for all Salad metrics data.
type DB struct {
	pool      *sql.DB
	bwMu      sync.RWMutex
	bwCached  *BandwidthSummary
	bwCacheTs time.Time
}

// New opens the SQLite database, enables WAL mode, and creates all tables/indexes.
func New(path string) (*DB, error) {
	pool, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Connection pool: 1 writer, up to 5 readers
	pool.SetMaxOpenConns(5)
	pool.SetMaxIdleConns(5)
	pool.SetConnMaxLifetime(0)

	db := &DB{pool: pool}
	if err := db.initialize(); err != nil {
		return nil, fmt.Errorf("init db: %w", err)
	}
	return db, nil
}

// Close closes the underlying database pool.
func (db *DB) Close() error {
	if db.pool != nil {
		return db.pool.Close()
	}
	return nil
}

func (db *DB) initialize() error {
	pragmas := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=NORMAL`,
		`PRAGMA cache_size=-64000`,
		`PRAGMA temp_store=MEMORY`,
	}
	for _, p := range pragmas {
		if _, err := db.pool.Exec(p); err != nil {
			return fmt.Errorf("pragma %q: %w", p, err)
		}
	}

	schema := `
	CREATE TABLE IF NOT EXISTS wallet (
		timestamp TEXT PRIMARY KEY,
		current REAL NOT NULL
	);
	CREATE TABLE IF NOT EXISTS earnings (
		timestamp TEXT PRIMARY KEY,
		container_id TEXT NOT NULL,
		rate REAL NOT NULL
	);
	CREATE TABLE IF NOT EXISTS bandwidth (
		timestamp TEXT PRIMARY KEY,
		throughput REAL NOT NULL
	);
	CREATE TABLE IF NOT EXISTS errors (
		timestamp TEXT PRIMARY KEY,
		message TEXT NOT NULL,
		dismissed INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS power (
		timestamp TEXT PRIMARY KEY,
		gpu_power REAL NOT NULL,
		cpu_power REAL NOT NULL,
		total_power REAL NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_wallet_ts    ON wallet(timestamp);
	CREATE INDEX IF NOT EXISTS idx_earnings_ts  ON earnings(timestamp);
	CREATE INDEX IF NOT EXISTS idx_bandwidth_ts ON bandwidth(timestamp);
	CREATE INDEX IF NOT EXISTS idx_errors_ts    ON errors(timestamp);
	CREATE INDEX IF NOT EXISTS idx_power_ts     ON power(timestamp);
	`
	if _, err := db.pool.Exec(schema); err != nil {
		return fmt.Errorf("schema: %w", err)
	}

	// Clean up legacy false-positive errors that were erroneously flagged
	cleanQuery := `
	DELETE FROM errors WHERE 
		message LIKE '%FailedPrecondition%' OR
		message LIKE '%container system info%' OR
		message LIKE '%SyncDecryptionKey%' OR
		message LIKE '%MixPanel%' OR
		message LIKE '%virtual hard disk%' OR
		message LIKE '%retrieve process%' OR
		message LIKE '%CpuUtilization%' OR
		message LIKE '%CpuMetrics%' OR
		message LIKE '%WSL%' OR
		message LIKE '%Failed = False%';
	`
	db.pool.Exec(cleanQuery)

	log.Println("[DB] Initialized with WAL mode and indexes.")
	return nil
}

// ─── Insert Helpers ──────────────────────────────────────────────────────────

// WalletRow represents one wallet balance reading.
type WalletRow struct {
	Timestamp string
	Current   float64
}

// EarningsRow represents one earnings report event.
type EarningsRow struct {
	Timestamp   string
	ContainerID string
	Rate        float64
}

// BandwidthRow represents one bandwidth throughput reading.
type BandwidthRow struct {
	Timestamp  string
	Throughput float64
}

// ErrorRow represents one detected error event.
type ErrorRow struct {
	Timestamp string
	Message   string
}

func (db *DB) InsertWallet(rows []WalletRow) {
	if len(rows) == 0 {
		return
	}
	tx, _ := db.pool.Begin()
	stmt, _ := tx.Prepare(`INSERT OR IGNORE INTO wallet (timestamp, current) VALUES (?, ?)`)
	defer stmt.Close()
	for _, r := range rows {
		stmt.Exec(r.Timestamp, r.Current)
	}
	tx.Commit()
}

func (db *DB) InsertEarnings(rows []EarningsRow) {
	if len(rows) == 0 {
		return
	}
	tx, _ := db.pool.Begin()
	stmt, _ := tx.Prepare(`INSERT OR IGNORE INTO earnings (timestamp, container_id, rate) VALUES (?, ?, ?)`)
	defer stmt.Close()
	for _, r := range rows {
		stmt.Exec(r.Timestamp, r.ContainerID, r.Rate)
	}
	tx.Commit()
}

func (db *DB) InsertBandwidth(rows []BandwidthRow) {
	if len(rows) == 0 {
		return
	}
	tx, _ := db.pool.Begin()
	stmt, _ := tx.Prepare(`INSERT OR IGNORE INTO bandwidth (timestamp, throughput) VALUES (?, ?)`)
	defer stmt.Close()
	for _, r := range rows {
		stmt.Exec(r.Timestamp, r.Throughput)
	}
	tx.Commit()

	db.bwMu.Lock()
	db.bwCached = nil
	db.bwMu.Unlock()
}

func (db *DB) InsertErrors(rows []ErrorRow) {
	if len(rows) == 0 {
		return
	}
	tx, _ := db.pool.Begin()
	stmt, _ := tx.Prepare(`INSERT OR IGNORE INTO errors (timestamp, message) VALUES (?, ?)`)
	defer stmt.Close()
	for _, r := range rows {
		stmt.Exec(r.Timestamp, r.Message)
	}
	tx.Commit()
}

// PowerRow represents one power consumption reading.
type PowerRow struct {
	Timestamp  string  `json:"timestamp"`
	GPUPower   float64 `json:"gpu_power"`
	CPUPower   float64 `json:"cpu_power"`
	TotalPower float64 `json:"total_power"`
}

func (db *DB) InsertPower(rows []PowerRow) {
	if len(rows) == 0 {
		return
	}
	tx, _ := db.pool.Begin()
	stmt, _ := tx.Prepare(`INSERT OR IGNORE INTO power (timestamp, gpu_power, cpu_power, total_power) VALUES (?, ?, ?, ?)`)
	defer stmt.Close()
	for _, r := range rows {
		stmt.Exec(r.Timestamp, r.GPUPower, r.CPUPower, r.TotalPower)
	}
	tx.Commit()
}

func (db *DB) InsertSinglePower(r PowerRow) {
	db.pool.Exec(`INSERT OR REPLACE INTO power (timestamp, gpu_power, cpu_power, total_power) VALUES (?, ?, ?, ?)`,
		r.Timestamp, r.GPUPower, r.CPUPower, r.TotalPower)
}

// ─── Query Helpers ────────────────────────────────────────────────────────────

// DashboardData is the full payload returned to the frontend.
type DashboardData struct {
	Wallet    []map[string]any `json:"wallet"`
	Earnings  []map[string]any `json:"earnings"`
	Bandwidth []map[string]any `json:"bandwidth"`
	Power     []map[string]any `json:"power"`
}

// GetData returns wallet, earnings, bandwidth, and power for the given hour window.
// hours=0 means "all time".
func (db *DB) GetData(hours int) DashboardData {
	var timeLimit string
	if hours > 0 {
		timeLimit = time.Now().Add(-time.Duration(hours) * time.Hour).Format("2006-01-02 15:04:05")
	}

	earningsRows := db.queryRows("earnings", []string{"timestamp", "container_id", "rate"}, timeLimit)

	// If currently running a container that started before timeLimit, ensure it is included so charts and history see it
	workload := db.GetActiveWorkload()
	if workload.Status == "running" && workload.ContainerID != "" {
		found := false
		for _, r := range earningsRows {
			if r["container_id"] == workload.ContainerID {
				found = true
				break
			}
		}
		if !found {
			activeRow := map[string]any{
				"timestamp":    workload.StartedAt,
				"container_id": workload.ContainerID,
				"rate":         workload.Rate,
			}
			earningsRows = append([]map[string]any{activeRow}, earningsRows...)
		}
	}

	return DashboardData{
		Wallet:    db.queryRows("wallet", []string{"timestamp", "current"}, timeLimit),
		Earnings:  earningsRows,
		Bandwidth: db.queryRows("bandwidth", []string{"timestamp", "throughput"}, timeLimit),
		Power:     db.queryRows("power", []string{"timestamp", "gpu_power", "cpu_power", "total_power"}, timeLimit),
	}
}

func (db *DB) queryRows(table string, cols []string, timeLimit string) []map[string]any {
	var query string
	if timeLimit == "" {
		query = fmt.Sprintf("SELECT * FROM %s ORDER BY timestamp", table)
	} else {
		query = fmt.Sprintf("SELECT * FROM %s WHERE timestamp >= '%s' ORDER BY timestamp", table, timeLimit)
	}

	rows, err := db.pool.Query(query)
	if err != nil {
		log.Printf("[DB] Query error on %s: %v", table, err)
		return []map[string]any{}
	}
	defer rows.Close()

	columnNames, _ := rows.Columns()
	var result []map[string]any

	for rows.Next() {
		values := make([]any, len(columnNames))
		valuePtrs := make([]any, len(columnNames))
		for i := range values {
			valuePtrs[i] = &values[i]
		}
		rows.Scan(valuePtrs...)
		row := make(map[string]any)
		for i, col := range columnNames {
			row[col] = values[i]
		}
		result = append(result, row)
	}
	if result == nil {
		return []map[string]any{}
	}
	return result
}

// GetLatestWallet returns the single most recent wallet balance row.
func (db *DB) GetLatestWallet() *WalletRow {
	row := db.pool.QueryRow(`SELECT timestamp, current FROM wallet ORDER BY timestamp DESC LIMIT 1`)
	w := &WalletRow{}
	if err := row.Scan(&w.Timestamp, &w.Current); err != nil {
		return nil
	}
	return w
}

// ParseTimestamp tries several standard formats used across Salad log files.
func ParseTimestamp(ts string) (time.Time, error) {
	formats := []string{
		"2006-01-02 15:04:05.999999999 -07:00",
		"2006-01-02 15:04:05.999 -07:00",
		"2006-01-02 15:04:05 -07:00",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, ts, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse timestamp: %q", ts)
}

// ActiveWorkload holds information about the currently running container.
type ActiveWorkload struct {
	Status      string  `json:"status"` // "running" or "idle"
	ContainerID string  `json:"container_id"`
	Rate        float64 `json:"rate"`
	StartedAt   string  `json:"started_at"`
}

// GetActiveWorkload determines if a container workload is actively running on this node.
func (db *DB) GetActiveWorkload() ActiveWorkload {
	var latestTs, latestCont string
	var latestRate float64
	err := db.pool.QueryRow(`SELECT timestamp, container_id, rate FROM earnings ORDER BY timestamp DESC LIMIT 1`).
		Scan(&latestTs, &latestCont, &latestRate)
	if err != nil || latestRate <= 0 {
		return ActiveWorkload{Status: "idle"}
	}

	// Check if Salad is actively logging: Salad writes wallet balance every 1-5 minutes while active.
	// Only mark Idle if wallet has had no updates for > 30 minutes (e.g. app closed, machine off).
	var latestWalletTs string
	var latestWalletVal float64
	wErr := db.pool.QueryRow(`SELECT timestamp, current FROM wallet ORDER BY timestamp DESC LIMIT 1`).
		Scan(&latestWalletTs, &latestWalletVal)
	if wErr == nil {
		if wTime, err := ParseTimestamp(latestWalletTs); err == nil {
			if time.Since(wTime) > 30*time.Minute {
				return ActiveWorkload{Status: "idle"}
			}
		}
	} else {
		if latestTime, err := ParseTimestamp(latestTs); err == nil && time.Since(latestTime) > 30*time.Minute {
			return ActiveWorkload{Status: "idle"}
		}
	}

	return ActiveWorkload{
		Status:      "running",
		ContainerID: latestCont,
		Rate:        latestRate,
		StartedAt:   latestTs,
	}
}

// GetSessionStats calculates active workload session uptime and earnings dynamically from log history.
func (db *DB) GetSessionStats() (string, float64) {
	workload := db.GetActiveWorkload()
	if workload.Status != "running" {
		return "Idle", 0.0
	}

	// Query earnings rows in reverse order to find where the current active session started
	rows, err := db.pool.Query(`SELECT timestamp, rate FROM earnings ORDER BY timestamp DESC LIMIT 1000`)
	if err != nil {
		return "0h 0m", 0.0
	}
	defer rows.Close()

	sessionStartTs := workload.StartedAt
	for rows.Next() {
		var ts string
		var rate float64
		if err := rows.Scan(&ts, &rate); err != nil {
			break
		}
		if rate <= 0 {
			break
		}
		sessionStartTs = ts
	}

	// 3. Find wallet balance at start of session (closest log around sessionStartTs)
	var startWallet float64
	var beforeTs string
	var beforeVal float64
	hasBefore := db.pool.QueryRow(`SELECT timestamp, current FROM wallet WHERE timestamp <= ? ORDER BY timestamp DESC LIMIT 1`, sessionStartTs).
		Scan(&beforeTs, &beforeVal) == nil

	var afterTs string
	var afterVal float64
	hasAfter := db.pool.QueryRow(`SELECT timestamp, current FROM wallet WHERE timestamp >= ? ORDER BY timestamp ASC LIMIT 1`, sessionStartTs).
		Scan(&afterTs, &afterVal) == nil

	startTime, err := ParseTimestamp(sessionStartTs)
	if err == nil {
		if hasBefore {
			tB, errB := ParseTimestamp(beforeTs)
			if errB == nil && startTime.Sub(tB) <= 20*time.Minute {
				startWallet = beforeVal
			} else if hasAfter {
				startWallet = afterVal
			} else {
				startWallet = beforeVal
			}
		} else if hasAfter {
			startWallet = afterVal
		}
	} else {
		if hasBefore {
			startWallet = beforeVal
		} else if hasAfter {
			startWallet = afterVal
		}
	}

	// 4. Find latest wallet balance
	var latestWallet float64
	_ = db.pool.QueryRow(`SELECT current FROM wallet ORDER BY timestamp DESC LIMIT 1`).
		Scan(&latestWallet)

	sessionEarnings := latestWallet - startWallet
	if sessionEarnings < 0 {
		sessionEarnings = 0
	}

	// 5. Calculate uptime
	uptimeStr := "0h 0m"
	if err == nil && !startTime.IsZero() {
		dur := time.Since(startTime)
		if dur < 0 {
			dur = 0
		}
		uptimeStr = fmt.Sprintf("%dh %dm", int(dur.Hours()), int(dur.Minutes())%60)
	}

	return uptimeStr, sessionEarnings
}

// BandwidthSummary holds total data transferred across standard time windows.
type BandwidthSummary struct {
	Usage24h  string  `json:"usage_24h"`
	Usage7d   string  `json:"usage_7d"`
	Usage30d  string  `json:"usage_30d"`
	Usage180d string  `json:"usage_180d"`
	UsageAll  string  `json:"usage_all"`
	Bytes24h  float64 `json:"bytes_24h_mb"`
	Bytes7d   float64 `json:"bytes_7d_mb"`
	Bytes30d  float64 `json:"bytes_30d_mb"`
	Bytes180d float64 `json:"bytes_180d_mb"`
	BytesAll  float64 `json:"bytes_all_mb"`
}

func formatMB(mb float64) string {
	if mb <= 0 {
		return "0.00 MB"
	}
	if mb >= 1024*1024 {
		return fmt.Sprintf("%.2f TB", mb/(1024*1024))
	}
	if mb >= 1024 {
		return fmt.Sprintf("%.2f GB", mb/1024)
	}
	return fmt.Sprintf("%.2f MB", mb)
}

// GetBandwidthSummary calculates total bandwidth transferred in 24h, 1w, 1m, 6m, and all time.
func (db *DB) GetBandwidthSummary() BandwidthSummary {
	db.bwMu.RLock()
	if db.bwCached != nil && time.Since(db.bwCacheTs) < 15*time.Second {
		cached := *db.bwCached
		db.bwMu.RUnlock()
		return cached
	}
	db.bwMu.RUnlock()

	db.bwMu.Lock()
	defer db.bwMu.Unlock()

	if db.bwCached != nil && time.Since(db.bwCacheTs) < 15*time.Second {
		return *db.bwCached
	}

	now := time.Now()
	t24h := now.Add(-24 * time.Hour).Format("2006-01-02 15:04:05")
	t7d := now.Add(-7 * 24 * time.Hour).Format("2006-01-02 15:04:05")
	t30d := now.Add(-30 * 24 * time.Hour).Format("2006-01-02 15:04:05")
	t180d := now.Add(-180 * 24 * time.Hour).Format("2006-01-02 15:04:05")

	rows, err := db.pool.Query(`SELECT timestamp, throughput FROM bandwidth ORDER BY timestamp ASC`)
	if err != nil {
		return BandwidthSummary{
			Usage24h:  "0.00 MB",
			Usage7d:   "0.00 MB",
			Usage30d:  "0.00 MB",
			Usage180d: "0.00 MB",
			UsageAll:  "0.00 MB",
		}
	}
	defer rows.Close()

	var mb24h, mb7d, mb30d, mb180d, mbAll float64
	var prevTime time.Time

	for rows.Next() {
		var ts string
		var tp float64
		if err := rows.Scan(&ts, &tp); err != nil {
			break
		}
		t, err := ParseTimestamp(ts)
		dt := 30.0
		if err == nil && !prevTime.IsZero() {
			diff := t.Sub(prevTime).Seconds()
			if diff > 0 && diff <= 120 {
				dt = diff
			}
		}
		if err == nil {
			prevTime = t
		}

		segmentMB := tp * dt
		mbAll += segmentMB
		if ts >= t180d {
			mb180d += segmentMB
		}
		if ts >= t30d {
			mb30d += segmentMB
		}
		if ts >= t7d {
			mb7d += segmentMB
		}
		if ts >= t24h {
			mb24h += segmentMB
		}
	}

	sum := BandwidthSummary{
		Usage24h:  formatMB(mb24h),
		Usage7d:   formatMB(mb7d),
		Usage30d:  formatMB(mb30d),
		Usage180d: formatMB(mb180d),
		UsageAll:  formatMB(mbAll),
		Bytes24h:  mb24h,
		Bytes7d:   mb7d,
		Bytes30d:  mb30d,
		Bytes180d: mb180d,
		BytesAll:  mbAll,
	}

	db.bwCached = &sum
	db.bwCacheTs = now
	return sum
}

// GetActiveErrors returns all non-dismissed error rows, newest first.
func (db *DB) GetActiveErrors() []map[string]any {
	rows, err := db.pool.Query(`SELECT timestamp, message, dismissed FROM errors WHERE dismissed = 0 ORDER BY timestamp DESC`)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		var ts, msg string
		var dismissed int
		rows.Scan(&ts, &msg, &dismissed)
		result = append(result, map[string]any{
			"timestamp": ts,
			"message":   msg,
			"dismissed": dismissed,
		})
	}
	if result == nil {
		return []map[string]any{}
	}
	return result
}

// DismissError marks a single error as dismissed by timestamp.
func (db *DB) DismissError(timestamp string) {
	db.pool.Exec(`UPDATE errors SET dismissed = 1 WHERE timestamp = ?`, timestamp)
}

// DismissAllErrors marks all errors as dismissed.
func (db *DB) DismissAllErrors() {
	db.pool.Exec(`UPDATE errors SET dismissed = 1`)
}

// ExportCSV streams table data formatted as CSV to the writer.
func (db *DB) ExportCSV(w io.Writer, table string) error {
	var query string
	switch table {
	case "wallet":
		query = "SELECT timestamp, current FROM wallet ORDER BY timestamp ASC"
	case "earnings":
		query = "SELECT timestamp, container_id, rate FROM earnings ORDER BY timestamp ASC"
	case "bandwidth":
		query = "SELECT timestamp, throughput FROM bandwidth ORDER BY timestamp ASC"
	case "power":
		query = "SELECT timestamp, gpu_power, cpu_power, total_power FROM power ORDER BY timestamp ASC"
	case "errors":
		query = "SELECT timestamp, message, dismissed FROM errors ORDER BY timestamp ASC"
	default:
		return fmt.Errorf("unknown table %s", table)
	}

	rows, err := db.pool.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return err
	}

	writer := csv.NewWriter(w)
	defer writer.Flush()

	// Write header
	if err := writer.Write(cols); err != nil {
		return err
	}

	// Write rows
	values := make([]any, len(cols))
	scanArgs := make([]any, len(cols))
	for i := range values {
		scanArgs[i] = &values[i]
	}

	for rows.Next() {
		if err := rows.Scan(scanArgs...); err != nil {
			return err
		}
		record := make([]string, len(cols))
		for i, val := range values {
			if val == nil {
				record[i] = ""
			} else {
				record[i] = fmt.Sprintf("%v", val)
			}
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}
	return rows.Err()
}
