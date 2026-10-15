package commit

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	branchcmd "github.com/piprim/git-zf/cmd/branch"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	_ "github.com/piprim/git-zf/tracker/fake"
)

// newOfferRepo is a repo with master and a branch "feat" one commit ahead.
func newOfferRepo(t *testing.T) *git.Client {
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

	c, err := git.NewClientAt(&pkg.IO{In: bytes.NewReader(nil), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	return c
}

func yesForm(context.Context, string, *branchcmd.PullRequestDraft) (bool, error) { return true, nil }

func TestPullRequestOffer(t *testing.T) {
	newCfg := func(typ string) *config.AppConfig {
		cfg := &config.AppConfig{}
		cfg.Branch.Base = "master"
		cfg.IssueTracker.Type = typ

		return cfg
	}

	t.Run("no tracker type: no offer", func(t *testing.T) {
		if pullRequestOffer(newOfferRepo(t), newCfg(""), "feat", yesForm) != nil {
			t.Error("want nil")
		}
	})
	t.Run("the base branch: no offer", func(t *testing.T) {
		if pullRequestOffer(newOfferRepo(t), newCfg("fake"), "master", yesForm) != nil {
			t.Error("want nil")
		}
	})
	t.Run("a feature branch: yes opens the pull request from the prefill", func(t *testing.T) {
		// tracker.New returns a fresh fake: its recorded create is out of reach.
		offer := pullRequestOffer(newOfferRepo(t), newCfg("fake"), "feat", yesForm)
		if offer == nil {
			t.Fatal("want an offer")
		}
		if err := offer.Open(t.Context()); err != nil {
			t.Fatalf("Open: %v", err)
		}
	})
	t.Run("a tracker that cannot be built: Open warns and creates nothing", func(t *testing.T) {
		client := newOfferRepo(t)
		offer := pullRequestOffer(client, newCfg("nope"), "feat", yesForm)
		if offer == nil {
			t.Fatal("want an offer")
		}
		if err := offer.Open(t.Context()); err != nil {
			t.Fatalf("Open: %v", err)
		}
		if got := client.IO().Err.(*bytes.Buffer).String(); !strings.Contains(got, "warning: init tracker") {
			t.Errorf("Err = %q", got)
		}
	})
}

func TestForgeOf(t *testing.T) {
	t.Run("no tracker type: nil", func(t *testing.T) {
		if forgeOf(&config.AppConfig{}, &bytes.Buffer{}) != nil {
			t.Error("want nil")
		}
	})
	t.Run("the fake tracker is a forge", func(t *testing.T) {
		cfg := &config.AppConfig{}
		cfg.IssueTracker.Type = "fake"
		if forgeOf(cfg, &bytes.Buffer{}) == nil {
			t.Error("want a forge")
		}
	})
	t.Run("an unknown tracker type is a warning, nil", func(t *testing.T) {
		cfg := &config.AppConfig{}
		cfg.IssueTracker.Type = "nope"
		errOut := &bytes.Buffer{}
		if forgeOf(cfg, errOut) != nil || errOut.Len() == 0 {
			t.Errorf("forge %v, warning %q", forgeOf(cfg, errOut), errOut.String())
		}
	})
}
