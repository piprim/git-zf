package config_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/piprim/git-zf/config"
)

func TestLoad_LogDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	t.Run("graph is the default command", func(t *testing.T) {
		if cfg.Log.DefaultCmd != "graph" || cfg.Log.Cmds[cfg.Log.DefaultIndex()].Name != "graph" {
			t.Errorf("DefaultCmd = %q, index = %d", cfg.Log.DefaultCmd, cfg.Log.DefaultIndex())
		}
	})

	t.Run("six commands keyed 1 to 6", func(t *testing.T) {
		var names []string
		for i, c := range cfg.Log.Cmds {
			names = append(names, c.Name)
			if c.Key != i+1 {
				t.Errorf("cmd %q key = %d, want %d", c.Name, c.Key, i+1)
			}
		}
		want := []string{"graph", "branch", "linear", "first-parent", "recent", "tags"}
		if !slices.Equal(names, want) {
			t.Errorf("names = %v, want %v", names, want)
		}
	})

	t.Run("every command is a git log command that splits cleanly", func(t *testing.T) {
		for _, c := range cfg.Log.Cmds {
			args, err := c.Args()
			if err != nil {
				t.Errorf("%s: %v", c.Name, err)

				continue
			}
			if args[0] != "log" {
				t.Errorf("%s: args = %q", c.Name, args)
			}
		}
	})

	t.Run("the graph command is the original one", func(t *testing.T) {
		args, _ := cfg.Log.Cmds[0].Args()
		if !slices.Contains(args, "--exclude=*zf/*") || !slices.Contains(args, "--date-order") ||
			!slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "--pretty=tformat:%Cred%h") }) {
			t.Errorf("args = %q", args)
		}
	})
}

func TestLogCmd_Args(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, cmd string
		want      []string
	}{
		{"leading git is dropped", "git log --graph", []string{"log", "--graph"}},
		{"a command without git", "log --oneline", []string{"log", "--oneline"}},
		{"single quotes group a format", "git log --pretty=format:'%h - %s'", []string{"log", "--pretty=format:%h - %s"}},
		{"double quotes too", `git log --since="2 weeks ago"`, []string{"log", "--since=2 weeks ago"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := config.LogCmd{Name: "x", Cmd: c.cmd}.Args()
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("Args(%q) = %q, want %q", c.cmd, got, c.want)
			}
		})
	}

	t.Run("an unbalanced quote is an error", func(t *testing.T) {
		if _, err := (config.LogCmd{Name: "x", Cmd: "git log --pretty='%h"}).Args(); err == nil {
			t.Error("expected an error")
		}
	})
}

func TestLoad_LogValidation(t *testing.T) {
	t.Parallel()

	load := func(t *testing.T, blob string) (*config.AppConfig, error) {
		t.Helper()

		return config.Load(writeTOML(t, blob))
	}

	t.Run("a user array replaces the defaults and default-cmd is kept", func(t *testing.T) {
		cfg, err := load(t, `
[[log.cmds]]
name = "graph"
cmd = "git log --graph"
`)
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Log.Cmds) != 1 || cfg.Log.DefaultCmd != "graph" || cfg.Log.Cmds[0].Key != 0 {
			t.Errorf("Log = %+v", cfg.Log)
		}
	})

	t.Run("a key outside 0-9 is an error", func(t *testing.T) {
		_, err := load(t, `
[log]
default-cmd = "a"
[[log.cmds]]
name = "a"
cmd = "git log"
key = 10
`)
		if err == nil || !strings.Contains(err.Error(), "key") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("a key used twice is an error", func(t *testing.T) {
		_, err := load(t, `
[log]
default-cmd = "a"
[[log.cmds]]
name = "a"
cmd = "git log"
key = 1
[[log.cmds]]
name = "b"
cmd = "git log"
key = 1
`)
		if err == nil || !strings.Contains(err.Error(), "key 1") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("a name used twice is an error", func(t *testing.T) {
		_, err := load(t, `
[log]
default-cmd = "a"
[[log.cmds]]
name = "a"
cmd = "git log"
[[log.cmds]]
name = "a"
cmd = "git log -n 1"
`)
		if err == nil || !strings.Contains(err.Error(), `"a"`) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("an empty name or command is an error", func(t *testing.T) {
		for _, blob := range []string{
			"[log]\ndefault-cmd = \"a\"\n[[log.cmds]]\nname = \"a\"\n",
			"[log]\n[[log.cmds]]\ncmd = \"git log\"\n",
		} {
			if _, err := load(t, blob); err == nil {
				t.Errorf("expected an error for %q", blob)
			}
		}
	})

	t.Run("a command with no words after git is an error, not a panic", func(t *testing.T) {
		for _, cmd := range []string{"git", "   "} {
			_, err := load(t, "[log]\ndefault-cmd = \"a\"\n[[log.cmds]]\nname = \"a\"\ncmd = \""+cmd+"\"\n")
			if err == nil || !strings.Contains(err.Error(), "git log") {
				t.Errorf("cmd %q: err = %v", cmd, err)
			}
		}
	})

	t.Run("a command that is not git log is an error", func(t *testing.T) {
		_, err := load(t, `
[log]
default-cmd = "a"
[[log.cmds]]
name = "a"
cmd = "git status"
`)
		if err == nil || !strings.Contains(err.Error(), "git log") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("an unknown default-cmd is an error naming it", func(t *testing.T) {
		_, err := load(t, `
[[log.cmds]]
name = "mine"
cmd = "git log"
`)
		if err == nil || !strings.Contains(err.Error(), `"graph"`) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("an empty default-cmd falls back to the first command", func(t *testing.T) {
		cfg, err := load(t, `
[log]
default-cmd = ""
[[log.cmds]]
name = "mine"
cmd = "git log"
`)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Log.DefaultIndex() != 0 {
			t.Errorf("DefaultIndex = %d", cfg.Log.DefaultIndex())
		}
	})

	t.Run("no command at all is an error", func(t *testing.T) {
		if _, err := load(t, "[log]\ncmds = []\n"); err == nil {
			t.Error("expected an error")
		}
	})
}
