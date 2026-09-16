package cmdutil

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

// scriptedMenuPrompter returns a canned pick (or error) instead of opening a
// huh form, and records what it was asked to show.
type scriptedMenuPrompter struct {
	pick  string
	err   error
	title string
	subs  []*cobra.Command
}

func (p *scriptedMenuPrompter) Select(_ context.Context, title string, subs []*cobra.Command) (string, error) {
	p.title = title
	p.subs = subs

	return p.pick, p.err
}

// recordingSub builds a subcommand whose RunE records the *cobra.Command it
// was invoked with.
func recordingSub(name string, got **cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: "short for " + name,
		RunE: func(cmd *cobra.Command, _ []string) error {
			*got = cmd

			return nil
		},
	}
}

// newParent builds an executing-style parent command with a context, a
// captured stdout, and the given subcommands registered (so Help lists them).
func newParent(t *testing.T, subs ...*cobra.Command) (*cobra.Command, *bytes.Buffer) {
	t.Helper()

	var out bytes.Buffer
	parent := &cobra.Command{Use: "git-zf", Short: "root"}
	parent.SetOut(&out)
	parent.SetErr(&out)
	parent.AddCommand(subs...)
	// Mirror what cobra does before RunE: the executing command carries a ctx.
	// cobra v1.1.3 has no SetContext; ExecuteContext on a no-op parent sets it.
	parent.RunE = func(*cobra.Command, []string) error { return nil }
	parent.SetArgs(nil)
	if err := parent.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("prime parent context: %v", err)
	}
	out.Reset()

	return parent, &out
}

func TestRunMenu(t *testing.T) {
	t.Parallel()

	t.Run("dispatches to the picked subcommand with the parent command", func(t *testing.T) {
		t.Parallel()

		var gotCommit, gotIssue *cobra.Command
		commit := recordingSub("commit", &gotCommit)
		issue := recordingSub("issue", &gotIssue)
		parent, _ := newParent(t, commit, issue)
		p := &scriptedMenuPrompter{pick: "issue"}

		if err := RunMenu(parent, "git zf:", []*cobra.Command{commit, issue}, p); err != nil {
			t.Fatalf("RunMenu: %v", err)
		}
		if gotCommit != nil {
			t.Error("commit RunE was called, want only issue")
		}
		if gotIssue != parent {
			t.Errorf("issue RunE received %v, want the parent command", gotIssue)
		}
	})

	t.Run("passes title and subs through to the prompter", func(t *testing.T) {
		t.Parallel()

		var got *cobra.Command
		commit := recordingSub("commit", &got)
		parent, _ := newParent(t, commit)
		p := &scriptedMenuPrompter{pick: "commit"}

		if err := RunMenu(parent, "git zf:", []*cobra.Command{commit}, p); err != nil {
			t.Fatalf("RunMenu: %v", err)
		}
		if p.title != "git zf:" {
			t.Errorf("title = %q, want %q", p.title, "git zf:")
		}
		if len(p.subs) != 1 || p.subs[0] != commit {
			t.Errorf("subs = %v, want [commit]", p.subs)
		}
	})

	t.Run("prints help when no interactive terminal is available", func(t *testing.T) {
		t.Parallel()

		var got *cobra.Command
		commit := recordingSub("commit", &got)
		parent, out := newParent(t, commit)
		p := &scriptedMenuPrompter{err: ErrMenuUnavailable}

		if err := RunMenu(parent, "git zf:", []*cobra.Command{commit}, p); err != nil {
			t.Fatalf("RunMenu: %v", err)
		}
		if got != nil {
			t.Error("subcommand RunE was called, want none")
		}
		if !strings.Contains(out.String(), "Available Commands:") || !strings.Contains(out.String(), "commit") {
			t.Errorf("help not printed, got:\n%s", out.String())
		}
	})

	t.Run("exits quietly when the user aborts the menu", func(t *testing.T) {
		t.Parallel()

		var got *cobra.Command
		commit := recordingSub("commit", &got)
		parent, out := newParent(t, commit)
		p := &scriptedMenuPrompter{err: huh.ErrUserAborted}

		if err := RunMenu(parent, "git zf:", []*cobra.Command{commit}, p); err != nil {
			t.Fatalf("RunMenu: %v", err)
		}
		if got != nil {
			t.Error("subcommand RunE was called, want none")
		}
		if out.Len() != 0 {
			t.Errorf("unexpected output: %q", out.String())
		}
	})

	t.Run("wraps other prompter errors", func(t *testing.T) {
		t.Parallel()

		var got *cobra.Command
		commit := recordingSub("commit", &got)
		parent, _ := newParent(t, commit)
		boom := errors.New("boom")
		p := &scriptedMenuPrompter{err: boom}

		err := RunMenu(parent, "git zf:", []*cobra.Command{commit}, p)
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want wrapped boom", err)
		}
		if got != nil {
			t.Error("subcommand RunE was called, want none")
		}
	})

	t.Run("rejects a pick that matches no subcommand", func(t *testing.T) {
		t.Parallel()

		var got *cobra.Command
		commit := recordingSub("commit", &got)
		parent, _ := newParent(t, commit)
		p := &scriptedMenuPrompter{pick: "bogus"}

		err := RunMenu(parent, "git zf:", []*cobra.Command{commit}, p)
		if err == nil || !strings.Contains(err.Error(), "bogus") {
			t.Fatalf("err = %v, want error naming the unknown action", err)
		}
		if got != nil {
			t.Error("subcommand RunE was called, want none")
		}
	})

	t.Run("rejects a subcommand without RunE", func(t *testing.T) {
		t.Parallel()

		noRunE := &cobra.Command{Use: "version", Run: func(*cobra.Command, []string) {}}
		parent, _ := newParent(t, noRunE)
		p := &scriptedMenuPrompter{pick: "version"}

		err := RunMenu(parent, "git zf:", []*cobra.Command{noRunE}, p)
		if err == nil || !strings.Contains(err.Error(), "version") {
			t.Fatalf("err = %v, want error naming the subcommand", err)
		}
	})

	t.Run("propagates the subcommand error, prefixed with the action name", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("sub failed")
		failing := &cobra.Command{Use: "commit", RunE: func(*cobra.Command, []string) error { return boom }}
		parent, _ := newParent(t, failing)
		p := &scriptedMenuPrompter{pick: "commit"}

		err := RunMenu(parent, "git zf:", []*cobra.Command{failing}, p)
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want sub failure", err)
		}
		if !strings.HasPrefix(err.Error(), "commit: ") {
			t.Errorf("err = %q, want it prefixed with the action name", err.Error())
		}
	})
}

func TestMenuOptions(t *testing.T) {
	t.Parallel()

	subs := []*cobra.Command{
		{Use: "commit", Short: "Record changes to the repository"},
		{Use: "prune-tracker [flags]", Short: "Reap branches"},
	}

	opts := menuOptions(subs)

	t.Run("one option per subcommand", func(t *testing.T) {
		t.Parallel()

		if len(opts) != 2 {
			t.Fatalf("len(opts) = %d, want 2", len(opts))
		}
	})

	t.Run("label is the capitalised command name", func(t *testing.T) {
		t.Parallel()

		if opts[0].Label != "Commit" {
			t.Errorf("Label = %q, want %q", opts[0].Label, "Commit")
		}
		if opts[1].Label != "Prune-tracker" {
			t.Errorf("Label = %q, want %q", opts[1].Label, "Prune-tracker")
		}
	})

	t.Run("description is the command Short", func(t *testing.T) {
		t.Parallel()

		if opts[0].Desc != "Record changes to the repository" {
			t.Errorf("Desc = %q", opts[0].Desc)
		}
	})

	t.Run("value is the bare command name", func(t *testing.T) {
		t.Parallel()

		if opts[1].Value != "prune-tracker" {
			t.Errorf("Value = %q, want %q", opts[1].Value, "prune-tracker")
		}
	})
}
