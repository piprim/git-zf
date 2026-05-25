package issue

import (
	"testing"

	"github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
)

func samplePickedIssue() *issue.Issue {
	return &issue.Issue{
		Type: "feat",
		Issue: tracker.Issue{
			ID:      "ABC-42",
			Subject: "Add OAuth Login",
		},
	}
}

func TestRebuildVariantBranch_valid(t *testing.T) {
	t.Parallel()

	b, err := rebuildVariantBranch(samplePickedIssue(), "spike")
	if err != nil {
		t.Fatalf("rebuildVariantBranch: %v", err)
	}

	if got, want := b.Name(), "ABC-42@feat@add-oauth-login@spike"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

func TestRebuildVariantBranch_invalidLabel(t *testing.T) {
	t.Parallel()

	if _, err := rebuildVariantBranch(samplePickedIssue(), "!!!"); err == nil {
		t.Error("expected error for label slugging to empty, got nil")
	}
}

func TestRebuildVariantBranch_emptyLabel(t *testing.T) {
	t.Parallel()

	if _, err := rebuildVariantBranch(samplePickedIssue(), ""); err == nil {
		t.Error("expected error for empty label, got nil")
	}
}
