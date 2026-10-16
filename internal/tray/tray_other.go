//go:build !windows

package tray

// Start is a no-op on non-Windows platforms.
func Start(port int, machineName string, hideConsole bool) {}

// Remove is a no-op on non-Windows platforms.
func Remove() {}
