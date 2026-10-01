package mergeflow

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/tui"
)

var _ Prompter = (*scriptedMergePrompter)(nil)

// scriptedMergePrompter returns canned answers instead of opening huh forms.
type scriptedMergePrompter struct {
	Strategy   commit.MergeStrategy
	Confirm    bool
	Message    []byte
	MessageErr error

	PickStrategyCalls int
}

func (s *scriptedMergePrompter) PickStrategy(context.Context) (commit.MergeStrategy, error) {
	s.PickStrategyCalls++

	return s.Strategy, nil
}

func (s *scriptedMergePrompter) ConfirmMerge(context.Context, string, string, commit.MergeStrategy) (bool, error) {
	return s.Confirm, nil
}

func (s *scriptedMergePrompter) ComposeMessage(context.Context, map[string]any) ([]byte, tui.CommitOption, error) {
	if s.MessageErr != nil {
		return nil, tui.CommitOption{}, s.MessageErr
	}

	return s.Message, tui.CommitOption{}, nil
}

// plainPrefill mirrors branch merge's subject-only prefill.
func plainPrefill(commit.MergeStrategy, git.Hash, git.Hash) map[string]any {
	return map[string]any{"subject": "chore: merge test"}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

type engineRig struct {
	dir    string
	client *git.Client
	stdout *bytes.Buffer
}

// initRepo builds a repo with a single commit on master.
func initRepo(t *testing.T) *engineRig {
	t.Helper()

	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "master")
	gitRun(t, dir, "config", "user.name", "Test User")
	gitRun(t, dir, "config", "user.email", "test@test.com")
	gitRun(t, dir, "config", "commit.gpgsign", "false")
	writeFile(t, dir, "base.txt", "base\n")
	gitRun(t, dir, "add", "base.txt")
	gitRun(t, dir, "commit", "-m", "chore: init")

	stdout := &bytes.Buffer{}
	ioStreams := &pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: &bytes.Buffer{}}
	client, err := git.NewClientAt(ioStreams, dir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}

	return &engineRig{dir: dir, client: client, stdout: stdout}
}

// newEngineRig: master, plus branch "feature" one commit ahead (modifies
// base.txt so an abort rollback fully reverts — no untracked residue).
func newEngineRig(t *testing.T) *engineRig {
	t.Helper()

	rig := initRepo(t)
	gitRun(t, rig.dir, "switch", "-c", "feature")
	writeFile(t, rig.dir, "base.txt", "base\nfeature\n")
	gitRun(t, rig.dir, "add", "base.txt")
	gitRun(t, rig.dir, "commit", "-m", "feat: feature work")
	gitRun(t, rig.dir, "switch", "master")

	return rig
}

// newConflictRig: feature and master edit base.txt divergently.
func newConflictRig(t *testing.T) *engineRig {
	t.Helper()

	rig := initRepo(t)
	gitRun(t, rig.dir, "switch", "-c", "feature")
	writeFile(t, rig.dir, "base.txt", "feature\n")
	gitRun(t, rig.dir, "add", "base.txt")
	gitRun(t, rig.dir, "commit", "-m", "feat: feature edit")
	gitRun(t, rig.dir, "switch", "master")
	writeFile(t, rig.dir, "base.txt", "master\n")
	gitRun(t, rig.dir, "add", "base.txt")
	gitRun(t, rig.dir, "commit", "-m", "chore: master edit")

	return rig
}

func (r *engineRig) headSubject(t *testing.T, branch string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "log", "-1", "--format=%s", branch)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log %s: %v\n%s", branch, err, out)
	}

	return string(bytes.TrimSpace(out))
}

func (r *engineRig) statusPorcelain(t *testing.T) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "status", "--porcelain")
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}

	return string(bytes.TrimSpace(out))
}

func TestRun_Squash(t *testing.T) {
	rig := newEngineRig(t)
	prompter := &scriptedMergePrompter{
		Strategy: commit.MergeStrategySquash,
		Confirm:  true,
		Message:  []byte("chore: squash feature into master\n"),
	}

	res, err := Run(t.Context(), rig.client,
		Params{Source: "feature", Target: "master"}, prompter, plainPrefill)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	t.Run("strategy recorded, not aborted", func(t *testing.T) {
		if res.Aborted || res.FastForwardDeferred || res.Strategy != commit.MergeStrategySquash {
			t.Fatalf("unexpected result: %+v", res)
		}
	})
	t.Run("squash commit landed on master", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: squash feature into master" {
			t.Fatalf("master HEAD subject = %q", got)
		}
	})
}

func TestRun_Classic(t *testing.T) {
	rig := newEngineRig(t)
	prompter := &scriptedMergePrompter{
		Strategy: commit.MergeStrategyClassic,
		Confirm:  true,
		Message:  []byte("chore: classic merge feature into master\n"),
	}

	res, err := Run(t.Context(), rig.client,
		Params{Source: "feature", Target: "master"}, prompter, plainPrefill)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	t.Run("merge commit landed on master", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: classic merge feature into master" {
			t.Fatalf("master HEAD subject = %q", got)
		}
	})
	t.Run("strategy is Classic", func(t *testing.T) {
		if res.Strategy != commit.MergeStrategyClassic {
			t.Fatalf("strategy = %q", res.Strategy)
		}
	})
}

func TestRun_Rebase(t *testing.T) {
	rig := newEngineRig(t)
	prompter := &scriptedMergePrompter{
		Strategy: commit.MergeStrategyRebase,
		Confirm:  true,
		Message:  []byte("chore: rebase feature into master\n"),
	}

	res, err := Run(t.Context(), rig.client,
		Params{Source: "feature", Target: "master"}, prompter, plainPrefill)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	t.Run("not fast-forward-deferred (target had not diverged)", func(t *testing.T) {
		if res.FastForwardDeferred {
			t.Fatal("unexpected FastForwardDeferred")
		}
	})
	t.Run("master fast-forwarded to the rebased commit", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: rebase feature into master" {
			t.Fatalf("master HEAD subject = %q", got)
		}
	})
}

func TestRun_UserDeclinesConfirm(t *testing.T) {
	rig := newEngineRig(t)
	prompter := &scriptedMergePrompter{
		Strategy: commit.MergeStrategySquash,
		Confirm:  false,
	}

	res, err := Run(t.Context(), rig.client,
		Params{Source: "feature", Target: "master"}, prompter, plainPrefill)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	t.Run("aborted", func(t *testing.T) {
		if !res.Aborted {
			t.Fatal("expected Aborted")
		}
	})
	t.Run("master unchanged (no commit)", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: init" {
			t.Fatalf("master HEAD subject = %q (expected no new commit)", got)
		}
	})
}

func TestRun_ConflictAborts(t *testing.T) {
	rig := newConflictRig(t)
	prompter := &scriptedMergePrompter{
		Strategy: commit.MergeStrategySquash,
		Confirm:  true,
		Message:  []byte("chore: should never commit\n"),
	}

	res, err := Run(t.Context(), rig.client,
		Params{Source: "feature", Target: "master"}, prompter, plainPrefill)

	t.Run("returns an error", func(t *testing.T) {
		if err == nil {
			t.Fatal("expected a conflict error")
		}
	})
	t.Run("not marked aborted (conflict is not a user decline)", func(t *testing.T) {
		if res.Aborted {
			t.Fatal("conflict should not set Aborted")
		}
	})
	t.Run("strategy never picked (short-circuits before PickStrategy)", func(t *testing.T) {
		if prompter.PickStrategyCalls != 0 {
			t.Fatalf("PickStrategy called %d times, want 0", prompter.PickStrategyCalls)
		}
	})
	t.Run("master unchanged", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: master edit" {
			t.Fatalf("master HEAD subject = %q", got)
		}
	})
}

func TestRun_MaterializedDiscardsResidueOnAbort(t *testing.T) {
	rig := newEngineRig(t)
	prompter := &scriptedMergePrompter{
		Strategy:   commit.MergeStrategySquash,
		Confirm:    true,
		MessageErr: errors.New("compose cancelled"),
	}

	_, err := Run(t.Context(), rig.client,
		Params{Source: "feature", Target: "master", SourceMaterialized: true}, prompter, plainPrefill)

	t.Run("returns the compose error", func(t *testing.T) {
		if err == nil {
			t.Fatal("expected the compose error to propagate")
		}
	})
	t.Run("staged squash residue discarded (clean tree)", func(t *testing.T) {
		if got := rig.statusPorcelain(t); got != "" {
			t.Fatalf("working tree not clean after materialized abort:\n%s", got)
		}
	})
}
