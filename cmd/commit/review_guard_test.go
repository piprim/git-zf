package commit

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/store"
)

type guardRig struct {
	dir    string
	client *git.Client
	store  *store.Store
	stdout *bytes.Buffer
}

func newGuardRig(t *testing.T) *guardRig {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.name", "T")
	run("config", "user.email", "t@t")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "base.txt")
	run("commit", "-m", "chore: init")
	run("checkout", "-b", "42@feat@title")
	if err := os.WriteFile(filepath.Join(dir, "feat.txt"), []byte("feat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "feat.txt")
	run("commit", "-m", "feat: work")
	// Reviewer branch with one commit, back to feature branch.
	run("checkout", "-b", "42@review")
	if err := os.WriteFile(filepath.Join(dir, "review-fix.txt"), []byte("fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "review-fix.txt")
	run("commit", "-m", "fix: reviewer nit")
	run("checkout", "42@feat@title")

	stdout := &bytes.Buffer{}
	client, err := git.NewClientAt(&pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: os.Stderr}, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}
	s, err := store.Open(t.Context(), filepath.Join(dir, ".git"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "42", Title: "title", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: "42@feat@title", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := client.WriteReviewRef(t.Context(), "42", git.ReviewRef{
		Status: string(store.ReviewStatusChangesRequested), Round: 1,
		FeatureSHA: "unused", CreatedAt: "2026-07-08T00:00:00Z",
	}, ""); err != nil {
		t.Fatalf("write review ref: %v", err)
	}
	return &guardRig{dir: dir, client: client, store: s, stdout: stdout}
}

func answer(v bool) reviewConfirmFunc {
	return func(context.Context, string) (bool, error) { return v, nil }
}

func TestGuardPendingReview(t *testing.T) {
	t.Run("accept merges and continues", func(t *testing.T) {
		rig := newGuardRig(t)
		if err := guardPendingReview(t.Context(), rig.client, rig.store, answer(true)); err != nil {
			t.Fatalf("want nil after accepted merge, got %v", err)
		}
		n, _ := rig.client.CommitsAhead(t.Context(), "42@review", "42@feat@title")
		if n != 0 {
			t.Fatalf("want reviewer commit incorporated, %d pending", n)
		}
	})

	t.Run("decline aborts with sync hint", func(t *testing.T) {
		rig := newGuardRig(t)
		err := guardPendingReview(t.Context(), rig.client, rig.store, answer(false))
		if err == nil || !strings.Contains(err.Error(), "git zf review sync") {
			t.Fatalf("want sync-hint error, got %v", err)
		}
	})

	t.Run("dirty tree refused before merge with stash hint", func(t *testing.T) {
		rig := newGuardRig(t)
		if err := os.WriteFile(filepath.Join(rig.dir, "feat.txt"), []byte("wip\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := guardPendingReview(t.Context(), rig.client, rig.store, answer(true))
		if err == nil || !strings.Contains(err.Error(), "git stash") {
			t.Fatalf("want stash-hint error, got %v", err)
		}
		inProgress, _ := rig.client.MergeInProgress()
		if inProgress {
			t.Fatal("no merge must be attempted on a dirty tree")
		}
	})

	t.Run("no pending review passes silently", func(t *testing.T) {
		rig := newGuardRig(t)
		mustRun := func(args ...string) {
			cmd := exec.CommandContext(t.Context(), "git", args...)
			cmd.Dir = rig.dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		mustRun("merge", "--no-edit", "42@review") // incorporate manually
		if err := guardPendingReview(t.Context(), rig.client, rig.store, answer(false)); err != nil {
			t.Fatalf("want silent pass, got %v", err)
		}
	})
}
