package branch

import (
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
)

func TestRunCloseBranch(t *testing.T) {
	t.Parallel()

	t.Run("an in-progress branch is recorded as closed and the git branch is kept", func(t *testing.T) {
		t.Parallel()

		rig := newPruneRig(t)
		rig.seedIssueAndBranch(t, "ACT-1", "ACT-1@feat@active", "feat")
		rig.createGitBranch(t, "ACT-1@feat@active")

		if err := runCloseBranch(t.Context(), rig.client, "ACT-1@feat@active"); err != nil {
			t.Fatalf("runCloseBranch: %v", err)
		}

		if got := rig.statusOf(t, "ACT-1@feat@active"); got != branch.StatusClosed {
			t.Errorf("status = %q, want closed", got)
		}
		if exists, _ := rig.client.BranchExists("ACT-1@feat@active"); !exists {
			t.Error("the git branch was deleted")
		}
		if out := rig.stdout.String(); !strings.Contains(out, "recorded as closed") || !strings.Contains(out, "Test User") {
			t.Errorf("stdout = %q", out)
		}
	})

	t.Run("a branch someone else started and that exists nowhere here is closed", func(t *testing.T) {
		t.Parallel()

		rig := newPruneRig(t)
		rig.seedIssueAndBranch(t, "TM-1", "TM-1@feat@theirs", "feat")
		rig.runPruneGit(t, "config", "user.name", "Someone Else")

		if err := runCloseBranch(t.Context(), rig.client, "TM-1@feat@theirs"); err != nil {
			t.Fatalf("runCloseBranch: %v", err)
		}
		if got := rig.statusOf(t, "TM-1@feat@theirs"); got != branch.StatusClosed {
			t.Errorf("status = %q, want closed", got)
		}
	})

	t.Run("only the named branch of an issue is closed", func(t *testing.T) {
		t.Parallel()

		rig := newPruneRig(t)
		rig.seedIssueAndBranch(t, "V-1", "V-1@feat@thing", "feat")
		rig.seedIssueAndBranch(t, "V-1", "V-1@feat@thing@spike", "feat")

		if err := runCloseBranch(t.Context(), rig.client, "V-1@feat@thing@spike"); err != nil {
			t.Fatalf("runCloseBranch: %v", err)
		}
		if got := rig.statusOf(t, "V-1@feat@thing@spike"); got != branch.StatusClosed {
			t.Errorf("spike status = %q, want closed", got)
		}
		if got := rig.statusOf(t, "V-1@feat@thing"); got != branch.StatusInProgress {
			t.Errorf("main branch status = %q, want in_progress", got)
		}
	})

	t.Run("the close is pushed", func(t *testing.T) {
		t.Parallel()

		rig := newPruneRigWithOrigin(t, newPruneOrigin(t))
		rig.seedIssueAndBranch(t, "P-1", "P-1@feat@pushed", "feat")

		if err := runCloseBranch(t.Context(), rig.client, "P-1@feat@pushed"); err != nil {
			t.Fatalf("runCloseBranch: %v", err)
		}
		pushed, err := rig.client.ChainRefPushed(t.Context(), git.BranchRefs, "P-1")
		if err != nil || !pushed {
			t.Errorf("ChainRefPushed = %v, %v", pushed, err)
		}
	})

	for _, status := range []string{branch.StatusMerged, branch.StatusClosed} {
		t.Run("a "+status+" branch is left as it is", func(t *testing.T) {
			t.Parallel()

			rig := newPruneRig(t)
			rig.seedIssueAndBranch(t, "D-1", "D-1@feat@done", "feat")
			if err := branch.SetStatus(t.Context(), rig.client, "D-1", "D-1@feat@done", status); err != nil {
				t.Fatalf("SetStatus: %v", err)
			}
			before, _ := rig.client.ChainTip(t.Context(), git.BranchRefs, "D-1")

			if err := runCloseBranch(t.Context(), rig.client, "D-1@feat@done"); err != nil {
				t.Fatalf("runCloseBranch: %v", err)
			}

			if after, _ := rig.client.ChainTip(t.Context(), git.BranchRefs, "D-1"); after != before {
				t.Error("an op was written on the chain")
			}
			if out := rig.stdout.String(); !strings.Contains(out, "is already "+status) {
				t.Errorf("stdout = %q", out)
			}
		})
	}

	for name, branchName := range map[string]string{
		"a branch git-zf does not track": "NOPE-9@feat@unknown",
		"a name that is not git-zf's":    "main",
	} {
		t.Run(name+" is an error", func(t *testing.T) {
			t.Parallel()

			rig := newPruneRig(t)
			err := runCloseBranch(t.Context(), rig.client, branchName)
			if err == nil || !strings.Contains(err.Error(), "not tracked") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestCloseCmd(t *testing.T) {
	t.Parallel()

	root := New(&config.AppConfig{}).GetRootCmd()
	sub, _, err := root.Find([]string{"close"})

	t.Run("branch close is a registered subcommand", func(t *testing.T) {
		if err != nil || sub == nil || sub.Name() != "close" {
			t.Fatalf("Find = %v, %v", sub, err)
		}
	})

	t.Run("it requires exactly one branch name", func(t *testing.T) {
		if sub.Args(sub, nil) == nil || sub.Args(sub, []string{"a", "b"}) == nil {
			t.Error("accepted zero or two arguments")
		}
		if argErr := sub.Args(sub, []string{"42@feat@x"}); argErr != nil {
			t.Errorf("rejected one argument: %v", argErr)
		}
	})
}
