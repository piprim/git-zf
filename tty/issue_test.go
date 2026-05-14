package tty

import (
	"bytes"
	"strings"
	"testing"

	"github.com/piprim/git-zf/store"
)

func TestRenderIssueTable(t *testing.T) {
	t.Parallel()

	t.Run("hides PROJECT column when all rows share one project", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer
		rows := []store.IssueRow{
			{IssueSlug: "1", Title: "a", Project: "octo/cat"},
			{IssueSlug: "2", Title: "b", Project: "octo/cat"},
		}

		RenderIssueTable(&buf, rows)

		if strings.Contains(buf.String(), "PROJECT") {
			t.Errorf("expected no PROJECT header for single-project rows, got:\n%s", buf.String())
		}
	})

	t.Run("shows PROJECT column and values when rows span multiple projects", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer
		rows := []store.IssueRow{
			{IssueSlug: "1", Title: "a", Project: "octo/cat"},
			{IssueSlug: "2", Title: "b", Project: "octo/dog"},
		}

		RenderIssueTable(&buf, rows)
		out := buf.String()

		if !strings.Contains(out, "PROJECT") {
			t.Errorf("expected PROJECT header for multi-project rows, got:\n%s", out)
		}
		if !strings.Contains(out, "octo/cat") || !strings.Contains(out, "octo/dog") {
			t.Errorf("expected project values in output, got:\n%s", out)
		}
	})
}
