package branch

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
)

// pruneTestRig bundles a real on-disk git repo + seeded branch chains so prune E2E
// tests share setup. The repo starts with one commit on master; tests seed
// additional branches (deleted / merged / active) via the rig's helpers.
type pruneTestRig struct {
	dir    string
	client *git.Client
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func newPruneRig(t *testing.T) *pruneTestRig {
	t.Helper()

	return newPruneRigWithOrigin(t, "")
}

// newPruneRigWithOrigin is newPruneRig with origin as the "origin" remote,
// added before the client is created: a client caches its remote name.
func newPruneRigWithOrigin(t *testing.T, origin string) *pruneTestRig {
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
	if origin != "" {
		runGit("remote", "add", "origin", origin)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	ioStreams := &pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stderr}

	client, err := git.NewClientAt(ioStreams, dir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}

	return &pruneTestRig{
		dir: dir, client: client,
		stdout: stdout, stderr: stderr,
	}
}

// seedIssueAndBranch tracks branchName as an in-progress branch of its issue.
func (r *pruneTestRig) seedIssueAndBranch(t *testing.T, issueSlug, branchName, branchType string) {
	t.Helper()

	branchtest.Seed(t, r.client,
		branch.Op{Branch: branchName, BranchType: branchType, Title: issueSlug}, branch.StatusInProgress)
}

// statusOf returns the recorded status of branchName, "" when untracked.
func (r *pruneTestRig) statusOf(t *testing.T, branchName string) string {
	t.Helper()

	_, e, err := branch.Find(t.Context(), r.client, branchName)
	if err != nil {
		t.Fatalf("Find(%s): %v", branchName, err)
	}
	if e == nil {
		return ""
	}

	return e.Status
}

// inProgress returns how many tracked branches are in progress.
func (r *pruneTestRig) inProgress(t *testing.T) int {
	t.Helper()

	rows, err := branch.ListRows(t.Context(), r.client, branch.StatusInProgress)
	if err != nil {
		t.Fatalf("ListRows: %v", err)
	}

	return len(rows)
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
	//   DEL-1 — tracked, no git branch anywhere → "to close"
	//   MRG-1 — tracked + git branch + merged into master → "to merge"
	//   ACT-1 — tracked + git branch + NOT merged → no action
	rig.seedIssueAndBranch(t, "DEL-1", "DEL-1@feat@gone", "feat")
	rig.seedIssueAndBranch(t, "MRG-1", "MRG-1@fix@done", "fix")
	rig.seedIssueAndBranch(t, "ACT-1", "ACT-1@feat@active", "feat")
	rig.mergeBranchIntoMaster(t, "MRG-1@fix@done", "merged\n")
	rig.createGitBranch(t, "ACT-1@feat@active")

	prompter := &scriptedPrunePrompter{Confirm: true}

	if err := runPrune(t.Context(), rig.stdout, rig.client, prompter, pruneFlags{}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	t.Run("DEL-1 is recorded as closed", func(t *testing.T) {
		if got := rig.statusOf(t, "DEL-1@feat@gone"); got != branch.StatusClosed {
			t.Errorf("DEL-1@feat@gone status = %q, want closed", got)
		}
	})

	t.Run("MRG-1 status flipped to merged", func(t *testing.T) {
		if got := rig.statusOf(t, "MRG-1@fix@done"); got != branch.StatusMerged {
			t.Errorf("MRG-1@fix@done status = %q, want merged", got)
		}
	})

	t.Run("ACT-1 left in-progress", func(t *testing.T) {
		if got := rig.statusOf(t, "ACT-1@feat@active"); got != branch.StatusInProgress {
			t.Errorf("ACT-1@feat@active status = %q, want in_progress", got)
		}
	})

	t.Run("prompter was called once with the right counts", func(t *testing.T) {
		if prompter.ConfirmCalls != 1 {
			t.Errorf("ConfirmCalls = %d, want 1", prompter.ConfirmCalls)
		}

		if prompter.LastToClose != 1 {
			t.Errorf("LastToClose = %d, want 1", prompter.LastToClose)
		}

		if prompter.LastToMerge != 1 {
			t.Errorf("LastToMerge = %d, want 1", prompter.LastToMerge)
		}
	})

	t.Run("stdout shows the summary", func(t *testing.T) {
		got := rig.stdout.String()
		if !bytes.Contains([]byte(got), []byte("Pruned: 1 closed, 1 marked merged")) {
			t.Errorf("stdout = %q, want it to contain 'Pruned: 1 closed, 1 marked merged'", got)
		}
	})
}

func TestRunPrune_DryRun_ReportsDeletedBranch(t *testing.T) {
	t.Parallel()

	rig := newPruneRig(t)
	rig.seedIssueAndBranch(t, "ABC-1", "ABC-1@feat@gone", "feat")

	prompter := &scriptedPrunePrompter{}

	if err := runPrune(t.Context(), rig.stdout, rig.client, prompter, pruneFlags{dryRun: true}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	t.Run("output mentions the deleted branch", func(t *testing.T) {
		if !bytes.Contains(rig.stdout.Bytes(), []byte("ABC-1@feat@gone")) {
			t.Errorf("stdout = %q, want it to mention 'ABC-1@feat@gone'", rig.stdout.String())
		}
	})

	t.Run("nothing is recorded after dry-run", func(t *testing.T) {
		if n := rig.inProgress(t); n != 1 {
			t.Errorf("in-progress branches = %d, want 1 (unchanged)", n)
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

	if err := runPrune(t.Context(), rig.stdout, rig.client, prompter, pruneFlags{dryRun: true}); err != nil {
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

	if err := runPrune(t.Context(), rig.stdout, rig.client, prompter, pruneFlags{dryRun: true}); err != nil {
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

	if err := runPrune(t.Context(), rig.stdout, rig.client, prompter, pruneFlags{dryRun: true}); err != nil {
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

	t.Run("nothing is recorded", func(t *testing.T) {
		if n := rig.inProgress(t); n != 3 {
			t.Errorf("in-progress branches = %d, want 3 (unchanged)", n)
		}
	})
}

func TestRunPrune_UserAbortsAtConfirm(t *testing.T) {
	t.Parallel()

	rig := newPruneRig(t)
	rig.seedIssueAndBranch(t, "DEL-1", "DEL-1@feat@gone", "feat")

	prompter := &scriptedPrunePrompter{Confirm: false}

	if err := runPrune(t.Context(), rig.stdout, rig.client, prompter, pruneFlags{}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	t.Run("DEL-1 is still in progress", func(t *testing.T) {
		if got := rig.statusOf(t, "DEL-1@feat@gone"); got != branch.StatusInProgress {
			t.Errorf("status = %q, want in_progress (unchanged on abort)", got)
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
	prompter := &autoConfirmPrunePrompter{}

	if err := runPrune(t.Context(), rig.stdout, rig.client, prompter, pruneFlags{yes: true}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	t.Run("DEL-1 closed (auto-confirm executed)", func(t *testing.T) {
		if got := rig.statusOf(t, "DEL-1@feat@gone"); got != branch.StatusClosed {
			t.Errorf("status = %q, want closed despite --yes", got)
		}
	})

	t.Run("stdout shows the success line", func(t *testing.T) {
		if !bytes.Contains(rig.stdout.Bytes(), []byte("Pruned: 1 closed")) {
			t.Errorf("stdout = %q, want 'Pruned: 1 closed'", rig.stdout.String())
		}
	})
}

// runPruneGit runs git in the rig's repository.
func (r *pruneTestRig) runPruneGit(t *testing.T, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = r.dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func newPruneOrigin(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.CommandContext(t.Context(), "git", "init", "-q", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}

	return dir
}

func TestRunPrune_LeavesABranchStartedBySomeoneElse(t *testing.T) {
	t.Parallel()

	// A teammate started TM-1: the record reached this clone, the branch never
	// did (issue start pushes the record, not the branch).
	rig := newPruneRig(t)
	rig.seedIssueAndBranch(t, "TM-1", "TM-1@feat@theirs", "feat")
	rig.runPruneGit(t, "config", "user.name", "Someone Else")

	prompter := &scriptedPrunePrompter{Confirm: true}
	if err := runPrune(t.Context(), rig.stdout, rig.client, prompter, pruneFlags{}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	t.Run("the branch stays in progress", func(t *testing.T) {
		if got := rig.statusOf(t, "TM-1@feat@theirs"); got != branch.StatusInProgress {
			t.Errorf("status = %q, want in_progress", got)
		}
	})

	t.Run("the output lists it as skipped, with who started it", func(t *testing.T) {
		out := rig.stdout.String()
		for _, want := range []string{"Skipped", "TM-1@feat@theirs", "Test User", "prune --others", "Nothing to prune."} {
			if !strings.Contains(out, want) {
				t.Errorf("stdout lacks %q:\n%s", want, out)
			}
		}
	})

	t.Run("no confirmation was asked", func(t *testing.T) {
		if prompter.ConfirmCalls != 0 {
			t.Errorf("ConfirmCalls = %d, want 0", prompter.ConfirmCalls)
		}
	})
}

func TestRunPrune_OthersClosesABranchStartedBySomeoneElse(t *testing.T) {
	t.Parallel()

	// TM-1 was started by someone who is gone: no branch anywhere, and its
	// author will never run prune.
	rig := newPruneRig(t)
	rig.seedIssueAndBranch(t, "TM-1", "TM-1@feat@theirs", "feat")
	rig.runPruneGit(t, "config", "user.name", "Someone Else")

	prompter := &scriptedPrunePrompter{Confirm: true}
	if err := runPrune(t.Context(), rig.stdout, rig.client, prompter, pruneFlags{others: true}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	t.Run("the branch is recorded as closed", func(t *testing.T) {
		if got := rig.statusOf(t, "TM-1@feat@theirs"); got != branch.StatusClosed {
			t.Errorf("status = %q, want closed", got)
		}
	})

	t.Run("the summary names who started it", func(t *testing.T) {
		out := rig.stdout.String()
		for _, want := range []string{"Will close", "TM-1@feat@theirs (started by Test User", "Pruned: 1 closed"} {
			if !strings.Contains(out, want) {
				t.Errorf("stdout lacks %q:\n%s", want, out)
			}
		}
	})

	t.Run("the confirmation counted the close", func(t *testing.T) {
		if prompter.ConfirmCalls != 1 || prompter.LastToClose != 1 {
			t.Errorf("confirm calls = %d, to close = %d", prompter.ConfirmCalls, prompter.LastToClose)
		}
	})
}

func TestRunPrune_LeavesABranchStillOnTheRemote(t *testing.T) {
	t.Parallel()

	rig := newPruneRigWithOrigin(t, newPruneOrigin(t))
	rig.seedIssueAndBranch(t, "RM-1", "RM-1@feat@pushed", "feat")
	rig.createGitBranch(t, "RM-1@feat@pushed")
	rig.runPruneGit(t, "push", "-q", "origin", "RM-1@feat@pushed")
	rig.runPruneGit(t, "branch", "-D", "RM-1@feat@pushed")

	if err := runPrune(t.Context(), rig.stdout, rig.client, &scriptedPrunePrompter{Confirm: true}, pruneFlags{}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	t.Run("gone locally but on the remote: still in progress", func(t *testing.T) {
		if got := rig.statusOf(t, "RM-1@feat@pushed"); got != branch.StatusInProgress {
			t.Errorf("status = %q, want in_progress", got)
		}
	})

	t.Run("output says 'Nothing to prune.'", func(t *testing.T) {
		if !strings.Contains(rig.stdout.String(), "Nothing to prune.") {
			t.Errorf("stdout = %q", rig.stdout.String())
		}
	})
}

func TestRunPrune_ClosesNothingWhenTheRemoteIsUnreachable(t *testing.T) {
	t.Parallel()

	rig := newPruneRigWithOrigin(t, filepath.Join(t.TempDir(), "unreachable.git"))
	rig.seedIssueAndBranch(t, "DEL-1", "DEL-1@feat@gone", "feat")
	rig.seedIssueAndBranch(t, "MRG-1", "MRG-1@fix@done", "fix")
	rig.mergeBranchIntoMaster(t, "MRG-1@fix@done", "merged\n")

	prompter := &scriptedPrunePrompter{Confirm: true}
	if err := runPrune(t.Context(), rig.stdout, rig.client, prompter, pruneFlags{}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	t.Run("the branch gone locally is not closed", func(t *testing.T) {
		if got := rig.statusOf(t, "DEL-1@feat@gone"); got != branch.StatusInProgress {
			t.Errorf("status = %q, want in_progress", got)
		}
	})

	t.Run("the merge is still recorded", func(t *testing.T) {
		if got := rig.statusOf(t, "MRG-1@fix@done"); got != branch.StatusMerged {
			t.Errorf("status = %q, want merged", got)
		}
	})

	t.Run("stderr says why nothing is closed", func(t *testing.T) {
		if !strings.Contains(rig.stderr.String(), "no branch will be closed") {
			t.Errorf("stderr = %q", rig.stderr.String())
		}
	})

	t.Run("the confirmation counted no close", func(t *testing.T) {
		if prompter.LastToClose != 0 || prompter.LastToMerge != 1 {
			t.Errorf("confirm(%d, %d), want (0, 1)", prompter.LastToClose, prompter.LastToMerge)
		}
	})
}
