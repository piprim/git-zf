//go:build !linux

package tui

// DiscardInput does nothing here.
// ponytail: Linux alone (TCFLSH); TIOCFLUSH on the BSDs and macOS when asked.
func DiscardInput() {}
