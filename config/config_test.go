package config_test

import (
	"testing"

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
	if cfg.IssueTracker.InProgressStatus != "In Progress" {
		t.Errorf("IssueTracker.InProgressStatus = %q, want %q",
			cfg.IssueTracker.InProgressStatus, "In Progress")
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

func TestLoad_overlay_issueTrackerPreservesDefault(t *testing.T) {
	// Not parallel — modifies global viper state.
	viper.Reset()
	defer viper.Reset()

	// Override type without touching in-progress-status; default must be preserved.
	viper.Set("issue-tracker.type", "redmine")
	viper.Set("issue-tracker.url", "https://example.com")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.IssueTracker.Type != "redmine" {
		t.Errorf("IssueTracker.Type = %q, want %q", cfg.IssueTracker.Type, "redmine")
	}

	if cfg.IssueTracker.InProgressStatus != "In Progress" {
		t.Errorf("IssueTracker.InProgressStatus = %q, want %q",
			cfg.IssueTracker.InProgressStatus, "In Progress")
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
