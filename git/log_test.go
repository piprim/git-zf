package git

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/piprim/git-zf/internal/pkg"
)

// newLogRepo is newDiskRepo plus a second commit and a commit reachable only
// from a git-zf ref.
func newLogRepo(t *testing.T) (*Client, string) {
	t.Helper()

	client, dir := newDiskRepo(t)

	run := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "file.txt")
	run("commit", "-m", "feat: second commit")

	run("checkout", "-b", "side")
	if err := os.WriteFile(filepath.Join(dir, "zf.txt"), []byte("zf\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "zf.txt")
	run("commit", "-m", "chore: zf only")
	run("update-ref", "refs/zf/issues/abc", "side")
	run("checkout", "-")
	run("branch", "-D", "side")

	return client, dir
}

// graphArgs is a short form of the default graph command of `git zf log`.
var graphArgs = []string{"log", "--exclude=*zf/*", "--graph", "--oneline", "--all", "--decorate",
	"--pretty=tformat:%Cred%h%Creset - %s", "--date-order"}

func TestLog(t *testing.T) {
	t.Parallel()

	client, _ := newLogRepo(t)

	lines, err := client.Log(t.Context(), graphArgs...)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	all := strings.Join(lines, "\n")

	t.Run("lists every commit newest first", func(t *testing.T) {
		if len(lines) != 2 {
			t.Fatalf("lines = %q", lines)
		}
		if !strings.Contains(lines[0], "feat: second commit") || !strings.Contains(lines[1], "chore: init") {
			t.Errorf("lines = %q", lines)
		}
	})

	t.Run("hides commits reachable only from git-zf refs", func(t *testing.T) {
		if strings.Contains(all, "zf only") {
			t.Errorf("lines = %q", lines)
		}
	})

	t.Run("lines carry git's colors and the graph", func(t *testing.T) {
		if !strings.Contains(lines[0], "\x1b[") || !strings.HasPrefix(lines[0], "*") {
			t.Errorf("line = %q", lines[0])
		}
	})

	t.Run("extra arguments reach git log", func(t *testing.T) {
		one, err := client.Log(t.Context(), append(graphArgs, "-n", "1")...)
		if err != nil {
			t.Fatal(err)
		}
		if len(one) != 1 {
			t.Errorf("lines = %q", one)
		}
	})

	t.Run("a bad argument is an error carrying git's message", func(t *testing.T) {
		_, err := client.Log(t.Context(), "log", "--no-such-flag")
		if err == nil || !strings.Contains(err.Error(), "no-such-flag") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestShowCommit(t *testing.T) {
	t.Parallel()

	client, dir := newLogRepo(t)

	head, err := exec.CommandContext(t.Context(), "git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}

	out, err := client.ShowCommit(t.Context(), strings.TrimSpace(string(head)))
	if err != nil {
		t.Fatalf("ShowCommit: %v", err)
	}

	t.Run("shows the patch and stat of that commit only", func(t *testing.T) {
		plain := ansi.Strip(out)
		if !strings.Contains(plain, "+hello") || !strings.Contains(plain, "file.txt | 1 +") {
			t.Errorf("out = %q", plain)
		}
		if strings.Contains(plain, "chore: init") || strings.Contains(plain, "base.go") {
			t.Errorf("out = %q", plain)
		}
	})

	t.Run("output is colored", func(t *testing.T) {
		if !strings.Contains(out, "\x1b[") {
			t.Errorf("out = %q", out)
		}
	})

	t.Run("a merge shows what it brought onto the branch", func(t *testing.T) {
		run := func(args ...string) {
			t.Helper()
			cmd := exec.CommandContext(t.Context(), "git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		run("checkout", "-b", "topic")
		if err := os.WriteFile(filepath.Join(dir, "merged.txt"), []byte("merged\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", "merged.txt")
		run("commit", "-m", "feat: on topic")
		run("checkout", "-")
		run("merge", "--no-ff", "-m", "Merge branch 'topic'", "topic")

		out, err := client.ShowCommit(t.Context(), "HEAD")
		if err != nil {
			t.Fatalf("ShowCommit: %v", err)
		}
		plain := ansi.Strip(out)
		if !strings.Contains(plain, "+merged") || !strings.Contains(plain, "merged.txt | 1 +") {
			t.Errorf("out = %q", plain)
		}
	})

	t.Run("a file named like the hash does not make it ambiguous", func(t *testing.T) {
		h := strings.TrimSpace(string(head))
		if err := os.WriteFile(filepath.Join(dir, h), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := client.ShowCommit(t.Context(), h); err != nil {
			t.Errorf("ShowCommit: %v", err)
		}
	})

	t.Run("an unknown hash is an error", func(t *testing.T) {
		if _, err := client.ShowCommit(t.Context(), "deadbeef"); err == nil {
			t.Error("expected an error")
		}
	})
}

func TestPrintLog(t *testing.T) {
	t.Parallel()

	client, _ := newLogRepo(t)
	var out bytes.Buffer
	client.io = &pkg.IO{In: strings.NewReader(""), Out: &out, Err: io.Discard}

	if err := client.PrintLog(t.Context(), append(graphArgs, "-n", "1")...); err != nil {
		t.Fatalf("PrintLog: %v", err)
	}

	t.Run("writes the graph to the client's output, honoring extra arguments", func(t *testing.T) {
		if got := out.String(); !strings.Contains(got, "feat: second commit") || strings.Contains(got, "chore: init") {
			t.Errorf("out = %q", got)
		}
	})

	t.Run("the output ends with a newline", func(t *testing.T) {
		if !strings.HasSuffix(out.String(), "\n") {
			t.Errorf("out = %q", out.String())
		}
	})

	t.Run("a pipe gets no colors", func(t *testing.T) {
		if strings.Contains(out.String(), "\x1b[") {
			t.Errorf("out = %q", out.String())
		}
	})

	t.Run("an explicit --color=always is honored", func(t *testing.T) {
		var forced bytes.Buffer
		client.io = &pkg.IO{In: strings.NewReader(""), Out: &forced, Err: io.Discard}
		if err := client.PrintLog(t.Context(), append(graphArgs, "--color=always")...); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(forced.String(), "\x1b[") {
			t.Errorf("out = %q", forced.String())
		}
	})
}
