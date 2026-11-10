package tty

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/piprim/git-zf/branch"
)

func TestRenderBranchRows(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	rows := []branch.Row{
		{BranchName: "master", Plain: true, Current: true, LastCommit: now},
		{BranchName: "42@feat@assign", IssueSlug: "42", Type: "feat", Status: branch.StatusInProgress, Title: "Assign", CreatedAt: now, LastCommit: now},
		{BranchName: "spike", Plain: true, LastCommit: now},
		{BranchName: "remote-thing", Plain: true, RemoteOnly: true, LastCommit: now},
	}
	var buf bytes.Buffer
	RenderBranchRows(&buf, rows, "origin")
	out := buf.String()

	t.Run("prints the seven headers", func(t *testing.T) {
		for _, h := range []string{"BRANCH", "ISSUE", "TYPE", "STATUS", "TITLE", "CREATED", "LAST COMMIT"} {
			if !strings.Contains(out, h) {
				t.Errorf("output lacks %q:\n%s", h, out)
			}
		}
	})
	t.Run("stars the current branch and prefixes a remote-only one", func(t *testing.T) {
		if !strings.Contains(out, "* master") || !strings.Contains(out, "  spike") || !strings.Contains(out, "origin/remote-thing") {
			t.Errorf("output:\n%s", out)
		}
	})
	t.Run("a zero Created is blank", func(t *testing.T) {
		if strings.Contains(out, "0001-01-01") || !strings.Contains(out, "2026-11-10") {
			t.Errorf("output:\n%s", out)
		}
	})
}
