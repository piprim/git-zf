package config

import (
	"bytes"
	"strings"
	"testing"

	appconfig "github.com/piprim/git-zf/config"
)

func TestMaskedJSON(t *testing.T) {
	t.Parallel()

	render := func(t *testing.T, cfg *appconfig.AppConfig) string {
		t.Helper()

		b, err := maskedJSON(cfg)
		if err != nil {
			t.Fatalf("maskedJSON: %v", err)
		}

		return string(b)
	}

	t.Run("masks a non-empty token and leaves the caller's config untouched", func(t *testing.T) {
		t.Parallel()

		cfg := &appconfig.AppConfig{
			IssueTracker: appconfig.IssueTrackerConfig{
				Type:  "plane",
				URL:   "https://plane.example.com",
				Token: "super-secret",
			},
		}

		out := render(t, cfg)

		if strings.Contains(out, "super-secret") {
			t.Errorf("output leaks the token:\n%s", out)
		}
		if !strings.Contains(out, `"token": "***"`) {
			t.Errorf("output has no masked token:\n%s", out)
		}
		if !strings.Contains(out, `"type": "plane"`) {
			t.Errorf("output lost the tracker type:\n%s", out)
		}
		if cfg.IssueTracker.Token != "super-secret" {
			t.Errorf("config token = %q after rendering, want it unchanged", cfg.IssueTracker.Token)
		}
	})

	t.Run("leaves an empty token empty", func(t *testing.T) {
		t.Parallel()

		if out := render(t, &appconfig.AppConfig{}); !strings.Contains(out, `"token": ""`) {
			t.Errorf("empty token should stay empty, got:\n%s", out)
		}
	})

	t.Run("excludes ProgName and ConfigFile from the output", func(t *testing.T) {
		t.Parallel()

		out := render(t, &appconfig.AppConfig{ProgName: "git-zf", ConfigFile: "/tmp/zf.toml"})

		for _, hidden := range []string{"ProgName", "git-zf", "ConfigFile", "/tmp/zf.toml"} {
			if strings.Contains(out, hidden) {
				t.Errorf("output must not contain %q, got:\n%s", hidden, out)
			}
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
