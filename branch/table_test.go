package branch

import (
	"bytes"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
)

func TestTableRows(t *testing.T) {
	c := newRepo(t, "alice", newOrigin(t))
	dir := c.WorkingTreeRoot()
	ctx := t.Context()

	for _, name := range []string{"42@feat@assign", "51@fix@prune", "spike", "remote-thing"} {
		runGit(t, dir, "branch", name)
	}
	mustStart(t, c, "42", Op{Branch: "42@feat@assign", BranchType: "feat", Title: "Assign"})
	mustStart(t, c, "51", Op{Branch: "51@fix@prune", BranchType: "fix"})
	if err := SetStatus(ctx, c, "51", "51@fix@prune", StatusMerged); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	mustStart(t, c, "77", Op{Branch: "77@feat@gone", BranchType: "feat"})
	runGit(t, dir, "push", "-q", "origin", "remote-thing", "spike")
	runGit(t, dir, "branch", "-D", "remote-thing")

	rows, err := TableRows(ctx, c, "origin")
	row := func(name string) Row {
		i := slices.IndexFunc(rows, func(r Row) bool { return r.BranchName == name })
		if i < 0 {
			t.Fatalf("no row %q in %+v", name, rows)
		}
		return rows[i]
	}

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("TableRows: %v", err)
		}
	})
	t.Run("one row per local branch in name order, then remote-only", func(t *testing.T) {
		var names []string
		for _, r := range rows {
			names = append(names, r.BranchName)
		}
		want := []string{"42@feat@assign", "51@fix@prune", "main", "spike", "remote-thing"}
		if !slices.Equal(names, want) {
			t.Errorf("names = %v, want %v", names, want)
		}
	})
	t.Run("a tracked row carries its record", func(t *testing.T) {
		r := row("42@feat@assign")
		if r.IssueSlug != "42" || r.Status != StatusInProgress || r.Title != "Assign" || r.Plain {
			t.Errorf("row = %+v", r)
		}
	})
	t.Run("a plain row is Plain with empty record fields", func(t *testing.T) {
		r := row("spike")
		if !r.Plain || r.IssueSlug != "" || r.Status != "" || r.Type != "" || !r.CreatedAt.IsZero() {
			t.Errorf("row = %+v", r)
		}
	})
	t.Run("the checked out branch is Current", func(t *testing.T) {
		if !row("main").Current || row("spike").Current {
			t.Errorf("main.Current=%v spike.Current=%v", row("main").Current, row("spike").Current)
		}
	})
	t.Run("a record whose branch is gone everywhere has no row", func(t *testing.T) {
		if slices.ContainsFunc(rows, func(r Row) bool { return r.BranchName == "77@feat@gone" }) {
			t.Errorf("77@feat@gone has a row: %+v", rows)
		}
	})
	t.Run("a branch both local and remote is one local row", func(t *testing.T) {
		n := 0
		for _, r := range rows {
			if r.BranchName == "spike" {
				n++
			}
		}
		if n != 1 || row("spike").RemoteOnly {
			t.Errorf("spike rows = %d, RemoteOnly = %v", n, row("spike").RemoteOnly)
		}
	})
	t.Run("a remote-only branch is RemoteOnly with its short name", func(t *testing.T) {
		if r := row("remote-thing"); !r.RemoteOnly || !r.Plain {
			t.Errorf("row = %+v", r)
		}
	})
	t.Run("every row has a LastCommit", func(t *testing.T) {
		for _, r := range rows {
			if r.LastCommit.IsZero() {
				t.Errorf("%s has no LastCommit", r.BranchName)
			}
		}
	})
	t.Run("an empty remote skips the remote list", func(t *testing.T) {
		rows2, err := TableRows(ctx, c, "")
		if err != nil {
			t.Fatalf("TableRows: %v", err)
		}
		if slices.ContainsFunc(rows2, func(r Row) bool { return r.RemoteOnly }) {
			t.Errorf("remote-only row with no remote: %+v", rows2)
		}
	})
	t.Run("a detached HEAD marks no row current", func(t *testing.T) {
		runGit(t, dir, "checkout", "-q", "--detach")
		rows3, err := TableRows(ctx, c, "origin")
		if err != nil {
			t.Fatalf("TableRows: %v", err)
		}
		if slices.ContainsFunc(rows3, func(r Row) bool { return r.Current }) {
			t.Errorf("a row is current on a detached HEAD: %+v", rows3)
		}
	})
}

func TestTableRows_PrintsTheListWarnings(t *testing.T) {
	c := newRepo(t, "alice", "")
	writeBlobRef(t, c, "old1", legacy42)
	var errW bytes.Buffer
	c, err := git.NewClientAt(&pkg.IO{In: strings.NewReader(""), Out: io.Discard, Err: &errW}, c.WorkingTreeRoot())
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	if _, err := TableRows(t.Context(), c, ""); err != nil {
		t.Fatalf("TableRows: %v", err)
	}

	t.Run("the legacy blob warning reaches stderr", func(t *testing.T) {
		if !strings.Contains(errW.String(), "old1") {
			t.Errorf("stderr = %q", errW.String())
		}
	})
}
