package watcher

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"salad-metrics/internal/db"
	"salad-metrics/internal/parser"
	"salad-metrics/internal/ws"
)

// Run starts the fsnotify-based file watcher. It blocks forever.
// When a log file is written to, it parses only the new bytes and
// immediately pushes a WebSocket event to all connected browsers.
func Run(logDir string, database *db.DB, hub *ws.Hub, machineName ...string) {
	mach := "Beast"
	if len(machineName) > 0 && machineName[0] != "" {
		mach = machineName[0]
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("[watcher] Failed to create fsnotify watcher: %v", err)
	}
	defer watcher.Close()

	// Do an initial full parse of existing files on startup
	log.Println("[watcher] Running initial log parse...")
	scanAndParse(logDir, database, hub, mach, true)
	log.Println("[watcher] Initial parse complete. Watching for changes...")

	// Watch the root log dir and all existing subdirectories
	watchDirRecursive(watcher, logDir)

	// Periodic poll fallback for Windows file locks/buffered appends
	pollTicker := time.NewTicker(30 * time.Second)
	defer pollTicker.Stop()

	for {
		select {
		case <-pollTicker.C:
			// Periodic poll scan — fast since Stat() skips unchanged files
			scanAndParse(logDir, database, hub, mach, false)

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			switch {
			case event.Has(fsnotify.Write):
				// A file was written to — parse only the new bytes
				handleFileChange(event.Name, database, hub, mach)

			case event.Has(fsnotify.Create):
				// A new file or directory was created
				info, err := os.Stat(event.Name)
				if err == nil && info.IsDir() {
					// New subdirectory — watch it too
					watchDirRecursive(watcher, event.Name)
				} else {
					// New file — parse it (offset starts at 0)
					parser.ResetOffset(event.Name)
					handleFileChange(event.Name, database, hub, mach)
				}
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("[watcher] Error: %v", err)
		}
	}
}

// handleFileChange processes a single file change event.
func handleFileChange(path string, database *db.DB, hub *ws.Hub, machine string) {
	if !isLogFile(path) {
		return
	}

	isBandwidth := strings.Contains(filepath.Dir(path), "Bandwidth-SGS-")
	data, hasData := parser.ParseFile(path, isBandwidth)
	if !hasData {
		return
	}

	// Write to DB with machine affiliation
	database.InsertWalletWithMachine(machine, data.Wallet)
	database.InsertEarningsWithMachine(machine, data.Earnings)
	database.InsertBandwidthWithMachine(machine, data.Bandwidth)
	database.InsertErrorsWithMachine(machine, data.Errors)

	// Push WebSocket event to all browsers — instant UI refresh
	hub.Broadcast("data_updated", map[string]any{"message": "New data available", "machine": machine})
	log.Printf("[watcher] Processed %s (%s): W=%d E=%d BW=%d ERR=%d",
		filepath.Base(path), machine,
		len(data.Wallet), len(data.Earnings), len(data.Bandwidth), len(data.Errors))
}

// scanAndParse does the initial startup parse of all existing log files,
// and acts as the periodic fallback sweep.
func scanAndParse(logDir string, database *db.DB, hub *ws.Hub, machine string, silent bool) {
	anyNewData := false
	_ = filepath.Walk(logDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !isLogFile(path) {
			return nil
		}
		isBandwidth := strings.Contains(filepath.Dir(path), "Bandwidth-SGS-")
		data, hasData := parser.ParseFile(path, isBandwidth)
		if hasData {
			database.InsertWalletWithMachine(machine, data.Wallet)
			database.InsertEarningsWithMachine(machine, data.Earnings)
			database.InsertBandwidthWithMachine(machine, data.Bandwidth)
			database.InsertErrorsWithMachine(machine, data.Errors)
			anyNewData = true
			log.Printf("[watcher] Processed %s (%s): W=%d E=%d BW=%d ERR=%d",
				filepath.Base(path), machine,
				len(data.Wallet), len(data.Earnings), len(data.Bandwidth), len(data.Errors))
		}
		return nil
	})

	if anyNewData && !silent {
		hub.Broadcast("data_updated", map[string]any{"message": "New data available", "machine": machine})
	}
}

// watchDirRecursive adds a directory and all its subdirectories to the watcher.
func watchDirRecursive(w *fsnotify.Watcher, root string) {
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() {
			return nil
		}
		if err := w.Add(path); err != nil {
			log.Printf("[watcher] Cannot watch %s: %v", path, err)
		}
		return nil
	})
}

// isLogFile returns true if the path looks like a Salad log file.
func isLogFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".txt" && ext != ".log" {
		return false
	}
	base := filepath.Base(path)
	return strings.HasPrefix(base, "log-") || strings.Contains(filepath.Dir(path), "Bandwidth-SGS-")
}

// Debounce helper (unused but available for noisy file systems)
var _ = time.After
