package log

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
)

func newLogRig(t *testing.T) (*git.Client, *bytes.Buffer, string) {
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
	run("init", "-q", "-b", "main")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@test.com")
	run("config", "commit.gpgsign", "false")
	for i, name := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", name+".txt")
		run("commit", "-q", "-m", []string{"chore: init", "feat: second commit"}[i])
	}

	out := &bytes.Buffer{}
	client, err := git.NewClientAt(&pkg.IO{In: strings.NewReader(""), Out: out, Err: io.Discard}, dir)
	if err != nil {
		t.Fatal(err)
	}

	return client, out, dir
}

// defaults is the built-in log configuration.
func defaults(t *testing.T) config.LogConfig {
	t.Helper()

	cfg, err := config.Load(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil {
		t.Fatal(err)
	}

	return cfg.Log
}

func TestRunLog_NoTTY(t *testing.T) {
	t.Parallel()

	client, out, _ := newLogRig(t)
	if err := runLog(t.Context(), client, defaults(t), []string{"-n", "1"}, false); err != nil {
		t.Fatalf("runLog: %v", err)
	}

	t.Run("prints the default command's graph to stdout like git log", func(t *testing.T) {
		if got := out.String(); !strings.HasPrefix(got, "* ") || !strings.Contains(got, "feat: second commit") {
			t.Errorf("out = %q", got)
		}
	})

	t.Run("extra arguments reach git log", func(t *testing.T) {
		if strings.Contains(out.String(), "chore: init") {
			t.Errorf("out = %q", out.String())
		}
	})

	t.Run("default-cmd picks the command", func(t *testing.T) {
		client, out, _ := newLogRig(t)
		cfg := defaults(t)
		cfg.DefaultCmd = "linear"
		if err := runLog(t.Context(), client, cfg, nil, false); err != nil {
			t.Fatalf("runLog: %v", err)
		}
		if got := out.String(); strings.HasPrefix(got, "* ") || !strings.Contains(got, "chore: init") {
			t.Errorf("out = %q", got)
		}
	})
}

func TestGetRootCmd(t *testing.T) {
	cmd := New(&config.AppConfig{}).GetRootCmd()

	t.Run("is named log", func(t *testing.T) {
		if cmd.Name() != "log" {
			t.Errorf("name = %q", cmd.Name())
		}
	})

	t.Run("accepts git log arguments after --", func(t *testing.T) {
		if err := cmd.ValidateArgs([]string{"-n", "5", "main..feature"}); err != nil {
			t.Errorf("ValidateArgs: %v", err)
		}
	})
}

func TestPlanLinearize(t *testing.T) {
	t.Parallel()

	client, _, dir := newLogRig(t)
	run := func(args ...string) string {
		t.Helper()

		out, err := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}

		return strings.TrimSpace(string(out))
	}

	t.Run("a plain commit is refused", func(t *testing.T) {
		if _, err := planLinearize(t.Context(), client, run("rev-parse", "--short", "HEAD")); err == nil {
			t.Error("planLinearize accepted a commit of one parent")
		}
	})

	run("switch", "-q", "-c", "side")
	run("commit", "-q", "--allow-empty", "-m", "feat: side")
	run("switch", "-q", "main")
	run("merge", "-q", "--no-ff", "side", "-m", "merge side")
	merge := run("rev-parse", "--short", "HEAD")
	l, err := planLinearize(t.Context(), client, merge)
	if err != nil {
		t.Fatalf("planLinearize: %v", err)
	}

	t.Run("the question names the merge and the branch, with no push without an upstream", func(t *testing.T) {
		if l.Confirm != "Linearize "+merge+" on main?" || l.PushConfirm != "" || l.Push != nil {
			t.Errorf("plan = %+v", l)
		}
	})

	t.Run("applying says what moved", func(t *testing.T) {
		text, err := l.Apply()
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if want := "main: " + merge + " → " + run("rev-parse", "--short", "main") + " (the old tip is in the reflog)"; text != want {
			t.Errorf("text = %q, want %q", text, want)
		}
		if got := run("rev-list", "--count", "main"); got != "3" {
			t.Errorf("main holds %s commits, want 3", got)
		}
	})
}
