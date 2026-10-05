package issueflow

import (
	"slices"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	"github.com/piprim/git-zf/git"
)

func rowNames(rows []branch.Row) []string {
	names := []string{}
	for i := range rows {
		names = append(names, rows[i].BranchName)
	}
	slices.Sort(names)

	return names
}

// candidatesRig is a clone that tracks four branches of four issues: one
// local, one only on the remote, one merged, and one that exists nowhere.
func candidatesRig(t *testing.T) *git.Client {
	t.Helper()

	c, run := newChainRepo(t, newBareOrigin(t))
	run("branch", "1@feat@local", "main")
	run("branch", "2@feat@remote-only", "main")
	run("push", "-q", "origin", "2@feat@remote-only")
	run("branch", "-D", "2@feat@remote-only")
	run("branch", "3@feat@merged", "main")

	branchtest.Seed(t, c, branch.Op{Branch: "1@feat@local"}, branch.StatusInProgress)
	branchtest.Seed(t, c, branch.Op{Branch: "2@feat@remote-only"}, branch.StatusInProgress)
	branchtest.Seed(t, c, branch.Op{Branch: "3@feat@merged"}, branch.StatusMerged)
	branchtest.Seed(t, c, branch.Op{Branch: "4@feat@nowhere"}, branch.StatusInProgress)

	return c
}

func TestCloseCandidates(t *testing.T) {
	t.Parallel()

	c := candidatesRig(t)

	rows, err := CloseCandidates(t.Context(), c)
	if err != nil {
		t.Fatalf("CloseCandidates: %v", err)
	}
	names := rowNames(rows)

	t.Run("a tracked branch present locally is offered", func(t *testing.T) {
		if !slices.Contains(names, "1@feat@local") {
			t.Errorf("candidates = %v", names)
		}
	})

	t.Run("a tracked branch present only on the remote is offered", func(t *testing.T) {
		if !slices.Contains(names, "2@feat@remote-only") {
			t.Errorf("candidates = %v", names)
		}
	})

	t.Run("a merged branch is excluded", func(t *testing.T) {
		if slices.Contains(names, "3@feat@merged") {
			t.Errorf("candidates = %v", names)
		}
	})

	t.Run("a branch that resolves nowhere is excluded", func(t *testing.T) {
		if slices.Contains(names, "4@feat@nowhere") {
			t.Errorf("candidates = %v", names)
		}
	})

	t.Run("a repository with no chain has no candidate", func(t *testing.T) {
		empty, _ := newChainRepo(t, "")
		rows, err := CloseCandidates(t.Context(), empty)
		if err != nil || len(rows) != 0 {
			t.Errorf("CloseCandidates = %v, %v", rows, err)
		}
	})
}

func TestLocalInProgress(t *testing.T) {
	t.Parallel()

	rows, err := LocalInProgress(t.Context(), candidatesRig(t))
	if err != nil {
		t.Fatalf("LocalInProgress: %v", err)
	}

	t.Run("only the in-progress branch that exists locally is returned", func(t *testing.T) {
		if got := rowNames(rows); !slices.Equal(got, []string{"1@feat@local"}) {
			t.Errorf("rows = %v", got)
		}
	})
}

func TestMaterializeBranch(t *testing.T) {
	t.Parallel()

	t.Run("absent branch is created (created=true)", func(t *testing.T) {
		t.Parallel()

		c := candidatesRig(t)
		created, err := MaterializeBranch(t.Context(), c, "2@feat@remote-only")
		if err != nil || !created {
			t.Fatalf("MaterializeBranch = %v, %v", created, err)
		}
		if exists, _ := c.BranchExists("2@feat@remote-only"); !exists {
			t.Error("the local branch was not created")
		}
	})

	t.Run("existing branch is a no-op (created=false)", func(t *testing.T) {
		t.Parallel()

		created, err := MaterializeBranch(t.Context(), candidatesRig(t), "1@feat@local")
		if err != nil || created {
			t.Fatalf("MaterializeBranch = %v, %v", created, err)
		}
	})

	t.Run("unresolvable start point surfaces error", func(t *testing.T) {
		t.Parallel()

		created, err := MaterializeBranch(t.Context(), candidatesRig(t), "4@feat@nowhere")
		if err == nil || created {
			t.Fatalf("MaterializeBranch = %v, %v, want an error", created, err)
		}
	})
}
