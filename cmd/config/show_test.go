package config

import (
	"bytes"
	"strings"
	"testing"

	appconfig "github.com/piprim/git-zf/config"
)

func TestToConfigOutput(t *testing.T) {
	t.Parallel()

	t.Run("masks non-empty token with asterisks", func(t *testing.T) {
		t.Parallel()

		cfg := &appconfig.AppConfig{
			IssueTracker: appconfig.IssueTrackerConfig{
				Type:  "plane",
				URL:   "https://plane.example.com",
				Token: "super-secret",
			},
		}

		out := toConfigOutput(cfg)

		if out.IssueTracker.Token != "***" {
			t.Errorf("token = %q, want %q", out.IssueTracker.Token, "***")
		}
		if out.IssueTracker.Type != "plane" {
			t.Errorf("type = %q, want %q", out.IssueTracker.Type, "plane")
		}
	})

	t.Run("leaves empty token unchanged", func(t *testing.T) {
		t.Parallel()

		out := toConfigOutput(&appconfig.AppConfig{})

		if out.IssueTracker.Token != "" {
			t.Errorf("empty token should stay empty, got %q", out.IssueTracker.Token)
		}
	})

	t.Run("excludes ProgName from marshalled output", func(t *testing.T) {
		t.Parallel()

		cfg := &appconfig.AppConfig{ProgName: "git-zf"}
		out := toConfigOutput(cfg)

		b, err := marshalConfig(&out)
		if err != nil {
			t.Fatalf("marshalConfig: %v", err)
		}
		if strings.Contains(string(b), "ProgName") {
			t.Errorf("output must not contain ProgName, got: %s", b)
		}
	})
}

func TestShowRunE_printsConfigFileLine(t *testing.T) {
	cfg := &appconfig.AppConfig{
		CommitTypes: []appconfig.CommitTypeOption{{Name: "feat", Desc: "A new feature"}},
	}
	c := Config{appConfig: cfg}

	var buf bytes.Buffer
	cmd := c.getShowCmd()
	cmd.SetOut(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("showRunE: %v", err)
	}

	out := buf.String()
	// appConfig.ConfigFile is empty here (cfg built without it) — expect the
	// "no config file" line.
	if !strings.Contains(out, "no config file found") {
		t.Errorf("expected 'no config file found' in output, got:\n%s", out)
	}
	if !strings.Contains(out, `"commit-types"`) {
		t.Errorf("expected commit-types in JSON output, got:\n%s", out)
	}
}
