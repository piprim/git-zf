package cmd

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestGetRootCmd_menu(t *testing.T) {
	root, err := GetRootCmd()
	if err != nil {
		t.Fatalf("GetRootCmd: %v", err)
	}

	t.Run("runs a menu when invoked without a subcommand", func(t *testing.T) {
		if root.RunE == nil {
			t.Fatal("root command has no RunE; expected the workflow menu")
		}
	})

	t.Run("menu offers the workflow commands in order", func(t *testing.T) {
		want := []string{"commit", "issue", "branch", "review", "log"}

		var got []string
		for _, sub := range menuSubs(root) {
			got = append(got, sub.Name())
		}

		if !slices.Equal(got, want) {
			t.Errorf("menu subs = %v, want %v", got, want)
		}
	})

	t.Run("menu entries are registered subcommands of the root", func(t *testing.T) {
		registered := root.Commands()
		for _, sub := range menuSubs(root) {
			if !slices.Contains(registered, sub) {
				t.Errorf("menu entry %q is not a registered subcommand", sub.Name())
			}
		}
	})

	t.Run("setup commands stay out of the menu", func(t *testing.T) {
		menu := menuSubs(root)
		for _, name := range []string{"install", "uninstall", "init", "config", "completion", "version"} {
			if slices.ContainsFunc(menu, func(c *cobra.Command) bool { return c.Name() == name }) {
				t.Errorf("setup command %q is in the menu", name)
			}
		}
	})
}

// binaryName matches a command written as the binary is called, not as users
// type it: "git-zf issue start" instead of "git zf issue start". The project
// name on its own ("the git-zf TUI") is fine.
var binaryName = regexp.MustCompile(`\bgit-zf (commit|issue|branch|review|log|init|install|uninstall|config|completion|version)\b`)

func TestGetRootCmd_help(t *testing.T) {
	root, err := GetRootCmd()
	if err != nil {
		t.Fatalf("GetRootCmd: %v", err)
	}

	t.Run("root long description is the pitch", func(t *testing.T) {
		want := "A TUI powered CLI that wraps a git-flow workflow"
		if !strings.HasPrefix(root.Long, want) {
			t.Errorf("root.Long = %q, want prefix %q", root.Long, want)
		}
	})

	t.Run("every visible command has a long description", func(t *testing.T) {
		var walk func(c *cobra.Command)
		walk = func(c *cobra.Command) {
			for _, sub := range c.Commands() {
				if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
					continue
				}
				if strings.TrimSpace(sub.Long) == "" {
					t.Errorf("%s: no Long description", sub.CommandPath())
				}
				walk(sub)
			}
		}
		walk(root)
	})

	t.Run("the reference pages the spec binds say what they must", func(t *testing.T) {
		find := func(path ...string) *cobra.Command {
			c := root
			for _, name := range path {
				var next *cobra.Command
				for _, sub := range c.Commands() {
					if sub.Name() == name {
						next = sub
					}
				}
				if next == nil {
					t.Fatalf("no command %v", path)
				}
				c = next
			}
			return c
		}
		for _, tc := range []struct {
			path []string
			want string
		}{
			{[]string{"issue", "close"}, "git zf issue abandon"},
			{[]string{"branch", "new"}, "files an issue in the repository"},
			{[]string{"branch", "new"}, "never asks the tracker"},
			{[]string{"commit"}, "pre-filled"},
			{[]string{"commit"}, "review"},
		} {
			if got := find(tc.path...).Long; !strings.Contains(got, tc.want) {
				t.Errorf("%v: Long lacks %q", tc.path, tc.want)
			}
		}
	})

	t.Run("long descriptions say git zf, not git-zf", func(t *testing.T) {
		var walk func(c *cobra.Command)
		walk = func(c *cobra.Command) {
			for _, sub := range c.Commands() {
				if binaryName.MatchString(sub.Long) {
					t.Errorf("%s: Long names a command as git-zf <sub>", sub.CommandPath())
				}
				walk(sub)
			}
		}
		walk(root)
	})
}
