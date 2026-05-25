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

func TestBuildPickerOpts(t *testing.T) {
	t.Parallel()

	t.Run("home only without existing file yields one option", func(t *testing.T) {
		t.Parallel()

		opts := buildPickerOpts("/home/user/.git-zf.toml", "", false, false)
		if len(opts) != 1 {
			t.Fatalf("expected 1 option, got %d", len(opts))
		}
	})

	t.Run("existing home and new repo: home labelled overwrite, repo labelled takes precedence", func(t *testing.T) {
		t.Parallel()

		opts := buildPickerOpts("/home/user/.git-zf.toml", "/repo/.git-zf.toml", true, false)
		if len(opts) != 2 {
			t.Fatalf("expected 2 options, got %d", len(opts))
		}
		if !strings.Contains(opts[0].Key, "[overwrite]") {
			t.Errorf("home option label %q missing [overwrite]", opts[0].Key)
		}
		if !strings.Contains(opts[1].Key, "takes precedence") {
			t.Errorf("repo option label %q missing precedence note", opts[1].Key)
		}
	})

	t.Run("both existing files are labelled overwrite", func(t *testing.T) {
		t.Parallel()

		opts := buildPickerOpts("/home/user/.git-zf.toml", "/repo/.git-zf.toml", true, true)
		if len(opts) != 2 {
			t.Fatalf("expected 2 options, got %d", len(opts))
		}
		if !strings.Contains(opts[0].Key, "[overwrite]") {
			t.Errorf("home option label %q missing [overwrite]", opts[0].Key)
		}
		if !strings.Contains(opts[1].Key, "[overwrite]") {
			t.Errorf("repo option label %q missing [overwrite]", opts[1].Key)
		}
	})
}

func TestWriteHomeDest(t *testing.T) {
	t.Parallel()

	t.Run("writes default TOML bytes and prints destination path", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		dest := filepath.Join(dir, ".git-zf.toml")

		var buf bytes.Buffer
		cmd := &cobra.Command{}
		cmd.SetOut(&buf)

		if err := writeHomeDest(cmd, dest); err != nil {
			t.Fatalf("writeHomeDest: %v", err)
		}

		content, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("read written file: %v", err)
		}
		if !bytes.Equal(content, appconfig.DefaultTOML()) {
			t.Errorf("file content does not match DefaultTOML")
		}
		if !strings.Contains(buf.String(), dest) {
			t.Errorf("expected path %q in output, got: %s", dest, buf.String())
		}
	})
}

func TestPickDest(t *testing.T) {
	t.Parallel()

	t.Run("auto-selects home when outside a repo and home file does not exist", func(t *testing.T) {
		t.Parallel()

		homePath := filepath.Join(t.TempDir(), ".git-zf.toml")
		// repoPath="" simulates being outside a git repo; homePath does not exist.
		dest, err := pickDest(t.Context(), homePath, "")
		if err != nil {
			t.Fatalf("pickDest: %v", err)
		}
		if dest != homePath {
			t.Errorf("dest = %q, want %q", dest, homePath)
		}
	})
}

func TestFileExists(t *testing.T) {
	t.Parallel()

	t.Run("returns true for an existing file", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "exists.toml")
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !fileExists(path) {
			t.Error("fileExists returned false for existing file")
		}
	})

	t.Run("returns false for a missing file", func(t *testing.T) {
		t.Parallel()

		if fileExists(filepath.Join(t.TempDir(), "missing.toml")) {
			t.Error("fileExists returned true for missing file")
		}
	})
}
