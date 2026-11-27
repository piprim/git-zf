package issue

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	commitpkg "github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/piprim/git-zf/tui"
)

const remoteFeature = "ABC-1@feat@add-thing"

// newRemoteCloseRig is a close rig whose main and feature branches are on an
// origin.
func newRemoteCloseRig(t *testing.T) *closeTestRig {
	t.Helper()

	rig := newCloseRig(t)
	rig.addOrigin(t)

	return rig
}

// pushFromElsewhere commits name (holding content) on branchName in a second
// clone of the rig's origin and pushes it: the rig's clone has not fetched it.
// It returns the new remote tip.
func pushFromElsewhere(t *testing.T, dir, branchName, name, content string) string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		t.Fatalf("get origin url: %v", err)
	}
	other := filepath.Join(t.TempDir(), "other")
	mustRunGitAt(t, filepath.Dir(other), "clone", "-q", strings.TrimSpace(string(out)), "other")
	mustRunGitAt(t, other, "config", "user.name", "Other")
	mustRunGitAt(t, other, "config", "user.email", "other@example.com")
	mustRunGitAt(t, other, "config", "commit.gpgsign", "false")
	mustRunGitAt(t, other, "checkout", "-q", branchName)
	writeFileAt(t, other, name, content)
	mustRunGitAt(t, other, "add", name)
	mustRunGitAt(t, other, "commit", "-q", "-m", "feat: "+name)
	mustRunGitAt(t, other, "push", "-q", "origin", branchName)

	return revParseInDir(t, other, "HEAD")
}

// commitLocally commits name on the rig's feature branch and returns to main.
func commitLocally(t *testing.T, rig *closeTestRig, name, content string) {
	t.Helper()

	mustRunGitAt(t, rig.dir, "checkout", "-q", remoteFeature)
	writeFileAt(t, rig.dir, name, content)
	mustRunGitAt(t, rig.dir, "add", name)
	mustRunGitAt(t, rig.dir, "commit", "-q", "-m", "feat: "+name)
	mustRunGitAt(t, rig.dir, "checkout", "-q", "main")
}

// assertStoppedAtRemote checks that the close stopped at the remote check and
// did not touch main.
func assertStoppedAtRemote(t *testing.T, rig *closeTestRig, mainBefore string, err error) {
	t.Helper()

	t.Run("the close stops with ErrRemoteMoved", func(t *testing.T) {
		if !errors.Is(err, ErrRemoteMoved) {
			t.Fatalf("runClose = %v, want ErrRemoteMoved", err)
		}
	})
	t.Run("main did not move", func(t *testing.T) {
		if got := revParseInDir(t, rig.dir, "refs/heads/main"); got != mainBefore {
			t.Errorf("main moved from %s to %s", mainBefore, got)
		}
	})
}

func featureTip(t *testing.T, rig *closeTestRig) string {
	t.Helper()

	return revParseInDir(t, rig.dir, "refs/heads/"+remoteFeature)
}

func TestClose_Remote_BehindYes(t *testing.T) {
	rig := newRemoteCloseRig(t)
	old := featureTip(t, rig)
	remoteTip := pushFromElsewhere(t, rig.dir, remoteFeature, "more.txt", "more\n")
	mainBefore := revParseInDir(t, rig.dir, "refs/heads/main")
	p := classicClosePrompter(rig)
	p.TakeRemote = true

	err := runClose(t.Context(), rig.deps(), p)

	assertStoppedAtRemote(t, rig, mainBefore, err)
	t.Run("the offer is a fast-forward of one commit", func(t *testing.T) {
		if len(p.RemoteOffers) != 1 || p.RemoteOffers[0].Merge || p.RemoteOffers[0].Commits != 1 {
			t.Errorf("offers = %+v", p.RemoteOffers)
		}
	})
	t.Run("the branch is fast-forwarded to the remote tip", func(t *testing.T) {
		if got := featureTip(t, rig); got != remoteTip {
			t.Errorf("tip = %s, want %s", got, remoteTip)
		}
	})
	t.Run("stdout shows what came in and what to do", func(t *testing.T) {
		out := rig.stdout.String()
		if want := "git log " + old[:7] + ".." + remoteFeature; !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
		if !strings.Contains(out, "run the close again") {
			t.Errorf("stdout lacks the next step:\n%s", out)
		}
	})
	t.Run("a second close merges", func(t *testing.T) {
		if err := runClose(t.Context(), rig.deps(), classicClosePrompter(rig)); err != nil {
			t.Fatalf("second runClose: %v", err)
		}
		if got := revParseInDir(t, rig.dir, "refs/heads/main"); got == mainBefore {
			t.Error("main did not move")
		}
	})
}

func TestClose_Remote_YesInstructions(t *testing.T) {
	t.Run("behind, landing on the approved commit: run the close again", func(t *testing.T) {
		rig := newRemoteCloseRig(t)
		remoteTip := pushFromElsewhere(t, rig.dir, remoteFeature, "more.txt", "more\n")
		seedApproval(t, rig, "ABC-1", remoteTip, false)
		p := classicClosePrompter(rig)
		p.TakeRemote = true

		err := runClose(t.Context(), rig.deps(), p)

		if !errors.Is(err, ErrRemoteMoved) || !strings.Contains(rig.stdout.String(), "run the close again") {
			t.Errorf("err = %v, stdout:\n%s", err, rig.stdout)
		}
		if strings.Contains(rig.stdout.String(), "git zf review request") {
			t.Errorf("stdout asks for a new round:\n%s", rig.stdout)
		}
	})

	t.Run("the approval is only on the remote yet: it is fetched", func(t *testing.T) {
		rig := newRemoteCloseRig(t)
		remoteTip := pushFromElsewhere(t, rig.dir, remoteFeature, "more.txt", "more\n")
		seedApproval(t, rig, "ABC-1", remoteTip, false)
		if err := reviewpkg.Push(t.Context(), rig.client, "ABC-1"); err != nil {
			t.Fatalf("push review: %v", err)
		}
		// This clone forgets the approval: the reviewer's clone pushed it.
		const ref = "refs/zf/reviews/ABC-1"
		before := revParseInDir(t, rig.dir, ref+"~1")
		mustRunGitAt(t, rig.dir, "update-ref", ref, before)
		mustRunGitAt(t, rig.dir, "update-ref", "refs/remotes/origin/zf/reviews/ABC-1", before)
		p := classicClosePrompter(rig)
		p.TakeRemote = true

		err := runClose(t.Context(), rig.deps(), p)

		if !errors.Is(err, ErrRemoteMoved) ||
			!strings.Contains(rig.stdout.String(), "The branch is on the approved commit") {
			t.Errorf("err = %v, stdout:\n%s", err, rig.stdout)
		}
	})

	t.Run("behind, landing past the approved commit: a new round", func(t *testing.T) {
		rig := newRemoteCloseRig(t)
		seedApproval(t, rig, "ABC-1", featureTip(t, rig), false)
		pushFromElsewhere(t, rig.dir, remoteFeature, "more.txt", "more\n")
		p := classicClosePrompter(rig)
		p.TakeRemote = true

		err := runClose(t.Context(), rig.deps(), p)

		if !errors.Is(err, ErrRemoteMoved) || !strings.Contains(rig.stdout.String(), "git zf review request") {
			t.Errorf("err = %v, stdout:\n%s", err, rig.stdout)
		}
	})

	t.Run("diverged and merged: a new round", func(t *testing.T) {
		rig := newRemoteCloseRig(t)
		commitLocally(t, rig, "mine.txt", "mine\n")
		seedApproval(t, rig, "ABC-1", featureTip(t, rig), false)
		pushFromElsewhere(t, rig.dir, remoteFeature, "more.txt", "more\n")
		p := classicClosePrompter(rig)
		p.TakeRemote = true

		err := runClose(t.Context(), rig.deps(), p)

		if !errors.Is(err, ErrRemoteMoved) || !strings.Contains(rig.stdout.String(), "git zf review request") {
			t.Errorf("err = %v, stdout:\n%s", err, rig.stdout)
		}
	})
}

func TestClose_Remote_BehindNo(t *testing.T) {
	rig := newRemoteCloseRig(t)
	old := featureTip(t, rig)
	pushFromElsewhere(t, rig.dir, remoteFeature, "more.txt", "more\n")
	mainBefore := revParseInDir(t, rig.dir, "refs/heads/main")
	p := classicClosePrompter(rig)

	err := runClose(t.Context(), rig.deps(), p)

	assertStoppedAtRemote(t, rig, mainBefore, err)
	t.Run("the question was asked once", func(t *testing.T) {
		if len(p.RemoteOffers) != 1 {
			t.Errorf("offers = %+v", p.RemoteOffers)
		}
	})
	t.Run("the branch did not move", func(t *testing.T) {
		if got := featureTip(t, rig); got != old {
			t.Errorf("tip = %s, want %s", got, old)
		}
	})
	t.Run("stdout gives the command that takes the commits", func(t *testing.T) {
		if want := "git merge --ff-only origin/" + remoteFeature; !strings.Contains(rig.stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, rig.stdout)
		}
	})
}

func TestClose_Remote_DivergedCleanYes(t *testing.T) {
	rig := newRemoteCloseRig(t)
	commitLocally(t, rig, "mine.txt", "mine\n")
	mine := featureTip(t, rig)
	remoteTip := pushFromElsewhere(t, rig.dir, remoteFeature, "more.txt", "more\n")
	mainBefore := revParseInDir(t, rig.dir, "refs/heads/main")
	p := classicClosePrompter(rig)
	p.TakeRemote = true

	err := runClose(t.Context(), rig.deps(), p)

	assertStoppedAtRemote(t, rig, mainBefore, err)
	t.Run("the offer was a merge", func(t *testing.T) {
		if len(p.RemoteOffers) != 1 || !p.RemoteOffers[0].Merge {
			t.Errorf("offers = %+v", p.RemoteOffers)
		}
	})
	t.Run("the branch holds a merge of both tips", func(t *testing.T) {
		parents := strings.Fields(revParseInDir(t, rig.dir, remoteFeature+"^@"))
		if len(parents) != 2 || parents[0] != mine || parents[1] != remoteTip {
			t.Errorf("parents = %v, want [%s %s]", parents, mine, remoteTip)
		}
	})
}

func TestClose_Remote_DivergedNo(t *testing.T) {
	rig := newRemoteCloseRig(t)
	commitLocally(t, rig, "mine.txt", "mine\n")
	mine := featureTip(t, rig)
	pushFromElsewhere(t, rig.dir, remoteFeature, "more.txt", "more\n")
	mainBefore := revParseInDir(t, rig.dir, "refs/heads/main")

	err := runClose(t.Context(), rig.deps(), classicClosePrompter(rig))

	assertStoppedAtRemote(t, rig, mainBefore, err)
	t.Run("the branch did not move", func(t *testing.T) {
		if got := featureTip(t, rig); got != mine {
			t.Errorf("tip = %s, want %s", got, mine)
		}
	})
}

func TestClose_Remote_DivergedConflict(t *testing.T) {
	rig := newRemoteCloseRig(t)
	commitLocally(t, rig, "clash.txt", "mine\n")
	mine := featureTip(t, rig)
	pushFromElsewhere(t, rig.dir, remoteFeature, "clash.txt", "theirs\n")
	mainBefore := revParseInDir(t, rig.dir, "refs/heads/main")
	p := classicClosePrompter(rig)
	p.TakeRemote = true

	err := runClose(t.Context(), rig.deps(), p)

	assertStoppedAtRemote(t, rig, mainBefore, err)
	t.Run("no question is asked", func(t *testing.T) {
		if len(p.RemoteOffers) != 0 {
			t.Errorf("offers = %+v", p.RemoteOffers)
		}
	})
	t.Run("the error names the conflicting file", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "clash.txt") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("nothing is left in progress and the branch did not move", func(t *testing.T) {
		if in, _ := rig.client.MergeInProgress(); in {
			t.Error("a merge is in progress")
		}
		if got := featureTip(t, rig); got != mine {
			t.Errorf("tip = %s, want %s", got, mine)
		}
	})
}

func TestClose_Remote_DirtyTree(t *testing.T) {
	cases := []struct {
		label string
		setup func(t *testing.T, rig *closeTestRig)
	}{
		{"behind", func(*testing.T, *closeTestRig) {}},
		{"diverged", func(t *testing.T, rig *closeTestRig) { commitLocally(t, rig, "mine.txt", "mine\n") }},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			rig := newRemoteCloseRig(t)
			tc.setup(t, rig)
			before := featureTip(t, rig)
			pushFromElsewhere(t, rig.dir, remoteFeature, "more.txt", "more\n")
			writeFileAt(t, rig.dir, "base.txt", "changed\n")
			mainBefore := revParseInDir(t, rig.dir, "refs/heads/main")
			p := classicClosePrompter(rig)
			p.TakeRemote = true

			err := runClose(t.Context(), rig.deps(), p)

			assertStoppedAtRemote(t, rig, mainBefore, err)
			t.Run("the branch did not move", func(t *testing.T) {
				if got := featureTip(t, rig); got != before {
					t.Errorf("tip = %s, want %s", got, before)
				}
			})
			t.Run("the error speaks of the uncommitted changes", func(t *testing.T) {
				if err == nil || !strings.Contains(err.Error(), "uncommitted") {
					t.Errorf("err = %v", err)
				}
			})
		})
	}
}

func TestClose_Remote_CannotAsk(t *testing.T) {
	rig := newRemoteCloseRig(t)
	old := featureTip(t, rig)
	pushFromElsewhere(t, rig.dir, remoteFeature, "more.txt", "more\n")
	mainBefore := revParseInDir(t, rig.dir, "refs/heads/main")
	p := classicClosePrompter(rig)
	p.TakeRemote, p.TakeRemoteErr = true, ErrCannotAsk

	err := runClose(t.Context(), rig.deps(), p)

	assertStoppedAtRemote(t, rig, mainBefore, err)
	t.Run("as a no: nothing moved, the command is printed", func(t *testing.T) {
		if got := featureTip(t, rig); got != old {
			t.Errorf("tip = %s, want %s", got, old)
		}
		if want := "git merge --ff-only origin/" + remoteFeature; !strings.Contains(rig.stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, rig.stdout)
		}
	})
}

// assertClosedWithoutAsking runs a classic close and checks that it completes
// and asks nothing about the remote.
func assertClosedWithoutAsking(t *testing.T, rig *closeTestRig) {
	t.Helper()

	mainBefore := revParseInDir(t, rig.dir, "refs/heads/main")
	p := classicClosePrompter(rig)
	p.Strategy = commitpkg.MergeStrategySquash
	err := runClose(t.Context(), rig.deps(), p)

	t.Run("the close completes", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v\n%s", err, rig.stderr)
		}
		if got := revParseInDir(t, rig.dir, "refs/heads/main"); got == mainBefore {
			t.Error("main did not move")
		}
	})
	t.Run("no question about the remote", func(t *testing.T) {
		if len(p.RemoteOffers) != 0 {
			t.Errorf("offers = %+v", p.RemoteOffers)
		}
	})
}

func TestClose_Remote_Unreachable(t *testing.T) {
	rig := newRemoteCloseRig(t)
	out, err := exec.CommandContext(t.Context(), "git", "-C", rig.dir, "remote", "get-url", "origin").Output()
	if err != nil {
		t.Fatalf("get origin url: %v", err)
	}
	origin := strings.TrimSpace(string(out))
	mustRunGitAt(t, rig.dir, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	rig.rebuildClient(t)

	mainBefore := revParseInDir(t, rig.dir, "refs/heads/main")
	// Squash never fetches on its own: it is the strategy that used to close
	// without the remote's commits.
	p := classicClosePrompter(rig)
	p.Strategy = commitpkg.MergeStrategySquash
	p.StrategyErr = errors.New("PickStrategy must not be reached")
	err = runClose(t.Context(), rig.deps(), p)

	t.Run("the close is refused with git's error, not a latched one", func(t *testing.T) {
		if !errors.Is(err, ErrRemoteUnreachable) || errors.Is(err, git.ErrRemoteDown) {
			t.Fatalf("runClose = %v, want ErrRemoteUnreachable\n%s", err, rig.stderr)
		}
	})
	t.Run("the error holds what git said", func(t *testing.T) {
		if !strings.Contains(err.Error(), "gone.git") {
			t.Errorf("runClose = %v, want git's message naming the repository", err)
		}
	})
	t.Run("no picker was opened", func(t *testing.T) {
		if p.PickBranchSeen != nil {
			t.Errorf("PickBranch was offered %+v", p.PickBranchSeen)
		}
	})
	t.Run("nothing was merged", func(t *testing.T) {
		if got := revParseInDir(t, rig.dir, "refs/heads/main"); got != mainBefore {
			t.Errorf("main moved: %s -> %s", mainBefore, got)
		}
	})
	t.Run("c in the issue table closes once the remote is back", func(t *testing.T) {
		mustRunGitAt(t, rig.dir, "remote", "set-url", "origin", origin)
		p := classicClosePrompter(rig)
		p.Strategy = commitpkg.MergeStrategySquash
		infra := issueListInfra{client: rig.client, stderr: rig.stderr, close: rig.deps()}

		_, err := New(rig.cfg).listAction(t.Context(), infra, &scriptedRecordPrompter{}, &scriptedStartPrompter{}, p,
			tui.IssuePickClose, &issuepkg.Row{IssueSlug: "ABC-1"})
		if err != nil {
			t.Fatalf("listAction = %v\n%s", err, rig.stderr)
		}
		if got := revParseInDir(t, rig.dir, "refs/heads/main"); got == mainBefore {
			t.Error("main did not move")
		}
	})
}

func TestClose_Remote_NoRemote(t *testing.T) {
	assertClosedWithoutAsking(t, newCloseRig(t))
}

func TestClose_Remote_NeverPushed(t *testing.T) {
	rig := newRemoteCloseRig(t)
	mustRunGitAt(t, rig.dir, "push", "-q", "origin", "--delete", remoteFeature)

	assertClosedWithoutAsking(t, rig)
}

func TestClose_Remote_LocalAhead(t *testing.T) {
	rig := newRemoteCloseRig(t)
	commitLocally(t, rig, "mine.txt", "mine\n")

	assertClosedWithoutAsking(t, rig)
}

func TestClose_Remote_MaterializedAtFreshTip(t *testing.T) {
	rig := newReviewerCloseRig(t)
	fresh := pushFromElsewhere(t, rig.dir, "ABC-1@feat@thing", "more.txt", "more\n")
	p := &scriptedPrompter{
		Branch: rig.refDerivedRow(), Strategy: commitpkg.MergeStrategyClassic, Confirm: true,
		Message: []byte("Merge ABC-1 into main\n"),
	}

	err := runClose(t.Context(), rig.deps, p)

	t.Run("the close completes without a question", func(t *testing.T) {
		if err != nil || len(p.RemoteOffers) != 0 {
			t.Fatalf("runClose = %v, offers = %+v\n%s", err, p.RemoteOffers, rig.stderr)
		}
	})
	t.Run("main holds the fresh remote tip", func(t *testing.T) {
		if ok, aErr := rig.client.IsAncestor(t.Context(), fresh, "refs/heads/main"); aErr != nil || !ok {
			t.Errorf("fresh tip %s is not in main (%v)", fresh, aErr)
		}
	})
}

func TestClose_Remote_Worktree(t *testing.T) {
	rig, wtDir := newWorktreeCloseRig(t)
	rig.addOrigin(t)
	remoteTip := pushFromElsewhere(t, rig.dir, remoteFeature, "more.txt", "more\n")
	mainBefore := revParseInDir(t, rig.dir, "refs/heads/main")
	p := worktreePrompter(rig, commitpkg.MergeStrategyClassic, false)
	p.TakeRemote = true

	err := runClose(t.Context(), rig.deps(), p)

	assertStoppedAtRemote(t, rig, mainBefore, err)
	t.Run("the worktree is fast-forwarded", func(t *testing.T) {
		if got := revParseInDir(t, wtDir, "HEAD"); got != remoteTip {
			t.Errorf("worktree HEAD = %s, want %s", got, remoteTip)
		}
		if _, sErr := os.Stat(filepath.Join(wtDir, "more.txt")); sErr != nil {
			t.Errorf("more.txt is not in the worktree: %v", sErr)
		}
	})
	t.Run("the main tree stays on main", func(t *testing.T) {
		if got, _ := rig.client.CurrentBranch(); got != "main" {
			t.Errorf("main tree is on %q", got)
		}
	})
}

// rewriteFromElsewhere amends the tip of branchName in a second clone of the
// rig's origin, adding name, and force-pushes it: the remote branch is
// rewritten and the rig's clone has not fetched it. It returns the new tip.
func rewriteFromElsewhere(t *testing.T, dir, branchName, name string) string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		t.Fatalf("get origin url: %v", err)
	}
	other := filepath.Join(t.TempDir(), "other")
	mustRunGitAt(t, filepath.Dir(other), "clone", "-q", strings.TrimSpace(string(out)), "other")
	mustRunGitAt(t, other, "config", "user.name", "Other")
	mustRunGitAt(t, other, "config", "user.email", "other@example.com")
	mustRunGitAt(t, other, "config", "commit.gpgsign", "false")
	mustRunGitAt(t, other, "checkout", "-q", branchName)
	writeFileAt(t, other, name, "rewritten\n")
	mustRunGitAt(t, other, "add", name)
	mustRunGitAt(t, other, "commit", "-q", "--amend", "-m", "feat: rewritten")
	mustRunGitAt(t, other, "push", "-q", "--force", "origin", branchName)

	return revParseInDir(t, other, "HEAD")
}

func TestClose_Remote_RewrittenStaleCopy(t *testing.T) {
	rig := newRemoteCloseRig(t)
	old := featureTip(t, rig)
	rewriteFromElsewhere(t, rig.dir, remoteFeature, "renamed.txt")
	mainBefore := revParseInDir(t, rig.dir, "refs/heads/main")
	p := classicClosePrompter(rig)
	p.TakeRemote = true

	err := runClose(t.Context(), rig.deps(), p)

	assertStoppedAtRemote(t, rig, mainBefore, err)
	t.Run("no merge is offered", func(t *testing.T) {
		if len(p.RemoteOffers) != 0 {
			t.Errorf("offers = %+v", p.RemoteOffers)
		}
	})
	t.Run("the branch did not move", func(t *testing.T) {
		if got := featureTip(t, rig); got != old {
			t.Errorf("tip = %s, want %s", got, old)
		}
	})
	for _, want := range []string{
		"rewritten",
		"git checkout -B " + remoteFeature + " origin/" + remoteFeature,
		"git log origin/" + remoteFeature + ".." + remoteFeature,
		"git branch <name> " + old[:7],
	} {
		t.Run("stdout says: "+want, func(t *testing.T) {
			if !strings.Contains(rig.stdout.String(), want) {
				t.Errorf("stdout lacks %q:\n%s", want, rig.stdout)
			}
		})
	}
}

func TestClose_Remote_RewrittenUnderOwnCommits(t *testing.T) {
	rig := newRemoteCloseRig(t)
	oldRemote := featureTip(t, rig)
	commitLocally(t, rig, "mine.txt", "mine\n")
	mine := featureTip(t, rig)
	rewriteFromElsewhere(t, rig.dir, remoteFeature, "renamed.txt")
	mainBefore := revParseInDir(t, rig.dir, "refs/heads/main")
	p := classicClosePrompter(rig)
	p.TakeRemote = true

	err := runClose(t.Context(), rig.deps(), p)

	assertStoppedAtRemote(t, rig, mainBefore, err)
	t.Run("no merge is offered", func(t *testing.T) {
		if len(p.RemoteOffers) != 0 {
			t.Errorf("offers = %+v", p.RemoteOffers)
		}
	})
	t.Run("the branch did not move", func(t *testing.T) {
		if got := featureTip(t, rig); got != mine {
			t.Errorf("tip = %s, want %s", got, mine)
		}
	})
	t.Run("stdout gives the rebase onto the rewritten branch, from the old remote tip", func(t *testing.T) {
		want := "git rebase --onto origin/" + remoteFeature + " " + oldRemote[:7] + " " + remoteFeature
		if !strings.Contains(rig.stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, rig.stdout)
		}
	})
}

// A rewrite the clone already followed is history: what diverges afterwards is
// an ordinary divergence.
func TestClose_Remote_RewriteAlreadyFollowedIsOrdinaryDivergence(t *testing.T) {
	rig := newRemoteCloseRig(t)
	rewriteFromElsewhere(t, rig.dir, remoteFeature, "renamed.txt")
	mustRunGitAt(t, rig.dir, "fetch", "-q", "origin")
	mustRunGitAt(t, rig.dir, "branch", "-f", remoteFeature, "origin/"+remoteFeature)
	commitLocally(t, rig, "mine.txt", "mine\n")
	pushFromElsewhere(t, rig.dir, remoteFeature, "more.txt", "more\n")
	mainBefore := revParseInDir(t, rig.dir, "refs/heads/main")
	p := classicClosePrompter(rig)

	err := runClose(t.Context(), rig.deps(), p)

	assertStoppedAtRemote(t, rig, mainBefore, err)
	t.Run("the merge is offered", func(t *testing.T) {
		if len(p.RemoteOffers) != 1 || !p.RemoteOffers[0].Merge {
			t.Errorf("offers = %+v", p.RemoteOffers)
		}
	})
}
