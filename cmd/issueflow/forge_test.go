package issueflow

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

// newPushedRepo is a repo with an origin, master and "feat" pushed.
func newPushedRepo(t *testing.T) (*git.Client, string) {
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
	run("init", "-q", "-b", "master")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@test.com")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "base.txt")
	run("commit", "-m", "chore: init")
	run("switch", "-c", "feat")
	if err := os.WriteFile(filepath.Join(dir, "feat.txt"), []byte("feat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "feat.txt")
	run("commit", "-m", "feat: x")
	run("switch", "master")
	origin := t.TempDir()
	if out, err := exec.CommandContext(t.Context(), "git", "init", "-q", "--bare", "-b", "master", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	run("remote", "add", "origin", origin)
	run("push", "-q", "origin", "master", "feat")

	c, err := git.NewClientAt(&pkg.IO{In: bytes.NewReader(nil), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	return c, dir
}

func gitAt(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestCheckPushed(t *testing.T) {
	t.Run("a branch at the remote's tip passes", func(t *testing.T) {
		c, _ := newPushedRepo(t)
		if err := CheckPushed(t.Context(), c, "feat"); err != nil {
			t.Errorf("CheckPushed: %v", err)
		}
	})
	t.Run("unpushed commits are refused", func(t *testing.T) {
		c, dir := newPushedRepo(t)
		gitAt(t, dir, "switch", "feat")
		gitAt(t, dir, "commit", "--allow-empty", "-m", "feat: more")
		err := CheckPushed(t.Context(), c, "feat")
		if err == nil || !strings.Contains(err.Error(), "unpushed") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a branch behind the remote is refused", func(t *testing.T) {
		c, dir := newPushedRepo(t)
		gitAt(t, dir, "switch", "feat")
		gitAt(t, dir, "commit", "--allow-empty", "-m", "feat: more")
		gitAt(t, dir, "push", "-q", "origin", "feat")
		gitAt(t, dir, "reset", "-q", "--hard", "HEAD~1")
		err := CheckPushed(t.Context(), c, "feat")
		if err == nil || !strings.Contains(err.Error(), "behind") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a branch not on the remote is refused", func(t *testing.T) {
		c, dir := newPushedRepo(t)
		gitAt(t, dir, "branch", "other", "master")
		err := CheckPushed(t.Context(), c, "other")
		if err == nil || !strings.Contains(err.Error(), "push it first") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestForgePullRequest(t *testing.T) {
	t.Run("no capability: nil, no warning", func(t *testing.T) {
		errOut := &bytes.Buffer{}
		forge, pr := ForgePullRequest(t.Context(), struct{ tracker.Tracker }{&fake.Tracker{}}, errOut, "feat")
		if forge != nil || pr != nil || errOut.Len() != 0 {
			t.Errorf("forge %v, pr %v, warning %q", forge, pr, errOut.String())
		}
	})
	t.Run("no pull request: the forge, no pull request, no warning", func(t *testing.T) {
		errOut := &bytes.Buffer{}
		forge, pr := ForgePullRequest(t.Context(), &fake.Tracker{}, errOut, "feat")
		if forge == nil || pr != nil || errOut.Len() != 0 {
			t.Errorf("forge %v, pr %v, warning %q", forge, pr, errOut.String())
		}
	})
	t.Run("a pull request is returned", func(t *testing.T) {
		ft := &fake.Tracker{PullRequests: []tracker.PullRequest{{Number: "7", Head: "feat", Base: "master"}}}
		forge, pr := ForgePullRequest(t.Context(), ft, &bytes.Buffer{}, "feat")
		if forge == nil || pr == nil || pr.Number != "7" {
			t.Errorf("forge %v, pr %+v", forge, pr)
		}
	})
	t.Run("a forge that cannot be asked is a warning, nil", func(t *testing.T) {
		errOut := &bytes.Buffer{}
		down := &downForge{Tracker: &fake.Tracker{}}
		forge, pr := ForgePullRequest(t.Context(), down, errOut, "feat")
		if forge != nil || pr != nil || !strings.Contains(errOut.String(), "warning") {
			t.Errorf("forge %v, pr %v, warning %q", forge, pr, errOut.String())
		}
	})
}

// downForge has the capability but every call fails.
type downForge struct{ tracker.Tracker }

func (downForge) FindPullRequest(context.Context, string) (tracker.PullRequest, error) {
	return tracker.PullRequest{}, errors.New("forge down")
}

func (downForge) CreatePullRequest(context.Context, string, string, string, string) (tracker.PullRequest, error) {
	return tracker.PullRequest{}, errors.New("forge down")
}

func (downForge) MergePullRequest(context.Context, string, tracker.MergeMethod) error {
	return errors.New("forge down")
}
