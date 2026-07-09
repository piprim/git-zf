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
