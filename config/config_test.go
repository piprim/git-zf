package config_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	toml "github.com/pelletier/go-toml"
	"github.com/spf13/viper"

	"github.com/piprim/git-zf/config"
)

func TestLoad_defaults(t *testing.T) {
	// Not parallel — modifies global viper state.
	viper.Reset()
	defer viper.Reset()

	cfg, err := config.Load()
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
	if cfg.IssueTracker.Type != "" {
		t.Errorf("IssueTracker.Type = %q, want empty", cfg.IssueTracker.Type)
	}
}

func TestLoad_overlay(t *testing.T) {
	// Not parallel — modifies global viper state.
	viper.Reset()
	defer viper.Reset()

	viper.Set("commit-types", []map[string]any{
		{"name": "custom", "desc": "Custom type"},
	})

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.CommitTypes) != 1 {
		t.Errorf("CommitTypes len = %d, want 1", len(cfg.CommitTypes))
	}
	if cfg.CommitTypes[0].Name != "custom" {
		t.Errorf("CommitTypes[0].Name = %q, want %q", cfg.CommitTypes[0].Name, "custom")
	}
	// Unset keys fall back to defaults.
	if cfg.CommitMessage.Template == "" {
		t.Error("CommitMessage.Template should remain from defaults")
	}
}

func TestLoad_overlay_partialCommitMessage(t *testing.T) {
	// Not parallel — modifies global viper state.
	viper.Reset()
	defer viper.Reset()

	// Override only items (not template); template must be preserved from defaults.
	viper.Set("commit-message.items", []map[string]any{
		{"name": "subject", "desc": "Custom subject:", "form": "input", "required": true},
	})

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.CommitMessage.Items) != 1 {
		t.Errorf("CommitMessage.Items len = %d, want 1", len(cfg.CommitMessage.Items))
	}

	if cfg.CommitMessage.Template == "" {
		t.Error("CommitMessage.Template must be preserved when only items is overridden")
	}
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

func TestLoad_projects(t *testing.T) {
	// Not parallel — modifies global viper state.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".git-zf.toml")

	const blob = `
[issue-tracker]
type = "github"
url = "https://api.github.com"
token = "x"
projects = ["a/b", "c/d"]
`
	if err := os.WriteFile(cfgPath, []byte(blob), 0o600); err != nil {
		t.Fatalf("write cfg: %v", err)
	}

	v := viper.New()
	v.SetConfigType("toml")
	v.SetConfigFile(cfgPath)
	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("read cfg: %v", err)
	}

	// Swap the global viper used by config.Load() with our local instance.
	viper.Reset()
	defer viper.Reset()
	for _, k := range v.AllKeys() {
		viper.Set(k, v.Get(k))
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := []string{"a/b", "c/d"}
	if !slices.Equal(cfg.IssueTracker.Projects, want) {
		t.Errorf("Projects = %v, want %v", cfg.IssueTracker.Projects, want)
	}
}

func TestLoad_twoFileMerge(t *testing.T) {
	// Not parallel — modifies global viper state.
	viper.Reset()
	defer viper.Reset()

	globalTOML := []byte(`
[[commit-types]]
name = "custom"
desc = "Custom type"
`)

	localTOML := []byte(`
[issue-tracker]
type = "redmine"
url = "https://redmine.example.com"
token = "tok"
`)

	globalPath := filepath.Join(t.TempDir(), ".git-zf.toml")
	localPath := filepath.Join(t.TempDir(), ".git-zf.toml")

	if err := os.WriteFile(globalPath, globalTOML, 0o600); err != nil {
		t.Fatalf("write global: %v", err)
	}

	if err := os.WriteFile(localPath, localTOML, 0o600); err != nil {
		t.Fatalf("write local: %v", err)
	}

	viper.SetConfigType("toml")
	viper.SetConfigFile(globalPath)

	if err := viper.ReadInConfig(); err != nil {
		t.Fatalf("ReadInConfig global: %v", err)
	}

	viper.SetConfigFile(localPath)

	if err := viper.MergeInConfig(); err != nil {
		t.Fatalf("MergeInConfig local: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// commit-types from global override (replaces built-in defaults).
	if len(cfg.CommitTypes) != 1 {
		t.Errorf("CommitTypes len = %d, want 1", len(cfg.CommitTypes))
	}

	if cfg.CommitTypes[0].Name != "custom" {
		t.Errorf("CommitTypes[0].Name = %q, want %q", cfg.CommitTypes[0].Name, "custom")
	}

	// issue-tracker from local.
	if cfg.IssueTracker.Type != "redmine" {
		t.Errorf("IssueTracker.Type = %q, want %q", cfg.IssueTracker.Type, "redmine")
	}

	if cfg.IssueTracker.URL != "https://redmine.example.com" {
		t.Errorf("IssueTracker.URL = %q, want %q", cfg.IssueTracker.URL, "https://redmine.example.com")
	}

	// template preserved from built-in default (neither file sets it).
	if cfg.CommitMessage.Template == "" {
		t.Error("CommitMessage.Template should be preserved from built-in default")
	}
}
