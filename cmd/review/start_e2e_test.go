package review

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	reviewpkg "github.com/piprim/git-zf/review"
)

const startBranch = "77@feat@my-feature"

// startRig is a developer clone and a reviewer clone of one bare origin. The
// review of issue 77 is requested and pushed; the reviewer (Carol) is on main,
// has fetched everything and has no local feature branch.
type startRig struct {
	dev      *reviewE2ERig
	dir      string // the reviewer's clone
	reviewer reviewDeps
	stdout   *bytes.Buffer
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}

	return strings.TrimSpace(string(out))
}

// commitFile adds one commit holding name to the branch checked out in dir.
func commitFile(t *testing.T, dir, name string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	gitIn(t, dir, "add", name)
	gitIn(t, dir, "commit", "-q", "-m", "feat: "+name)
}

// newStartRig builds the rig. beforeRequest, when not nil, runs in the
// developer's clone, on the feature branch, before the review is requested.
func newStartRig(t *testing.T, beforeRequest func(devDir string)) *startRig {
	t.Helper()

	dev := newReviewE2ERigWithOrigin(t)
	gitIn(t, dev.dir, "checkout", "-q", startBranch)
	if beforeRequest != nil {
		beforeRequest(dev.dir)
	}
	if err := runReviewRequest(t.Context(), dev.deps(), "77", startBranch); err != nil {
		t.Fatalf("review request: %v", err)
	}

	dir := t.TempDir()
	gitIn(t, filepath.Dir(dir), "clone", "-q", gitIn(t, dev.dir, "remote", "get-url", "origin"), filepath.Base(dir))
	gitIn(t, dir, "config", "user.name", "Carol")
	gitIn(t, dir, "config", "user.email", "carol@example.com")
	gitIn(t, dir, "config", "commit.gpgsign", "false")

	stdout := &bytes.Buffer{}
	client, err := git.NewClientAt(&pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: &bytes.Buffer{}}, dir)
	if err != nil {
		t.Fatalf("reviewer NewClientAt: %v", err)
	}
	if err := reviewpkg.Fetch(t.Context(), client, false); err != nil {
		t.Fatalf("reviewer fetch reviews: %v", err)
	}

	return &startRig{dev: dev, dir: dir, reviewer: reviewDeps{client: client, cfg: &config.AppConfig{}}, stdout: stdout}
}

func (r *startRig) start(t *testing.T) error {
	t.Helper()

	return runReviewStart(t.Context(), r.reviewer, "77")
}

// reviewerOf returns the reviewer the reviewer's clone has recorded.
func (r *startRig) reviewerOf(t *testing.T) string {
	t.Helper()

	st, err := reviewpkg.Load(t.Context(), r.reviewer.client, "77")
	if err != nil || st == nil {
		t.Fatalf("Load = %v, %v", st, err)
	}

	return st.Reviewer
}

// staleCopy leaves in the reviewer's clone a local feature branch holding a
// commit the remote branch never had, and returns that commit. HEAD is main.
func (r *startRig) staleCopy(t *testing.T) string {
	t.Helper()

	gitIn(t, r.dir, "checkout", "-q", "-b", startBranch, "origin/main")
	commitFile(t, r.dir, "old.txt")
	gitIn(t, r.dir, "checkout", "-q", "main")

	return tipOf(t, r.dir, startBranch)
}

func TestReviewStart_CreatesLocalBranchFromRemote(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t, nil)
	err := rig.start(t)

	t.Run("start succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewStart: %v", err)
		}
	})
	t.Run("HEAD is the feature branch", func(t *testing.T) {
		if got := gitIn(t, rig.dir, "symbolic-ref", "--short", "HEAD"); got != startBranch {
			t.Errorf("HEAD = %q", got)
		}
	})
	t.Run("at the remote tip", func(t *testing.T) {
		if got, want := tipOf(t, rig.dir, startBranch), tipOf(t, rig.dir, "origin/"+startBranch); got != want {
			t.Errorf("tip = %s, want %s", got, want)
		}
	})
	t.Run("no review branch is created", func(t *testing.T) {
		if exists, _ := rig.reviewer.client.BranchExists("77@review"); exists {
			t.Error("77@review exists")
		}
	})
	t.Run("a start op names the reviewer", func(t *testing.T) {
		if got := rig.reviewerOf(t); !strings.Contains(got, "Carol") {
			t.Errorf("Reviewer = %q", got)
		}
	})
}

func TestReviewStart_FastForwardsWhenBehind(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t, nil)
	gitIn(t, rig.dir, "branch", startBranch, "origin/main")
	err := rig.start(t)

	t.Run("start succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewStart: %v", err)
		}
	})
	t.Run("the local branch is at the remote tip", func(t *testing.T) {
		if got, want := tipOf(t, rig.dir, "HEAD"), tipOf(t, rig.dir, "origin/"+startBranch); got != want {
			t.Errorf("HEAD = %s, want %s", got, want)
		}
	})
}

func TestReviewStart_ResetsStaleCopy(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t, nil)
	old := rig.staleCopy(t)
	err := rig.start(t)

	t.Run("start succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewStart: %v", err)
		}
	})
	t.Run("the local branch is at the remote tip", func(t *testing.T) {
		if got, want := tipOf(t, rig.dir, "HEAD"), tipOf(t, rig.dir, "origin/"+startBranch); got != want {
			t.Errorf("HEAD = %s, want %s", got, want)
		}
	})
	t.Run("the message gives the commit the branch was on", func(t *testing.T) {
		if !strings.Contains(rig.stdout.String(), old[:7]) {
			t.Errorf("stdout does not name %s:\n%s", old[:7], rig.stdout)
		}
	})
	t.Run("the old commit is still in the clone", func(t *testing.T) {
		gitIn(t, rig.dir, "cat-file", "-e", old)
	})
}

func TestReviewStart_StaleCopyDirtyTreeRefuses(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t, nil)
	old := rig.staleCopy(t)
	if err := os.WriteFile(filepath.Join(rig.dir, "base.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}
	err := rig.start(t)

	t.Run("start refuses", func(t *testing.T) {
		if err == nil {
			t.Fatal("err = nil")
		}
	})
	t.Run("HEAD is unchanged", func(t *testing.T) {
		if got := gitIn(t, rig.dir, "symbolic-ref", "--short", "HEAD"); got != "main" {
			t.Errorf("HEAD = %q", got)
		}
	})
	t.Run("the branch is unchanged", func(t *testing.T) {
		if got := tipOf(t, rig.dir, startBranch); got != old {
			t.Errorf("tip = %s, want %s", got, old)
		}
	})
	t.Run("no start op", func(t *testing.T) {
		if got := rig.reviewerOf(t); got != "" {
			t.Errorf("Reviewer = %q", got)
		}
	})
}

func TestReviewStart_SubmittedCommitNotOnRemoteRefuses(t *testing.T) {
	t.Parallel()

	// The developer commits, does not push, and requests the review.
	rig := newStartRig(t, func(devDir string) { commitFile(t, devDir, "unpushed.txt") })
	submitted := tipOf(t, rig.dev.dir, startBranch)
	gitIn(t, rig.dir, "branch", startBranch, "origin/"+startBranch)
	before := tipOf(t, rig.dir, startBranch)
	err := rig.start(t)

	t.Run("start refuses and names the submitted commit", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), submitted[:7]) {
			t.Fatalf("err = %v, want one naming %s", err, submitted[:7])
		}
	})
	t.Run("the local branch is unchanged", func(t *testing.T) {
		if got := tipOf(t, rig.dir, startBranch); got != before {
			t.Errorf("tip = %s, want %s", got, before)
		}
	})
	t.Run("HEAD is unchanged", func(t *testing.T) {
		if got := gitIn(t, rig.dir, "symbolic-ref", "--short", "HEAD"); got != "main" {
			t.Errorf("HEAD = %q", got)
		}
	})
}

func TestReviewStart_KeepsUnpushedReviewerCommits(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t, nil)
	if err := rig.start(t); err != nil {
		t.Fatalf("first start: %v", err)
	}
	commitFile(t, rig.dir, "reviewer.txt")
	tip := tipOf(t, rig.dir, startBranch)
	ops := gitIn(t, rig.dir, "rev-list", "--count", "refs/zf/reviews/77")
	err := rig.start(t)

	t.Run("the second start succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewStart: %v", err)
		}
	})
	t.Run("the reviewer's commit is kept", func(t *testing.T) {
		if got := tipOf(t, rig.dir, startBranch); got != tip {
			t.Errorf("tip = %s, want %s", got, tip)
		}
	})
	t.Run("no second start op", func(t *testing.T) {
		if got := gitIn(t, rig.dir, "rev-list", "--count", "refs/zf/reviews/77"); got != ops {
			t.Errorf("ops = %s, want %s", got, ops)
		}
	})
}

func TestReviewStart_UnpushedReviewerCommitsAndRemoteMoved(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t, nil)
	if err := rig.start(t); err != nil {
		t.Fatalf("first start: %v", err)
	}
	commitFile(t, rig.dir, "reviewer.txt")
	tip := tipOf(t, rig.dir, startBranch)
	commitFile(t, rig.dev.dir, "more.txt")
	gitIn(t, rig.dev.dir, "push", "-q", "origin", startBranch)
	rig.stdout.Reset()
	err := rig.start(t)

	t.Run("start succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewStart: %v", err)
		}
	})
	t.Run("the branch is left alone", func(t *testing.T) {
		if got := tipOf(t, rig.dir, startBranch); got != tip {
			t.Errorf("tip = %s, want %s", got, tip)
		}
	})
	t.Run("the message says what to merge", func(t *testing.T) {
		if want := "git merge origin/" + startBranch; !strings.Contains(rig.stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, rig.stdout)
		}
	})
}

func TestReviewStart_BranchHeldByAnotherWorktree(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t, nil)
	gitIn(t, rig.dir, "worktree", "add", "-q", filepath.Join(t.TempDir(), "wt"), "-b", startBranch, "origin/"+startBranch)
	err := rig.start(t)

	t.Run("start refuses", func(t *testing.T) {
		if err == nil {
			t.Fatal("err = nil")
		}
	})
	t.Run("HEAD is unchanged", func(t *testing.T) {
		if got := gitIn(t, rig.dir, "symbolic-ref", "--short", "HEAD"); got != "main" {
			t.Errorf("HEAD = %q", got)
		}
	})
	t.Run("no start op", func(t *testing.T) {
		if got := rig.reviewerOf(t); got != "" {
			t.Errorf("Reviewer = %q", got)
		}
	})
}

func TestReviewStart_OnTheBranchWithStagedChange_NothingNeeded(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t, nil)
	gitIn(t, rig.dir, "checkout", "-q", "-b", startBranch, "origin/"+startBranch)
	if err := os.WriteFile(filepath.Join(rig.dir, "note.txt"), []byte("note\n"), 0o644); err != nil {
		t.Fatalf("write note.txt: %v", err)
	}
	gitIn(t, rig.dir, "add", "note.txt")
	err := rig.start(t)

	t.Run("start succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewStart: %v", err)
		}
	})
	t.Run("the staged file is intact", func(t *testing.T) {
		if got := gitIn(t, rig.dir, "diff", "--cached", "--name-only"); got != "note.txt" {
			t.Errorf("staged = %q", got)
		}
	})
	t.Run("the reviewer is recorded", func(t *testing.T) {
		if got := rig.reviewerOf(t); !strings.Contains(got, "Carol") {
			t.Errorf("Reviewer = %q", got)
		}
	})
}

func TestReviewStart_NoRemote(t *testing.T) {
	t.Parallel()

	rig := newReviewE2ERig(t)
	bringRigToInReview(t, rig)
	gitIn(t, rig.dir, "checkout", "-q", "main")
	err := runReviewStart(t.Context(), rig.deps(), "77")

	t.Run("start succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewStart: %v", err)
		}
	})
	t.Run("HEAD is the feature branch", func(t *testing.T) {
		if got := gitIn(t, rig.dir, "symbolic-ref", "--short", "HEAD"); got != startBranch {
			t.Errorf("HEAD = %q", got)
		}
	})
	t.Run("no review branch is created", func(t *testing.T) {
		if exists, _ := rig.client.BranchExists("77@review"); exists {
			t.Error("77@review exists")
		}
	})
	t.Run("the commands it ends on take no argument, as approve and reject do", func(t *testing.T) {
		out := rig.stdout.String()
		for _, want := range []string{"  git zf review approve\n", "  git zf review reject\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		}
	})
}

// An untracked file the remote tip tracks and the local copy does not: git
// refuses the move, and must do so before HEAD leaves main.
func TestReviewStart_UntrackedFileInTheWayChangesNothing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		label string
		setup func(t *testing.T, rig *startRig) string // returns the local tip
	}{
		{"a stale copy to reset", func(t *testing.T, rig *startRig) string { return rig.staleCopy(t) }},
		{"a copy to fast-forward", func(t *testing.T, rig *startRig) string {
			gitIn(t, rig.dir, "branch", startBranch, "origin/main")

			return tipOf(t, rig.dir, startBranch)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			rig := newStartRig(t, nil)
			before := tc.setup(t, rig)
			if err := os.WriteFile(filepath.Join(rig.dir, "feature.txt"), []byte("mine\n"), 0o644); err != nil {
				t.Fatalf("write feature.txt: %v", err)
			}
			err := rig.start(t)

			t.Run("start refuses", func(t *testing.T) {
				if err == nil {
					t.Fatal("err = nil")
				}
			})
			t.Run("HEAD is unchanged", func(t *testing.T) {
				if got := gitIn(t, rig.dir, "symbolic-ref", "--short", "HEAD"); got != "main" {
					t.Errorf("HEAD = %q", got)
				}
			})
			t.Run("the branch is unchanged", func(t *testing.T) {
				if got := tipOf(t, rig.dir, startBranch); got != before {
					t.Errorf("tip = %s, want %s", got, before)
				}
			})
			t.Run("the untracked file is intact", func(t *testing.T) {
				if got, _ := os.ReadFile(filepath.Join(rig.dir, "feature.txt")); string(got) != "mine\n" {
					t.Errorf("feature.txt = %q", got)
				}
			})
			t.Run("no start op", func(t *testing.T) {
				if got := rig.reviewerOf(t); got != "" {
					t.Errorf("Reviewer = %q", got)
				}
			})
		})
	}
}
