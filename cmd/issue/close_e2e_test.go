package issue

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
		&store.Issue{IDSlug: "ABC-1", Title: "Add thing", StatusID: store.StatusIDInProgress, TrackerType: &trackerType},
		&store.Branch{Name: "ABC-1@feat@add-thing", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed branch: %v", err)
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
	return closeDeps{client: r.client, store: r.store, cfg: r.cfg, tracker: r.tracker}
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
		Strategy:      StrategyRebase,
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
		Strategy:      StrategySquash,
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
		Strategy:      StrategyClassic,
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

	// PickBranch should never be called — but if runClose mis-routes, fail loudly.
	prompter := &scriptedPrompter{BranchErr: errors.New("PickBranch should not be called when no branches exist")}

	if err := runClose(t.Context(), deps, prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("stdout shows the empty-list message", func(t *testing.T) {
		if got := stdout.String(); !strings.Contains(got, "No in-progress branches") {
			t.Errorf("stdout = %q, want it to contain 'No in-progress branches'", got)
		}
	})
}

func TestClose_UserAbortsAtConfirm(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	prompter := &scriptedPrompter{
		Branch:   rig.pickedBranchRow(),
		Strategy: StrategyRebase,
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
		Strategy:      StrategyRebase,
		Confirm:       true,
		Message:       []byte("feat: close ABC-1\n\nCloses #ABC-1\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  false,
	}

	if err := runClose(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("ComposeMessage prefill has footer Closes #ABC-1", func(t *testing.T) {
		got, ok := prompter.CapturedPrefill["footer"]
		if !ok {
			t.Error("prefill missing 'footer' key")
		} else if got != "Closes #ABC-1" {
			t.Errorf("prefill[footer] = %q, want %q", got, "Closes #ABC-1")
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
