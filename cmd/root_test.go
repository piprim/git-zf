package cmd

import (
	"slices"
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
		want := []string{"commit", "issue", "branch", "review"}

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
