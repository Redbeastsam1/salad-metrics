package db

import (
	"database/sql"
	"fmt"
	"log"
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
	CREATE INDEX IF NOT EXISTS idx_wallet_ts    ON wallet(timestamp);
	CREATE INDEX IF NOT EXISTS idx_earnings_ts  ON earnings(timestamp);
	CREATE INDEX IF NOT EXISTS idx_bandwidth_ts ON bandwidth(timestamp);
	CREATE INDEX IF NOT EXISTS idx_errors_ts    ON errors(timestamp);
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

// ─── Query Helpers ────────────────────────────────────────────────────────────

// DashboardData is the full payload returned to the frontend.
type DashboardData struct {
	Wallet    []map[string]any `json:"wallet"`
	Earnings  []map[string]any `json:"earnings"`
	Bandwidth []map[string]any `json:"bandwidth"`
}

// GetData returns wallet, earnings, and bandwidth for the given hour window.
// hours=0 means "all time".
func (db *DB) GetData(hours int) DashboardData {
	var timeLimit string
	if hours > 0 {
		timeLimit = time.Now().Add(-time.Duration(hours) * time.Hour).Format("2006-01-02 15:04:05")
	}

	return DashboardData{
		Wallet:    db.queryRows("wallet", []string{"timestamp", "current"}, timeLimit),
		Earnings:  db.queryRows("earnings", []string{"timestamp", "container_id", "rate"}, timeLimit),
		Bandwidth: db.queryRows("bandwidth", []string{"timestamp", "throughput"}, timeLimit),
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

// GetFirstWalletAfter returns the first wallet reading at or after the given time (for session earnings).
func (db *DB) GetFirstWalletAfter(t time.Time) *WalletRow {
	ts := t.Format("2006-01-02 15:04:05")
	row := db.pool.QueryRow(`SELECT timestamp, current FROM wallet WHERE timestamp >= ? ORDER BY timestamp ASC LIMIT 1`, ts)
	w := &WalletRow{}
	if err := row.Scan(&w.Timestamp, &w.Current); err != nil {
		return nil
	}
	return w
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
