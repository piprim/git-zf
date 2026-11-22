package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/config"
)

func TestLoad_UI(t *testing.T) {
	t.Parallel()

	t.Run("defaults", func(t *testing.T) {
		cfg, err := config.Load(filepath.Join(t.TempDir(), "missing.toml"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if want := (config.UIConfig{Split: true, Size: 50}); cfg.UI != want {
			t.Errorf("UI = %+v, want %+v", cfg.UI, want)
		}
	})

	t.Run("split = false keeps the size", func(t *testing.T) {
		cfg, err := config.Load(writeTOML(t, "[ui]\nsplit = false\n"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if want := (config.UIConfig{Split: false, Size: 50}); cfg.UI != want {
			t.Errorf("UI = %+v, want %+v", cfg.UI, want)
		}
	})

	t.Run("size = 70 keeps the split", func(t *testing.T) {
		cfg, err := config.Load(writeTOML(t, "[ui]\nsize = 70\n"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if want := (config.UIConfig{Split: true, Size: 70}); cfg.UI != want {
			t.Errorf("UI = %+v, want %+v", cfg.UI, want)
		}
	})

	for name, size := range map[string]string{"size below 20": "19", "size above 80": "81"} {
		t.Run(name, func(t *testing.T) {
			_, err := config.Load(writeTOML(t, "[ui]\nsize = "+size+"\n"))
			if err == nil || !strings.Contains(err.Error(), "ui.size") {
				t.Errorf("err = %v, want one naming ui.size", err)
			}
		})
	}
}
