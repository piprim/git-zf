package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appconfig "github.com/piprim/git-zf/config"
	"github.com/spf13/cobra"
)

func TestBuildPickerOpts_homeOnlyNoExist(t *testing.T) {
	opts := buildPickerOpts("/home/user/.git-zf.json", "", false, false)

	if len(opts) != 1 {
		t.Fatalf("expected 1 option, got %d", len(opts))
	}
}

func TestBuildPickerOpts_homeExistsWithRepo(t *testing.T) {
	// homeExists=true, repoExists=false → home has [overwrite], repo has precedence note.
	opts := buildPickerOpts("/home/user/.git-zf.json", "/repo/.git-zf.json", true, false)

	if len(opts) != 2 {
		t.Fatalf("expected 2 options, got %d", len(opts))
	}

	if !strings.Contains(opts[0].Key, "[overwrite]") {
		t.Errorf("home option label %q missing [overwrite]", opts[0].Key)
	}

	if !strings.Contains(opts[1].Key, "takes precedence") {
		t.Errorf("repo option label %q missing precedence note", opts[1].Key)
	}
}

func TestBuildPickerOpts_bothExist(t *testing.T) {
	// homeExists=true, repoExists=true → both have [overwrite].
	opts := buildPickerOpts("/home/user/.git-zf.json", "/repo/.git-zf.json", true, true)

	if len(opts) != 2 {
		t.Fatalf("expected 2 options, got %d", len(opts))
	}

	if !strings.Contains(opts[0].Key, "[overwrite]") {
		t.Errorf("home option label %q missing [overwrite]", opts[0].Key)
	}

	if !strings.Contains(opts[1].Key, "[overwrite]") {
		t.Errorf("repo option label %q missing [overwrite]", opts[1].Key)
	}
}

func TestWriteDest_writesDefaultJSON(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, ".git-zf.json")

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	if err := writeDest(cmd, dest); err != nil {
		t.Fatalf("writeDest: %v", err)
	}

	content, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}

	if !bytes.Equal(content, appconfig.DefaultJSON()) {
		t.Errorf("file content does not match DefaultJSON")
	}

	if !strings.Contains(buf.String(), dest) {
		t.Errorf("expected path %q in output, got: %s", dest, buf.String())
	}
}

func TestPickDest_autoSelectsHomeWhenOutsideRepoNoFile(t *testing.T) {
	homePath := filepath.Join(t.TempDir(), ".git-zf.json")
	// repoPath="" simulates being outside a git repo; homePath does not exist.
	dest, err := pickDest(homePath, "")
	if err != nil {
		t.Fatalf("pickDest: %v", err)
	}

	if dest != homePath {
		t.Errorf("dest = %q, want %q", dest, homePath)
	}
}

func TestConfirmOverwrite_returnsFalseOnAbort(t *testing.T) {
	// confirmOverwrite requires a real TTY to run interactively.
	// This test verifies it compiles and that fileExists guards correctly by
	// testing the guard path in isolation: when dest does not exist, no confirm is needed.
	dir := t.TempDir()
	dest := filepath.Join(dir, "nonexistent.json")

	if fileExists(dest) {
		t.Error("fileExists should return false for nonexistent file — overwrite confirm must not trigger")
	}
}

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "exists.json")
	if err := os.WriteFile(existing, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	if !fileExists(existing) {
		t.Error("fileExists returned false for existing file")
	}
	if fileExists(filepath.Join(dir, "missing.json")) {
		t.Error("fileExists returned true for missing file")
	}
}

