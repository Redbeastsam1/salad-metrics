package db

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"math"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps a sql.DB connection pool for all Salad metrics data.
type DB struct {
	pool *sql.DB
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
		timestamp TEXT,
		machine TEXT NOT NULL DEFAULT 'Beast',
		current REAL NOT NULL,
		PRIMARY KEY (timestamp, machine)
	);
	CREATE TABLE IF NOT EXISTS earnings (
		timestamp TEXT,
		machine TEXT NOT NULL DEFAULT 'Beast',
		container_id TEXT NOT NULL,
		rate REAL NOT NULL,
		PRIMARY KEY (timestamp, machine)
	);
	CREATE TABLE IF NOT EXISTS bandwidth (
		timestamp TEXT,
		machine TEXT NOT NULL DEFAULT 'Beast',
		throughput REAL NOT NULL,
		PRIMARY KEY (timestamp, machine)
	);
	CREATE TABLE IF NOT EXISTS errors (
		timestamp TEXT,
		machine TEXT NOT NULL DEFAULT 'Beast',
		message TEXT NOT NULL,
		dismissed INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (timestamp, machine)
	);
	CREATE TABLE IF NOT EXISTS power (
		timestamp TEXT,
		machine TEXT NOT NULL DEFAULT 'Beast',
		gpu_power REAL NOT NULL,
		cpu_power REAL NOT NULL,
		total_power REAL NOT NULL,
		PRIMARY KEY (timestamp, machine)
	);
	CREATE INDEX IF NOT EXISTS idx_wallet_ts    ON wallet(timestamp);
	CREATE INDEX IF NOT EXISTS idx_earnings_ts  ON earnings(timestamp);
	CREATE INDEX IF NOT EXISTS idx_bandwidth_ts ON bandwidth(timestamp);
	CREATE INDEX IF NOT EXISTS idx_errors_ts    ON errors(timestamp);
	CREATE INDEX IF NOT EXISTS idx_power_ts     ON power(timestamp);
	CREATE INDEX IF NOT EXISTS idx_wallet_mach    ON wallet(machine);
	CREATE INDEX IF NOT EXISTS idx_earnings_mach  ON earnings(machine);
	CREATE INDEX IF NOT EXISTS idx_bandwidth_mach ON bandwidth(machine);
	CREATE INDEX IF NOT EXISTS idx_errors_mach    ON errors(machine);
	CREATE INDEX IF NOT EXISTS idx_power_mach     ON power(machine);
	`
	if _, err := db.pool.Exec(schema); err != nil {
		return fmt.Errorf("schema: %w", err)
	}

	// Ensure machine column exists on legacy databases
	tables := []string{"wallet", "earnings", "bandwidth", "power", "errors"}
	for _, t := range tables {
		db.ensureMachineColumn(t)
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

	log.Println("[DB] Initialized with WAL mode, indexes, and multi-node machine tracking.")
	return nil
}

func (db *DB) ensureMachineColumn(table string) {
	rows, err := db.pool.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return
	}
	defer rows.Close()

	hasMachine := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err == nil {
			if name == "machine" {
				hasMachine = true
				break
			}
		}
	}
	if !hasMachine {
		_, _ = db.pool.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN machine TEXT NOT NULL DEFAULT 'Beast'", table))
		_, _ = db.pool.Exec(fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_mach ON %s(machine)", table, table))
	}
}

// ─── Insert Helpers ──────────────────────────────────────────────────────────

// WalletRow represents one wallet balance reading.
type WalletRow struct {
	Timestamp string `json:"timestamp"`
	Machine   string `json:"machine,omitempty"`
	Current   float64 `json:"current"`
}

// EarningsRow represents one earnings report event.
type EarningsRow struct {
	Timestamp   string  `json:"timestamp"`
	Machine     string  `json:"machine,omitempty"`
	ContainerID string  `json:"container_id"`
	Rate        float64 `json:"rate"`
}

// BandwidthRow represents one bandwidth throughput reading.
type BandwidthRow struct {
	Timestamp  string  `json:"timestamp"`
	Machine    string  `json:"machine,omitempty"`
	Throughput float64 `json:"throughput"`
}

// ErrorRow represents one detected error event.
type ErrorRow struct {
	Timestamp string `json:"timestamp"`
	Machine   string `json:"machine,omitempty"`
	Message   string `json:"message"`
}

// PowerRow represents one power consumption reading.
type PowerRow struct {
	Timestamp  string  `json:"timestamp"`
	Machine    string  `json:"machine,omitempty"`
	GPUPower   float64 `json:"gpu_power"`
	CPUPower   float64 `json:"cpu_power"`
	TotalPower float64 `json:"total_power"`
}

func (db *DB) InsertWallet(rows []WalletRow) {
	db.InsertWalletWithMachine("", rows)
}

func (db *DB) InsertWalletWithMachine(machine string, rows []WalletRow) {
	if len(rows) == 0 {
		return
	}
	if machine == "" {
		machine = "Beast"
	}
	tx, err := db.pool.Begin()
	if err != nil {
		return
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO wallet (timestamp, machine, current) VALUES (?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return
	}
	defer stmt.Close()
	for _, r := range rows {
		m := r.Machine
		if m == "" {
			m = machine
		}
		stmt.Exec(r.Timestamp, m, r.Current)
	}
	tx.Commit()
}

func (db *DB) InsertEarnings(rows []EarningsRow) {
	db.InsertEarningsWithMachine("", rows)
}

func (db *DB) InsertEarningsWithMachine(machine string, rows []EarningsRow) {
	if len(rows) == 0 {
		return
	}
	if machine == "" {
		machine = "Beast"
	}
	tx, err := db.pool.Begin()
	if err != nil {
		return
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO earnings (timestamp, machine, container_id, rate) VALUES (?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return
	}
	defer stmt.Close()
	for _, r := range rows {
		m := r.Machine
		if m == "" {
			m = machine
		}
		stmt.Exec(r.Timestamp, m, r.ContainerID, r.Rate)
	}
	tx.Commit()
}

func (db *DB) InsertBandwidth(rows []BandwidthRow) {
	db.InsertBandwidthWithMachine("", rows)
}

func (db *DB) InsertBandwidthWithMachine(machine string, rows []BandwidthRow) {
	if len(rows) == 0 {
		return
	}
	if machine == "" {
		machine = "Beast"
	}
	tx, err := db.pool.Begin()
	if err != nil {
		return
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO bandwidth (timestamp, machine, throughput) VALUES (?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return
	}
	defer stmt.Close()
	for _, r := range rows {
		m := r.Machine
		if m == "" {
			m = machine
		}
		stmt.Exec(r.Timestamp, m, r.Throughput)
	}
	tx.Commit()
}

func (db *DB) InsertErrors(rows []ErrorRow) {
	db.InsertErrorsWithMachine("", rows)
}

func (db *DB) InsertErrorsWithMachine(machine string, rows []ErrorRow) {
	if len(rows) == 0 {
		return
	}
	if machine == "" {
		machine = "Beast"
	}
	tx, err := db.pool.Begin()
	if err != nil {
		return
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO errors (timestamp, machine, message) VALUES (?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return
	}
	defer stmt.Close()
	for _, r := range rows {
		m := r.Machine
		if m == "" {
			m = machine
		}
		stmt.Exec(r.Timestamp, m, r.Message)
	}
	tx.Commit()
}

func (db *DB) InsertPower(rows []PowerRow) {
	db.InsertPowerWithMachine("", rows)
}

func (db *DB) InsertPowerWithMachine(machine string, rows []PowerRow) {
	if len(rows) == 0 {
		return
	}
	if machine == "" {
		machine = "Beast"
	}
	tx, err := db.pool.Begin()
	if err != nil {
		return
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO power (timestamp, machine, gpu_power, cpu_power, total_power) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return
	}
	defer stmt.Close()
	for _, r := range rows {
		m := r.Machine
		if m == "" {
			m = machine
		}
		stmt.Exec(r.Timestamp, m, r.GPUPower, r.CPUPower, r.TotalPower)
	}
	tx.Commit()
}

func (db *DB) InsertSinglePower(r PowerRow) {
	db.InsertSinglePowerWithMachine("", r)
}

func (db *DB) InsertSinglePowerWithMachine(machine string, r PowerRow) {
	if machine == "" {
		machine = r.Machine
		if machine == "" {
			machine = "Beast"
		}
	}
	db.pool.Exec(`INSERT OR REPLACE INTO power (timestamp, machine, gpu_power, cpu_power, total_power) VALUES (?, ?, ?, ?, ?)`,
		r.Timestamp, machine, r.GPUPower, r.CPUPower, r.TotalPower)
}

// ─── Query Helpers & Adaptive Downsampling ───────────────────────────────────

// DashboardData is the full payload returned to the frontend.
type DashboardData struct {
	Wallet    []map[string]any `json:"wallet"`
	Earnings  []map[string]any `json:"earnings"`
	Bandwidth []map[string]any `json:"bandwidth"`
	Power     []map[string]any `json:"power"`
}

// ParseTimestamp tries several standard formats used across Salad log files.
func ParseTimestamp(ts string) (time.Time, error) {
	if len(ts) >= 19 {
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", ts[:19], time.Local); err == nil {
			return t, nil
		}
	}
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

// GetData returns wallet, earnings, bandwidth, and power with adaptive downsampling for chart performance.
func (db *DB) GetData(hours int, machine string) DashboardData {
	var timeLimit string
	if hours > 0 {
		timeLimit = time.Now().Add(-time.Duration(hours) * time.Hour).Format("2006-01-02 15:04:05")
	}

	return DashboardData{
		Wallet:    db.getDownsampledWallet(timeLimit, machine),
		Earnings:  db.getDeduplicatedEarnings(timeLimit, machine),
		Bandwidth: db.getDownsampledBandwidth(timeLimit, machine),
		Power:     db.getDownsampledPower(timeLimit, machine),
	}
}

// ── Wallet Downsampling ──
func (db *DB) getDownsampledWallet(timeLimit, machine string) []map[string]any {
	var query string
	var args []any
	if timeLimit == "" {
		if machine != "" && machine != "all" {
			query = "SELECT timestamp, current FROM wallet WHERE machine = ? ORDER BY timestamp ASC"
			args = append(args, machine)
		} else {
			query = "SELECT timestamp, current FROM wallet ORDER BY timestamp ASC"
		}
	} else {
		if machine != "" && machine != "all" {
			query = "SELECT timestamp, current FROM wallet WHERE timestamp >= ? AND machine = ? ORDER BY timestamp ASC"
			args = append(args, timeLimit, machine)
		} else {
			query = "SELECT timestamp, current FROM wallet WHERE timestamp >= ? ORDER BY timestamp ASC"
			args = append(args, timeLimit)
		}
	}

	rows, err := db.pool.Query(query, args...)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()

	type rawW struct {
		t  time.Time
		v  float64
		ts string
	}
	var data []rawW
	for rows.Next() {
		var ts string
		var v float64
		if err := rows.Scan(&ts, &v); err == nil {
			if t, err := ParseTimestamp(ts); err == nil {
				data = append(data, rawW{t: t, v: v, ts: ts})
			}
		}
	}

	target := 350
	if len(data) <= target {
		res := make([]map[string]any, len(data))
		for i, d := range data {
			res[i] = map[string]any{"timestamp": d.ts, "current": d.v}
		}
		return res
	}

	start := data[0].t
	end := data[len(data)-1].t
	bucketDur := end.Sub(start) / time.Duration(target)
	if bucketDur < time.Second {
		bucketDur = time.Second
	}

	var downsampled []map[string]any
	idx := 0
	for bStart := start; bStart.Before(end) || idx < len(data); bStart = bStart.Add(bucketDur) {
		bEnd := bStart.Add(bucketDur)
		var lastVal float64
		var lastTs string
		count := 0

		for idx < len(data) && (data[idx].t.Before(bEnd) || idx == len(data)-1) {
			lastVal = data[idx].v
			lastTs = data[idx].ts
			count++
			idx++
			if idx == len(data) {
				break
			}
		}

		if count > 0 {
			downsampled = append(downsampled, map[string]any{
				"timestamp": lastTs,
				"current":   lastVal,
			})
		}
		if idx >= len(data) {
			break
		}
	}
	return downsampled
}

// ── Bandwidth Downsampling ──
func (db *DB) getDownsampledBandwidth(timeLimit, machine string) []map[string]any {
	var query string
	var args []any
	if timeLimit == "" {
		if machine != "" && machine != "all" {
			query = "SELECT timestamp, throughput FROM bandwidth WHERE machine = ? ORDER BY timestamp ASC"
			args = append(args, machine)
		} else {
			query = "SELECT timestamp, throughput FROM bandwidth ORDER BY timestamp ASC"
		}
	} else {
		if machine != "" && machine != "all" {
			query = "SELECT timestamp, throughput FROM bandwidth WHERE timestamp >= ? AND machine = ? ORDER BY timestamp ASC"
			args = append(args, timeLimit, machine)
		} else {
			query = "SELECT timestamp, throughput FROM bandwidth WHERE timestamp >= ? ORDER BY timestamp ASC"
			args = append(args, timeLimit)
		}
	}

	rows, err := db.pool.Query(query, args...)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()

	type rawBW struct {
		t  time.Time
		v  float64
		ts string
	}
	var data []rawBW
	for rows.Next() {
		var ts string
		var v float64
		if err := rows.Scan(&ts, &v); err == nil {
			if t, err := ParseTimestamp(ts); err == nil {
				data = append(data, rawBW{t: t, v: v, ts: ts})
			}
		}
	}

	target := 350
	if len(data) <= target {
		res := make([]map[string]any, len(data))
		for i, d := range data {
			res[i] = map[string]any{"timestamp": d.ts, "throughput": d.v}
		}
		return res
	}

	start := data[0].t
	end := data[len(data)-1].t
	bucketDur := end.Sub(start) / time.Duration(target)
	if bucketDur < time.Second {
		bucketDur = time.Second
	}

	var downsampled []map[string]any
	idx := 0
	for bStart := start; bStart.Before(end) || idx < len(data); bStart = bStart.Add(bucketDur) {
		bEnd := bStart.Add(bucketDur)
		var sum float64
		count := 0
		var lastTs string

		for idx < len(data) && (data[idx].t.Before(bEnd) || idx == len(data)-1) {
			sum += data[idx].v
			lastTs = data[idx].ts
			count++
			idx++
			if idx == len(data) {
				break
			}
		}

		if count > 0 {
			downsampled = append(downsampled, map[string]any{
				"timestamp":  lastTs,
				"throughput": math.Round((sum/float64(count))*1000) / 1000,
			})
		}
		if idx >= len(data) {
			break
		}
	}
	return downsampled
}

// ── Power Downsampling ──
func (db *DB) getDownsampledPower(timeLimit, machine string) []map[string]any {
	var query string
	var args []any
	if timeLimit == "" {
		if machine != "" && machine != "all" {
			query = "SELECT timestamp, gpu_power, cpu_power, total_power FROM power WHERE machine = ? ORDER BY timestamp ASC"
			args = append(args, machine)
		} else {
			query = "SELECT timestamp, gpu_power, cpu_power, total_power FROM power ORDER BY timestamp ASC"
		}
	} else {
		if machine != "" && machine != "all" {
			query = "SELECT timestamp, gpu_power, cpu_power, total_power FROM power WHERE timestamp >= ? AND machine = ? ORDER BY timestamp ASC"
			args = append(args, timeLimit, machine)
		} else {
			query = "SELECT timestamp, gpu_power, cpu_power, total_power FROM power WHERE timestamp >= ? ORDER BY timestamp ASC"
			args = append(args, timeLimit)
		}
	}

	rows, err := db.pool.Query(query, args...)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()

	type rawP struct {
		t     time.Time
		gpu   float64
		cpu   float64
		total float64
		ts    string
	}
	var data []rawP
	for rows.Next() {
		var ts string
		var g, c, tot float64
		if err := rows.Scan(&ts, &g, &c, &tot); err == nil {
			if t, err := ParseTimestamp(ts); err == nil {
				data = append(data, rawP{t: t, gpu: g, cpu: c, total: tot, ts: ts})
			}
		}
	}

	target := 350
	if len(data) <= target {
		res := make([]map[string]any, len(data))
		for i, d := range data {
			res[i] = map[string]any{
				"timestamp":   d.ts,
				"gpu_power":   d.gpu,
				"cpu_power":   d.cpu,
				"total_power": d.total,
			}
		}
		return res
	}

	start := data[0].t
	end := data[len(data)-1].t
	bucketDur := end.Sub(start) / time.Duration(target)
	if bucketDur < time.Second {
		bucketDur = time.Second
	}

	var downsampled []map[string]any
	idx := 0
	for bStart := start; bStart.Before(end) || idx < len(data); bStart = bStart.Add(bucketDur) {
		bEnd := bStart.Add(bucketDur)
		var sumG, sumC, sumTot float64
		count := 0
		var lastTs string

		for idx < len(data) && (data[idx].t.Before(bEnd) || idx == len(data)-1) {
			sumG += data[idx].gpu
			sumC += data[idx].cpu
			sumTot += data[idx].total
			lastTs = data[idx].ts
			count++
			idx++
			if idx == len(data) {
				break
			}
		}

		if count > 0 {
			downsampled = append(downsampled, map[string]any{
				"timestamp":   lastTs,
				"gpu_power":   math.Round((sumG/float64(count))*10) / 10,
				"cpu_power":   math.Round((sumC/float64(count))*10) / 10,
				"total_power": math.Round((sumTot/float64(count))*10) / 10,
			})
		}
		if idx >= len(data) {
			break
		}
	}
	return downsampled
}

// ── Earnings Step-Function Deduplication ──
func (db *DB) getDeduplicatedEarnings(timeLimit, machine string) []map[string]any {
	var query string
	var args []any
	if timeLimit == "" {
		if machine != "" && machine != "all" {
			query = "SELECT timestamp, container_id, rate, machine FROM earnings WHERE machine = ? ORDER BY timestamp ASC"
			args = append(args, machine)
		} else {
			query = "SELECT timestamp, container_id, rate, machine FROM earnings ORDER BY timestamp ASC"
		}
	} else {
		if machine != "" && machine != "all" {
			query = "SELECT timestamp, container_id, rate, machine FROM earnings WHERE timestamp >= ? AND machine = ? ORDER BY timestamp ASC"
			args = append(args, timeLimit, machine)
		} else {
			query = "SELECT timestamp, container_id, rate, machine FROM earnings WHERE timestamp >= ? ORDER BY timestamp ASC"
			args = append(args, timeLimit)
		}
	}

	rows, err := db.pool.Query(query, args...)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()

	type rawE struct {
		ts      string
		contID  string
		rate    float64
		machine string
	}
	var raw []rawE
	for rows.Next() {
		var ts, contID, m string
		var r float64
		if err := rows.Scan(&ts, &contID, &r, &m); err == nil {
			raw = append(raw, rawE{ts: ts, contID: contID, rate: r, machine: m})
		}
	}

	// Ensure active running workload is included if started before timeLimit
	workload := db.GetActiveWorkload(machine)
	if workload.Status == "running" && workload.ContainerID != "" {
		found := false
		for _, r := range raw {
			if r.contID == workload.ContainerID {
				found = true
				break
			}
		}
		if !found {
			raw = append([]rawE{{
				ts:      workload.StartedAt,
				contID:  workload.ContainerID,
				rate:    workload.Rate,
				machine: machine,
			}}, raw...)
		}
	}

	if len(raw) <= 2 {
		res := make([]map[string]any, len(raw))
		for i, r := range raw {
			res[i] = map[string]any{
				"timestamp":    r.ts,
				"container_id": r.contID,
				"rate":         r.rate,
				"machine":      r.machine,
			}
		}
		return res
	}

	// Step-function deduplication: retain transition edges and bounds
	var deduped []map[string]any
	for i := 0; i < len(raw); i++ {
		isFirst := (i == 0)
		isLast := (i == len(raw)-1)
		rateChangedPrev := !isFirst && (raw[i].rate != raw[i-1].rate || raw[i].contID != raw[i-1].contID || raw[i].machine != raw[i-1].machine)
		rateChangedNext := !isLast && (raw[i].rate != raw[i+1].rate || raw[i].contID != raw[i+1].contID || raw[i].machine != raw[i+1].machine)

		if isFirst || isLast || rateChangedPrev || rateChangedNext {
			deduped = append(deduped, map[string]any{
				"timestamp":    raw[i].ts,
				"container_id": raw[i].contID,
				"rate":         raw[i].rate,
				"machine":      raw[i].machine,
			})
		}
	}
	return deduped
}

// GetLatestWallet returns the single most recent wallet balance row.
func (db *DB) GetLatestWallet(machine ...string) *WalletRow {
	mach := ""
	if len(machine) > 0 {
		mach = machine[0]
	}
	var query string
	var row *sql.Row
	if mach != "" && mach != "all" {
		query = `SELECT timestamp, machine, current FROM wallet WHERE machine = ? ORDER BY timestamp DESC LIMIT 1`
		row = db.pool.QueryRow(query, mach)
	} else {
		query = `SELECT timestamp, machine, current FROM wallet ORDER BY timestamp DESC LIMIT 1`
		row = db.pool.QueryRow(query)
	}
	w := &WalletRow{}
	if err := row.Scan(&w.Timestamp, &w.Machine, &w.Current); err != nil {
		return nil
	}
	return w
}

// ActiveWorkload holds information about the currently running container.
type ActiveWorkload struct {
	Status      string  `json:"status"` // "running" or "idle"
	ContainerID string  `json:"container_id"`
	Rate        float64 `json:"rate"`
	StartedAt   string  `json:"started_at"`
	Machine     string  `json:"machine,omitempty"`
}

// GetActiveWorkload determines if a container workload is actively running.
func (db *DB) GetActiveWorkload(machine ...string) ActiveWorkload {
	mach := ""
	if len(machine) > 0 {
		mach = machine[0]
	}
	var query string
	var row *sql.Row
	if mach != "" && mach != "all" {
		query = `SELECT timestamp, machine, container_id, rate FROM earnings WHERE machine = ? ORDER BY timestamp DESC LIMIT 1`
		row = db.pool.QueryRow(query, mach)
	} else {
		query = `SELECT timestamp, machine, container_id, rate FROM earnings ORDER BY timestamp DESC LIMIT 1`
		row = db.pool.QueryRow(query)
	}

	var latestTs, latestMach, latestCont string
	var latestRate float64
	err := row.Scan(&latestTs, &latestMach, &latestCont, &latestRate)
	if err != nil || latestRate <= 0 {
		return ActiveWorkload{Status: "idle", Machine: mach}
	}

	// Salad writes wallet balance every 1-5 minutes while active.
	// Only mark Idle if wallet has had no updates for > 30 minutes.
	var latestWalletTs string
	var latestWalletVal float64
	var wErr error
	if mach != "" && mach != "all" {
		wErr = db.pool.QueryRow(`SELECT timestamp, current FROM wallet WHERE machine = ? ORDER BY timestamp DESC LIMIT 1`, mach).
			Scan(&latestWalletTs, &latestWalletVal)
	} else {
		wErr = db.pool.QueryRow(`SELECT timestamp, current FROM wallet ORDER BY timestamp DESC LIMIT 1`).
			Scan(&latestWalletTs, &latestWalletVal)
	}

	if wErr == nil {
		if wTime, err := ParseTimestamp(latestWalletTs); err == nil {
			if time.Since(wTime) > 30*time.Minute {
				return ActiveWorkload{Status: "idle", Machine: latestMach}
			}
		}
	} else {
		if latestTime, err := ParseTimestamp(latestTs); err == nil && time.Since(latestTime) > 30*time.Minute {
			return ActiveWorkload{Status: "idle", Machine: latestMach}
		}
	}

	return ActiveWorkload{
		Status:      "running",
		ContainerID: latestCont,
		Rate:        latestRate,
		StartedAt:   latestTs,
		Machine:     latestMach,
	}
}

// GetSessionStats calculates active workload session uptime and earnings dynamically from log history.
func (db *DB) GetSessionStats(machine ...string) (string, float64) {
	mach := ""
	if len(machine) > 0 {
		mach = machine[0]
	}
	workload := db.GetActiveWorkload(mach)
	if workload.Status != "running" {
		return "Idle", 0.0
	}

	var rows *sql.Rows
	var err error
	if mach != "" && mach != "all" {
		rows, err = db.pool.Query(`SELECT timestamp, rate FROM earnings WHERE machine = ? ORDER BY timestamp DESC LIMIT 1000`, mach)
	} else {
		rows, err = db.pool.Query(`SELECT timestamp, rate FROM earnings ORDER BY timestamp DESC LIMIT 1000`)
	}
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

	var startWallet float64
	var beforeTs string
	var beforeVal float64
	var hasBefore bool
	if mach != "" && mach != "all" {
		hasBefore = db.pool.QueryRow(`SELECT timestamp, current FROM wallet WHERE machine = ? AND timestamp <= ? ORDER BY timestamp DESC LIMIT 1`, mach, sessionStartTs).
			Scan(&beforeTs, &beforeVal) == nil
	} else {
		hasBefore = db.pool.QueryRow(`SELECT timestamp, current FROM wallet WHERE timestamp <= ? ORDER BY timestamp DESC LIMIT 1`, sessionStartTs).
			Scan(&beforeTs, &beforeVal) == nil
	}

	var afterTs string
	var afterVal float64
	var hasAfter bool
	if mach != "" && mach != "all" {
		hasAfter = db.pool.QueryRow(`SELECT timestamp, current FROM wallet WHERE machine = ? AND timestamp >= ? ORDER BY timestamp ASC LIMIT 1`, mach, sessionStartTs).
			Scan(&afterTs, &afterVal) == nil
	} else {
		hasAfter = db.pool.QueryRow(`SELECT timestamp, current FROM wallet WHERE timestamp >= ? ORDER BY timestamp ASC LIMIT 1`, sessionStartTs).
			Scan(&afterTs, &afterVal) == nil
	}

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

	var latestWallet float64
	if mach != "" && mach != "all" {
		_ = db.pool.QueryRow(`SELECT current FROM wallet WHERE machine = ? ORDER BY timestamp DESC LIMIT 1`, mach).
			Scan(&latestWallet)
	} else {
		_ = db.pool.QueryRow(`SELECT current FROM wallet ORDER BY timestamp DESC LIMIT 1`).
			Scan(&latestWallet)
	}

	sessionEarnings := latestWallet - startWallet
	if sessionEarnings < 0 {
		sessionEarnings = 0
	}

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

// GetKnownMachines returns a sorted list of all unique machine names recorded in the database.
func (db *DB) GetKnownMachines() []string {
	rows, err := db.pool.Query(`
		SELECT DISTINCT machine FROM earnings WHERE machine != ''
		UNION
		SELECT DISTINCT machine FROM wallet WHERE machine != ''
		UNION
		SELECT DISTINCT machine FROM power WHERE machine != ''
		ORDER BY machine ASC
	`)
	if err != nil {
		return []string{"Beast"}
	}
	defer rows.Close()

	var machines []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err == nil && m != "" {
			machines = append(machines, m)
		}
	}
	if len(machines) == 0 {
		return []string{"Beast"}
	}
	return machines
}

// FleetSummary holds aggregated metrics across all active Salad nodes.
type FleetSummary struct {
	TotalRate       float64 `json:"total_rate"`
	TotalEarnings   float64 `json:"total_earnings"`
	WalletBalance   float64 `json:"wallet_balance"`
	TotalPower      float64 `json:"total_power"`
	ActiveNodes     int     `json:"active_nodes"`
	TotalNodes      int     `json:"total_nodes"`
}

// GetActiveErrors returns all non-dismissed error rows, newest first.
func (db *DB) GetActiveErrors(machine ...string) []map[string]any {
	mach := ""
	if len(machine) > 0 {
		mach = machine[0]
	}
	var query string
	var args []any
	if mach != "" && mach != "all" {
		query = `SELECT timestamp, message, dismissed, machine FROM errors WHERE dismissed = 0 AND machine = ? ORDER BY timestamp DESC`
		args = append(args, mach)
	} else {
		query = `SELECT timestamp, message, dismissed, machine FROM errors WHERE dismissed = 0 ORDER BY timestamp DESC`
	}

	rows, err := db.pool.Query(query, args...)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		var ts, msg, m string
		var dismissed int
		if err := rows.Scan(&ts, &msg, &dismissed, &m); err == nil {
			result = append(result, map[string]any{
				"timestamp": ts,
				"message":   msg,
				"dismissed": dismissed,
				"machine":   m,
			})
		}
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
func (db *DB) ExportCSV(w io.Writer, table string, machine ...string) error {
	mach := ""
	if len(machine) > 0 {
		mach = machine[0]
	}
	var query string
	var args []any
	switch table {
	case "wallet":
		if mach != "" && mach != "all" {
			query = "SELECT timestamp, machine, current FROM wallet WHERE machine = ? ORDER BY timestamp ASC"
			args = append(args, mach)
		} else {
			query = "SELECT timestamp, machine, current FROM wallet ORDER BY timestamp ASC"
		}
	case "earnings":
		if mach != "" && mach != "all" {
			query = "SELECT timestamp, machine, container_id, rate FROM earnings WHERE machine = ? ORDER BY timestamp ASC"
			args = append(args, mach)
		} else {
			query = "SELECT timestamp, machine, container_id, rate FROM earnings ORDER BY timestamp ASC"
		}
	case "bandwidth":
		if mach != "" && mach != "all" {
			query = "SELECT timestamp, machine, throughput FROM bandwidth WHERE machine = ? ORDER BY timestamp ASC"
			args = append(args, mach)
		} else {
			query = "SELECT timestamp, machine, throughput FROM bandwidth ORDER BY timestamp ASC"
		}
	case "power":
		if mach != "" && mach != "all" {
			query = "SELECT timestamp, machine, gpu_power, cpu_power, total_power FROM power WHERE machine = ? ORDER BY timestamp ASC"
			args = append(args, mach)
		} else {
			query = "SELECT timestamp, machine, gpu_power, cpu_power, total_power FROM power ORDER BY timestamp ASC"
		}
	case "errors":
		if mach != "" && mach != "all" {
			query = "SELECT timestamp, machine, message, dismissed FROM errors WHERE machine = ? ORDER BY timestamp ASC"
			args = append(args, mach)
		} else {
			query = "SELECT timestamp, machine, message, dismissed FROM errors ORDER BY timestamp ASC"
		}
	default:
		return fmt.Errorf("unknown table %s", table)
	}

	rows, err := db.pool.Query(query, args...)
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

	if err := writer.Write(cols); err != nil {
		return err
	}

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
