package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mergeRig is a clone of a bare origin whose main holds a merge of a side
// branch at its tip; the merge is pushed when pushed is true. It returns the
// client, the clone, the origin and the merge hash.
func mergeRig(t *testing.T, pushed bool) (*Client, string, string, string) {
	t.Helper()
	c, dir, origin := newDiskRepoWithOrigin(t)
	commit := func(branch, name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		mustGit(t, dir, "add", name+".txt")
		mustGit(t, dir, "commit", "-q", "-m", "feat: "+name)
	}
	mustGit(t, dir, "switch", "-q", "-c", "side")
	commit("side", "side")
	mustGit(t, dir, "switch", "-q", "main")
	commit("main", "docs")
	mustGit(t, dir, "merge", "-q", "--no-ff", "side", "-m", "merge side\n\nwith a conflict fixed")
	if pushed {
		mustGit(t, dir, "push", "-q", "origin", "main")
	}
	merge, err := c.output(t.Context(), "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	return c, dir, origin, merge
}

func rev(t *testing.T, c *Client, args ...string) string {
	t.Helper()
	out, err := c.output(context.Background(), args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}

	return out
}

func TestLinearize_Pushed(t *testing.T) {
	t.Parallel()
	c, dir, origin, merge := mergeRig(t, true)
	ctx := t.Context()

	plan, err := c.LinearizePlan(ctx, merge[:7])
	if err != nil {
		t.Fatalf("LinearizePlan: %v", err)
	}

	t.Run("the plan names the branch, its upstream and a forced push", func(t *testing.T) {
		want := Linearization{
			Merge: merge, Parent: rev(t, c, "rev-parse", "main^1"), Branch: "main",
			Upstream: "origin/main", UpstreamHash: merge, Remote: "origin", RemoteRef: "refs/heads/main", Force: true,
		}
		if plan != want {
			t.Errorf("plan = %+v, want %+v", plan, want)
		}
	})

	commit, err := c.LinearizeMerge(ctx, &plan)
	if err != nil {
		t.Fatalf("LinearizeMerge: %v", err)
	}

	t.Run("the branch moves to one commit with the merge's tree on its first parent", func(t *testing.T) {
		if got := rev(t, c, "rev-parse", "main"); got != commit {
			t.Errorf("main = %s, want %s", got, commit)
		}
		if got := rev(t, c, "rev-list", "--parents", "-n", "1", commit); got != commit+" "+plan.Parent {
			t.Errorf("parents = %q", got)
		}
		if got, want := rev(t, c, "rev-parse", commit+"^{tree}"), rev(t, c, "rev-parse", merge+"^{tree}"); got != want {
			t.Errorf("tree = %s, want %s", got, want)
		}
	})

	t.Run("the commit keeps the merge's message and author", func(t *testing.T) {
		format := "--format=%an <%ae> %ad%n%B"
		if got, want := rev(t, c, "show", "-s", format, commit), rev(t, c, "show", "-s", format, merge); got != want {
			t.Errorf("show = %q, want %q", got, want)
		}
	})

	t.Run("the old tip is in the reflog and the working tree is clean", func(t *testing.T) {
		if got := rev(t, c, "rev-parse", "main@{1}"); got != merge {
			t.Errorf("main@{1} = %s, want %s", got, merge)
		}
		if got := rev(t, c, "status", "--porcelain"); got != "" {
			t.Errorf("status = %q", got)
		}
	})

	t.Run("the push moves the upstream", func(t *testing.T) {
		if err := c.PushLinearized(ctx, &plan); err != nil {
			t.Fatalf("PushLinearized: %v", err)
		}
		if got := rev(t, c, "-C", origin, "rev-parse", "main"); got != commit {
			t.Errorf("origin main = %s, want %s", got, commit)
		}
		if got := rev(t, c, "rev-parse", "origin/main"); got != commit {
			t.Errorf("origin/main = %s, want %s", got, commit)
		}
	})

	t.Run("the lease refuses to overwrite what someone pushed since the plan", func(t *testing.T) {
		mustGit(t, dir, "branch", "tmp", commit)
		mustGit(t, dir, "switch", "-q", "tmp")
		mustGit(t, dir, "commit", "-q", "--allow-empty", "-m", "theirs")
		mustGit(t, dir, "push", "-q", "origin", "tmp:main")
		mustGit(t, dir, "switch", "-q", "main")
		theirs := rev(t, c, "rev-parse", "tmp")
		if err := c.PushLinearized(ctx, &plan); err == nil {
			t.Fatal("push succeeded against a moved upstream")
		}
		if got := rev(t, c, "-C", origin, "rev-parse", "main"); got != theirs {
			t.Errorf("origin main = %s, want %s", got, theirs)
		}
	})
}

func TestLinearize_Unpushed(t *testing.T) {
	t.Parallel()
	c, _, origin, merge := mergeRig(t, false)
	ctx := t.Context()

	plan, err := c.LinearizePlan(ctx, merge)
	if err != nil {
		t.Fatalf("LinearizePlan: %v", err)
	}

	t.Run("an upstream behind the merge makes the push a fast-forward", func(t *testing.T) {
		if plan.Upstream != "origin/main" || plan.Force || plan.UpstreamHash != rev(t, c, "rev-parse", "main~2") {
			t.Errorf("plan = %+v", plan)
		}
	})

	t.Run("the push fast-forwards the upstream", func(t *testing.T) {
		commit, err := c.LinearizeMerge(ctx, &plan)
		if err != nil {
			t.Fatalf("LinearizeMerge: %v", err)
		}
		if err := c.PushLinearized(ctx, &plan); err != nil {
			t.Fatalf("PushLinearized: %v", err)
		}
		if got := rev(t, c, "-C", origin, "rev-parse", "main"); got != commit {
			t.Errorf("origin main = %s, want %s", got, commit)
		}
	})
}

func TestLinearize_NoUpstream(t *testing.T) {
	t.Parallel()
	c, dir := newDiskRepo(t)
	mustGit(t, dir, "switch", "-q", "-c", "side")
	mustGit(t, dir, "commit", "-q", "--allow-empty", "-m", "side")
	mustGit(t, dir, "switch", "-q", "main")
	mustGit(t, dir, "merge", "-q", "--no-ff", "side", "-m", "merge side")

	plan, err := c.LinearizePlan(t.Context(), rev(t, c, "rev-parse", "HEAD"))
	if err != nil {
		t.Fatalf("LinearizePlan: %v", err)
	}

	t.Run("nothing is to push", func(t *testing.T) {
		if plan.Branch != "main" || plan.Upstream != "" || plan.Force {
			t.Errorf("plan = %+v", plan)
		}
	})
}

func TestLinearize_Refusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		setup  func(t *testing.T, c *Client, dir, origin, merge string) string // returns the hash to plan
		reason string
	}{
		{"a commit with one parent", func(t *testing.T, c *Client, dir, origin, merge string) string {
			return merge + "^1"
		}, "not a merge"},
		{"a merge under the tip", func(t *testing.T, c *Client, dir, origin, merge string) string {
			mustGit(t, dir, "commit", "-q", "--allow-empty", "-m", "later")

			return merge
		}, "no local branch has"},
		{"several branches at the merge, none checked out", func(t *testing.T, c *Client, dir, origin, merge string) string {
			mustGit(t, dir, "branch", "other", merge)
			mustGit(t, dir, "switch", "-q", "--detach", merge+"^1")

			return merge
		}, "several branches"},
		{"an upstream that moved", func(t *testing.T, c *Client, dir, origin, merge string) string {
			mustGit(t, dir, "switch", "-q", "-c", "tmp")
			mustGit(t, dir, "commit", "-q", "--allow-empty", "-m", "theirs")
			mustGit(t, dir, "push", "-q", "origin", "tmp:main")
			mustGit(t, dir, "switch", "-q", "main")

			return merge
		}, "moved"},
		{"an unreachable remote", func(t *testing.T, c *Client, dir, origin, merge string) string {
			if err := os.RemoveAll(origin); err != nil {
				t.Fatal(err)
			}

			return merge
		}, "fetch origin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, dir, origin, merge := mergeRig(t, true)
			hash := tc.setup(t, c, dir, origin, merge)
			_, err := c.LinearizePlan(t.Context(), hash)
			if err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("err = %v, want %q", err, tc.reason)
			}
		})
	}

	t.Run("the checked-out branch wins among several at the merge", func(t *testing.T) {
		t.Parallel()
		c, dir, _, merge := mergeRig(t, true)
		mustGit(t, dir, "branch", "other", merge)
		plan, err := c.LinearizePlan(t.Context(), merge)
		if err != nil || plan.Branch != "main" {
			t.Errorf("plan = %+v, err = %v", plan, err)
		}
	})
}
