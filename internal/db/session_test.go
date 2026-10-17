package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSessionStatsCalculation(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer database.Close()

	// Initial empty state should be Idle / 0.0
	uptime, earnings := database.GetSessionStats()
	if uptime != "Idle" || earnings != 0.0 {
		t.Fatalf("Expected Idle / 0.0 on empty DB, got %s / %f", uptime, earnings)
	}

	// 1. Insert workload start 1 hour ago
	now := time.Now()
	startTime := now.Add(-1 * time.Hour).Format("2006-01-02 15:04:05")
	database.InsertEarnings([]EarningsRow{
		{Timestamp: startTime, ContainerID: "test-container-1234", Rate: 2.50},
	})

	// 2. Insert wallet at start ($1.00) and recent wallet ($1.10) 2 minutes ago
	recentWalletTime := now.Add(-2 * time.Minute).Format("2006-01-02 15:04:05")
	database.InsertWallet([]WalletRow{
		{Timestamp: startTime, Current: 1.00},
		{Timestamp: recentWalletTime, Current: 1.10},
	})

	// Even though earnings report was 1 hour ago, recent wallet means workload is ACTIVE!
	uptime, earnings = database.GetSessionStats()
	if uptime != "1h 0m" {
		t.Errorf("Expected uptime 1h 0m, got %s", uptime)
	}
	if earnings < 0.09 || earnings > 0.11 {
		t.Errorf("Expected session earnings ~0.10, got %f", earnings)
	}

	// 3. Stop workload: insert rate 0
	stopTime := now.Format("2006-01-02 15:04:05")
	database.InsertEarnings([]EarningsRow{
		{Timestamp: stopTime, ContainerID: "", Rate: 0.0},
	})

	uptime, earnings = database.GetSessionStats()
	if uptime != "Idle" || earnings != 0.0 {
		t.Errorf("Expected Idle / 0.0 after stop, got %s / %f", uptime, earnings)
	}
}
