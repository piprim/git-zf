package branch

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	"github.com/piprim/git-zf/config"
)

func TestBranchList(t *testing.T) {
	t.Parallel()

	t.Run("json output includes branch and issue fields", func(t *testing.T) {
		t.Parallel()

		s := newPruneRig(t).client
		branchtest.Seed(t, s, branch.Op{Branch: "ABC-42@feat@add-oauth-login@550e8400", Title: "Add OAuth login"},
			branch.StatusInProgress)

		var buf bytes.Buffer
		if err := runList(t.Context(), &buf, s, listFlags{jsonOut: true}); err != nil {
			t.Fatalf("runList: %v", err)
		}

		var rows []branch.Row
		if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("got %d rows, want 1", len(rows))
		}
		if rows[0].IssueSlug != "ABC-42" {
			t.Errorf("IssueSlug = %q, want ABC-42", rows[0].IssueSlug)
		}
		if rows[0].BranchName != "ABC-42@feat@add-oauth-login@550e8400" {
			t.Errorf("BranchName = %q", rows[0].BranchName)
		}
		if rows[0].Status != "in_progress" {
			t.Errorf("Status = %q, want in_progress", rows[0].Status)
		}
	})

	t.Run("json output is an empty array for a repository with no tracked branch", func(t *testing.T) {
		t.Parallel()

		s := newPruneRig(t).client
		var buf bytes.Buffer
		if err := runList(t.Context(), &buf, s, listFlags{jsonOut: true}); err != nil {
			t.Fatalf("runList: %v", err)
		}

		var rows []branch.Row
		if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("got %d rows, want 0", len(rows))
		}
	})

	t.Run("stdout output includes header and branch slug", func(t *testing.T) {
		t.Parallel()

		s := newPruneRig(t).client
		branchtest.Seed(t, s, branch.Op{Branch: "XY-1@feat@some-feature@aabbccdd", Title: "Some feature"},
			branch.StatusInProgress)

		var buf bytes.Buffer
		if err := runList(t.Context(), &buf, s, listFlags{stdout: true}); err != nil {
			t.Fatalf("runList: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "ISSUE ID") {
			t.Errorf("output missing ISSUE ID header: %q", out)
		}
		if !strings.Contains(out, "XY-1") {
			t.Errorf("output missing issue slug XY-1: %q", out)
		}
	})

	t.Run("stdout output says 'No branches found' for a repository with no tracked branch", func(t *testing.T) {
		t.Parallel()

		s := newPruneRig(t).client
		var buf bytes.Buffer
		if err := runList(t.Context(), &buf, s, listFlags{stdout: true}); err != nil {
			t.Fatalf("runList: %v", err)
		}
		if !strings.Contains(buf.String(), "No branches found.") {
			t.Errorf("expected 'No branches found.', got: %q", buf.String())
		}
	})
}

// TestNewRunE_InteractiveDispatch is a regression test for the bug where
// `git zf branch` → "New" dispatched through the branch root command (which
// defines no --variant flag) and the handler tried to read that flag, failing
// with "read --variant flag: flag accessed but not defined: variant". The
// branch menu (cmdutil.RunMenu) runs the subcommand's RunE with the root
// command, so that RunE must tolerate the missing flag.
func TestNewRunE_InteractiveDispatch(t *testing.T) {
	b := New(&config.AppConfig{})
	root := b.GetRootCmd()

	t.Run("branch root command defines no --variant flag", func(t *testing.T) {
		if root.Flags().Lookup("variant") != nil {
			t.Fatal("branch root command unexpectedly defines a --variant flag")
		}
	})

	t.Run("new subcommand still defines --variant", func(t *testing.T) {
		newSub, _, err := root.Find([]string{"new"})
		if err != nil {
			t.Fatalf("find new subcommand: %v", err)
		}
		if newSub.Flags().Lookup("variant") == nil {
			t.Fatal("new subcommand lost its --variant flag")
		}
	})

	t.Run("new's RunE on the root command tolerates the undefined --variant flag", func(t *testing.T) {
		t.Chdir(t.TempDir()) // a directory outside any git repo

		newSub, _, err := root.Find([]string{"new"})
		if err != nil {
			t.Fatalf("find new subcommand: %v", err)
		}

		err = newSub.RunE(root, nil) // what the branch menu does
		if err == nil {
			t.Fatal("expected an error outside a git repo, got nil")
		}
		if strings.Contains(err.Error(), "flag accessed but not defined") {
			t.Fatalf("regression: dispatch path still reads the undefined --variant flag: %v", err)
		}
		// The flow fails later, at repo detection — proving flag reading was
		// passed without error.
		if !strings.Contains(err.Error(), "git repository") {
			t.Fatalf("expected a git-repository error, got: %v", err)
		}
	})
}

func TestBranchList_ShowsABranchStartedInAnotherClone(t *testing.T) {
	t.Parallel()

	origin := newPruneOrigin(t)
	alice, bob := newPruneRigWithOrigin(t, origin), newPruneRigWithOrigin(t, origin)

	alice.seedIssueAndBranch(t, "TM-1", "TM-1@feat@theirs", "feat")
	if err := branch.Push(t.Context(), alice.client, "TM-1"); err != nil {
		t.Fatalf("alice push branch ref: %v", err)
	}

	// branch list does not contact the remote: it shows what a plain
	// `git fetch` brought, once `git zf init` has configured the refspecs.
	if _, err := bob.client.ConfigureChainFetch(t.Context()); err != nil {
		t.Fatalf("ConfigureChainFetch: %v", err)
	}
	bob.runPruneGit(t, "fetch", "-q", "origin")

	var buf bytes.Buffer
	if err := runList(t.Context(), &buf, bob.client, listFlags{jsonOut: true}); err != nil {
		t.Fatalf("runList: %v", err)
	}

	var rows []branch.Row
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	t.Run("the other clone's branch is listed as in progress", func(t *testing.T) {
		if len(rows) != 1 || rows[0].BranchName != "TM-1@feat@theirs" || rows[0].Status != branch.StatusInProgress {
			t.Errorf("rows = %+v", rows)
		}
	})

	t.Run("the JSON row has no issue_id field", func(t *testing.T) {
		if strings.Contains(buf.String(), "issue_id") {
			t.Errorf("json = %s", buf.String())
		}
	})
}
