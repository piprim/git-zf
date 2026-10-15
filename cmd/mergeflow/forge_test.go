package mergeflow

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

// newForgeRig is newEngineRig with a bare origin holding master and feature,
// and the client rebuilt so it sees the remote. forgeMerged plays the forge:
// it moves origin/master to feature's tip, as a merge there would.
func newForgeRig(t *testing.T) (*engineRig, *bytes.Buffer) {
	t.Helper()

	rig := newEngineRig(t)
	origin := t.TempDir()
	if out, err := exec.CommandContext(t.Context(), "git", "init", "-q", "--bare", "-b", "master", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	gitRun(t, rig.dir, "remote", "add", "origin", origin)
	gitRun(t, rig.dir, "push", "-q", "origin", "master", "feature")

	stderr := &bytes.Buffer{}
	client, err := git.NewClientAt(&pkg.IO{In: bytes.NewReader(nil), Out: rig.stdout, Err: stderr}, rig.dir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}
	rig.client = client

	return rig, stderr
}

func forgeMerged(t *testing.T, rig *engineRig) {
	t.Helper()

	gitRun(t, rig.dir, "push", "-q", "origin", "feature:master")
}

func TestRun_Forge(t *testing.T) {
	for _, tc := range []struct {
		strategy commit.MergeStrategy
		method   tracker.MergeMethod
	}{
		{commit.MergeStrategyClassic, tracker.MergeMethodMerge},
		{commit.MergeStrategySquash, tracker.MergeMethodSquash},
		{commit.MergeStrategyRebase, tracker.MergeMethodRebase},
	} {
		t.Run(string(tc.strategy)+" merges on the forge and fast-forwards master", func(t *testing.T) {
			rig, _ := newForgeRig(t)
			forgeMerged(t, rig)
			ft := &fake.Tracker{PullRequests: []tracker.PullRequest{{Number: "7", Head: "feature", Base: "master"}}}
			p := &scriptedMergePrompter{Strategy: tc.strategy, Confirm: true}

			res, err := Run(t.Context(), rig.client, Params{
				Source: "feature", Target: "master", PullRequest: &ForgeMerge{Forge: ft, Number: "7"},
			}, p, plainPrefill)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !res.ViaForge || res.Strategy != tc.strategy {
				t.Errorf("result = %+v", res)
			}
			head, err := rig.client.ResolveRef("refs/heads/feature")
			if err != nil {
				t.Fatalf("ResolveRef: %v", err)
			}
			want := fake.Merge{Number: "7", Method: tc.method, Head: string(head)}
			if len(ft.RecordedMerges) != 1 || ft.RecordedMerges[0] != want {
				t.Errorf("RecordedMerges = %+v, want [%+v]", ft.RecordedMerges, want)
			}
			if got := rig.headSubject(t, "master"); got != "feat: feature work" {
				t.Errorf("master head = %q, want the forge's merge", got)
			}
			if rig.statusPorcelain(t) != "" {
				t.Errorf("dirty tree: %q", rig.statusPorcelain(t))
			}
		})
	}

	t.Run("the forge's refusal is the error and nothing changes locally", func(t *testing.T) {
		rig, _ := newForgeRig(t)
		ft := &fake.Tracker{
			PullRequests: []tracker.PullRequest{{Number: "7", Head: "feature", Base: "master"}},
			MergeError:   errors.New("Pull Request is not mergeable"),
		}
		p := &scriptedMergePrompter{Strategy: commit.MergeStrategySquash, Confirm: true}

		_, err := Run(t.Context(), rig.client, Params{
			Source: "feature", Target: "master", PullRequest: &ForgeMerge{Forge: ft, Number: "7"},
		}, p, plainPrefill)
		if err == nil || !strings.Contains(err.Error(), "not mergeable") {
			t.Fatalf("err = %v", err)
		}
		if got := rig.headSubject(t, "master"); got != "chore: init" {
			t.Errorf("master head = %q, want untouched", got)
		}
	})

	t.Run("PushFirst pushes the source before the merge", func(t *testing.T) {
		rig, _ := newForgeRig(t)
		gitRun(t, rig.dir, "switch", "feature")
		writeFile(t, rig.dir, "review.txt", "review\n")
		gitRun(t, rig.dir, "add", "review.txt")
		gitRun(t, rig.dir, "commit", "-m", "fix: reviewer change")
		gitRun(t, rig.dir, "switch", "master")
		ft := &fake.Tracker{PullRequests: []tracker.PullRequest{{Number: "7", Head: "feature", Base: "master"}}}
		p := &scriptedMergePrompter{Strategy: commit.MergeStrategySquash, Confirm: true}

		_, err := Run(t.Context(), rig.client, Params{
			Source: "feature", Target: "master", PullRequest: &ForgeMerge{Forge: ft, Number: "7", PushFirst: true},
		}, p, plainPrefill)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		gitRun(t, rig.dir, "fetch", "-q", "origin")

		t.Run("origin's feature holds the local commit", func(t *testing.T) {
			if got := rig.headSubject(t, "origin/feature"); got != "fix: reviewer change" {
				t.Errorf("origin/feature head = %q", got)
			}
		})
		t.Run("the merge is recorded", func(t *testing.T) {
			if len(ft.RecordedMerges) != 1 {
				t.Errorf("RecordedMerges = %+v", ft.RecordedMerges)
			}
		})
	})

	t.Run("the fake refuses a moved head", func(t *testing.T) {
		rig, _ := newForgeRig(t)
		ft := &fake.Tracker{
			PullRequests: []tracker.PullRequest{{Number: "7", Head: "feature", Base: "master"}},
			HeadSHA:      map[string]string{"7": "0000000000000000000000000000000000000000"},
		}
		p := &scriptedMergePrompter{Strategy: commit.MergeStrategySquash, Confirm: true}

		_, err := Run(t.Context(), rig.client, Params{
			Source: "feature", Target: "master", PullRequest: &ForgeMerge{Forge: ft, Number: "7"},
		}, p, plainPrefill)

		t.Run("the error says the head moved", func(t *testing.T) {
			if err == nil || !strings.Contains(err.Error(), "head moved") {
				t.Errorf("err = %v", err)
			}
		})
		t.Run("master is untouched", func(t *testing.T) {
			if got := rig.headSubject(t, "master"); got != "chore: init" {
				t.Errorf("master head = %q", got)
			}
		})
	})

	t.Run("a declined confirm merges nothing on the forge", func(t *testing.T) {
		rig, _ := newForgeRig(t)
		ft := &fake.Tracker{PullRequests: []tracker.PullRequest{{Number: "7", Head: "feature", Base: "master"}}}
		p := &scriptedMergePrompter{Strategy: commit.MergeStrategySquash, Confirm: false}

		res, err := Run(t.Context(), rig.client, Params{
			Source: "feature", Target: "master", PullRequest: &ForgeMerge{Forge: ft, Number: "7"},
		}, p, plainPrefill)
		if err != nil || !res.Aborted || len(ft.RecordedMerges) != 0 {
			t.Errorf("err %v, res %+v, merges %+v", err, res, ft.RecordedMerges)
		}
	})

	t.Run("a local master with its own commits is not fast-forwarded: a warning, the merge stands", func(t *testing.T) {
		rig, stderr := newForgeRig(t)
		forgeMerged(t, rig)
		writeFile(t, rig.dir, "local.txt", "local\n")
		gitRun(t, rig.dir, "add", "local.txt")
		gitRun(t, rig.dir, "commit", "-m", "chore: local only")
		ft := &fake.Tracker{PullRequests: []tracker.PullRequest{{Number: "7", Head: "feature", Base: "master"}}}
		p := &scriptedMergePrompter{Strategy: commit.MergeStrategyClassic, Confirm: true}

		res, err := Run(t.Context(), rig.client, Params{
			Source: "feature", Target: "master", PullRequest: &ForgeMerge{Forge: ft, Number: "7"},
		}, p, plainPrefill)
		if err != nil || !res.ViaForge {
			t.Fatalf("err %v, res %+v", err, res)
		}
		if !strings.Contains(stderr.String(), "warning") || !strings.Contains(stderr.String(), "fast-forward") {
			t.Errorf("stderr %q lacks the fast-forward warning", stderr.String())
		}
		if got := rig.headSubject(t, "master"); got != "chore: local only" {
			t.Errorf("master head = %q, want the local commit kept", got)
		}
	})
}

func TestMethodOf(t *testing.T) {
	t.Parallel()

	t.Run("classic is merge, squash is squash, rebase is rebase", func(t *testing.T) {
		t.Parallel()
		if methodOf(commit.MergeStrategyClassic) != tracker.MergeMethodMerge ||
			methodOf(commit.MergeStrategySquash) != tracker.MergeMethodSquash ||
			methodOf(commit.MergeStrategyRebase) != tracker.MergeMethodRebase {
			t.Error("wrong mapping")
		}
	})
}
