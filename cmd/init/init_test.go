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
	// init writes where git reads hooks: keep a core.hooksPath of the
	// developer's own configuration out of the tests.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
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

// writeHook writes a hook file in dir and returns its path.
func writeHook(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}

	return path
}

func TestInit_InstallsNoHook(t *testing.T) {
	dir := newInitRepo(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitOut(t, dir, "init", "-q", "--bare", origin)
	gitOut(t, dir, "remote", "add", "origin", origin)

	runInit(t, dir)

	for _, name := range []string{"pre-push", "pre-commit", "pre-rebase"} {
		t.Run("no "+name+" hook is written", func(t *testing.T) {
			if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", name)); err == nil {
				t.Fatalf("%s was installed", name)
			}
		})
	}
	t.Run("the fetch refspecs are configured", func(t *testing.T) {
		specs := gitOut(t, dir, "config", "--get-all", "remote.origin.fetch")
		if !strings.Contains(specs, "+refs/zf/reviews/*:refs/remotes/origin/zf/reviews/*") {
			t.Fatalf("review refspec missing:\n%s", specs)
		}
	})
}

func TestInit_DeletesRetiredHooks(t *testing.T) {
	dir := newInitRepo(t)
	hooksDir := filepath.Join(dir, ".git", "hooks")
	for _, h := range retiredHooks {
		writeHook(t, hooksDir, h.name, h.script)
	}

	out := runInit(t, dir)

	for _, h := range retiredHooks {
		t.Run(h.name+" is deleted", func(t *testing.T) {
			if _, err := os.Stat(filepath.Join(hooksDir, h.name)); err == nil {
				t.Fatalf("%s survives", h.name)
			}
		})
		t.Run(h.name+" is named on stdout", func(t *testing.T) {
			if !strings.Contains(out, filepath.Join(hooksDir, h.name)) {
				t.Fatalf("output does not name %s:\n%s", h.name, out)
			}
		})
	}
	t.Run("a second run says nothing about hooks", func(t *testing.T) {
		if again := runInit(t, dir); strings.Contains(again, "hook") {
			t.Fatalf("unexpected hook message:\n%s", again)
		}
	})
}

func TestInit_KeepsForeignHook(t *testing.T) {
	t.Run("a hook that never called git zf is untouched and not mentioned", func(t *testing.T) {
		dir := newInitRepo(t)
		foreign := "#!/bin/sh\necho custom\n"
		hookPath := writeHook(t, filepath.Join(dir, ".git", "hooks"), "pre-commit", foreign)

		out := runInit(t, dir)

		if b, _ := os.ReadFile(hookPath); string(b) != foreign {
			t.Fatalf("foreign hook was changed:\n%s", b)
		}
		if strings.Contains(out, "pre-commit") {
			t.Fatalf("unexpected message:\n%s", out)
		}
	})

	t.Run("a hook that calls the guard is untouched, the call can be removed", func(t *testing.T) {
		dir := newInitRepo(t)
		foreign := "#!/bin/sh\necho custom\ngit zf review guard-commit || exit 1\n"
		hookPath := writeHook(t, filepath.Join(dir, ".git", "hooks"), "pre-commit", foreign)

		out := runInit(t, dir)

		if b, _ := os.ReadFile(hookPath); string(b) != foreign {
			t.Fatalf("foreign hook was changed:\n%s", b)
		}
		if !strings.Contains(out, hookPath) || !strings.Contains(out, "can be removed") {
			t.Fatalf("want a note that the call can be removed, got:\n%s", out)
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

func TestInit_LinkedWorktree_DeletesRetiredHooksInCommonDir(t *testing.T) {
	dir := newInitRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "commit", "-q", "--allow-empty", "-m", "root")
	gitOut(t, dir, "worktree", "add", "-q", "-b", "feature", wt)
	for _, h := range retiredHooks {
		writeHook(t, filepath.Join(dir, ".git", "hooks"), h.name, h.script)
	}

	out := runInit(t, wt)

	for _, h := range retiredHooks {
		t.Run(h.name+" deleted from the common git dir", func(t *testing.T) {
			if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", h.name)); err == nil {
				t.Fatalf("%s survives in the common git dir\n%s", h.name, out)
			}
		})
	}
}

func TestInit_HooksPath(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "shared-hooks")

	cases := []struct {
		name      string
		hooksPath string
		worktree  bool // run init from a linked worktree
		wantDir   func(repo, wt string) string
	}{
		{
			name:      "relative path: under the working tree root",
			hooksPath: ".githooks",
			wantDir:   func(repo, _ string) string { return filepath.Join(repo, ".githooks") },
		},
		{
			name:      "absolute path: outside the repository",
			hooksPath: outside,
			wantDir:   func(_, _ string) string { return outside },
		},
		{
			name:      "relative path in a linked worktree: under that worktree's root",
			hooksPath: ".githooks",
			worktree:  true,
			wantDir:   func(_, wt string) string { return filepath.Join(wt, ".githooks") },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newInitRepo(t)
			wt := filepath.Join(t.TempDir(), "wt")
			gitOut(t, repo, "config", "core.hooksPath", tc.hooksPath)

			from := repo
			if tc.worktree {
				gitOut(t, repo, "commit", "-q", "--allow-empty", "-m", "root")
				gitOut(t, repo, "worktree", "add", "-q", "-b", "feature", wt)
				from = wt
			}
			// The same script in .git/hooks, which git no longer reads: it
			// is not init's to delete.
			for _, h := range retiredHooks {
				writeHook(t, tc.wantDir(repo, wt), h.name, h.script)
				writeHook(t, filepath.Join(repo, ".git", "hooks"), h.name, h.script)
			}

			out := runInit(t, from)

			for _, h := range retiredHooks {
				t.Run(h.name+" deleted from core.hooksPath", func(t *testing.T) {
					if _, err := os.Stat(filepath.Join(tc.wantDir(repo, wt), h.name)); err == nil {
						t.Fatalf("%s survives in core.hooksPath\n%s", h.name, out)
					}
				})
				t.Run(h.name+" left in .git/hooks, which git no longer reads", func(t *testing.T) {
					if _, err := os.Stat(filepath.Join(repo, ".git", "hooks", h.name)); err != nil {
						t.Fatalf("%s deleted outside the hooks path: %v", h.name, err)
					}
				})
			}
		})
	}
}
