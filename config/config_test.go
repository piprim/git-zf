package config_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml"

	"github.com/piprim/git-zf/config"
)

// writeTOML writes blob to a fresh temp file and returns its path.
func writeTOML(t *testing.T, blob string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), ".git-zf.toml")
	if err := os.WriteFile(path, []byte(blob), 0o600); err != nil {
		t.Fatalf("write cfg: %v", err)
	}

	return path
}

func TestLoad(t *testing.T) {
	t.Parallel()

	t.Run("returns built-in defaults when no file exists", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.Load(filepath.Join(t.TempDir(), "missing.toml"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		if len(cfg.CommitTypes) != 10 {
			t.Errorf("CommitTypes len = %d, want 10", len(cfg.CommitTypes))
		}
		if cfg.CommitTypes[0].Name != "feat" {
			t.Errorf("CommitTypes[0].Name = %q, want %q", cfg.CommitTypes[0].Name, "feat")
		}
		if cfg.CommitMessage.Template == "" {
			t.Error("CommitMessage.Template is empty")
		}
		if len(cfg.CommitMessage.Items) != 4 {
			t.Errorf("CommitMessage.Items len = %d, want 4", len(cfg.CommitMessage.Items))
		}
		if cfg.CommitMessage.RefFormat != "Refs #%s" {
			t.Errorf("CommitMessage.RefFormat = %q, want %q", cfg.CommitMessage.RefFormat, "Refs #%s")
		}
		if cfg.CommitMessage.CloseFormat != "Closes #%s" {
			t.Errorf("CommitMessage.CloseFormat = %q, want %q", cfg.CommitMessage.CloseFormat, "Closes #%s")
		}
		if cfg.IssueTracker.Type != "" {
			t.Errorf("IssueTracker.Type = %q, want empty", cfg.IssueTracker.Type)
		}
		if cfg.Branch.Remote != "" {
			t.Errorf("Branch.Remote default = %q, want empty string", cfg.Branch.Remote)
		}
		if cfg.Branch.UseWorktree != nil {
			t.Errorf("Branch.UseWorktree default = %v, want nil", *cfg.Branch.UseWorktree)
		}
		if cfg.ConfigFile != "" {
			t.Errorf("ConfigFile = %q, want empty when no file is read", cfg.ConfigFile)
		}
	})

	t.Run("a file's commit-types replaces the defaults wholesale", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.Load(writeTOML(t, `
[[commit-types]]
name = "custom"
desc = "Custom type"
`))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		if len(cfg.CommitTypes) != 1 {
			t.Errorf("CommitTypes len = %d, want 1", len(cfg.CommitTypes))
		}
		if cfg.CommitTypes[0].Name != "custom" {
			t.Errorf("CommitTypes[0].Name = %q, want %q", cfg.CommitTypes[0].Name, "custom")
		}
		if cfg.CommitMessage.Template == "" {
			t.Error("CommitMessage.Template should remain from defaults")
		}
	})

	t.Run("preserves template when only items is overridden", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.Load(writeTOML(t, `
[[commit-message.items]]
name = "subject"
desc = "Custom subject:"
form = "input"
required = true
`))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		if len(cfg.CommitMessage.Items) != 1 {
			t.Errorf("CommitMessage.Items len = %d, want 1", len(cfg.CommitMessage.Items))
		}
		if cfg.CommitMessage.Template == "" {
			t.Error("CommitMessage.Template must be preserved when only items is overridden")
		}
	})

	t.Run("reads projects list from a TOML config file", func(t *testing.T) {
		t.Parallel()

		cfgPath := writeTOML(t, `
[issue-tracker]
type = "github"
url = "https://api.github.com"
token = "x"
projects = ["a/b", "c/d"]
`)

		cfg, err := config.Load(cfgPath)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		want := []string{"a/b", "c/d"}
		if !slices.Equal(cfg.IssueTracker.Projects, want) {
			t.Errorf("Projects = %v, want %v", cfg.IssueTracker.Projects, want)
		}

		t.Run("records the source path in ConfigFile", func(t *testing.T) {
			if cfg.ConfigFile != cfgPath {
				t.Errorf("ConfigFile = %q, want %q", cfg.ConfigFile, cfgPath)
			}
		})
	})

	t.Run("merges global and local TOML files, local wins", func(t *testing.T) {
		t.Parallel()

		globalPath := writeTOML(t, `
[[commit-types]]
name = "custom"
desc = "Custom type"

[branch]
remote = "origin"
base = "develop"
`)
		localPath := writeTOML(t, `
[branch]
remote = "upstream"

[issue-tracker]
type = "redmine"
url = "https://redmine.example.com"
token = "tok"
`)

		cfg, err := config.Load(globalPath, localPath)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		t.Run("commit-types from global replace the defaults", func(t *testing.T) {
			if len(cfg.CommitTypes) != 1 || cfg.CommitTypes[0].Name != "custom" {
				t.Errorf("CommitTypes = %+v, want one entry named custom", cfg.CommitTypes)
			}
		})
		t.Run("issue-tracker from local", func(t *testing.T) {
			if cfg.IssueTracker.Type != "redmine" || cfg.IssueTracker.URL != "https://redmine.example.com" {
				t.Errorf("IssueTracker = %+v", cfg.IssueTracker)
			}
		})
		t.Run("local scalar overrides global scalar", func(t *testing.T) {
			if cfg.Branch.Remote != "upstream" {
				t.Errorf("Branch.Remote = %q, want %q", cfg.Branch.Remote, "upstream")
			}
		})
		t.Run("global scalar survives when local leaves it out", func(t *testing.T) {
			if cfg.Branch.Base != "develop" {
				t.Errorf("Branch.Base = %q, want %q", cfg.Branch.Base, "develop")
			}
		})
		t.Run("template preserved from built-in default", func(t *testing.T) {
			if cfg.CommitMessage.Template == "" {
				t.Error("CommitMessage.Template should be preserved from built-in default")
			}
		})
		t.Run("ConfigFile is the last file read", func(t *testing.T) {
			if cfg.ConfigFile != localPath {
				t.Errorf("ConfigFile = %q, want %q", cfg.ConfigFile, localPath)
			}
		})
	})

	t.Run("a missing file between two existing ones is skipped", func(t *testing.T) {
		t.Parallel()

		first := writeTOML(t, "[branch]\nremote = \"a\"\n")
		last := writeTOML(t, "[branch]\nbase = \"b\"\n")

		cfg, err := config.Load(first, filepath.Join(t.TempDir(), "nope.toml"), last)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Branch.Remote != "a" || cfg.Branch.Base != "b" {
			t.Errorf("Branch = %+v, want remote a / base b", cfg.Branch)
		}
	})

	t.Run("ref-format and close-format are loaded", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.Load(writeTOML(t, `
[commit-message]
ref-format = "Refs: %s"
close-format = "Closes #%s"
`))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		t.Run("ref-format", func(t *testing.T) {
			if cfg.CommitMessage.RefFormat != "Refs: %s" {
				t.Errorf("RefFormat = %q, want %q", cfg.CommitMessage.RefFormat, "Refs: %s")
			}
		})
		t.Run("close-format", func(t *testing.T) {
			if cfg.CommitMessage.CloseFormat != "Closes #%s" {
				t.Errorf("CloseFormat = %q, want %q", cfg.CommitMessage.CloseFormat, "Closes #%s")
			}
		})
	})

	t.Run("branch.use-worktree is a tri-state", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.Load(writeTOML(t, "[branch]\nuse-worktree = false\n"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Branch.UseWorktree == nil || *cfg.Branch.UseWorktree {
			t.Errorf("Branch.UseWorktree = %v, want pointer to false", cfg.Branch.UseWorktree)
		}
	})

	t.Run("malformed file is an error naming the path", func(t *testing.T) {
		t.Parallel()

		path := writeTOML(t, "this is = not [toml")
		_, err := config.Load(path)
		if err == nil {
			t.Fatal("Load: want error, got nil")
		}
		if got := err.Error(); !strings.Contains(got, path) {
			t.Errorf("error %q does not name %q", got, path)
		}
	})
}

func TestLoadPushPropose(t *testing.T) {
	t.Parallel()

	t.Run("defaults to true", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !cfg.Push.Propose {
			t.Fatalf("Push.Propose = false, want true (default)")
		}
	})

	t.Run("can be overridden to false", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.Load(writeTOML(t, "[push]\npropose = false\n"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Push.Propose {
			t.Fatalf("Push.Propose = true, want false (override)")
		}
	})
}

func TestDefaultTOML_isValidTOML(t *testing.T) {
	t.Parallel()

	b := config.DefaultTOML()
	if len(b) == 0 {
		t.Fatal("DefaultTOML returned empty bytes")
	}

	var v map[string]any
	if err := toml.Unmarshal(b, &v); err != nil {
		t.Fatalf("DefaultTOML is not valid TOML: %v", err)
	}

	if _, ok := v["commit-types"]; !ok {
		t.Error("DefaultTOML missing 'commit-types' key")
	}
}

func TestLoadReviewRequireSigned(t *testing.T) {
	t.Parallel()

	t.Run("defaults to false", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Review.RequireSigned {
			t.Fatalf("Review.RequireSigned = true, want false (default)")
		}
	})

	t.Run("can be set to true", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.Load(writeTOML(t, "[review]\nrequire-signed = true\n"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !cfg.Review.RequireSigned {
			t.Fatalf("Review.RequireSigned = false, want true (override)")
		}
	})
}
