package init_cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func newInitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "T"},
		{"config", "user.email", "t@t"},
	} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func runInit(t *testing.T, dir string) string {
	t.Helper()
	t.Chdir(dir)
	cmd := New().GetRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetIn(bytes.NewReader(nil))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v\n%s", err, out.String())
	}
	return out.String()
}

func TestInit_InstallsBothHooks(t *testing.T) {
	dir := newInitRepo(t)
	out := runInit(t, dir)

	t.Run("pre-push hook written", func(t *testing.T) {
		if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", "pre-push")); err != nil {
			t.Fatalf("pre-push missing: %v", err)
		}
	})
	t.Run("pre-commit hook written and calls guard-commit", func(t *testing.T) {
		b, err := os.ReadFile(filepath.Join(dir, ".git", "hooks", "pre-commit"))
		if err != nil {
			t.Fatalf("pre-commit missing: %v", err)
		}
		if !strings.Contains(string(b), "git zf review guard-commit") {
			t.Fatalf("pre-commit does not call guard-commit:\n%s", b)
		}
	})
	t.Run("reports both hooks", func(t *testing.T) {
		if !strings.Contains(out, "pre-push") || !strings.Contains(out, "pre-commit") {
			t.Fatalf("output missing hook names:\n%s", out)
		}
	})
}

func TestInit_Idempotent(t *testing.T) {
	dir := newInitRepo(t)
	runInit(t, dir)
	out := runInit(t, dir)
	t.Run("second run reports up to date", func(t *testing.T) {
		if !strings.Contains(out, "already up to date") {
			t.Fatalf("want up-to-date message, got:\n%s", out)
		}
	})
}

func TestInit_PreservesForeignPreCommit(t *testing.T) {
	dir := newInitRepo(t)
	hookPath := filepath.Join(dir, ".git", "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := "#!/bin/sh\necho custom\n"
	if err := os.WriteFile(hookPath, []byte(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	out := runInit(t, dir)
	t.Run("foreign hook untouched", func(t *testing.T) {
		b, _ := os.ReadFile(hookPath)
		if string(b) != foreign {
			t.Fatalf("foreign hook was overwritten:\n%s", b)
		}
	})
	t.Run("warning with snippet printed", func(t *testing.T) {
		if !strings.Contains(out, "WARNING") || !strings.Contains(out, "guard-commit") {
			t.Fatalf("want warning + snippet, got:\n%s", out)
		}
	})
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}

	return strings.TrimSpace(string(out))
}

func TestInit_ConfiguresChainFetch(t *testing.T) {
	dir := newInitRepo(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitOut(t, dir, "init", "-q", "--bare", origin)
	gitOut(t, dir, "remote", "add", "origin", origin)
	// A tracking ref of the layout used before refs/remotes/<remote>/zf/.
	gitOut(t, dir, "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "init")
	gitOut(t, dir, "update-ref", "refs/zf/remote/issues/abc", "HEAD")

	out := runInit(t, dir)
	runInit(t, dir)

	specs := gitOut(t, dir, "config", "--get-all", "remote.origin.fetch")

	for _, want := range []string{
		"+refs/zf/reviews/*:refs/remotes/origin/zf/reviews/*",
		"+refs/zf/issues/*:refs/remotes/origin/zf/issues/*",
		"+refs/zf/branches/*:refs/remotes/origin/zf/branches/*",
	} {
		t.Run("refspec added exactly once: "+want, func(t *testing.T) {
			if n := strings.Count(specs, want); n != 1 {
				t.Fatalf("refspec appears %d times in:\n%s", n, specs)
			}
		})
	}

	t.Run("the default branch refspec is kept", func(t *testing.T) {
		if !strings.Contains(specs, "+refs/heads/*:refs/remotes/origin/*") {
			t.Fatalf("default refspec lost:\n%s", specs)
		}
	})

	t.Run("stale refs/zf/remote/ tracking refs are deleted", func(t *testing.T) {
		if refs := gitOut(t, dir, "for-each-ref", "refs/zf/remote/"); refs != "" {
			t.Fatalf("stale refs survive:\n%s", refs)
		}
	})

	t.Run("init reports the remote it configured", func(t *testing.T) {
		if !strings.Contains(out, `"origin"`) {
			t.Fatalf("output does not name the remote:\n%s", out)
		}
	})
}

func TestInit_NoRemote_SkipsChainFetch(t *testing.T) {
	dir := newInitRepo(t)
	out := runInit(t, dir)

	t.Run("init succeeds and configures no refspec", func(t *testing.T) {
		if strings.Contains(out, "refs/zf/") {
			t.Fatalf("unexpected refspec message:\n%s", out)
		}
	})
}

func TestInit_ConfiguresEveryRemote(t *testing.T) {
	dir := newInitRepo(t)
	for _, name := range []string{"upstream", "fork"} {
		bare := filepath.Join(t.TempDir(), name+".git")
		gitOut(t, dir, "init", "-q", "--bare", bare)
		gitOut(t, dir, "remote", "add", name, bare)
	}

	out := runInit(t, dir)

	for _, name := range []string{"upstream", "fork"} {
		specs := gitOut(t, dir, "config", "--get-all", "remote."+name+".fetch")

		for _, want := range []string{
			"+refs/zf/reviews/*:refs/remotes/" + name + "/zf/reviews/*",
			"+refs/zf/issues/*:refs/remotes/" + name + "/zf/issues/*",
			"+refs/zf/branches/*:refs/remotes/" + name + "/zf/branches/*",
		} {
			t.Run("remote "+name+" gets "+want, func(t *testing.T) {
				if n := strings.Count(specs, want); n != 1 {
					t.Fatalf("refspec appears %d times in:\n%s", n, specs)
				}
			})
		}

		t.Run("init reports remote "+name, func(t *testing.T) {
			if !strings.Contains(out, `"`+name+`"`) {
				t.Fatalf("output does not name %s:\n%s", name, out)
			}
		})
	}
}

func TestInit_MentionsTheUnusedDatabase(t *testing.T) {
	t.Run("a repository with a leftover git-zf.db is told it can go", func(t *testing.T) {
		dir := newInitRepo(t)
		if err := os.WriteFile(filepath.Join(dir, ".git", "git-zf.db"), []byte("x"), 0o600); err != nil {
			t.Fatalf("write git-zf.db: %v", err)
		}

		out := runInit(t, dir)
		for _, want := range []string{"git-zf.db", "no longer used", "git zf issue track"} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		}
	})

	t.Run("a repository without it is told nothing", func(t *testing.T) {
		if out := runInit(t, newInitRepo(t)); strings.Contains(out, "git-zf.db") {
			t.Errorf("unexpected notice:\n%s", out)
		}
	})
}
