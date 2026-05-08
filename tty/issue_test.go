package tty

import (
	"bytes"
	"strings"
	"testing"

	"github.com/piprim/git-zf/store"
)

func TestRenderIssueTable_hidesProjectColumnWhenSingle(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	rows := []store.IssueRow{
		{IssueSlug: "1", Title: "a", Project: "octo/cat"},
		{IssueSlug: "2", Title: "b", Project: "octo/cat"},
	}

	RenderIssueTable(&buf, rows)
	out := buf.String()

	if strings.Contains(out, "PROJECT") {
		t.Errorf("expected no PROJECT header for single-project rows, got:\n%s", out)
	}
}

func TestRenderIssueTable_showsProjectColumnWhenMultiple(t *testing.T) {
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
}
