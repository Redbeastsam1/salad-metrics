package parser

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseBandwidth(t *testing.T) {
	sampleLog := `2026-10-22 04:00:00.000 +05:30 [INF] Running with args: -machine_id test
2026-10-22 04:00:05.123 +05:30 [INF] {"level":"info","ts":"Oct 22 04:00:05","caller":"client/client-impl.go:766","msg":"Connected to server"}
2026-10-22 04:00:30.456 +05:30 [INF] {"level":"info","ts":"Oct 22 04:00:30","caller":"client/client-impl.go:500","msg":"periodic stats","BidirThroughput":3600217.6}
2026-10-22 04:01:00.789 +05:30 [DBG] {"level":"debug","ts":"Oct 22 04:01:00","msg":"stats","BidirThroughput": 7200435.2}
2026-10-22 04:01:30.100 +05:30 [INF] {"BidirThroughput":1800108.8,"status":"ok"}
`
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "Bandwidth-SGS-IN-2-test.log")
	if err := os.WriteFile(logPath, []byte(sampleLog), 0644); err != nil {
		t.Fatalf("Failed to write sample log: %v", err)
	}

	data, hasData := ParseFile(logPath, true)
	if !hasData {
		t.Fatalf("Expected ParseFile to find data, but got false")
	}

	if len(data.Bandwidth) != 3 {
		t.Fatalf("Expected 3 bandwidth records, got %d", len(data.Bandwidth))
	}

	if data.Bandwidth[0].Timestamp != "2026-10-22 04:00:30.456 +05:30" {
		t.Errorf("Unexpected timestamp: %s", data.Bandwidth[0].Timestamp)
	}

	expectedThroughput := 3600217.6 / (280000.0 * 8.0)
	if data.Bandwidth[0].Throughput < expectedThroughput-0.001 || data.Bandwidth[0].Throughput > expectedThroughput+0.001 {
		t.Errorf("Expected throughput ~%f, got %f", expectedThroughput, data.Bandwidth[0].Throughput)
	}
}
