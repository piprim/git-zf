package issue

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	commitpkg "github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

// closeTestRig bundles a real on-disk git repo, a seeded store, and a fake
// tracker so each E2E test sets up state in one line.
type closeTestRig struct {
	dir     string
	client  *git.Client
	store   *store.Store
	tracker *fake.Tracker
	cfg     *config.AppConfig
	stdout  *bytes.Buffer
	stderr  *bytes.Buffer
}

// newCloseRig initialises a temp git repo with a "main" branch carrying one
// commit, a feature branch ABC-1@feat@add-thing one commit ahead of main, a
// store with the matching in-progress row, and a fake tracker.
//
// The returned rig embeds the IO buffers so tests can assert on stdout/stderr.
func newCloseRig(t *testing.T) *closeTestRig {
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

	runGit("init", "-q", "-b", "main")
	runGit("config", "user.name", "Test User")
	runGit("config", "user.email", "test@test.com")
	runGit("config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}
	runGit("add", "base.txt")
	runGit("commit", "-m", "chore: init")

	runGit("checkout", "-b", "ABC-1@feat@add-thing")
	if err := os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	runGit("add", "feature.txt")
	runGit("commit", "-m", "feat: add thing")
	runGit("checkout", "main")

	return seedCloseRig(t, dir)
}

// seedCloseRig builds the rig around an already-created repo at dir whose
// "main" and "ABC-1@feat@add-thing" branches exist: the IO buffers, a client
// anchored on dir, an opened store seeded with the matching in-progress rows,
// the tracker-born BranchRef, the app config and the fake tracker. Shared by
// newCloseRig (feature branch in the main tree) and newWorktreeCloseRig
// (feature branch in a linked worktree), which differ only in how they create
// that branch.
func seedCloseRig(t *testing.T, dir string) *closeTestRig {
	t.Helper()

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

	trackerType := "fake"
	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "ABC-1", Title: "Add thing", TrackerType: &trackerType},
		&store.Branch{Name: "ABC-1@feat@add-thing", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed branch: %v", err)
	}

	// Seed a tracker-born BranchRef so the close-flow origin gate treats ABC-1
	// as created from the tracker (mirrors what `issue start` writes). The git
	// object is the cross-machine source of truth for the tracker-status prompt.
	if _, err := client.WriteBranchRef(t.Context(), "ABC-1", git.BranchRef{
		IssueSlug:   "ABC-1",
		BranchName:  "ABC-1@feat@add-thing",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		TrackerType: "fake",
	}); err != nil {
		t.Fatalf("seed branch ref: %v", err)
	}

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"
	cfg.IssueTracker.Type = "fake"

	rawT, err := tracker.New(cfg.IssueTracker)
	if err != nil {
		t.Fatalf("tracker.New: %v", err)
	}
	fakeT, ok := rawT.(*fake.Tracker)
	if !ok {
		t.Fatalf("tracker.New returned %T, want *fake.Tracker", rawT)
	}

	return &closeTestRig{
		dir: dir, client: client, store: s, tracker: fakeT, cfg: cfg,
		stdout: stdout, stderr: stderr,
	}
}

func (r *closeTestRig) deps() closeDeps {
	return closeDeps{
		client: r.client, store: r.store, cfg: r.cfg, tracker: r.tracker,
		// Mirrors buildCloseDeps for a command typed in the main tree: no
		// invokedFrom, so the branch falls back to the main client's HEAD.
		invokedBranch: invokedBranchFor(context.Background(), r.client, ""),
	}
}

// cdHint rebuilds the exact needle RemoveWorktreeStep prints for dir: the
// symlink-resolved (go-git) root, shell-quoted.
func cdHint(t *testing.T, dir string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		resolved = dir
	}

	return "cd " + strconv.Quote(resolved)
}

// pickedBranchRow returns the BranchRow the picker would have returned for
// the seeded branch. Used by every scriptedPrompter setup.
func (r *closeTestRig) pickedBranchRow() *store.BranchRow {
	return &store.BranchRow{
		IssueID:    1, // first row in the empty store
		IssueSlug:  "ABC-1",
		Title:      "Add thing",
		BranchName: "ABC-1@feat@add-thing",
		Type:       "feat",
		Status:     store.BranchStatusInProgress,
	}
}

// addBranch creates a local branch off main (no extra commits) so tests can
// exercise the multi-candidate merge-target picker.
func (r *closeTestRig) addBranch(t *testing.T, name string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "branch", name, "main")
	cmd.Dir = r.dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git branch %s: %v\n%s", name, err, out)
	}
}

// addOrigin wires a bare origin, pushes main and the seeded feature branch,
// and re-opens the client so remote auto-detection sees it.
func (r *closeTestRig) addOrigin(t *testing.T) string {
	t.Helper()

	originDir := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.CommandContext(t.Context(), "git", "init", "-q", "--bare", "--initial-branch=main", originDir).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	mustRunGitAt(t, r.dir, "remote", "add", "origin", originDir)
	mustRunGitAt(t, r.dir, "push", "-q", "origin", "main", "ABC-1@feat@add-thing")
	r.rebuildClient(t)

	return originDir
}

// rebuildClient re-opens the git client on the rig's repo (needed after the
// remote set changes, since Remote() caches its detection).
func (r *closeTestRig) rebuildClient(t *testing.T) {
	t.Helper()

	c, err := git.NewClientAt(&pkg.IO{In: bytes.NewReader(nil), Out: r.stdout, Err: r.stderr}, r.dir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}
	r.client = c
}

func lsRemoteHeads(t *testing.T, dir, branch string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "ls-remote", "--heads", "origin", branch)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-remote: %v", err)
	}

	return strings.TrimSpace(string(out))
}

func TestClose_DeletesRemoteFeatureBranch(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)
	rig.addOrigin(t)

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      commitpkg.MergeStrategySquash,
		Confirm:       true,
		Message:       []byte("feat(thing): close ABC-1\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  true,
	}

	err := runClose(t.Context(), rig.deps(), prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
	})
	t.Run("local feature branch deleted", func(t *testing.T) {
		assertBranchAbsent(t, rig.client, "ABC-1@feat@add-thing")
	})
	t.Run("remote feature branch deleted", func(t *testing.T) {
		if got := lsRemoteHeads(t, rig.dir, "ABC-1@feat@add-thing"); got != "" {
			t.Fatalf("origin still has the branch: %q", got)
		}
	})
}

// mustRunGitAt runs a git command in dir, failing the test on error. Shared
// helper for tests that need ad-hoc git operations beyond what closeTestRig
// exposes (e.g. creating a review branch with its own commits).
func mustRunGitAt(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// writeFileAt writes content to name under dir, failing the test on error.
func writeFileAt(t *testing.T, dir, name, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// assertHeadSubject asserts the subject line of HEAD on branch matches want.
func assertHeadSubject(t *testing.T, dir, branch, want string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "log", "-1", "--format=%s", branch)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log %s: %v", branch, err)
	}
	got := string(bytes.TrimSpace(out))
	if got != want {
		t.Errorf("HEAD subject on %q = %q, want %q", branch, got, want)
	}
}

// assertBranchAbsent asserts that branch no longer exists locally.
func assertBranchAbsent(t *testing.T, c *git.Client, branch string) {
	t.Helper()

	exists, err := c.BranchExists(branch)
	if err != nil {
		t.Fatalf("BranchExists: %v", err)
	}
	if exists {
		t.Errorf("branch %q still exists; expected it to be deleted", branch)
	}
}

func TestClose_RebaseHappyPath(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      commitpkg.MergeStrategyRebase,
		Confirm:       true,
		Message:       []byte("feat(thing): close ABC-1\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  true,
	}

	if err := runClose(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("main HEAD carries the new commit subject", func(t *testing.T) {
		// Rebase strategy lands the squashed merge directly on main via the
		// post-commit fast-forward.
		assertHeadSubject(t, rig.dir, "main", "feat(thing): close ABC-1")
	})

	t.Run("feature branch is deleted", func(t *testing.T) {
		// force=true for rebase.
		assertBranchAbsent(t, rig.client, "ABC-1@feat@add-thing")
	})

	t.Run("store records branch as merged", func(t *testing.T) {
		branches, err := rig.store.ListBranches(t.Context(), store.BranchStatusMerged)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}
		if len(branches) != 1 || branches[0].BranchName != "ABC-1@feat@add-thing" {
			t.Errorf("expected one merged branch row for ABC-1@feat@add-thing, got %+v", branches)
		}
	})

	t.Run("tracker recorded one UpdateIssueStatus call", func(t *testing.T) {
		if got, want := len(rig.tracker.RecordedUpdates), 1; got != want {
			t.Fatalf("RecordedUpdates len = %d, want %d", got, want)
		}
		if rig.tracker.RecordedUpdates[0] != (fake.Update{IssueID: "ABC-1", StatusName: "Closed"}) {
			t.Errorf("RecordedUpdates[0] = %+v, want {ABC-1 Closed}", rig.tracker.RecordedUpdates[0])
		}
	})
}

func TestClose_SquashHappyPath(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      commitpkg.MergeStrategySquash,
		Confirm:       true,
		Message:       []byte("feat(thing): squash-close ABC-1\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  true,
	}

	if err := runClose(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	// Squash strategy: the merge commit lands on the CURRENT branch (the
	// feature branch — MergeSquash stages, then Commit fires on whatever HEAD
	// is). We then delete the feature branch, so the merge commit lives only
	// on its reflog. No head-subject assertion — store/tracker state is what's
	// observable.

	t.Run("feature branch is deleted", func(t *testing.T) {
		assertBranchAbsent(t, rig.client, "ABC-1@feat@add-thing")
	})

	t.Run("store records branch as merged", func(t *testing.T) {
		branches, err := rig.store.ListBranches(t.Context(), store.BranchStatusMerged)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}
		if len(branches) != 1 {
			t.Fatalf("expected one merged branch row, got %d (%+v)", len(branches), branches)
		}
	})

	t.Run("tracker recorded one UpdateIssueStatus call", func(t *testing.T) {
		if len(rig.tracker.RecordedUpdates) != 1 {
			t.Fatalf("RecordedUpdates len = %d, want 1", len(rig.tracker.RecordedUpdates))
		}
	})
}

func TestClose_ClassicHappyPath(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      commitpkg.MergeStrategyClassic,
		Confirm:       true,
		Message:       []byte("Merge ABC-1 into main\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  false, // exercise the "don't delete" path
	}

	if err := runClose(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("main HEAD carries the merge commit", func(t *testing.T) {
		// Classic strategy: merge --no-ff lands the merge commit directly on main.
		assertHeadSubject(t, rig.dir, "main", "Merge ABC-1 into main")
	})

	t.Run("feature branch is retained", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-1@feat@add-thing")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if !exists {
			t.Error("feature branch was deleted; expected it to remain (DeleteBranch=false)")
		}
	})

	t.Run("store records branch as merged", func(t *testing.T) {
		branches, err := rig.store.ListBranches(t.Context(), store.BranchStatusMerged)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}
		if len(branches) != 1 {
			t.Fatalf("expected one merged branch row, got %d", len(branches))
		}
	})

	t.Run("tracker recorded one UpdateIssueStatus call", func(t *testing.T) {
		if len(rig.tracker.RecordedUpdates) != 1 {
			t.Errorf("RecordedUpdates len = %d, want 1", len(rig.tracker.RecordedUpdates))
		}
	})
}

// TestClose_ManualIssue_NoTrackerPrompt guards the gating bug where closing a
// manually-created issue prompted for a tracker status update purely because a
// tracker was configured. The origin signal lives in BranchRef.TrackerType: an
// empty value means "manual" and must suppress the prompt.
func TestClose_ManualIssue_NoTrackerPrompt(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	// Overwrite ABC-1's ref so it carries no tracker origin (manual issue). A
	// tracker is still configured (rig.tracker != nil), but close must not prompt.
	if _, err := rig.client.WriteBranchRef(t.Context(), "ABC-1", git.BranchRef{
		IssueSlug:  "ABC-1",
		BranchName: "ABC-1@feat@add-thing",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
		// TrackerType intentionally empty → manual.
	}); err != nil {
		t.Fatalf("WriteBranchRef: %v", err)
	}

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      commitpkg.MergeStrategyRebase,
		Confirm:       true,
		Message:       []byte("feat(thing): close ABC-1\n"),
		TrackerStatus: "Closed", // would be recorded if the prompt wrongly fired
		DeleteBranch:  true,
	}

	if err := runClose(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("no tracker update recorded for a manual issue", func(t *testing.T) {
		if got := len(rig.tracker.RecordedUpdates); got != 0 {
			t.Errorf("RecordedUpdates len = %d, want 0; got %+v", got, rig.tracker.RecordedUpdates)
		}
	})
}

func TestClose_NoInProgressBranches(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Bare repo init — no seeded branch row.
	cmd := exec.CommandContext(t.Context(), "git", "init", "-q", "-b", "main", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	ioStreams := &pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stderr}

	client, err := git.NewClientAt(ioStreams, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	s, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"

	deps := closeDeps{client: client, store: s, cfg: cfg}
	deps.invokedBranch = invokedBranchFor(t.Context(), client, "")

	// PickBranch should never be called — but if runClose mis-routes, fail loudly.
	prompter := &scriptedPrompter{BranchErr: errors.New("PickBranch should not be called when no branches exist")}

	if err := runClose(t.Context(), deps, prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("stdout shows the empty-list message", func(t *testing.T) {
		if got := stdout.String(); !strings.Contains(got, "No branches available to close") {
			t.Errorf("stdout = %q, want it to contain 'No branches available to close'", got)
		}
	})
}

// TestClose_EmptyState_RedirectsToBranchMerge verifies that when there are no
// close candidates and HEAD is on a non-issue branch with unmerged commits,
// close points the user at `git zf branch merge` instead of the bare
// "No branches available to close." message.
func TestClose_EmptyState_RedirectsToBranchMerge(t *testing.T) {
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runGit("init", "-q", "-b", "main")
	runGit("config", "user.name", "Test User")
	runGit("config", "user.email", "test@test.com")
	runGit("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}
	runGit("add", "base.txt")
	runGit("commit", "-m", "chore: init")

	// A non-issue branch with an unmerged commit; leave HEAD on it.
	runGit("checkout", "-b", "hotfix")
	if err := os.WriteFile(filepath.Join(dir, "hotfix.txt"), []byte("hotfix\n"), 0o644); err != nil {
		t.Fatalf("write hotfix.txt: %v", err)
	}
	runGit("add", "hotfix.txt")
	runGit("commit", "-m", "fix: urgent")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	ioStreams := &pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stderr}

	client, err := git.NewClientAt(ioStreams, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	s, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"

	deps := closeDeps{client: client, store: s, cfg: cfg}
	deps.invokedBranch = invokedBranchFor(t.Context(), client, "")

	// PickBranch must never be called — no candidates exist.
	prompter := &scriptedPrompter{BranchErr: errors.New("PickBranch should not be called when no candidates exist")}

	if err := runClose(t.Context(), deps, prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("suggests branch merge", func(t *testing.T) {
		if got := stdout.String(); !strings.Contains(got, "git zf branch merge") {
			t.Errorf("stdout = %q, want it to suggest 'git zf branch merge'", got)
		}
	})
	t.Run("does not print the plain empty-list message", func(t *testing.T) {
		if got := stdout.String(); strings.Contains(got, "No branches available to close") {
			t.Errorf("stdout should redirect, not show the plain empty message; got %q", got)
		}
	})
}

// TestClose_EmptyState_OnUntrackedIssueBranch_DoesNotRedirect guards the
// misfire fixed in getPickedBranch: an issue-shaped branch with unmerged
// commits but no store row and no refs/zf/branches ref (a fresh local issue
// branch, or a reviewer clone pre-reconciliation) yields zero close
// candidates and lands in the empty-state block. The `branch merge` nudge
// must NOT fire there — it claims the branch "isn't an issue branch" (false)
// and `branch merge` refuses issue branches on the same branch.Parse gate,
// which would bounce the user straight back to `issue close`.
func TestClose_EmptyState_OnUntrackedIssueBranch_DoesNotRedirect(t *testing.T) {
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runGit("init", "-q", "-b", "main")
	runGit("config", "user.name", "Test User")
	runGit("config", "user.email", "test@test.com")
	runGit("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}
	runGit("add", "base.txt")
	runGit("commit", "-m", "chore: init")

	// An issue-shaped branch (parses under branch.Parse) with an unmerged
	// commit, but deliberately NO store row and NO branch ref. Leave HEAD on it.
	runGit("checkout", "-b", "DEF-9@feat@fresh")
	if err := os.WriteFile(filepath.Join(dir, "fresh.txt"), []byte("fresh\n"), 0o644); err != nil {
		t.Fatalf("write fresh.txt: %v", err)
	}
	runGit("add", "fresh.txt")
	runGit("commit", "-m", "feat: fresh")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	ioStreams := &pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stderr}

	client, err := git.NewClientAt(ioStreams, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	s, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"

	deps := closeDeps{client: client, store: s, cfg: cfg}
	deps.invokedBranch = invokedBranchFor(t.Context(), client, "")

	// PickBranch must never be called — no candidates exist.
	prompter := &scriptedPrompter{BranchErr: errors.New("PickBranch should not be called when no candidates exist")}

	if err := runClose(t.Context(), deps, prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("does not misfire the branch merge nudge", func(t *testing.T) {
		if got := stdout.String(); strings.Contains(got, "git zf branch merge") {
			t.Errorf("stdout should not redirect an issue branch to `branch merge`; got %q", got)
		}
	})
	t.Run("prints the plain empty-list message", func(t *testing.T) {
		if got := stdout.String(); !strings.Contains(got, "No branches available to close") {
			t.Errorf("stdout = %q, want it to contain 'No branches available to close'", got)
		}
	})
}

func TestClose_UserAbortsAtConfirm(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	prompter := &scriptedPrompter{
		Branch:   rig.pickedBranchRow(),
		Strategy: commitpkg.MergeStrategyRebase,
		Confirm:  false, // operator declines
	}

	if err := runClose(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("stdout shows the Aborted message", func(t *testing.T) {
		if got := rig.stdout.String(); !strings.Contains(got, "Aborted.") {
			t.Errorf("stdout = %q, want it to contain 'Aborted.'", got)
		}
	})

	t.Run("feature branch is preserved", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-1@feat@add-thing")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if !exists {
			t.Error("feature branch was deleted on user-abort path")
		}
	})

	t.Run("tracker recorded no updates", func(t *testing.T) {
		if len(rig.tracker.RecordedUpdates) != 0 {
			t.Errorf("RecordedUpdates = %+v, want empty on abort", rig.tracker.RecordedUpdates)
		}
	})
}

func TestClose_PrefillHasFixFooter(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	// Give the config a footer item so IssueHint.Prefill can populate it.
	rig.cfg.CommitMessage.Items = []config.CommitItem{
		{Name: "subject", Required: true},
		{Name: "footer"},
	}

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      commitpkg.MergeStrategyRebase,
		Confirm:       true,
		Message:       []byte("feat: close ABC-1\n\nCloses #ABC-1\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  false,
	}

	if err := runClose(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("ComposeMessage prefill footer has close ref and merge info", func(t *testing.T) {
		got, ok := prompter.CapturedPrefill["footer"].(string)
		if !ok {
			t.Fatal("prefill missing 'footer' key")
		}
		// The SHAs are freshly created by the rig, so only the fixed parts
		// of "Closes #ABC-1 - Rebase <sha> into <sha>." can be asserted.
		if !strings.HasPrefix(got, "Closes #ABC-1 - Rebase ") || !strings.HasSuffix(got, ".") {
			t.Errorf("prefill[footer] = %q, want \"Closes #ABC-1 - Rebase <sha> into <sha>.\"", got)
		}
	})

	t.Run("ComposeMessage prefill subject includes the ticket subject", func(t *testing.T) {
		got, ok := prompter.CapturedPrefill["subject"]
		if !ok {
			t.Fatal("prefill missing 'subject' key")
		}
		if got != "[close] Add thing" {
			t.Errorf("prefill[subject] = %q, want %q", got, "[close] Add thing")
		}
	})
}

func TestClose_ConflictAborts(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	// Create a conflicting change on main before calling runClose.
	runGit := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = rig.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Touch the same file on main with a different content, creating a dry-run conflict.
	if err := os.WriteFile(filepath.Join(rig.dir, "feature.txt"), []byte("main-conflict\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt on main: %v", err)
	}
	runGit("add", "feature.txt")
	runGit("commit", "-m", "chore: conflict")

	prompter := &scriptedPrompter{
		Branch:  rig.pickedBranchRow(),
		Confirm: true,
		// Strategy/Message irrelevant — the dry-run fails before either is asked.
	}

	err := runClose(t.Context(), rig.deps(), prompter)

	t.Run("runClose returns an error", func(t *testing.T) {
		if err == nil {
			t.Fatal("runClose: expected an error from the dry-run conflict, got nil")
		}
	})

	t.Run("error mentions merge conflicts", func(t *testing.T) {
		if err == nil {
			t.Skip("no error returned (covered by prior subtest)")
		}
		if !strings.Contains(err.Error(), "merge conflicts") {
			t.Errorf("error = %v, want it to mention 'merge conflicts'", err)
		}
	})

	t.Run("stdout lists the conflicting files", func(t *testing.T) {
		if got := rig.stdout.String(); !strings.Contains(got, "Conflicts detected") {
			t.Errorf("stdout = %q, want it to mention 'Conflicts detected'", got)
		}
	})
}

// seedReviewRef writes a git review ref so reviewPreflight (which reads the
// ref as authoritative) can see the correct status in tests.
func seedReviewRef(t *testing.T, rig *closeTestRig, issueSlug string, status store.ReviewStatus, round int) {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", "-C", rig.dir, "rev-parse", "ABC-1@feat@add-thing").Output()
	if err != nil {
		t.Fatalf("rev-parse feature branch: %v", err)
	}
	ref := git.ReviewRef{
		Status:     string(status),
		Round:      round,
		FeatureSHA: strings.TrimSpace(string(out)),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := rig.client.WriteReviewRef(t.Context(), issueSlug, ref, ""); err != nil {
		t.Fatalf("WriteReviewRef: %v", err)
	}
}

func TestClose_ReviewPreflight(t *testing.T) {
	t.Parallel()

	t.Run("blocked when branch is in_review", func(t *testing.T) {
		t.Parallel()

		rig := newCloseRig(t)
		if _, err := rig.store.InsertReview(t.Context(), "ABC-1", "alice"); err != nil {
			t.Fatalf("InsertReview: %v", err)
		}
		seedReviewRef(t, rig, "ABC-1", store.ReviewStatusInReview, 1)

		p := &scriptedPrompter{Branch: rig.pickedBranchRow()}
		err := runClose(t.Context(), rig.deps(), p)

		t.Run("returns error", func(t *testing.T) {
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
		t.Run("error wraps ErrBranchLockedForReview", func(t *testing.T) {
			if !errors.Is(err, ErrBranchLockedForReview) {
				t.Errorf("expected ErrBranchLockedForReview, got: %v", err)
			}
		})
	})

	t.Run("blocked when changes_requested", func(t *testing.T) {
		t.Parallel()

		rig := newCloseRig(t)
		row, err := rig.store.InsertReview(t.Context(), "ABC-1", "alice")
		if err != nil {
			t.Fatalf("InsertReview: %v", err)
		}
		if err := rig.store.UpdateReviewStatus(t.Context(), row.ID, store.ReviewStatusChangesRequested, false); err != nil {
			t.Fatalf("UpdateReviewStatus: %v", err)
		}
		seedReviewRef(t, rig, "ABC-1", store.ReviewStatusChangesRequested, 1)

		p := &scriptedPrompter{Branch: rig.pickedBranchRow()}
		err = runClose(t.Context(), rig.deps(), p)

		t.Run("returns error", func(t *testing.T) {
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
		t.Run("error wraps ErrReviewChangesRequested", func(t *testing.T) {
			if !errors.Is(err, ErrReviewChangesRequested) {
				t.Errorf("expected ErrReviewChangesRequested, got: %v", err)
			}
		})
	})
}

// TestClose_ReviewPreflight_IncorporatesRemoteOnlyReviewerCommits verifies
// Phase 10: Dan pushed reviewer commits to origin/X.2@review but Bob never
// checked out X.2@review locally. reviewPreflight must still detect the commits
// (via the remote tracking ref) and fast-forward the feature branch.
func TestClose_ReviewPreflight_IncorporatesRemoteOnlyReviewerCommits(t *testing.T) {
	t.Parallel()

	originDir := filepath.Join(t.TempDir(), "origin.git")
	bobDir := t.TempDir()

	runIn := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}

	// ----- origin -----
	if err := os.MkdirAll(originDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runIn(originDir, "init", "--bare", "--initial-branch=main")

	seedDir := t.TempDir()
	runIn(seedDir, "init", "--initial-branch=main")
	runIn(seedDir, "config", "user.name", "Seed")
	runIn(seedDir, "config", "user.email", "seed@example.com")
	runIn(seedDir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(seedDir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runIn(seedDir, "add", "base.txt")
	runIn(seedDir, "commit", "-m", "chore: init")
	runIn(seedDir, "remote", "add", "origin", originDir)
	runIn(seedDir, "push", "origin", "main")

	// Feature branch ABC-1@feat@thing
	runIn(seedDir, "checkout", "-b", "ABC-1@feat@thing")
	if err := os.WriteFile(filepath.Join(seedDir, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runIn(seedDir, "add", "feature.txt")
	runIn(seedDir, "commit", "-m", "feat: implement")
	runIn(seedDir, "push", "origin", "ABC-1@feat@thing")

	// Reviewer creates ABC-1@review and pushes a fix.
	runIn(seedDir, "checkout", "-b", "ABC-1@review")
	if err := os.WriteFile(filepath.Join(seedDir, "feature.txt"), []byte("feature\nreviewer fix\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runIn(seedDir, "add", "feature.txt")
	runIn(seedDir, "commit", "-m", "fix: reviewer fix")
	runIn(seedDir, "push", "origin", "ABC-1@review")
	runIn(seedDir, "checkout", "main")

	// ----- Bob's clone: never checks out ABC-1@review locally -----
	runIn(bobDir, "init", "--initial-branch=main")
	runIn(bobDir, "config", "user.name", "Bob")
	runIn(bobDir, "config", "user.email", "bob@example.com")
	runIn(bobDir, "config", "commit.gpgsign", "false")
	runIn(bobDir, "remote", "add", "origin", originDir)
	runIn(bobDir, "fetch", "origin")
	runIn(bobDir, "checkout", "main")
	runIn(bobDir, "checkout", "-b", "ABC-1@feat@thing", "origin/ABC-1@feat@thing")
	// ABC-1@review deliberately NOT checked out locally.

	stdout := &bytes.Buffer{}
	bobClient, err := git.NewClientAt(&pkg.IO{
		In: bytes.NewReader(nil), Out: stdout, Err: &bytes.Buffer{},
	}, bobDir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	bobStore, err := store.Open(t.Context(), filepath.Join(bobDir, ".git"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = bobStore.Close() })

	if err := bobStore.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "ABC-1", Title: "thing"},
		&store.Branch{Name: "ABC-1@feat@thing", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Write approved review ref and push it to origin — FetchReviewRefs uses
	// --prune, so any ref not on the remote would be deleted before the close reads it.
	featureSHA, _ := bobClient.ResolveRef("refs/heads/ABC-1@feat@thing")
	reviewRef := git.ReviewRef{
		Status: "approved", Round: 1,
		FeatureSHA: featureSHA.String(),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := bobClient.WriteReviewRef(t.Context(), "ABC-1", reviewRef, ""); err != nil {
		t.Fatalf("WriteReviewRef: %v", err)
	}
	// Push the ref so --prune doesn't remove it from local on the next fetch.
	runIn(bobDir, "push", "--force", "origin", "refs/zf/reviews/ABC-1")

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"
	deps := closeDeps{client: bobClient, store: bobStore, cfg: cfg}

	pickedRow := &store.BranchRow{
		IssueID: 1, IssueSlug: "ABC-1",
		BranchName: "ABC-1@feat@thing", Type: "feat",
		Status: store.BranchStatusInProgress,
	}
	prompter := &scriptedPrompter{
		Branch: pickedRow, Strategy: commitpkg.MergeStrategySquash, Confirm: true,
		Message: []byte("feat: close\n"), DeleteBranch: false,
	}

	if err := runClose(t.Context(), deps, prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("reviewer fix incorporated into feature branch", func(t *testing.T) {
		out, _ := exec.CommandContext(t.Context(), "git", "-C", bobDir,
			"log", "--oneline", "ABC-1@feat@thing").Output()
		if !strings.Contains(string(out), "reviewer fix") {
			t.Errorf("reviewer commit not in feature branch history; log:\n%s", out)
		}
	})

	t.Run("stdout says incorporating reviewer commits", func(t *testing.T) {
		if !strings.Contains(stdout.String(), "Incorporating") {
			t.Errorf("stdout = %q — expected 'Incorporating' message", stdout.String())
		}
	})
}

// TestClose_SubtaskDryRunFallsBackToRemoteBase verifies that the initial
// merge dry-run in doMerge succeeds when the base branch (a parent integration
// branch) exists only as a remote tracking ref (origin/<base>) and was never
// checked out locally. This is the failure mode from Phase 10 of the review
// E2E scenario: Bob closes X.2 but never checked out X@feat@big locally.
func TestClose_SubtaskDryRunFallsBackToRemoteBase(t *testing.T) {
	t.Parallel()

	originDir := t.TempDir()
	bobDir := t.TempDir()

	runIn := func(dir string, args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// ----- origin repo -----
	runIn(originDir, "init", "-q", "-b", "main")
	runIn(originDir, "config", "user.name", "Alice")
	runIn(originDir, "config", "user.email", "alice@example.com")
	runIn(originDir, "config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(originDir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}
	runIn(originDir, "add", "base.txt")
	runIn(originDir, "commit", "-m", "chore: init")

	// X@feat@big — parent integration branch.
	runIn(originDir, "checkout", "-b", "X@feat@big")
	if err := os.WriteFile(filepath.Join(originDir, "parent.txt"), []byte("parent\n"), 0o644); err != nil {
		t.Fatalf("write parent.txt: %v", err)
	}
	runIn(originDir, "add", "parent.txt")
	runIn(originDir, "commit", "-m", "feat(X): parent")

	// X.2@feat@two — sub-task branched from X@feat@big.
	runIn(originDir, "checkout", "-b", "X.2@feat@two")
	if err := os.WriteFile(filepath.Join(originDir, "subtask.txt"), []byte("subtask\n"), 0o644); err != nil {
		t.Fatalf("write subtask.txt: %v", err)
	}
	runIn(originDir, "add", "subtask.txt")
	runIn(originDir, "commit", "-m", "feat(X.2): subtask")
	runIn(originDir, "checkout", "main")

	// ----- Bob's repo: init, add remote, fetch, check out only the subtask -----
	runIn(bobDir, "init", "-q", "-b", "main")
	runIn(bobDir, "config", "user.name", "Bob")
	runIn(bobDir, "config", "user.email", "bob@example.com")
	runIn(bobDir, "config", "commit.gpgsign", "false")
	runIn(bobDir, "remote", "add", "origin", originDir)
	runIn(bobDir, "fetch", "origin")
	// DWIM: creates local main tracking origin/main.
	runIn(bobDir, "checkout", "main")
	// DWIM: creates local X.2@feat@two tracking origin/X.2@feat@two.
	runIn(bobDir, "checkout", "-b", "X.2@feat@two", "origin/X.2@feat@two")
	// Return to main; X@feat@big is intentionally NOT checked out locally.
	runIn(bobDir, "checkout", "main")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	ioStreams := &pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stderr}

	client, err := git.NewClientAt(ioStreams, bobDir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}

	// Write branch refs locally so parentSlug resolves without a remote fetch.
	parentBR := git.BranchRef{
		IssueSlug:  "X",
		BranchName: "X@feat@big",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := client.WriteBranchRef(t.Context(), "X", parentBR); err != nil {
		t.Fatalf("WriteBranchRef X: %v", err)
	}
	childBR := git.BranchRef{
		IssueSlug:  "X.2",
		BranchName: "X.2@feat@two",
		ParentSlug: "X",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := client.WriteBranchRef(t.Context(), "X.2", childBR); err != nil {
		t.Fatalf("WriteBranchRef X.2: %v", err)
	}

	s, err := store.Open(t.Context(), bobDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "X.2", Title: "part-two"},
		&store.Branch{Name: "X.2@feat@two", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed branch: %v", err)
	}

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"
	deps := closeDeps{client: client, store: s, cfg: cfg}

	pickedRow := &store.BranchRow{
		IssueID:    1,
		IssueSlug:  "X.2",
		Title:      "part-two",
		BranchName: "X.2@feat@two",
		Type:       "feat",
		Status:     store.BranchStatusInProgress,
	}

	prompter := &scriptedPrompter{
		Branch:       pickedRow,
		Strategy:     commitpkg.MergeStrategySquash,
		Confirm:      true,
		Message:      []byte("feat(X.2): close\n"),
		DeleteBranch: true,
	}

	if err := runClose(t.Context(), deps, prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("squash commit landed on parent integration branch", func(t *testing.T) {
		// X@feat@big is created locally by git checkout DWIM during MergeSquash.
		assertHeadSubject(t, bobDir, "X@feat@big", "feat(X.2): close")
	})

	t.Run("main is unchanged", func(t *testing.T) {
		assertHeadSubject(t, bobDir, "main", "chore: init")
	})
}

// TestClose_CrossMachine_UsesParentBranchRef verifies that close correctly
// merges a sub-task into its parent integration branch even when the local
// SQLite store has no parent-child relation record (cross-machine scenario:
// a developer who fetched and checked out the branch without running issue start).
func TestClose_CrossMachine_UsesParentBranchRef(t *testing.T) {
	t.Parallel()

	// Build a repo that looks like Bob's machine:
	//   - main branch with one commit
	//   - X@feat@big-feature (parent integration branch), one commit ahead of main
	//   - X.1@feat@part-one (sub-task), one commit ahead of X@feat@big-feature
	//   - refs/zf/branches/X    → {branch_name: "X@feat@big-feature"}
	//   - refs/zf/branches/X.1  → {branch_name: "X.1@feat@part-one", parent_slug: "X"}
	//   - SQLite store: only X.1 branch row (no issue_relations record)

	dir := t.TempDir()

	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runGit("init", "-q", "-b", "main")
	runGit("config", "user.name", "Bob")
	runGit("config", "user.email", "bob@example.com")
	runGit("config", "commit.gpgsign", "false")

	// main commit
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}
	runGit("add", "base.txt")
	runGit("commit", "-m", "chore: init")

	// X@feat@big-feature
	runGit("checkout", "-b", "X@feat@big-feature")
	if err := os.WriteFile(filepath.Join(dir, "parent.txt"), []byte("parent\n"), 0o644); err != nil {
		t.Fatalf("write parent.txt: %v", err)
	}
	runGit("add", "parent.txt")
	runGit("commit", "-m", "feat(X): parent commit")

	// X.1@feat@part-one (branched from X@feat@big-feature)
	runGit("checkout", "-b", "X.1@feat@part-one")
	if err := os.WriteFile(filepath.Join(dir, "subtask.txt"), []byte("subtask\n"), 0o644); err != nil {
		t.Fatalf("write subtask.txt: %v", err)
	}
	runGit("add", "subtask.txt")
	runGit("commit", "-m", "feat(X.1): subtask commit")

	runGit("checkout", "main")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	ioStreams := &pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stderr}

	client, err := git.NewClientAt(ioStreams, dir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}

	// Write branch refs (as Alice would have pushed them; Bob fetched them).
	parentRef := git.BranchRef{
		IssueSlug:  "X",
		BranchName: "X@feat@big-feature",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := client.WriteBranchRef(t.Context(), "X", parentRef); err != nil {
		t.Fatalf("WriteBranchRef X: %v", err)
	}

	childRef := git.BranchRef{
		IssueSlug:  "X.1",
		BranchName: "X.1@feat@part-one",
		ParentSlug: "X",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := client.WriteBranchRef(t.Context(), "X.1", childRef); err != nil {
		t.Fatalf("WriteBranchRef X.1: %v", err)
	}

	// Store: only X.1 branch row; no issue_relations.
	s, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "X.1", Title: "part-one"},
		&store.Branch{Name: "X.1@feat@part-one", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed branch: %v", err)
	}

	// Seed review ref so reviewPreflight passes (approved, no reviewer commits).
	reviewRef := git.ReviewRef{
		Status: "approved",
		Round:  1,
		// FeatureSHA is not checked by reviewPreflight for approved status
		// when no X.1@review branch exists.
		FeatureSHA: "ignored",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := client.WriteReviewRef(t.Context(), "X.1", reviewRef, ""); err != nil {
		t.Fatalf("WriteReviewRef: %v", err)
	}

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"

	deps := closeDeps{client: client, store: s, cfg: cfg}

	pickedRow := &store.BranchRow{
		IssueID:    1,
		IssueSlug:  "X.1",
		Title:      "part-one",
		BranchName: "X.1@feat@part-one",
		Type:       "feat",
		Status:     store.BranchStatusInProgress,
	}

	prompter := &scriptedPrompter{
		Branch:       pickedRow,
		Strategy:     commitpkg.MergeStrategySquash,
		Confirm:      true,
		Message:      []byte("feat(X.1): close\n"),
		DeleteBranch: true,
	}

	if err := runClose(t.Context(), deps, prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("merged into parent branch not main", func(t *testing.T) {
		// X@feat@big-feature should carry the squash commit; main should not.
		cmd := exec.CommandContext(t.Context(), "git", "log", "-1", "--format=%s", "X@feat@big-feature")
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git log X@feat@big-feature: %v", err)
		}
		got := strings.TrimSpace(string(out))
		if got != "feat(X.1): close" {
			t.Errorf("X@feat@big-feature HEAD subject = %q, want %q", got, "feat(X.1): close")
		}
	})

	t.Run("main is unchanged", func(t *testing.T) {
		cmd := exec.CommandContext(t.Context(), "git", "log", "-1", "--format=%s", "main")
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git log main: %v", err)
		}
		got := strings.TrimSpace(string(out))
		if got != "chore: init" {
			t.Errorf("main HEAD = %q, expected it to be unchanged (%q)", got, "chore: init")
		}
	})
}

// TestClose_ParentClose_ReconcilesMergedChildFromRef covers Phase 12 of the
// demo: Alice tries to close parent X, but her local store still has X.2 as
// in_progress (Bob closed it in his clone). The branch ref for X.2 has
// Merged=true (written by Bob's close). The reconcile getPickedBranch runs
// (issueflow.ReconcileMergedFromRefs) should update Alice's store so
// ChildrenAllMerged passes.
func TestClose_ParentClose_ReconcilesMergedChildFromRef(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t) // main + ABC-1@feat@add-thing

	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = rig.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Rename the seeded branch to simulate the parent integration branch X.
	// Create child X.1 already merged, and child X.2 in_progress (sibling's close
	// not yet reflected in this store).
	runGit("checkout", "-b", "X@feat@big")
	if err := os.WriteFile(filepath.Join(rig.dir, "parent.txt"), []byte("parent\n"), 0o644); err != nil {
		t.Fatalf("write parent.txt: %v", err)
	}
	runGit("add", "parent.txt")
	runGit("commit", "-m", "feat(X): parent")
	runGit("checkout", "main")

	// Re-seed store: parent X, and two children X.1 (merged) and X.2 (in_progress).
	trackerType := "fake"
	if err := rig.store.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "X", Title: "big", TrackerType: &trackerType},
		&store.Branch{Name: "X@feat@big", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed X: %v", err)
	}
	now := time.Now()
	if err := rig.store.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "X.1", Title: "one"},
		&store.Branch{Name: "X.1@feat@one", Type: "feat", StatusID: store.StatusIDMerged, MergedAt: &now},
	); err != nil {
		t.Fatalf("seed X.1: %v", err)
	}
	if err := rig.store.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "X.2", Title: "two"},
		&store.Branch{Name: "X.2@feat@two", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed X.2: %v", err)
	}
	// Record parent-child relations.
	if err := rig.store.InsertIssueRelation(t.Context(), "X", "X.1"); err != nil {
		t.Fatalf("relation X→X.1: %v", err)
	}
	if err := rig.store.InsertIssueRelation(t.Context(), "X", "X.2"); err != nil {
		t.Fatalf("relation X→X.2: %v", err)
	}

	// Simulate Bob having closed X.2 in his clone: write a branch ref with Merged=true.
	mergedRef := git.BranchRef{
		IssueSlug:  "X.2",
		BranchName: "X.2@feat@two",
		ParentSlug: "X",
		CreatedAt:  now.UTC().Format(time.RFC3339),
		Merged:     true,
	}
	if _, err := rig.client.WriteBranchRef(t.Context(), "X.2", mergedRef); err != nil {
		t.Fatalf("WriteBranchRef X.2: %v", err)
	}

	// Update the store config: X@feat@big should merge into main.
	rig.cfg.Branch.Base = "main"

	xRow := &store.BranchRow{
		IssueID:    2, // second InsertIssueWithBranch call
		IssueSlug:  "X",
		Title:      "big",
		BranchName: "X@feat@big",
		Type:       "feat",
		Status:     store.BranchStatusInProgress,
	}

	prompter := &scriptedPrompter{
		Branch:        xRow,
		Strategy:      commitpkg.MergeStrategySquash,
		Confirm:       true,
		Message:       []byte("feat(X): close into main\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  false,
	}

	if err := runClose(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("parent X merged into main", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "feat(X): close into main")
	})

	t.Run("X.2 reconciled as merged in store", func(t *testing.T) {
		merged, err := rig.store.ListBranches(t.Context(), store.BranchStatusMerged)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}
		var found bool
		for _, b := range merged {
			if b.BranchName == "X.2@feat@two" {
				found = true
			}
		}
		if !found {
			t.Error("X.2@feat@two not reconciled as merged in store")
		}
	})
}

func TestClose_BaseOverrideMergesIntoChosenBranch(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)
	rig.addBranch(t, "release-1.0")

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      commitpkg.MergeStrategyClassic,
		Confirm:       true,
		Message:       []byte("Merge ABC-1 into release-1.0\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  false,
	}

	deps := rig.deps()
	deps.baseOverride = "release-1.0"

	if err := runClose(t.Context(), deps, prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("release-1.0 HEAD carries the merge commit", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "release-1.0", "Merge ABC-1 into release-1.0")
	})

	t.Run("picker is bypassed by the flag", func(t *testing.T) {
		if prompter.BaseCalled {
			t.Error("PickBaseBranch was called; --base must bypass the picker")
		}
	})

	t.Run("main is untouched", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "chore: init")
	})
}

func TestClose_BaseOverrideRejectsUnknownBranch(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	prompter := &scriptedPrompter{
		Branch:  rig.pickedBranchRow(),
		Confirm: true,
	}

	deps := rig.deps()
	deps.baseOverride = "no-such-branch"

	err := runClose(t.Context(), deps, prompter)

	t.Run("returns an error", func(t *testing.T) {
		if err == nil {
			t.Fatal("expected error for unknown --base branch, got nil")
		}
	})

	t.Run("error names the bad branch", func(t *testing.T) {
		if err != nil && !strings.Contains(err.Error(), "no-such-branch") {
			t.Errorf("error %q does not mention the bad branch", err)
		}
	})

	t.Run("main is untouched", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "chore: init")
	})
}

func TestClose_PickerOffersMergeTargetWhenMultipleCandidates(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)
	rig.addBranch(t, "release-1.0")

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      commitpkg.MergeStrategyClassic,
		Confirm:       true,
		Base:          "release-1.0",
		Message:       []byte("Merge ABC-1 into release-1.0\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  false,
	}

	if err := runClose(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("picker was shown", func(t *testing.T) {
		if !prompter.BaseCalled {
			t.Error("PickBaseBranch was not called despite >1 candidate")
		}
	})

	t.Run("merge landed on the picked branch", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "release-1.0", "Merge ABC-1 into release-1.0")
	})
}

func TestClose_PickerSkippedWhenSingleCandidate(t *testing.T) {
	t.Parallel()

	// Default rig has main + the feature branch; the feature branch is excluded
	// as a target, leaving only main => single candidate => no picker.
	rig := newCloseRig(t)

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      commitpkg.MergeStrategyClassic,
		Confirm:       true,
		Base:          "should-not-be-used",
		Message:       []byte("Merge ABC-1 into main\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  false,
	}

	if err := runClose(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("picker was not shown", func(t *testing.T) {
		if prompter.BaseCalled {
			t.Error("PickBaseBranch was called with only one candidate")
		}
	})

	t.Run("merge landed on the smart default (main)", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "Merge ABC-1 into main")
	})
}

func TestClose_BaseOverrideRejectsClosingBranch(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	prompter := &scriptedPrompter{
		Branch:  rig.pickedBranchRow(),
		Confirm: true,
	}

	deps := rig.deps()
	deps.baseOverride = "ABC-1@feat@add-thing" // the branch being closed

	err := runClose(t.Context(), deps, prompter)

	t.Run("returns an error", func(t *testing.T) {
		if err == nil {
			t.Fatal("expected error when --base is the branch being closed, got nil")
		}
	})

	t.Run("error explains it is the closing branch", func(t *testing.T) {
		if err != nil && !strings.Contains(err.Error(), "being closed") {
			t.Errorf("error %q should explain the target is the branch being closed", err)
		}
	})

	t.Run("main is untouched", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "chore: init")
	})
}

func TestCloseCmd_RegistersBaseFlag(t *testing.T) {
	t.Parallel()

	cmd := New(&config.AppConfig{}).getCloseCmd()

	t.Run("--base flag is registered", func(t *testing.T) {
		if cmd.Flags().Lookup("base") == nil {
			t.Fatal("expected --base flag on `issue close`")
		}
	})

	t.Run("--base flag defaults to empty", func(t *testing.T) {
		f := cmd.Flags().Lookup("base")
		if f == nil {
			t.Fatal("expected --base flag on `issue close`")
		}
		if f.DefValue != "" {
			t.Errorf("--base DefValue = %q, want empty (empty preserves smart-default + picker)", f.DefValue)
		}
	})
}

func TestGetPickedBranch_ExcludesBranchMergedInSiblingClone(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	// Simulate a sibling clone having closed DEF-2: this clone's store still
	// shows it in_progress, but its branch ref carries Merged=true — the
	// cross-machine source of truth that updateClosedStatus writes + pushes on
	// close. The picker must not offer it.
	trackerType := "fake"
	if err := rig.store.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "DEF-2", Title: "two", TrackerType: &trackerType},
		&store.Branch{Name: "DEF-2@feat@two", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed DEF-2: %v", err)
	}
	if _, err := rig.client.WriteBranchRef(t.Context(), "DEF-2", git.BranchRef{
		IssueSlug:   "DEF-2",
		BranchName:  "DEF-2@feat@two",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		TrackerType: "fake",
		Merged:      true,
	}); err != nil {
		t.Fatalf("seed DEF-2 merged ref: %v", err)
	}

	prompter := &scriptedPrompter{Branch: rig.pickedBranchRow()}

	invoked := invokedBranchFor(t.Context(), rig.client, "")
	if _, err := getPickedBranch(t.Context(), rig.store, rig.client, invoked, prompter); err != nil {
		t.Fatalf("getPickedBranch: %v", err)
	}

	t.Run("picker is not offered the sibling-merged branch", func(t *testing.T) {
		for _, b := range prompter.PickBranchSeen {
			if b.IssueSlug == "DEF-2" {
				t.Errorf("picker was offered DEF-2 (merged in a sibling clone); offered: %+v", prompter.PickBranchSeen)
			}
		}
	})

	t.Run("the still-open branch is still offered", func(t *testing.T) {
		found := false
		for _, b := range prompter.PickBranchSeen {
			if b.IssueSlug == "ABC-1" {
				found = true
			}
		}
		if !found {
			t.Errorf("picker should still offer the open ABC-1; offered: %+v", prompter.PickBranchSeen)
		}
	})

	t.Run("store reconciles the sibling-merged branch to merged", func(t *testing.T) {
		merged, err := rig.store.ListBranches(t.Context(), store.BranchStatusMerged)
		if err != nil {
			t.Fatalf("ListBranches merged: %v", err)
		}
		found := false
		for _, b := range merged {
			if b.BranchName == "DEF-2@feat@two" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected DEF-2@feat@two reconciled to merged in store; merged rows: %+v", merged)
		}
	})
}

// revParseInDir runs `git rev-parse <ref>` in dir and returns the trimmed SHA.
func revParseInDir(t *testing.T, dir, ref string) string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", "-C", dir, "rev-parse", ref).Output()
	if err != nil {
		t.Fatalf("git rev-parse %s in %s: %v", ref, dir, err)
	}

	return strings.TrimSpace(string(out))
}

// newCloseOriginRig builds a bare origin + a clone with a seeded in-progress
// branch one commit ahead of main, mirroring the rig pattern used by
// TestClose_SubtaskDryRunFallsBackToRemoteBase. Returns (deps, cloneDir, originDir, baseBranchName).
func newCloseOriginRig(t *testing.T) (closeDeps, string, string, string) {
	t.Helper()

	originDir := t.TempDir()
	cloneDir := t.TempDir()

	runIn := func(dir string, args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}

	// Build a non-bare seed repo, then push to a bare origin.
	seedDir := t.TempDir()
	runIn(seedDir, "init", "-q", "--initial-branch=main")
	runIn(seedDir, "config", "user.name", "Seed")
	runIn(seedDir, "config", "user.email", "seed@example.com")
	runIn(seedDir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(seedDir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}
	runIn(seedDir, "add", "base.txt")
	runIn(seedDir, "commit", "-m", "chore: init")

	// Bare origin.
	runIn(originDir, "init", "-q", "--bare", "--initial-branch=main")
	runIn(seedDir, "remote", "add", "origin", originDir)
	runIn(seedDir, "push", "origin", "main")

	// Clone from origin.
	runIn(cloneDir, "init", "-q", "--initial-branch=main")
	runIn(cloneDir, "config", "user.name", "Dev")
	runIn(cloneDir, "config", "user.email", "dev@example.com")
	runIn(cloneDir, "config", "commit.gpgsign", "false")
	runIn(cloneDir, "remote", "add", "origin", originDir)
	runIn(cloneDir, "fetch", "origin")
	runIn(cloneDir, "checkout", "-b", "main", "--track", "origin/main")

	// Feature branch one commit ahead of main.
	runIn(cloneDir, "checkout", "-b", "ABC-1@feat@push-test")
	if err := os.WriteFile(filepath.Join(cloneDir, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	runIn(cloneDir, "add", "feature.txt")
	runIn(cloneDir, "commit", "-m", "feat: implement push test")
	runIn(cloneDir, "checkout", "main")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	ioStreams := &pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stderr}

	client, err := git.NewClientAt(ioStreams, cloneDir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}

	s, err := store.Open(t.Context(), cloneDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Seed the store and a tracker-born branch ref.
	trackerType := "fake"
	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "ABC-1", Title: "Push test", TrackerType: &trackerType},
		&store.Branch{Name: "ABC-1@feat@push-test", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed branch: %v", err)
	}
	if _, err := client.WriteBranchRef(t.Context(), "ABC-1", git.BranchRef{
		IssueSlug:   "ABC-1",
		BranchName:  "ABC-1@feat@push-test",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		TrackerType: "fake",
	}); err != nil {
		t.Fatalf("seed branch ref: %v", err)
	}

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"
	cfg.IssueTracker.Type = "fake"

	deps := closeDeps{client: client, store: s, cfg: cfg}

	return deps, cloneDir, originDir, "main"
}

// TestClose_ProposesPushOfMergeTarget verifies that after a successful close,
// proposeClosePush pushes the merge target (base) to origin when pushConfirm
// returns true and cfg.Push.Propose is true.
func TestClose_ProposesPushOfMergeTarget(t *testing.T) {
	t.Parallel()

	deps, cloneDir, originDir, base := newCloseOriginRig(t)
	deps.cfg.Push.Propose = true
	deps.pushConfirm = func(_ context.Context, _ string) (bool, error) { return true, nil }

	pickedRow := &store.BranchRow{
		IssueID:    1,
		IssueSlug:  "ABC-1",
		Title:      "Push test",
		BranchName: "ABC-1@feat@push-test",
		Type:       "feat",
		Status:     store.BranchStatusInProgress,
	}
	prompter := &scriptedPrompter{
		Branch:        pickedRow,
		Strategy:      commitpkg.MergeStrategyRebase,
		Confirm:       true,
		Message:       []byte("feat(push-test): close ABC-1\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  false,
	}

	if err := runClose(t.Context(), deps, prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("origin main advanced to local main", func(t *testing.T) {
		local := revParseInDir(t, cloneDir, "refs/heads/"+base)
		remote := revParseInDir(t, originDir, "refs/heads/"+base)
		if local != remote {
			t.Fatalf("origin %s = %s, want local tip %s", base, remote, local)
		}
	})
}

// TestClose_ReviewPreflight_DivergedCleanAutoMerges verifies that when the
// feature branch has moved on since the review branch was created (diverged,
// not a simple fast-forward), reviewPreflight falls back to a real merge
// (MergeForward) as long as the merge is conflict-free, incorporating the
// reviewer's commits without developer intervention.
func TestClose_ReviewPreflight_DivergedCleanAutoMerges(t *testing.T) {
	rig := newCloseRig(t)
	slug := rig.pickedBranchRow().IssueSlug
	feature := rig.pickedBranchRow().BranchName

	// Reviewer branch with a commit touching a reviewer-only file.
	mustRunGitAt(t, rig.dir, "checkout", "-b", slug+"@review", feature)
	writeFileAt(t, rig.dir, "reviewer-only.txt", "r\n")
	mustRunGitAt(t, rig.dir, "add", "reviewer-only.txt")
	mustRunGitAt(t, rig.dir, "commit", "-m", "fix: reviewer nit")
	// Developer continues on the feature branch (non-conflicting file) → diverged.
	mustRunGitAt(t, rig.dir, "checkout", feature)
	writeFileAt(t, rig.dir, "dev-later.txt", "d\n")
	mustRunGitAt(t, rig.dir, "add", "dev-later.txt")
	mustRunGitAt(t, rig.dir, "commit", "-m", "feat: more work")

	seedReviewRef(t, rig, slug, store.ReviewStatusApproved, 1)

	cleanup, err := reviewPreflight(t.Context(), rig.deps(), rig.pickedBranchRow(), nil)

	t.Run("preflight succeeds via real merge", func(t *testing.T) {
		if err != nil {
			t.Fatalf("reviewPreflight: %v", err)
		}
	})
	t.Run("reviewer commit incorporated", func(t *testing.T) {
		n, cErr := rig.client.CommitsAhead(t.Context(), slug+"@review", feature)
		// branch may already be deleted by cleanup; only check when it survives
		if cErr == nil && n != 0 {
			t.Fatalf("want 0 pending, got %d", n)
		}
	})
	t.Run("cleanup deferred: review ref survives until the merge lands", func(t *testing.T) {
		ref, _, _ := rig.client.ReadReviewRef(t.Context(), slug)
		if ref == nil {
			t.Fatal("review ref must survive until the returned cleanup runs")
		}
	})

	if cleanup == nil {
		t.Fatal("reviewPreflight returned nil cleanup for an approved review")
	}
	cleanup(t.Context())

	t.Run("review ref cleaned up", func(t *testing.T) {
		ref, _, _ := rig.client.ReadReviewRef(t.Context(), slug)
		if ref != nil {
			t.Fatalf("want review ref deleted, got %+v", ref)
		}
	})
}

// TestClose_ReviewPreflight_DivergedConflictRefusesWithSyncHint verifies that
// when the reviewer's commits conflict with the developer's own commits on
// the feature branch, reviewPreflight refuses to close: it never leaves
// MERGE_HEAD behind, it wraps ErrReviewSyncNeeded, and the review ref survives
// so a subsequent `git zf review sync` can pick up where this left off.
func TestClose_ReviewPreflight_DivergedConflictRefusesWithSyncHint(t *testing.T) {
	rig := newCloseRig(t)
	slug := rig.pickedBranchRow().IssueSlug
	feature := rig.pickedBranchRow().BranchName

	// Reviewer and developer edit the same file differently → conflict.
	mustRunGitAt(t, rig.dir, "checkout", "-b", slug+"@review", feature)
	writeFileAt(t, rig.dir, "clash.txt", "reviewer\n")
	mustRunGitAt(t, rig.dir, "add", "clash.txt")
	mustRunGitAt(t, rig.dir, "commit", "-m", "fix: reviewer version")
	mustRunGitAt(t, rig.dir, "checkout", feature)
	writeFileAt(t, rig.dir, "clash.txt", "developer\n")
	mustRunGitAt(t, rig.dir, "add", "clash.txt")
	mustRunGitAt(t, rig.dir, "commit", "-m", "feat: developer version")

	seedReviewRef(t, rig, slug, store.ReviewStatusApproved, 1)

	_, err := reviewPreflight(t.Context(), rig.deps(), rig.pickedBranchRow(), nil)

	t.Run("refused with ErrReviewSyncNeeded", func(t *testing.T) {
		if !errors.Is(err, ErrReviewSyncNeeded) {
			t.Fatalf("want ErrReviewSyncNeeded, got %v", err)
		}
	})
	t.Run("sync hint present", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "git zf review sync") {
			t.Fatalf("want sync hint, got %v", err)
		}
	})
	t.Run("repo left clean (no MERGE_HEAD)", func(t *testing.T) {
		inProgress, _ := rig.client.MergeInProgress()
		if inProgress {
			t.Fatal("close must never leave a merge in progress")
		}
	})
	t.Run("review ref NOT cleaned up", func(t *testing.T) {
		ref, _, _ := rig.client.ReadReviewRef(t.Context(), slug)
		if ref == nil {
			t.Fatal("review ref must survive a refused close")
		}
	})
}

// reviewerCloseRig is the shared fixture for reviewer-initiated close tests: a
// bare origin seeded by a developer (main + pushed ABC-1@feat@thing + published
// refs/zf/branches/ABC-1, manual issue with no tracker origin), plus Carol's
// clone with an empty store and no local feature branch.
type reviewerCloseRig struct {
	deps   closeDeps
	client *git.Client
	store  *store.Store
	dir    string
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

// refDerivedRow returns the candidate row the close picker synthesizes from
// refs/zf/branches/ABC-1 — IssueID 0 is the "not yet tracked" marker.
func (r *reviewerCloseRig) refDerivedRow() *store.BranchRow {
	return &store.BranchRow{
		IssueID: 0, IssueSlug: "ABC-1", Title: "thing",
		BranchName: "ABC-1@feat@thing", Type: "feat", Status: store.BranchStatusInProgress,
	}
}

func newReviewerCloseRig(t *testing.T) *reviewerCloseRig {
	t.Helper()

	originDir := filepath.Join(t.TempDir(), "origin.git")
	carolDir := t.TempDir()

	runIn := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}

	// ----- origin + seed (developer) -----
	if err := os.MkdirAll(originDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runIn(originDir, "init", "--bare", "--initial-branch=main")

	seedDir := t.TempDir()
	runIn(seedDir, "init", "--initial-branch=main")
	runIn(seedDir, "config", "user.name", "Seed")
	runIn(seedDir, "config", "user.email", "seed@example.com")
	runIn(seedDir, "config", "commit.gpgsign", "false")
	writeFileAt(t, seedDir, "base.txt", "base\n")
	runIn(seedDir, "add", "base.txt")
	runIn(seedDir, "commit", "-m", "chore: init")
	runIn(seedDir, "remote", "add", "origin", originDir)
	runIn(seedDir, "push", "origin", "main")

	runIn(seedDir, "checkout", "-b", "ABC-1@feat@thing")
	writeFileAt(t, seedDir, "feature.txt", "feature\n")
	runIn(seedDir, "add", "feature.txt")
	runIn(seedDir, "commit", "-m", "feat: implement")
	runIn(seedDir, "push", "origin", "ABC-1@feat@thing")
	runIn(seedDir, "checkout", "main")

	// Developer publishes the branch ref (manual issue: no tracker origin).
	seedClient, err := git.NewClientAt(nil, seedDir)
	if err != nil {
		t.Fatalf("seed NewClientAt: %v", err)
	}
	if _, err := seedClient.WriteBranchRef(t.Context(), "ABC-1", git.BranchRef{
		IssueSlug: "ABC-1", BranchName: "ABC-1@feat@thing",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("seed WriteBranchRef: %v", err)
	}
	runIn(seedDir, "push", "origin", "refs/zf/branches/ABC-1")

	// ----- Carol's clone: empty store, no local feature branch -----
	runIn(carolDir, "init", "--initial-branch=main")
	runIn(carolDir, "config", "user.name", "Carol")
	runIn(carolDir, "config", "user.email", "carol@example.com")
	runIn(carolDir, "config", "commit.gpgsign", "false")
	runIn(carolDir, "remote", "add", "origin", originDir)
	runIn(carolDir, "fetch", "origin")
	runIn(carolDir, "checkout", "main")
	// ABC-1@feat@thing deliberately NOT checked out locally.

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	carolClient, err := git.NewClientAt(&pkg.IO{
		In: bytes.NewReader(nil), Out: stdout, Err: stderr,
	}, carolDir)
	if err != nil {
		t.Fatalf("carol NewClientAt: %v", err)
	}

	carolStore, err := store.Open(t.Context(), filepath.Join(carolDir, ".git"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = carolStore.Close() })

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"

	return &reviewerCloseRig{
		deps:   closeDeps{client: carolClient, store: carolStore, cfg: cfg},
		client: carolClient,
		store:  carolStore,
		dir:    carolDir,
		stdout: stdout,
		stderr: stderr,
	}
}

// TestClose_ReviewerInitiated verifies a reviewer/teammate can close an issue
// they did not start: an empty local store, the feature branch present only as
// origin/<feature>, and only refs/zf/branches/<slug> to go on. The close flow
// must surface the ref-derived candidate, materialize the feature branch,
// merge it, track it once the merge commit lands, and stamp the ref
// merged=true.
func TestClose_ReviewerInitiated(t *testing.T) {
	t.Parallel()

	rig := newReviewerCloseRig(t)

	// The picker returns the ref-derived candidate (IssueID 0). runClose must
	// materialize the feature branch before merging (MaterializeBranch) and
	// promote it into store rows after the merge commits (TrackCandidate).
	prompter := &scriptedPrompter{
		Branch:       rig.refDerivedRow(),
		Strategy:     commitpkg.MergeStrategySquash,
		Confirm:      true,
		Message:      []byte("feat(thing): reviewer closes ABC-1\n"),
		DeleteBranch: false,
	}

	if err := runClose(t.Context(), rig.deps, prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("picker was offered the ref-derived candidate", func(t *testing.T) {
		var found bool
		for _, b := range prompter.PickBranchSeen {
			if b.IssueSlug == "ABC-1" && b.BranchName == "ABC-1@feat@thing" && b.IssueID == 0 {
				found = true
			}
		}
		if !found {
			t.Errorf("PickBranchSeen = %+v, want an ABC-1 ref-derived (IssueID 0) row", prompter.PickBranchSeen)
		}
	})

	t.Run("feature branch materialized locally", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-1@feat@thing")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if !exists {
			t.Errorf("expected local ABC-1@feat@thing to be materialized")
		}
	})

	t.Run("main HEAD carries the close commit", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "feat(thing): reviewer closes ABC-1")
	})

	t.Run("store now tracks ABC-1 as merged", func(t *testing.T) {
		merged, err := rig.store.ListBranches(t.Context(), store.BranchStatusMerged)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}
		if len(merged) != 1 || merged[0].BranchName != "ABC-1@feat@thing" {
			t.Errorf("merged rows = %+v, want one ABC-1@feat@thing", merged)
		}
	})

	t.Run("branch ref stamped merged=true", func(t *testing.T) {
		ref, err := rig.client.ReadBranchRef(t.Context(), "ABC-1")
		if err != nil {
			t.Fatalf("ReadBranchRef: %v", err)
		}
		if ref == nil || !ref.Merged {
			t.Errorf("branch ref = %+v, want Merged=true", ref)
		}
	})
}

// TestClose_ReviewerInitiated_AbortRollsBack verifies the "aborts without
// touching anything" invariant for the reviewer path: when the close stops
// before the merge commit lands (here: the reviewer declines at the confirm
// prompt), the materialized feature branch is removed again and no store rows
// are inserted — the clone ends up exactly as the close found it.
func TestClose_ReviewerInitiated_AbortRollsBack(t *testing.T) {
	t.Parallel()

	rig := newReviewerCloseRig(t)

	prompter := &scriptedPrompter{
		Branch:   rig.refDerivedRow(),
		Strategy: commitpkg.MergeStrategySquash,
		Confirm:  false, // decline at the merge confirm prompt
	}

	if err := runClose(t.Context(), rig.deps, prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("stdout shows the abort message", func(t *testing.T) {
		if got := rig.stdout.String(); !strings.Contains(got, "Aborted.") {
			t.Errorf("stdout = %q, want it to contain 'Aborted.'", got)
		}
	})

	t.Run("materialized feature branch rolled back", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-1@feat@thing")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if exists {
			t.Errorf("local ABC-1@feat@thing still exists, want it rolled back")
		}
	})

	t.Run("rollback reported on stderr", func(t *testing.T) {
		if got := rig.stderr.String(); !strings.Contains(got, "Rolled back: materialized branch") {
			t.Errorf("stderr = %q, want it to report the branch rollback", got)
		}
	})

	t.Run("no store rows inserted", func(t *testing.T) {
		rows, err := rig.store.ListBranches(t.Context(), store.BranchStatusAll)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("store rows = %+v, want none after an aborted close", rows)
		}
	})

	t.Run("branch ref not stamped merged", func(t *testing.T) {
		ref, err := rig.client.ReadBranchRef(t.Context(), "ABC-1")
		if err != nil {
			t.Fatalf("ReadBranchRef: %v", err)
		}
		if ref == nil || ref.Merged {
			t.Errorf("branch ref = %+v, want Merged=false (close aborted)", ref)
		}
	})
}

// TestClose_ReviewerInitiated_AbortAfterCheckoutRollsBack covers the abort
// paths that fail AFTER a strategy has checked out the materialized branch
// (rebase/classic preflight, Esc in the commit form): HEAD is on the branch
// the rollback must delete, so the rollback has to switch back to base first.
// The failure is injected as a ComposeMessage error inside the Rebase
// strategy — by then rebasePreflight has checked out ABC-1@feat@thing.
func TestClose_ReviewerInitiated_AbortAfterCheckoutRollsBack(t *testing.T) {
	t.Parallel()

	rig := newReviewerCloseRig(t)

	composeErr := errors.New("compose aborted")
	prompter := &scriptedPrompter{
		Branch:     rig.refDerivedRow(),
		Strategy:   commitpkg.MergeStrategyRebase,
		Confirm:    true,
		MessageErr: composeErr, // fails inside the strategy, HEAD on the feature branch
	}

	err := runClose(t.Context(), rig.deps, prompter)

	t.Run("runClose surfaces the compose error", func(t *testing.T) {
		if !errors.Is(err, composeErr) {
			t.Fatalf("runClose error = %v, want it to wrap the compose error", err)
		}
	})

	t.Run("HEAD restored to base", func(t *testing.T) {
		cur, curErr := rig.client.CurrentBranch()
		if curErr != nil {
			t.Fatalf("CurrentBranch: %v", curErr)
		}
		if cur != "main" {
			t.Errorf("HEAD on %q, want main after rollback", cur)
		}
	})

	t.Run("materialized feature branch rolled back", func(t *testing.T) {
		assertBranchAbsent(t, rig.client, "ABC-1@feat@thing")
	})

	t.Run("rollback reported on stderr", func(t *testing.T) {
		if got := rig.stderr.String(); !strings.Contains(got, "Rolled back: materialized branch") {
			t.Errorf("stderr = %q, want it to report the branch rollback", got)
		}
	})

	t.Run("no store rows inserted", func(t *testing.T) {
		rows, listErr := rig.store.ListBranches(t.Context(), store.BranchStatusAll)
		if listErr != nil {
			t.Fatalf("ListBranches: %v", listErr)
		}
		if len(rows) != 0 {
			t.Errorf("store rows = %+v, want none after an aborted close", rows)
		}
	})
}

// TestClose_ReviewerInitiated_SquashComposeAbortRestoresCleanTree covers the
// squash variant of the compose-abort: `git merge --squash` has already staged
// the merge onto base when the commit form is cancelled. For a locally-started
// branch that staged diff is deliberately kept for inspection, but for a
// materialized pick the rollback must discard it too — branch and diff are both
// reproducible from origin, and the clone must end exactly as the close found
// it.
func TestClose_ReviewerInitiated_SquashComposeAbortRestoresCleanTree(t *testing.T) {
	t.Parallel()

	rig := newReviewerCloseRig(t)

	composeErr := errors.New("compose aborted")
	prompter := &scriptedPrompter{
		Branch:     rig.refDerivedRow(),
		Strategy:   commitpkg.MergeStrategySquash,
		Confirm:    true,
		MessageErr: composeErr, // Esc in the commit form, squash diff already staged
	}

	err := runClose(t.Context(), rig.deps, prompter)

	t.Run("runClose surfaces the compose error", func(t *testing.T) {
		if !errors.Is(err, composeErr) {
			t.Fatalf("runClose error = %v, want it to wrap the compose error", err)
		}
	})

	t.Run("working tree left clean (staged squash discarded)", func(t *testing.T) {
		out, sErr := exec.CommandContext(t.Context(), "git", "-C", rig.dir,
			"status", "--porcelain").Output()
		if sErr != nil {
			t.Fatalf("git status: %v", sErr)
		}
		if len(bytes.TrimSpace(out)) != 0 {
			t.Errorf("git status --porcelain = %q, want empty after rollback", out)
		}
	})

	t.Run("base tip unchanged", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "chore: init")
	})

	t.Run("materialized feature branch rolled back", func(t *testing.T) {
		assertBranchAbsent(t, rig.client, "ABC-1@feat@thing")
	})

	t.Run("both rollbacks reported on stderr", func(t *testing.T) {
		got := rig.stderr.String()
		if !strings.Contains(got, "staged squash changes") {
			t.Errorf("stderr = %q, want it to report the discarded squash changes", got)
		}
		if !strings.Contains(got, "Rolled back: materialized branch") {
			t.Errorf("stderr = %q, want it to report the branch rollback", got)
		}
	})

	t.Run("no store rows inserted", func(t *testing.T) {
		rows, listErr := rig.store.ListBranches(t.Context(), store.BranchStatusAll)
		if listErr != nil {
			t.Fatalf("ListBranches: %v", listErr)
		}
		if len(rows) != 0 {
			t.Errorf("store rows = %+v, want none after an aborted close", rows)
		}
	})
}

// TestClose_ReviewerInitiated_AbortKeepsReviewerCommitSources guards against
// losing reviewer commits in the abort window: reviewPreflight merges the
// pending reviewer commits into the just-materialized feature branch, the
// user then declines at the confirm prompt, and the rollback force-deletes
// that branch. The review branch and review ref — the only other home of
// those commits — must survive the abort (their destructive cleanup is
// deferred until the merge commit lands), so a retry can redo the
// incorporation and close cleanly.
func TestClose_ReviewerInitiated_AbortKeepsReviewerCommitSources(t *testing.T) {
	t.Parallel()

	rig := newReviewerCloseRig(t)

	// Carol reviewed the branch herself: local ABC-1@review with one fix
	// commit on top of the feature branch, pushed to origin, plus an approved
	// review ref. The ref is pushed because FetchReviewRefs prunes local
	// review refs missing from the remote.
	mustRunGitAt(t, rig.dir, "checkout", "-b", "ABC-1@review", "origin/ABC-1@feat@thing")
	writeFileAt(t, rig.dir, "feature.txt", "feature\nreviewer fix\n")
	mustRunGitAt(t, rig.dir, "add", "feature.txt")
	mustRunGitAt(t, rig.dir, "commit", "-m", "fix: reviewer nit")
	mustRunGitAt(t, rig.dir, "push", "origin", "ABC-1@review")
	mustRunGitAt(t, rig.dir, "checkout", "main")

	featureSHA, err := rig.client.ResolveRef("refs/remotes/origin/ABC-1@feat@thing")
	if err != nil {
		t.Fatalf("resolve feature: %v", err)
	}
	if _, err := rig.client.WriteReviewRef(t.Context(), "ABC-1", git.ReviewRef{
		Status: "approved", Round: 1,
		FeatureSHA: featureSHA.String(),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}, ""); err != nil {
		t.Fatalf("WriteReviewRef: %v", err)
	}
	mustRunGitAt(t, rig.dir, "push", "--force", "origin", "refs/zf/reviews/ABC-1")

	// ----- abort: decline at the confirm prompt, AFTER incorporation -----
	abort := &scriptedPrompter{
		Branch:   rig.refDerivedRow(),
		Strategy: commitpkg.MergeStrategySquash,
		Confirm:  false,
	}
	if err := runClose(t.Context(), rig.deps, abort); err != nil {
		t.Fatalf("runClose (abort): %v", err)
	}

	t.Run("materialized feature branch rolled back despite being checked out", func(t *testing.T) {
		assertBranchAbsent(t, rig.client, "ABC-1@feat@thing")
		cur, curErr := rig.client.CurrentBranch()
		if curErr != nil {
			t.Fatalf("CurrentBranch: %v", curErr)
		}
		if cur != "main" {
			t.Errorf("HEAD on %q, want main after rollback", cur)
		}
	})

	t.Run("local review branch survives the abort", func(t *testing.T) {
		exists, bErr := rig.client.BranchExists("ABC-1@review")
		if bErr != nil {
			t.Fatalf("BranchExists: %v", bErr)
		}
		if !exists {
			t.Error("ABC-1@review deleted by an aborted close — reviewer commits lost")
		}
	})

	t.Run("remote review branch survives the abort", func(t *testing.T) {
		if !rig.client.RemoteBranchExists(t.Context(), "ABC-1@review") {
			t.Error("origin/ABC-1@review deleted by an aborted close")
		}
	})

	t.Run("review ref survives the abort", func(t *testing.T) {
		ref, _, rErr := rig.client.ReadReviewRef(t.Context(), "ABC-1")
		if rErr != nil {
			t.Fatalf("ReadReviewRef: %v", rErr)
		}
		if ref == nil {
			t.Fatal("review ref deleted by an aborted close")
		}
	})

	// ----- retry: same close, confirmed — must redo incorporation and land -----
	retry := &scriptedPrompter{
		Branch:       rig.refDerivedRow(),
		Strategy:     commitpkg.MergeStrategySquash,
		Confirm:      true,
		Message:      []byte("feat(thing): close ABC-1 after review\n"),
		DeleteBranch: false,
	}
	if err := runClose(t.Context(), rig.deps, retry); err != nil {
		t.Fatalf("runClose (retry): %v", err)
	}

	t.Run("retry lands the close commit on main", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "feat(thing): close ABC-1 after review")
	})

	t.Run("retry lands the reviewer fix on main", func(t *testing.T) {
		out, sErr := exec.CommandContext(t.Context(), "git", "-C", rig.dir,
			"show", "main:feature.txt").Output()
		if sErr != nil {
			t.Fatalf("git show main:feature.txt: %v", sErr)
		}
		if !strings.Contains(string(out), "reviewer fix") {
			t.Errorf("main:feature.txt = %q, want the reviewer fix included", out)
		}
	})

	t.Run("review branch cleaned up after the successful close", func(t *testing.T) {
		assertBranchAbsent(t, rig.client, "ABC-1@review")
	})

	t.Run("review ref cleaned up after the successful close", func(t *testing.T) {
		ref, _, rErr := rig.client.ReadReviewRef(t.Context(), "ABC-1")
		if rErr != nil {
			t.Fatalf("ReadReviewRef: %v", rErr)
		}
		if ref != nil {
			t.Fatalf("review ref = %+v, want it deleted once the merge landed", ref)
		}
	})
}

// newWorktreeCloseRig mirrors newCloseRig but creates the feature branch in a
// linked worktree (as `issue start` does when the user picks "worktree") and
// makes its commit there. rig.client stays on the main tree.
func newWorktreeCloseRig(t *testing.T) (*closeTestRig, string) {
	t.Helper()

	dir := t.TempDir()
	runGit := func(cwd string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = cwd
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runGit(dir, "init", "-q", "-b", "main")
	runGit(dir, "config", "user.name", "Test User")
	runGit(dir, "config", "user.email", "test@test.com")
	runGit(dir, "config", "commit.gpgsign", "false")
	writeFileAt(t, dir, "base.txt", "base\n")
	runGit(dir, "add", "base.txt")
	runGit(dir, "commit", "-m", "chore: init")

	wtDir := filepath.Join(t.TempDir(), "repo--ABC-1")
	runGit(dir, "worktree", "add", "-q", "-b", "ABC-1@feat@add-thing", wtDir, "main")
	writeFileAt(t, wtDir, "feature.txt", "feature\n")
	runGit(wtDir, "add", "feature.txt")
	runGit(wtDir, "commit", "-m", "feat: add thing")

	return seedCloseRig(t, dir), wtDir
}

func worktreePrompter(rig *closeTestRig, strategy commitpkg.MergeStrategy, remove bool) *scriptedPrompter {
	return &scriptedPrompter{
		Branch:         rig.pickedBranchRow(),
		Strategy:       strategy,
		Confirm:        true,
		Message:        []byte("feat(thing): close ABC-1\n"),
		TrackerStatus:  "Closed",
		DeleteBranch:   true,
		RemoveWorktree: remove,
	}
}

func assertDirGone(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("%s still exists (%v)", dir, err)
	}
}

func assertDirPresent(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("%s missing: %v", dir, err)
	}
}

func TestClose_Worktree_RebaseRemovesWorktreeAndBranch(t *testing.T) {
	t.Parallel()

	rig, wtDir := newWorktreeCloseRig(t)
	rig.addOrigin(t)
	prompter := worktreePrompter(rig, commitpkg.MergeStrategyRebase, true)

	err := runClose(t.Context(), rig.deps(), prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
	})
	t.Run("main carries the close commit", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "feat(thing): close ABC-1")
	})
	t.Run("remove prompt asked once", func(t *testing.T) {
		if prompter.RemoveWorktreeCalls != 1 {
			t.Fatalf("RemoveWorktreeCalls = %d", prompter.RemoveWorktreeCalls)
		}
	})
	t.Run("worktree directory removed", func(t *testing.T) {
		assertDirGone(t, wtDir)
	})
	t.Run("local branch deleted", func(t *testing.T) {
		assertBranchAbsent(t, rig.client, "ABC-1@feat@add-thing")
	})
	t.Run("remote branch deleted", func(t *testing.T) {
		if got := lsRemoteHeads(t, rig.dir, "ABC-1@feat@add-thing"); got != "" {
			t.Fatalf("origin still has the branch: %q", got)
		}
	})
	t.Run("store records branch as merged", func(t *testing.T) {
		rows, err := rig.store.ListBranches(t.Context(), store.BranchStatusMerged)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("merged rows = %d, want 1", len(rows))
		}
	})
}

func TestClose_Worktree_ClassicAndSquash(t *testing.T) {
	t.Parallel()

	for _, strategy := range []commitpkg.MergeStrategy{commitpkg.MergeStrategyClassic, commitpkg.MergeStrategySquash} {
		t.Run(string(strategy), func(t *testing.T) {
			t.Parallel()

			rig, wtDir := newWorktreeCloseRig(t)
			prompter := worktreePrompter(rig, strategy, true)

			err := runClose(t.Context(), rig.deps(), prompter)

			t.Run("no error", func(t *testing.T) {
				if err != nil {
					t.Fatalf("runClose: %v", err)
				}
			})
			t.Run("main carries the close commit", func(t *testing.T) {
				assertHeadSubject(t, rig.dir, "main", "feat(thing): close ABC-1")
			})
			t.Run("worktree directory removed", func(t *testing.T) {
				assertDirGone(t, wtDir)
			})
			t.Run("local branch deleted", func(t *testing.T) {
				assertBranchAbsent(t, rig.client, "ABC-1@feat@add-thing")
			})
		})
	}
}

func TestClose_Worktree_RemovalDeclinedKeepsBranch(t *testing.T) {
	t.Parallel()

	rig, wtDir := newWorktreeCloseRig(t)
	prompter := worktreePrompter(rig, commitpkg.MergeStrategyRebase, false)

	err := runClose(t.Context(), rig.deps(), prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
	})
	t.Run("worktree kept", func(t *testing.T) {
		assertDirPresent(t, wtDir)
		if !strings.Contains(rig.stdout.String(), "kept") {
			t.Fatalf("stdout = %q, want 'kept'", rig.stdout.String())
		}
	})
	t.Run("local branch kept with a warning", func(t *testing.T) {
		exists, _ := rig.client.BranchExists("ABC-1@feat@add-thing")
		if !exists {
			t.Fatal("branch should still exist")
		}
		if !strings.Contains(rig.stderr.String(), "still checked out in its worktree") {
			t.Fatalf("stderr = %q", rig.stderr.String())
		}
	})
}

func TestClose_Worktree_UntrackedFileBlocksRemoval(t *testing.T) {
	t.Parallel()

	rig, wtDir := newWorktreeCloseRig(t)
	writeFileAt(t, wtDir, "scratch.txt", "keep me\n")
	prompter := worktreePrompter(rig, commitpkg.MergeStrategySquash, true)

	err := runClose(t.Context(), rig.deps(), prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
	})
	t.Run("merge landed", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "feat(thing): close ABC-1")
	})
	t.Run("worktree and untracked file survive", func(t *testing.T) {
		assertDirPresent(t, filepath.Join(wtDir, "scratch.txt"))
	})
	t.Run("force hint printed", func(t *testing.T) {
		if !strings.Contains(rig.stderr.String(), "git worktree remove --force") {
			t.Fatalf("stderr = %q", rig.stderr.String())
		}
	})
	t.Run("local branch kept", func(t *testing.T) {
		exists, _ := rig.client.BranchExists("ABC-1@feat@add-thing")
		if !exists {
			t.Fatal("branch should still exist")
		}
	})
}

func TestClose_Worktree_InvokedFromInsidePrintsCdHint(t *testing.T) {
	t.Parallel()

	rig, wtDir := newWorktreeCloseRig(t)
	deps := rig.deps()
	deps.invokedFrom = wtDir
	prompter := worktreePrompter(rig, commitpkg.MergeStrategyRebase, true)

	err := runClose(t.Context(), deps, prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
	})
	t.Run("worktree removed", func(t *testing.T) {
		assertDirGone(t, wtDir)
	})
	t.Run("cd hint back to the main tree", func(t *testing.T) {
		if want := cdHint(t, rig.dir); !strings.Contains(rig.stdout.String(), want) {
			t.Fatalf("stdout = %q, want it to contain %q", rig.stdout.String(), want)
		}
	})
}

// invokedBranchFor is what gives the branch picker its pre-selection: deps.client
// is anchored on the MAIN tree, so its own CurrentBranch() names the main
// checkout's branch, not the branch the user is standing on inside a worktree.
func TestInvokedBranchFor(t *testing.T) {
	t.Parallel()

	rig, wtDir := newWorktreeCloseRig(t)

	t.Run("from inside the linked worktree: the worktree's branch", func(t *testing.T) {
		if got := invokedBranchFor(t.Context(), rig.client, wtDir); got != "ABC-1@feat@add-thing" {
			t.Fatalf("invokedBranchFor = %q, want %q", got, "ABC-1@feat@add-thing")
		}
	})
	t.Run("from a symlinked path to the worktree: the worktree's branch", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(wtDir, link); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		if got := invokedBranchFor(t.Context(), rig.client, link); got != "ABC-1@feat@add-thing" {
			t.Fatalf("invokedBranchFor = %q, want %q", got, "ABC-1@feat@add-thing")
		}
	})
	t.Run("from the main tree: the main checkout's branch", func(t *testing.T) {
		if got := invokedBranchFor(t.Context(), rig.client, rig.dir); got != "main" {
			t.Fatalf("invokedBranchFor = %q, want %q", got, "main")
		}
	})
	t.Run("empty invokedFrom falls back to the main client's HEAD", func(t *testing.T) {
		if got := invokedBranchFor(t.Context(), rig.client, ""); got != "main" {
			t.Fatalf("invokedBranchFor = %q, want %q", got, "main")
		}
	})
	t.Run("an unknown path falls back to the main client's HEAD", func(t *testing.T) {
		if got := invokedBranchFor(t.Context(), rig.client, filepath.Join(t.TempDir(), "elsewhere")); got != "main" {
			t.Fatalf("invokedBranchFor = %q, want %q", got, "main")
		}
	})
}

// The headline scenario: `git zf issue close` typed INSIDE the linked worktree
// must still hand the picker the branch the user is standing on. Before the
// fix the main-tree client's CurrentBranch() ("main") was passed instead, so
// the picker opened with no row pre-selected.
func TestClose_Worktree_InvokedFromInsidePreselectsWorktreeBranch(t *testing.T) {
	t.Parallel()

	rig, wtDir := newWorktreeCloseRig(t)
	deps := rig.deps()
	deps.invokedFrom = wtDir
	deps.invokedBranch = invokedBranchFor(t.Context(), rig.client, wtDir)
	prompter := worktreePrompter(rig, commitpkg.MergeStrategyRebase, true)

	err := runClose(t.Context(), deps, prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
	})
	t.Run("deps carries the worktree's branch, not the main checkout's", func(t *testing.T) {
		if deps.invokedBranch != "ABC-1@feat@add-thing" {
			t.Fatalf("deps.invokedBranch = %q, want %q", deps.invokedBranch, "ABC-1@feat@add-thing")
		}
	})
	t.Run("picker pre-selected the worktree's branch", func(t *testing.T) {
		if prompter.PickBranchCurrent != "ABC-1@feat@add-thing" {
			t.Fatalf("PickBranch current = %q, want %q", prompter.PickBranchCurrent, "ABC-1@feat@add-thing")
		}
	})
}

// A prunable entry (directory deleted by hand) makes git refuse to check out
// or delete the branch until `git worktree prune`; the Rebase strategy would
// fail mid-flow with a bare checkout error, so close refuses up front, before
// anything is touched, with the command to run.
func TestClose_Worktree_PrunableEntryRefusesWithPruneHint(t *testing.T) {
	t.Parallel()

	rig, wtDir := newWorktreeCloseRig(t)
	if err := os.RemoveAll(wtDir); err != nil {
		t.Fatalf("rm worktree dir: %v", err)
	}
	prompter := worktreePrompter(rig, commitpkg.MergeStrategyRebase, true)

	err := runClose(t.Context(), rig.deps(), prompter)

	t.Run("error names git worktree prune", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "git worktree prune") {
			t.Fatalf("runClose err = %v, want prune instruction", err)
		}
	})
	t.Run("nothing merged", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "chore: init")
	})
	t.Run("no merge prompt asked", func(t *testing.T) {
		if prompter.RemoveWorktreeCalls != 0 {
			t.Fatalf("RemoveWorktreeCalls = %d, want 0", prompter.RemoveWorktreeCalls)
		}
	})
	t.Run("store row still in progress", func(t *testing.T) {
		rows, lerr := rig.store.ListBranches(t.Context(), store.BranchStatusInProgress)
		if lerr != nil {
			t.Fatalf("ListBranches: %v", lerr)
		}
		if len(rows) != 1 {
			t.Fatalf("in-progress rows = %d, want 1", len(rows))
		}
	})
}

// showFileAt returns the contents of path as recorded on branch, failing the
// test when the blob is missing.
func showFileAt(t *testing.T, dir, branch, path string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "show", branch+":"+path)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git show %s:%s: %v", branch, path, err)
	}

	return string(out)
}

// seedApprovedReviewBranch creates ABC-1@review off the feature branch with one
// reviewer commit and marks the review approved. The review branch is built in
// the MAIN tree (a branch other than the feature branch may be checked out
// there even while the feature branch lives in a linked worktree), and the main
// tree is left back on "main".
func seedApprovedReviewBranch(t *testing.T, rig *closeTestRig, file, content string) {
	t.Helper()

	mustRunGitAt(t, rig.dir, "checkout", "-q", "-b", "ABC-1@review", "ABC-1@feat@add-thing")
	writeFileAt(t, rig.dir, file, content)
	mustRunGitAt(t, rig.dir, "add", file)
	mustRunGitAt(t, rig.dir, "commit", "-q", "-m", "fix: reviewer nit")
	mustRunGitAt(t, rig.dir, "checkout", "-q", "main")

	seedReviewRef(t, rig, "ABC-1", store.ReviewStatusApproved, 1)
}

// The feature branch lives in a linked worktree, so the reviewer-commit
// incorporation in reviewPreflight has to run THERE: `git checkout
// ABC-1@feat@add-thing` in the main tree is refused by git while the branch is
// held by the worktree. This covers the fast-forward arm (the feature branch
// has not moved since the review branch forked).
func TestClose_Worktree_IncorporatesReviewerCommits(t *testing.T) {
	t.Parallel()

	rig, wtDir := newWorktreeCloseRig(t)
	seedApprovedReviewBranch(t, rig, "feature.txt", "feature\nreviewer nit\n")
	prompter := worktreePrompter(rig, commitpkg.MergeStrategyRebase, true)

	err := runClose(t.Context(), rig.deps(), prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
	})
	t.Run("reviewer change landed on main", func(t *testing.T) {
		if got := showFileAt(t, rig.dir, "main", "feature.txt"); !strings.Contains(got, "reviewer nit") {
			t.Fatalf("main:feature.txt = %q, want the reviewer line", got)
		}
	})
	t.Run("main moved only via the close commit", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "feat(thing): close ABC-1")
	})
	t.Run("review branch cleaned up", func(t *testing.T) {
		assertBranchAbsent(t, rig.client, "ABC-1@review")
	})
	t.Run("review ref cleaned up", func(t *testing.T) {
		ref, _, _ := rig.client.ReadReviewRef(t.Context(), "ABC-1")
		if ref != nil {
			t.Fatalf("want review ref deleted, got %+v", ref)
		}
	})
	t.Run("worktree removed", func(t *testing.T) {
		assertDirGone(t, wtDir)
	})
}

// Same setup, but the developer commits again in the worktree after the review
// branch forked, so reviewPreflight takes the diverged arm (MergeForward). That
// merge must also run in the worktree.
func TestClose_Worktree_MergesDivergedReviewerCommits(t *testing.T) {
	t.Parallel()

	rig, wtDir := newWorktreeCloseRig(t)
	seedApprovedReviewBranch(t, rig, "reviewer-only.txt", "r\n")

	// Developer keeps working — in the worktree, the only place the feature
	// branch is checked out.
	writeFileAt(t, wtDir, "dev-later.txt", "d\n")
	mustRunGitAt(t, wtDir, "add", "dev-later.txt")
	mustRunGitAt(t, wtDir, "commit", "-q", "-m", "feat: more work")

	prompter := worktreePrompter(rig, commitpkg.MergeStrategySquash, true)

	err := runClose(t.Context(), rig.deps(), prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
	})
	t.Run("reviewer file landed on main", func(t *testing.T) {
		if got := showFileAt(t, rig.dir, "main", "reviewer-only.txt"); got != "r\n" {
			t.Fatalf("main:reviewer-only.txt = %q, want %q", got, "r\n")
		}
	})
	t.Run("developer's later commit landed on main", func(t *testing.T) {
		if got := showFileAt(t, rig.dir, "main", "dev-later.txt"); got != "d\n" {
			t.Fatalf("main:dev-later.txt = %q, want %q", got, "d\n")
		}
	})
	t.Run("main moved only via the close commit", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "feat(thing): close ABC-1")
	})
	t.Run("review branch cleaned up", func(t *testing.T) {
		assertBranchAbsent(t, rig.client, "ABC-1@review")
	})
	t.Run("worktree removed", func(t *testing.T) {
		assertDirGone(t, wtDir)
	})
}
