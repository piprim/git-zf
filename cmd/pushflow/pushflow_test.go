package pushflow

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
)

type fakePusher struct {
	remote  string
	dry     git.PushOutcome
	dryOK   bool
	dryErr  error
	pushErr error
	pushed  []string
	out     *bytes.Buffer

	// merge-preview inputs
	isAncestor     map[[2]string]bool // [child, ancestor] → result
	mergeConflicts []string
	mergeErr       error
}

func (f *fakePusher) Remote() (string, error) { return f.remote, nil }
func (f *fakePusher) PushDryRun(_ context.Context, _ string) (git.PushOutcome, bool, error) {
	return f.dry, f.dryOK, f.dryErr
}
func (f *fakePusher) PushBranch(_ context.Context, b string) error {
	f.pushed = append(f.pushed, b)
	return f.pushErr
}
func (f *fakePusher) IO() *pkg.IO {
	return &pkg.IO{In: strings.NewReader(""), Out: f.out, Err: f.out}
}
func (f *fakePusher) IsAncestor(_ context.Context, child, ancestor string) (bool, error) {
	return f.isAncestor[[2]string{child, ancestor}], nil
}
func (f *fakePusher) MergeDryRun(_ context.Context, _, _ string) ([]string, error) {
	return f.mergeConflicts, f.mergeErr
}

func newFake() *fakePusher {
	return &fakePusher{
		remote: "origin",
		dry:    git.PushOutcome{Kind: git.PushFastForward, Summary: "aaa..bbb"},
		dryOK:  true,
		out:    &bytes.Buffer{},
	}
}

func yes(_ context.Context, _ string) (bool, error) { return true, nil }
func no(_ context.Context, _ string) (bool, error)  { return false, nil }

func TestPropose(t *testing.T) {
	t.Parallel()

	t.Run("Skip → never pushes", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		if err := Propose(t.Context(), f, Opts{Branch: "b", Skip: true}, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(f.pushed) != 0 {
			t.Fatalf("pushed %v, want none", f.pushed)
		}
	})

	t.Run("no remote → never pushes", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		f.remote = ""
		if err := Propose(t.Context(), f, Opts{Branch: "b"}, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(f.pushed) != 0 {
			t.Fatalf("pushed %v, want none", f.pushed)
		}
	})

	t.Run("nothing to push (dry ok=false) → never pushes", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		f.dryOK = false
		f.dry = git.PushOutcome{Kind: git.PushUpToDate}
		if err := Propose(t.Context(), f, Opts{Branch: "b"}, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(f.pushed) != 0 {
			t.Fatalf("pushed %v, want none", f.pushed)
		}
	})

	t.Run("dry-run error → skip, nil error", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		f.dryErr = errors.New("unreachable")
		if err := Propose(t.Context(), f, Opts{Branch: "b"}, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(f.pushed) != 0 {
			t.Fatalf("pushed %v, want none", f.pushed)
		}
	})

	t.Run("non-interactive without auto-confirm → skip", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		boom := func(_ context.Context, _ string) (bool, error) {
			t.Fatal("confirm must not be called")
			return false, nil
		}
		if err := Propose(t.Context(), f, Opts{Branch: "b", NonInteractive: true}, boom); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(f.pushed) != 0 {
			t.Fatalf("pushed %v, want none", f.pushed)
		}
	})

	t.Run("auto-confirm pushes without calling confirm", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		boom := func(_ context.Context, _ string) (bool, error) {
			t.Fatal("confirm must not be called under AutoConfirm")
			return false, nil
		}
		if err := Propose(t.Context(), f, Opts{Branch: "b", AutoConfirm: true, NonInteractive: true}, boom); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(f.pushed) != 1 || f.pushed[0] != "b" {
			t.Fatalf("pushed %v, want [b]", f.pushed)
		}
	})

	t.Run("auto-confirm without NonInteractive → pushes without calling confirm", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		boom := func(_ context.Context, _ string) (bool, error) {
			t.Fatal("confirm must not be called under AutoConfirm")
			return false, nil
		}
		if err := Propose(t.Context(), f, Opts{Branch: "b", AutoConfirm: true}, boom); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(f.pushed) != 1 || f.pushed[0] != "b" {
			t.Fatalf("pushed %v, want [b]", f.pushed)
		}
	})

	t.Run("confirm Yes → pushes the branch", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		if err := Propose(t.Context(), f, Opts{Branch: "b"}, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(f.pushed) != 1 || f.pushed[0] != "b" {
			t.Fatalf("pushed %v, want [b]", f.pushed)
		}
	})

	t.Run("confirm No → does not push", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		if err := Propose(t.Context(), f, Opts{Branch: "b"}, no); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(f.pushed) != 0 {
			t.Fatalf("pushed %v, want none", f.pushed)
		}
	})

	t.Run("confirm error propagates", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		boom := func(_ context.Context, _ string) (bool, error) { return false, errors.New("tty fail") }
		if err := Propose(t.Context(), f, Opts{Branch: "b"}, boom); err == nil {
			t.Fatal("want error from confirm, got nil")
		}
	})

	t.Run("push failure is returned", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		f.pushErr = errors.New("denied")
		if err := Propose(t.Context(), f, Opts{Branch: "b"}, yes); err == nil {
			t.Fatal("want push error, got nil")
		}
	})

	t.Run("preview is printed before confirm", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		var previewSeen bool
		before := func(_ context.Context, _ string) (bool, error) {
			previewSeen = strings.Contains(f.out.String(), "aaa..bbb")
			return true, nil
		}
		if err := Propose(t.Context(), f, Opts{Branch: "b"}, before); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if !previewSeen {
			t.Fatal("preview was not written before confirm was called")
		}
	})
}

func TestResolveFlags(t *testing.T) {
	t.Parallel()

	t.Run("both flags → error", func(t *testing.T) {
		t.Parallel()
		if _, _, err := ResolveFlags(true, true, true); err == nil {
			t.Fatal("want error for --push + --no-push")
		}
	})

	t.Run("no-push → skip", func(t *testing.T) {
		t.Parallel()
		skip, auto, err := ResolveFlags(false, true, true)
		if err != nil || !skip || auto {
			t.Fatalf("got skip=%v auto=%v err=%v, want skip=true auto=false", skip, auto, err)
		}
	})

	t.Run("propose=false → skip", func(t *testing.T) {
		t.Parallel()
		skip, _, err := ResolveFlags(false, false, false)
		if err != nil || !skip {
			t.Fatalf("got skip=%v err=%v, want skip=true", skip, err)
		}
	})

	t.Run("push → auto-confirm, no skip", func(t *testing.T) {
		t.Parallel()
		skip, auto, err := ResolveFlags(true, false, true)
		if err != nil || skip || !auto {
			t.Fatalf("got skip=%v auto=%v err=%v, want skip=false auto=true", skip, auto, err)
		}
	})

	t.Run("neither → no skip, no auto", func(t *testing.T) {
		t.Parallel()
		skip, auto, err := ResolveFlags(false, false, true)
		if err != nil || skip || auto {
			t.Fatalf("got skip=%v auto=%v err=%v, want both false", skip, auto, err)
		}
	})
}

func TestPropose_MergePreview(t *testing.T) {
	t.Parallel()

	// withMerge returns a fake set up for a merge-preview run: a fast-forward
	// push to origin, plus the IsAncestor results the test overrides per case.
	newMergeFake := func() *fakePusher {
		f := newFake()
		f.isAncestor = map[[2]string]bool{}
		return f
	}
	mergeOpts := Opts{Branch: "b", IncludeMergePreview: true, Parent: "p"}

	t.Run("already merged into parent", func(t *testing.T) {
		t.Parallel()
		f := newMergeFake()
		f.isAncestor[[2]string{"b", "p"}] = true // current is ancestor of parent
		if err := Propose(t.Context(), f, mergeOpts, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if !strings.Contains(f.out.String(), "Already merged into p") {
			t.Fatalf("output missing already-merged line; got %q", f.out.String())
		}
	})

	t.Run("fast-forwards into parent", func(t *testing.T) {
		t.Parallel()
		f := newMergeFake()
		f.isAncestor[[2]string{"p", "b"}] = true // parent is ancestor of current
		if err := Propose(t.Context(), f, mergeOpts, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if !strings.Contains(f.out.String(), "Fast-forwards into p") {
			t.Fatalf("output missing fast-forward line; got %q", f.out.String())
		}
	})

	t.Run("diverged, no conflicts → merge commit", func(t *testing.T) {
		t.Parallel()
		f := newMergeFake() // both IsAncestor false, no conflicts
		if err := Propose(t.Context(), f, mergeOpts, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if !strings.Contains(f.out.String(), "Merges into p with a merge commit (no conflicts)") {
			t.Fatalf("output missing merge-commit line; got %q", f.out.String())
		}
	})

	t.Run("diverged with conflicts", func(t *testing.T) {
		t.Parallel()
		f := newMergeFake()
		f.mergeConflicts = []string{"a.go", "b.go"}
		if err := Propose(t.Context(), f, mergeOpts, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if !strings.Contains(f.out.String(), "Conflicts with p: a.go, b.go") {
			t.Fatalf("output missing conflicts line; got %q", f.out.String())
		}
	})

	t.Run("not included → no merge line, push still proceeds", func(t *testing.T) {
		t.Parallel()
		f := newMergeFake()
		f.isAncestor[[2]string{"p", "b"}] = true
		if err := Propose(t.Context(), f, Opts{Branch: "b"}, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if strings.Contains(f.out.String(), "into p") {
			t.Fatalf("merge line shown when IncludeMergePreview=false; got %q", f.out.String())
		}
		if len(f.pushed) != 1 {
			t.Fatalf("push did not proceed; pushed=%v", f.pushed)
		}
	})

	t.Run("parent equal to branch → no merge line", func(t *testing.T) {
		t.Parallel()
		f := newMergeFake()
		if err := Propose(t.Context(), f, Opts{Branch: "b", IncludeMergePreview: true, Parent: "b"}, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if strings.Contains(f.out.String(), "into b") {
			t.Fatalf("merge line shown when parent==branch; got %q", f.out.String())
		}
	})
}
