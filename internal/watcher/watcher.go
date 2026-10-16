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
func Run(logDir string, database *db.DB, hub *ws.Hub) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("[watcher] Failed to create fsnotify watcher: %v", err)
	}
	defer watcher.Close()

	// Do an initial full parse of existing files on startup
	log.Println("[watcher] Running initial log parse...")
	scanAndParse(logDir, database, hub, true)
	log.Println("[watcher] Initial parse complete. Watching for changes...")

	// Watch the root log dir and all existing subdirectories
	watchDirRecursive(watcher, logDir)

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			switch {
			case event.Has(fsnotify.Write):
				// A file was written to — parse only the new bytes
				handleFileChange(event.Name, database, hub)

			case event.Has(fsnotify.Create):
				// A new file or directory was created
				info, err := os.Stat(event.Name)
				if err == nil && info.IsDir() {
					// New subdirectory — watch it too
					watchDirRecursive(watcher, event.Name)
				} else {
					// New file — parse it (offset starts at 0)
					parser.ResetOffset(event.Name)
					handleFileChange(event.Name, database, hub)
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
func handleFileChange(path string, database *db.DB, hub *ws.Hub) {
	if !isLogFile(path) {
		return
	}

	isBandwidth := strings.Contains(filepath.Dir(path), "Bandwidth-SGS-")
	data, hasData := parser.ParseFile(path, isBandwidth)
	if !hasData {
		return
	}

	// Write to DB
	database.InsertWallet(data.Wallet)
	database.InsertEarnings(data.Earnings)
	database.InsertBandwidth(data.Bandwidth)
	database.InsertErrors(data.Errors)

	// Push WebSocket event to all browsers — instant UI refresh
	hub.Broadcast("data_updated", map[string]any{"message": "New data available"})
	log.Printf("[watcher] Processed %s: W=%d E=%d BW=%d ERR=%d",
		filepath.Base(path),
		len(data.Wallet), len(data.Earnings), len(data.Bandwidth), len(data.Errors))
}

// scanAndParse does the initial startup parse of all existing log files.
func scanAndParse(logDir string, database *db.DB, hub *ws.Hub, silent bool) {
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
			database.InsertWallet(data.Wallet)
			database.InsertEarnings(data.Earnings)
			database.InsertBandwidth(data.Bandwidth)
			database.InsertErrors(data.Errors)
			if !silent {
				hub.Broadcast("data_updated", map[string]any{"message": "Startup parse complete"})
			}
		}
		return nil
	})
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
