package commit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func subjects(t *testing.T, rows []HistoryRow) []string {
	t.Helper()

	out := []string{}
	for _, r := range rows {
		var p struct {
			Subject string `json:"subject"`
		}
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			t.Fatalf("payload %s: %v", r.Payload, err)
		}
		out = append(out, p.Subject)
	}

	return out
}

func TestHistory(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	t.Run("a missing file is an empty history", func(t *testing.T) {
		t.Parallel()

		rows, err := NewHistory(t.TempDir()).ListCommandHistory(ctx, "commit", 10)
		if err != nil || len(rows) != 0 {
			t.Errorf("ListCommandHistory = %v, %v", rows, err)
		}
	})

	t.Run("entries come back newest first, with their time", func(t *testing.T) {
		t.Parallel()

		h := NewHistory(t.TempDir())
		for _, s := range []string{"first", "second", "third"} {
			if err := h.InsertCommandHistory(ctx, "commit", map[string]any{"subject": s}); err != nil {
				t.Fatalf("InsertCommandHistory: %v", err)
			}
		}

		rows, err := h.ListCommandHistory(ctx, "commit", 10)
		if err != nil {
			t.Fatalf("ListCommandHistory: %v", err)
		}
		if got := strings.Join(subjects(t, rows), ","); got != "third,second,first" {
			t.Errorf("subjects = %s", got)
		}
		if age := time.Since(rows[0].CreatedAt); age < 0 || age > time.Minute {
			t.Errorf("CreatedAt = %v", rows[0].CreatedAt)
		}
	})

	t.Run("limit keeps the newest entries", func(t *testing.T) {
		t.Parallel()

		h := NewHistory(t.TempDir())
		for i := range 5 {
			if err := h.InsertCommandHistory(ctx, "commit", map[string]any{"subject": fmt.Sprint(i)}); err != nil {
				t.Fatalf("InsertCommandHistory: %v", err)
			}
		}

		rows, _ := h.ListCommandHistory(ctx, "commit", 2)
		if got := strings.Join(subjects(t, rows), ","); got != "4,3" {
			t.Errorf("subjects = %s", got)
		}
	})

	t.Run("entries of another command are left out", func(t *testing.T) {
		t.Parallel()

		h := NewHistory(t.TempDir())
		_ = h.InsertCommandHistory(ctx, "commit", map[string]any{"subject": "mine"})
		_ = h.InsertCommandHistory(ctx, "branch", map[string]any{"subject": "other"})

		rows, _ := h.ListCommandHistory(ctx, "commit", 10)
		if got := strings.Join(subjects(t, rows), ","); got != "mine" {
			t.Errorf("subjects = %s", got)
		}
	})

	t.Run("the file keeps its newest 100 entries", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		h := NewHistory(dir)
		for i := range historyCap + 5 {
			if err := h.InsertCommandHistory(ctx, "commit", map[string]any{"subject": fmt.Sprint(i)}); err != nil {
				t.Fatalf("InsertCommandHistory: %v", err)
			}
		}

		data, err := os.ReadFile(filepath.Join(dir, historyFile))
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		if n := strings.Count(string(data), "\n"); n != historyCap {
			t.Errorf("file has %d lines, want %d", n, historyCap)
		}

		rows, _ := h.ListCommandHistory(ctx, "commit", 1)
		if got := strings.Join(subjects(t, rows), ","); got != fmt.Sprint(historyCap+4) {
			t.Errorf("newest = %s", got)
		}
	})

	t.Run("a line that does not parse is skipped and dropped at the next save", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		h := NewHistory(dir)
		_ = h.InsertCommandHistory(ctx, "commit", map[string]any{"subject": "before"})

		path := filepath.Join(dir, historyFile)
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		_, _ = f.WriteString("not json\n")
		_ = f.Close()

		if err := h.InsertCommandHistory(ctx, "commit", map[string]any{"subject": "after"}); err != nil {
			t.Fatalf("InsertCommandHistory: %v", err)
		}

		rows, err := h.ListCommandHistory(ctx, "commit", 10)
		if err != nil {
			t.Fatalf("ListCommandHistory: %v", err)
		}
		if got := strings.Join(subjects(t, rows), ","); got != "after,before" {
			t.Errorf("subjects = %s", got)
		}
		if data, _ := os.ReadFile(path); strings.Contains(string(data), "not json") {
			t.Errorf("corrupt line survived:\n%s", data)
		}
	})

	t.Run("a payload that cannot be marshalled is an error", func(t *testing.T) {
		t.Parallel()

		if err := NewHistory(t.TempDir()).InsertCommandHistory(ctx, "commit", func() {}); err == nil {
			t.Error("InsertCommandHistory accepted a func")
		}
	})
}
