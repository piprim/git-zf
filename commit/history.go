package commit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/piprim/git-zf/git"
)

const (
	historyFile = "git-zf-history.jsonl"
	// historyCap is how many entries the file keeps, all commands together.
	historyCap = 100
)

// HistoryRow is one saved form submission.
type HistoryRow struct {
	Payload   json.RawMessage // raw JSON; caller unmarshals into the shape they need
	CreatedAt time.Time
}

// historyEntry is one line of the history file.
type historyEntry struct {
	Command string          `json:"command"`
	At      string          `json:"at"` // RFC 3339, UTC
	Payload json.RawMessage `json:"payload"`
}

// History keeps the submissions of the commit form in a JSON Lines file, one
// entry per line, oldest first. It is the historyStore of FillOutForm.
//
// ponytail: no lock. Two commits finishing at the same instant in two
// worktrees may lose one entry; add a lock file if that is ever seen.
type History struct {
	path string
}

// OpenHistory returns the history of the repository c works on. The file
// lives in the common git directory, so every worktree shares it.
func OpenHistory(c *git.Client) (*History, error) {
	dir, err := c.CommonDir()
	if err != nil {
		return nil, fmt.Errorf("resolve common git dir: %w", err)
	}

	return NewHistory(dir), nil
}

// NewHistory returns the history stored in dir.
func NewHistory(dir string) *History {
	return &History{path: filepath.Join(dir, historyFile)}
}

// read returns the entries of the file, oldest first. A missing file is an
// empty history; a line that does not parse is skipped.
func (h *History) read() ([]historyEntry, error) {
	data, err := os.ReadFile(h.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}

	var entries []historyEntry
	for line := range bytes.Lines(data) {
		var e historyEntry
		if err := json.Unmarshal(line, &e); err != nil || e.Command == "" {
			continue
		}
		entries = append(entries, e)
	}

	return entries, nil
}

// InsertCommandHistory records one completed form submission. payload must be
// JSON-serialisable. The file keeps its newest historyCap entries.
func (h *History) InsertCommandHistory(_ context.Context, command string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	entries, err := h.read()
	if err != nil {
		return err
	}

	entries = append(entries, historyEntry{
		Command: command, At: time.Now().UTC().Format(time.RFC3339), Payload: data,
	})
	entries = entries[max(0, len(entries)-historyCap):]

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf) // one entry per line
	for i := range entries {
		if err := enc.Encode(&entries[i]); err != nil {
			return fmt.Errorf("encode history: %w", err)
		}
	}

	// Written beside the file, then renamed: a reader never sees half a file.
	tmp := h.path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write history: %w", err)
	}
	if err := os.Rename(tmp, h.path); err != nil {
		return fmt.Errorf("replace history: %w", err)
	}

	return nil
}

// ListCommandHistory returns the most recent limit entries for command,
// newest first.
func (h *History) ListCommandHistory(_ context.Context, command string, limit int) ([]HistoryRow, error) {
	entries, err := h.read()
	if err != nil {
		return nil, err
	}

	rows := []HistoryRow{}
	for _, e := range slices.Backward(entries) {
		if e.Command != command {
			continue
		}
		if len(rows) == limit {
			break
		}

		at, _ := time.Parse(time.RFC3339, e.At)
		rows = append(rows, HistoryRow{Payload: e.Payload, CreatedAt: at})
	}

	return rows, nil
}
