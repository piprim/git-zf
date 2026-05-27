package branch

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/store"
	fakeTracker "github.com/piprim/git-zf/tracker/fake"
)

type pruneTrackerTestRig struct {
	dir    string
	client *git.Client
	store  *store.Store
	fake   *fakeTracker.Tracker
	stdout *bytes.Buffer
}

func newPruneTrackerRig(t *testing.T) *pruneTrackerTestRig {
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
	ioStreams := &pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stdout}

	client, err := git.NewClientAt(ioStreams, dir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}

	s, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	t.Cleanup(func() { _ = s.Close() })

	fakeT := &fakeTracker.Tracker{
		Closed:  map[string]bool{},
		Unknown: map[string]bool{},
		Errors:  map[string]error{},
	}

	return &pruneTrackerTestRig{
		dir: dir, client: client, store: s, fake: fakeT, stdout: stdout,
	}
}

// seedMergedBranch creates a branch pointing at HEAD (fully merged into master)
// and inserts the corresponding store rows.
func (r *pruneTrackerTestRig) seedMergedBranch(t *testing.T, issueSlug, branchName string) {
	t.Helper()

	runGit := func(args ...string) {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = r.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runGit("branch", branchName)

	if err := r.store.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: issueSlug, Title: issueSlug + " title", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: branchName, Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("InsertIssueWithBranch: %v", err)
	}
}

// seedDivergentBranch creates a branch with a unique commit (not merged into master).
func (r *pruneTrackerTestRig) seedDivergentBranch(t *testing.T, issueSlug, branchName string) {
	t.Helper()

	runGit := func(args ...string) {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = r.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runGit("checkout", "-b", branchName)

	fp := filepath.Join(r.dir, branchName+".txt")
	if err := os.WriteFile(fp, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	runGit("add", branchName+".txt")
	runGit("commit", "-m", "x")
	runGit("checkout", "master")

	if err := r.store.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: issueSlug, Title: issueSlug + " title", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: branchName, Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("InsertIssueWithBranch: %v", err)
	}
}

// statusOf reads the current status of a branch row.
func (r *pruneTrackerTestRig) statusOf(t *testing.T, branchName string) store.BranchStatus {
	t.Helper()

	rows, err := r.store.ListBranches(t.Context(), store.BranchStatusAll)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}

	for _, b := range rows {
		if b.BranchName == branchName {
			return b.Status
		}
	}

	return ""
}

// hasLocalRef reports whether branchName exists as a local git ref.
func (r *pruneTrackerTestRig) hasLocalRef(t *testing.T, branchName string) bool {
	t.Helper()

	names, err := r.client.LocalBranchNames()
	if err != nil {
		t.Fatalf("LocalBranchNames: %v", err)
	}

	for _, n := range names {
		if n == branchName {
			return true
		}
	}

	return false
}

func Test_PruneTracker_E2E(t *testing.T) {
	runRun := func(t *testing.T, rig *pruneTrackerTestRig, prompter TrackerPrunePrompter, flags pruneTrackerFlags) error {
		t.Helper()

		return runPruneTracker(t.Context(), rig.stdout, rig.store, rig.client, rig.fake, prompter, flags)
	}

	t.Run("nothing to prune", func(t *testing.T) {
		rig := newPruneTrackerRig(t)

		err := runRun(t, rig, &scriptedTrackerPrunePrompter{}, pruneTrackerFlags{})
		if err != nil {
			t.Fatalf("err: %v", err)
		}

		if !strings.Contains(rig.stdout.String(), "Nothing to prune from tracker.") {
			t.Fatalf("output: %s", rig.stdout.String())
		}
	})

	t.Run("--dry-run lists candidates and does not mutate", func(t *testing.T) {
		rig := newPruneTrackerRig(t)
		rig.seedMergedBranch(t, "ABC-42", "ABC-42@feat@x")
		rig.fake.Closed["ABC-42"] = true

		if err := runRun(t, rig, nil, pruneTrackerFlags{dryRun: true}); err != nil {
			t.Fatalf("err: %v", err)
		}

		t.Run("local ref still exists after dry-run", func(t *testing.T) {
			if !rig.hasLocalRef(t, "ABC-42@feat@x") {
				t.Fatal("local ref should still exist after dry-run")
			}
		})

		t.Run("store row still in_progress after dry-run", func(t *testing.T) {
			if rig.statusOf(t, "ABC-42@feat@x") != store.BranchStatusInProgress {
				t.Fatal("store row should still be in_progress after dry-run")
			}
		})
	})

	t.Run("--safe-delete happy path: ref gone, store flipped to closed", func(t *testing.T) {
		rig := newPruneTrackerRig(t)
		rig.seedMergedBranch(t, "ABC-42", "ABC-42@feat@x")
		rig.fake.Closed["ABC-42"] = true

		if err := runRun(t, rig, newFixedActionPrompter("safe"), pruneTrackerFlags{safeDelete: true}); err != nil {
			t.Fatalf("err: %v", err)
		}

		t.Run("local ref is gone", func(t *testing.T) {
			if rig.hasLocalRef(t, "ABC-42@feat@x") {
				t.Fatal("local ref should be gone")
			}
		})

		t.Run("store status is closed", func(t *testing.T) {
			if rig.statusOf(t, "ABC-42@feat@x") != store.BranchStatusClosed {
				t.Fatalf("store status = %q, want closed", rig.statusOf(t, "ABC-42@feat@x"))
			}
		})
	})

	t.Run("--safe-delete with unmerged branch: kept + warned + store NOT flipped", func(t *testing.T) {
		rig := newPruneTrackerRig(t)
		rig.seedDivergentBranch(t, "ABC-42", "ABC-42@feat@x")
		rig.fake.Closed["ABC-42"] = true

		if err := runRun(t, rig, newFixedActionPrompter("safe"), pruneTrackerFlags{safeDelete: true}); err != nil {
			t.Fatalf("err: %v", err)
		}

		t.Run("local ref still exists (safe-delete refused)", func(t *testing.T) {
			if !rig.hasLocalRef(t, "ABC-42@feat@x") {
				t.Fatal("local ref should still exist (safe-delete refused)")
			}
		})

		t.Run("store status remains in_progress", func(t *testing.T) {
			if rig.statusOf(t, "ABC-42@feat@x") != store.BranchStatusInProgress {
				t.Fatalf("store status should remain in_progress when safe-delete refused, got %q",
					rig.statusOf(t, "ABC-42@feat@x"))
			}
		})

		t.Run("output contains git refused safe-delete warning", func(t *testing.T) {
			if !strings.Contains(rig.stdout.String(), "git refused safe-delete") {
				t.Fatalf("expected warning in output:\n%s", rig.stdout.String())
			}
		})
	})

	t.Run("--force-delete: ref gone even when unmerged, store flipped", func(t *testing.T) {
		rig := newPruneTrackerRig(t)
		rig.seedDivergentBranch(t, "ABC-42", "ABC-42@feat@x")
		rig.fake.Closed["ABC-42"] = true

		if err := runRun(t, rig, newFixedActionPrompter("force"), pruneTrackerFlags{forceDelete: true}); err != nil {
			t.Fatalf("err: %v", err)
		}

		t.Run("local ref is gone (force)", func(t *testing.T) {
			if rig.hasLocalRef(t, "ABC-42@feat@x") {
				t.Fatal("local ref should be gone (force)")
			}
		})

		t.Run("store status is closed", func(t *testing.T) {
			if rig.statusOf(t, "ABC-42@feat@x") != store.BranchStatusClosed {
				t.Fatalf("store status = %q, want closed", rig.statusOf(t, "ABC-42@feat@x"))
			}
		})
	})

	t.Run("--skip-delete: ref intact, store flipped", func(t *testing.T) {
		rig := newPruneTrackerRig(t)
		rig.seedDivergentBranch(t, "ABC-42", "ABC-42@feat@x")
		rig.fake.Closed["ABC-42"] = true

		if err := runRun(t, rig, newFixedActionPrompter("skip"), pruneTrackerFlags{skipDelete: true}); err != nil {
			t.Fatalf("err: %v", err)
		}

		t.Run("local ref still exists (skip)", func(t *testing.T) {
			if !rig.hasLocalRef(t, "ABC-42@feat@x") {
				t.Fatal("local ref should still exist (skip)")
			}
		})

		t.Run("store status is closed", func(t *testing.T) {
			if rig.statusOf(t, "ABC-42@feat@x") != store.BranchStatusClosed {
				t.Fatalf("store status = %q, want closed", rig.statusOf(t, "ABC-42@feat@x"))
			}
		})
	})

	t.Run("regex no-match: silently skipped", func(t *testing.T) {
		rig := newPruneTrackerRig(t)

		// Create a local branch with a name that won't extract any ID.
		cmd := exec.CommandContext(t.Context(), "git", "branch", "no-issue-id-here")
		cmd.Dir = rig.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git branch: %v %s", err, out)
		}

		err := runRun(t, rig, nil, pruneTrackerFlags{dryRun: true})
		if err != nil {
			t.Fatalf("err: %v", err)
		}

		if !strings.Contains(rig.stdout.String(), "Nothing to prune from tracker.") {
			t.Fatalf("output: %s", rig.stdout.String())
		}
	})

	t.Run("tracker error: warn + skip", func(t *testing.T) {
		rig := newPruneTrackerRig(t)
		rig.seedMergedBranch(t, "ABC-42", "ABC-42@feat@x")
		rig.fake.Errors["ABC-42"] = errors.New("boom")

		err := runRun(t, rig, nil, pruneTrackerFlags{dryRun: true})
		if err != nil {
			t.Fatalf("err: %v", err)
		}

		t.Run("output contains lookup failed warning", func(t *testing.T) {
			if !strings.Contains(rig.stdout.String(), "lookup failed") {
				t.Fatalf("expected lookup-failed warning:\n%s", rig.stdout.String())
			}
		})

		t.Run("output contains nothing-to-prune line", func(t *testing.T) {
			if !strings.Contains(rig.stdout.String(), "Nothing to prune from tracker.") {
				t.Fatalf("expected nothing-to-prune line:\n%s", rig.stdout.String())
			}
		})
	})

	t.Run("tracker 404 (ErrIssueNotFound): warn + skip", func(t *testing.T) {
		rig := newPruneTrackerRig(t)
		rig.seedMergedBranch(t, "ABC-42", "ABC-42@feat@x")
		rig.fake.Unknown["ABC-42"] = true

		err := runRun(t, rig, nil, pruneTrackerFlags{dryRun: true})
		if err != nil {
			t.Fatalf("err: %v", err)
		}

		if !strings.Contains(rig.stdout.String(), "not found in tracker") {
			t.Fatalf("expected not-found warning:\n%s", rig.stdout.String())
		}
	})

	t.Run("branch known to git but not in store: ref deleted, no store action", func(t *testing.T) {
		rig := newPruneTrackerRig(t)

		// Create a local branch without seeding the store.
		cmd := exec.CommandContext(t.Context(), "git", "branch", "ABC-99@feat@rogue")
		cmd.Dir = rig.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git branch: %v %s", err, out)
		}

		rig.fake.Closed["ABC-99"] = true

		if err := runRun(t, rig, newFixedActionPrompter("safe"), pruneTrackerFlags{safeDelete: true}); err != nil {
			t.Fatalf("err: %v", err)
		}

		t.Run("local ref is gone", func(t *testing.T) {
			if rig.hasLocalRef(t, "ABC-99@feat@rogue") {
				t.Fatal("local ref should be gone")
			}
		})

		t.Run("no store row was created or flipped", func(t *testing.T) {
			rows, _ := rig.store.ListBranches(t.Context(), store.BranchStatusAll)
			for _, r := range rows {
				if r.BranchName == "ABC-99@feat@rogue" {
					t.Fatalf("unexpected store row for rogue branch: %+v", r)
				}
			}
		})
	})
}
