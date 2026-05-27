package branch

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/store"
)

// pruneTestRig bundles a real on-disk git repo + seeded store so prune E2E
// tests share setup. The repo starts with one commit on master; tests seed
// additional branches (deleted / merged / active) via the rig's helpers.
type pruneTestRig struct {
	dir    string
	client *git.Client
	store  *store.Store
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func newPruneRig(t *testing.T) *pruneTestRig {
	t.Helper()

	dir := t.TempDir()

	runGit := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runGit("init", "-q", "-b", "master")
	runGit("config", "user.name", "Test User")
	runGit("config", "user.email", "test@test.com")
	runGit("config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}

	runGit("add", "base.txt")
	runGit("commit", "-m", "chore: init")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	ioStreams := &pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stderr}

	client, err := git.NewClientAt(ioStreams, dir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}

	s, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	t.Cleanup(func() { _ = s.Close() })

	return &pruneTestRig{
		dir: dir, client: client, store: s,
		stdout: stdout, stderr: stderr,
	}
}

// seedIssueAndBranch inserts an Issue + Branch row into the store with the
// in-progress status.
func (r *pruneTestRig) seedIssueAndBranch(t *testing.T, issueSlug, branchName, branchType string) {
	t.Helper()

	if err := r.store.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: issueSlug, Title: issueSlug, StatusID: store.StatusIDInProgress},
		&store.Branch{Name: branchName, Type: branchType, StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed %q: %v", branchName, err)
	}
}

// createGitBranch creates a real local git branch that is NOT merged into
// master: it adds one extra commit on the new branch, then returns master
// to its previous HEAD. The branch's tip is therefore unreachable from
// master, so IsMergedInto(branch, master) → false.
func (r *pruneTestRig) createGitBranch(t *testing.T, branchName string) {
	t.Helper()

	runGit := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = r.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runGit("checkout", "-q", "-b", branchName)

	fname := branchName + ".txt"
	if err := os.WriteFile(filepath.Join(r.dir, fname), []byte("active\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", fname, err)
	}

	runGit("add", fname)
	runGit("commit", "-m", "feat: "+branchName)
	runGit("checkout", "-q", "master")
}

// mergeBranchIntoMaster creates a branch with one extra commit, then
// fast-forward-merges it into master.
func (r *pruneTestRig) mergeBranchIntoMaster(t *testing.T, branchName, fileContent string) {
	t.Helper()

	runGit := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = r.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runGit("checkout", "-q", "-b", branchName)

	fname := branchName + ".txt"
	if err := os.WriteFile(filepath.Join(r.dir, fname), []byte(fileContent), 0o644); err != nil {
		t.Fatalf("write %s: %v", fname, err)
	}

	runGit("add", fname)
	runGit("commit", "-m", "feat: "+branchName)
	runGit("checkout", "-q", "master")
	runGit("merge", "--ff", branchName)
}

func TestRunPrune_HappyPath_DeleteAndMerge(t *testing.T) {
	t.Parallel()

	rig := newPruneRig(t)

	// Seed three branches:
	//   DEL-1 — store row only, no git branch → "to delete"
	//   MRG-1 — store row + git branch + merged into master → "to merge"
	//   ACT-1 — store row + git branch + NOT merged → no action
	rig.seedIssueAndBranch(t, "DEL-1", "DEL-1@feat@gone", "feat")
	rig.seedIssueAndBranch(t, "MRG-1", "MRG-1@fix@done", "fix")
	rig.seedIssueAndBranch(t, "ACT-1", "ACT-1@feat@active", "feat")
	rig.mergeBranchIntoMaster(t, "MRG-1@fix@done", "merged\n")
	rig.createGitBranch(t, "ACT-1@feat@active")

	prompter := &scriptedPrunePrompter{Confirm: true}

	if err := runPrune(t.Context(), rig.stdout, rig.store, rig.client, prompter, pruneFlags{}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	t.Run("DEL-1 row removed from store", func(t *testing.T) {
		rows, err := rig.store.ListBranches(t.Context(), store.BranchStatusAll)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}

		for _, r := range rows {
			if r.BranchName == "DEL-1@feat@gone" {
				t.Errorf("DEL-1@feat@gone still present after prune")
			}
		}
	})

	t.Run("MRG-1 status flipped to merged", func(t *testing.T) {
		merged, err := rig.store.ListBranches(t.Context(), store.BranchStatusMerged)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}

		found := false
		for _, r := range merged {
			if r.BranchName == "MRG-1@fix@done" {
				found = true
				break
			}
		}

		if !found {
			t.Errorf("MRG-1@fix@done not flagged as merged")
		}
	})

	t.Run("ACT-1 left in-progress", func(t *testing.T) {
		inProgress, err := rig.store.ListBranches(t.Context(), store.BranchStatusInProgress)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}

		found := false
		for _, r := range inProgress {
			if r.BranchName == "ACT-1@feat@active" {
				found = true
				break
			}
		}

		if !found {
			t.Errorf("ACT-1@feat@active should still be in-progress")
		}
	})

	t.Run("prompter was called once with the right counts", func(t *testing.T) {
		if prompter.ConfirmCalls != 1 {
			t.Errorf("ConfirmCalls = %d, want 1", prompter.ConfirmCalls)
		}

		if prompter.LastToDelete != 1 {
			t.Errorf("LastToDelete = %d, want 1", prompter.LastToDelete)
		}

		if prompter.LastToMerge != 1 {
			t.Errorf("LastToMerge = %d, want 1", prompter.LastToMerge)
		}
	})

	t.Run("stdout shows the summary", func(t *testing.T) {
		got := rig.stdout.String()
		if !bytes.Contains([]byte(got), []byte("Pruned: 1 deleted, 1 marked merged")) {
			t.Errorf("stdout = %q, want it to contain 'Pruned: 1 deleted, 1 marked merged'", got)
		}
	})
}

func TestRunPrune_DryRun_ReportsDeletedBranch(t *testing.T) {
	t.Parallel()

	rig := newPruneRig(t)
	rig.seedIssueAndBranch(t, "ABC-1", "ABC-1@feat@gone", "feat")

	prompter := &scriptedPrunePrompter{}

	if err := runPrune(t.Context(), rig.stdout, rig.store, rig.client, prompter, pruneFlags{dryRun: true}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	t.Run("output mentions the deleted branch", func(t *testing.T) {
		if !bytes.Contains(rig.stdout.Bytes(), []byte("ABC-1@feat@gone")) {
			t.Errorf("stdout = %q, want it to mention 'ABC-1@feat@gone'", rig.stdout.String())
		}
	})

	t.Run("store is unchanged after dry-run", func(t *testing.T) {
		rows, err := rig.store.ListBranches(t.Context(), store.BranchStatusAll)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}

		if len(rows) != 1 {
			t.Errorf("store rows = %d, want 1 (unchanged)", len(rows))
		}
	})

	t.Run("prompter was not invoked", func(t *testing.T) {
		if prompter.ConfirmCalls != 0 {
			t.Errorf("ConfirmCalls = %d, want 0 on dry-run", prompter.ConfirmCalls)
		}
	})
}

func TestRunPrune_DryRun_ReportsMergedBranch(t *testing.T) {
	t.Parallel()

	rig := newPruneRig(t)
	rig.seedIssueAndBranch(t, "XY-1", "XY-1@fix@bug", "fix")
	rig.mergeBranchIntoMaster(t, "XY-1@fix@bug", "bugfix\n")

	prompter := &scriptedPrunePrompter{}

	if err := runPrune(t.Context(), rig.stdout, rig.store, rig.client, prompter, pruneFlags{dryRun: true}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	t.Run("output mentions the merged branch", func(t *testing.T) {
		if !bytes.Contains(rig.stdout.Bytes(), []byte("XY-1@fix@bug")) {
			t.Errorf("stdout = %q, want it to mention 'XY-1@fix@bug'", rig.stdout.String())
		}
	})

	t.Run("prompter was not invoked", func(t *testing.T) {
		if prompter.ConfirmCalls != 0 {
			t.Errorf("ConfirmCalls = %d, want 0 on dry-run", prompter.ConfirmCalls)
		}
	})
}

func TestRunPrune_DryRun_NothingToPrune(t *testing.T) {
	t.Parallel()

	rig := newPruneRig(t)
	rig.seedIssueAndBranch(t, "Z-1", "Z-1@feat@active", "feat")
	rig.createGitBranch(t, "Z-1@feat@active")

	prompter := &scriptedPrunePrompter{}

	if err := runPrune(t.Context(), rig.stdout, rig.store, rig.client, prompter, pruneFlags{dryRun: true}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	t.Run("output says 'Nothing to prune.'", func(t *testing.T) {
		if !bytes.Contains(rig.stdout.Bytes(), []byte("Nothing to prune.")) {
			t.Errorf("stdout = %q, want it to contain 'Nothing to prune.'", rig.stdout.String())
		}
	})

	t.Run("prompter was not invoked", func(t *testing.T) {
		if prompter.ConfirmCalls != 0 {
			t.Errorf("ConfirmCalls = %d, want 0 when nothing to prune", prompter.ConfirmCalls)
		}
	})
}

func TestRunPrune_DryRun_MixedCategories(t *testing.T) {
	t.Parallel()

	rig := newPruneRig(t)
	rig.seedIssueAndBranch(t, "DEL-1", "DEL-1@feat@gone", "feat")
	rig.seedIssueAndBranch(t, "MRG-1", "MRG-1@fix@done", "fix")
	rig.seedIssueAndBranch(t, "ACT-1", "ACT-1@feat@active", "feat")
	rig.mergeBranchIntoMaster(t, "MRG-1@fix@done", "merged\n")
	rig.createGitBranch(t, "ACT-1@feat@active")

	prompter := &scriptedPrunePrompter{}

	if err := runPrune(t.Context(), rig.stdout, rig.store, rig.client, prompter, pruneFlags{dryRun: true}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	out := rig.stdout.String()

	t.Run("output mentions the deleted branch", func(t *testing.T) {
		if !bytes.Contains([]byte(out), []byte("DEL-1@feat@gone")) {
			t.Errorf("stdout = %q, want it to mention 'DEL-1@feat@gone'", out)
		}
	})

	t.Run("output mentions the merged branch", func(t *testing.T) {
		if !bytes.Contains([]byte(out), []byte("MRG-1@fix@done")) {
			t.Errorf("stdout = %q, want it to mention 'MRG-1@fix@done'", out)
		}
	})

	t.Run("output does NOT mention the active branch", func(t *testing.T) {
		if bytes.Contains([]byte(out), []byte("ACT-1@feat@active")) {
			t.Errorf("active branch should not appear in dry-run output, got: %q", out)
		}
	})

	t.Run("store is unchanged", func(t *testing.T) {
		rows, err := rig.store.ListBranches(t.Context(), store.BranchStatusAll)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}

		if len(rows) != 3 {
			t.Errorf("store rows = %d, want 3 (unchanged)", len(rows))
		}
	})
}

func TestRunPrune_UserAbortsAtConfirm(t *testing.T) {
	t.Parallel()

	rig := newPruneRig(t)
	rig.seedIssueAndBranch(t, "DEL-1", "DEL-1@feat@gone", "feat")

	prompter := &scriptedPrunePrompter{Confirm: false}

	if err := runPrune(t.Context(), rig.stdout, rig.store, rig.client, prompter, pruneFlags{}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	t.Run("DEL-1 row still present in store", func(t *testing.T) {
		rows, err := rig.store.ListBranches(t.Context(), store.BranchStatusAll)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}

		if len(rows) != 1 {
			t.Errorf("store rows = %d, want 1 (unchanged on abort)", len(rows))
		}
	})

	t.Run("stdout shows 'Aborted.'", func(t *testing.T) {
		if !bytes.Contains(rig.stdout.Bytes(), []byte("Aborted.")) {
			t.Errorf("stdout = %q, want 'Aborted.'", rig.stdout.String())
		}
	})

	t.Run("prompter was called exactly once", func(t *testing.T) {
		if prompter.ConfirmCalls != 1 {
			t.Errorf("ConfirmCalls = %d, want 1", prompter.ConfirmCalls)
		}
	})
}

func TestRunPrune_YesFlagSkipsConfirm(t *testing.T) {
	t.Parallel()

	rig := newPruneRig(t)
	rig.seedIssueAndBranch(t, "DEL-1", "DEL-1@feat@gone", "feat")

	// Mirror what pruneRunE does when --yes is set: use autoConfirmPrunePrompter directly.
	prompter := newAutoConfirmPrunePrompter()

	if err := runPrune(t.Context(), rig.stdout, rig.store, rig.client, prompter, pruneFlags{yes: true}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	t.Run("DEL-1 row removed (auto-confirm executed)", func(t *testing.T) {
		rows, err := rig.store.ListBranches(t.Context(), store.BranchStatusAll)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}

		for _, r := range rows {
			if r.BranchName == "DEL-1@feat@gone" {
				t.Errorf("DEL-1@feat@gone still present despite --yes")
			}
		}
	})

	t.Run("stdout shows the success line", func(t *testing.T) {
		if !bytes.Contains(rig.stdout.Bytes(), []byte("Pruned: 1 deleted")) {
			t.Errorf("stdout = %q, want 'Pruned: 1 deleted'", rig.stdout.String())
		}
	})
}
