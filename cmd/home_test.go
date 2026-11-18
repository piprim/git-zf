package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

// scriptedHomePrompter answers the home screen with picks in order, then
// quits, and records what each display was given.
type scriptedHomePrompter struct {
	picks   []tui.HomePick
	data    []tui.HomeData
	cursors []string
}

func (p *scriptedHomePrompter) Pick(
	_ context.Context, data tui.HomeData, _ []tui.HomeEntry, cursor string,
) (tui.HomePick, error) {
	p.data, p.cursors = append(p.data, data), append(p.cursors, cursor)
	if n := len(p.data); n <= len(p.picks) {
		return p.picks[n-1], nil
	}

	return tui.HomePick{Kind: tui.HomeQuit}, nil
}

// homeCmd is a parent command primed with a context, as cobra does before
// RunE, with a buffer each for stdout and stderr.
func homeCmd(t *testing.T, subs ...*cobra.Command) (cmd *cobra.Command, out, errOut *bytes.Buffer) {
	t.Helper()

	out, errOut = &bytes.Buffer{}, &bytes.Buffer{}
	cmd = &cobra.Command{Use: "git-zf", Short: "root", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.AddCommand(subs...)
	cmd.SetArgs(nil)
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("prime the command's context: %v", err)
	}
	out.Reset()
	errOut.Reset()

	return cmd, out, errOut
}

func TestRunHome(t *testing.T) {
	t.Parallel()

	run := tui.HomePick{Kind: tui.HomeRun, Name: "commit"}
	// rigDeps are the deps of a home screen on a real repository.
	rigDeps := func(t *testing.T, p homePrompter) (homeDeps, *homeRig, *int) {
		t.Helper()

		r, opens := newHomeRig(t, ""), 0

		return homeDeps{
			prompter: p,
			isTTY:    func() bool { return true },
			open: func(*cobra.Command) (*git.Client, error) {
				opens++
				c, _ := clientAt(t, r.dir)

				return c, nil
			},
		}, r, &opens
	}
	sub := func(err error, ran *[]*cobra.Command) *cobra.Command {
		return &cobra.Command{Use: "commit", RunE: func(cmd *cobra.Command, _ []string) error {
			*ran = append(*ran, cmd)

			return err
		}}
	}

	t.Run("without a terminal the help alone is printed", func(t *testing.T) {
		t.Parallel()

		var ran []*cobra.Command
		commit := sub(nil, &ran)
		cmd, out, errOut := homeCmd(t, commit)
		p := &scriptedHomePrompter{}
		d := homeDeps{prompter: p, isTTY: func() bool { return false }, open: func(*cobra.Command) (*git.Client, error) {
			t.Error("the repository was opened")

			return nil, errors.New("unexpected")
		}}

		if err := runHome(cmd, "git-zf", []*cobra.Command{commit}, d); err != nil {
			t.Fatalf("runHome: %v", err)
		}
		if !strings.Contains(out.String(), "Available Commands:") || errOut.Len() != 0 || len(p.data) != 0 {
			t.Errorf("stdout = %q, stderr = %q, displays = %d", out.String(), errOut.String(), len(p.data))
		}
	})

	t.Run("outside a repository the line and the help are printed", func(t *testing.T) {
		t.Parallel()

		var ran []*cobra.Command
		commit := sub(nil, &ran)
		cmd, out, errOut := homeCmd(t, commit)
		p := &scriptedHomePrompter{}
		d := homeDeps{prompter: p, isTTY: func() bool { return true }, open: func(*cobra.Command) (*git.Client, error) {
			return nil, errors.New("not a git repository: exit status 128")
		}}

		if err := runHome(cmd, "git-zf", []*cobra.Command{commit}, d); err != nil {
			t.Fatalf("runHome: %v", err)
		}
		if errOut.String() != "not a git repository\n" || !strings.Contains(out.String(), "Available Commands:") {
			t.Errorf("stderr = %q, stdout = %q", errOut.String(), out.String())
		}
		if len(p.data) != 0 {
			t.Error("the home screen was shown")
		}
	})

	t.Run("runs the picked command with the parent and asks again", func(t *testing.T) {
		t.Parallel()

		var ran []*cobra.Command
		commit := sub(nil, &ran)
		cmd, _, errOut := homeCmd(t, commit)
		p := &scriptedHomePrompter{picks: []tui.HomePick{run}}
		d, _, opens := rigDeps(t, p)

		if err := runHome(cmd, "git-zf", []*cobra.Command{commit}, d); err != nil {
			t.Fatalf("runHome: %v", err)
		}
		if len(ran) != 1 || ran[0] != cmd {
			t.Errorf("commit ran with %v, want once with the parent", ran)
		}
		if len(p.cursors) != 2 || p.cursors[0] != "" || p.cursors[1] != "commit" {
			t.Errorf("cursors = %q, want an empty one then commit", p.cursors)
		}
		if *opens != 2 || errOut.Len() != 0 {
			t.Errorf("opens = %d, stderr = %q, want a fresh client per display", *opens, errOut.String())
		}
		if p.data[0].Prog != "git-zf" || p.data[0].Branch != "main" {
			t.Errorf("data = %+v", p.data[0])
		}
	})

	t.Run("an error is printed and the screen comes back", func(t *testing.T) {
		t.Parallel()

		var ran []*cobra.Command
		commit := sub(errors.New("boom"), &ran)
		cmd, _, errOut := homeCmd(t, commit)
		p := &scriptedHomePrompter{picks: []tui.HomePick{run}}
		d, _, _ := rigDeps(t, p)

		if err := runHome(cmd, "git-zf", []*cobra.Command{commit}, d); err != nil {
			t.Fatalf("runHome: %v", err)
		}
		if errOut.String() != "error: commit: boom\n" || len(p.data) != 2 {
			t.Errorf("stderr = %q, displays = %d", errOut.String(), len(p.data))
		}
	})

	t.Run("an aborted command is not an error", func(t *testing.T) {
		t.Parallel()

		var ran []*cobra.Command
		commit := sub(huh.ErrUserAborted, &ran)
		cmd, _, errOut := homeCmd(t, commit)
		p := &scriptedHomePrompter{picks: []tui.HomePick{run}}
		d, _, _ := rigDeps(t, p)

		if err := runHome(cmd, "git-zf", []*cobra.Command{commit}, d); err != nil {
			t.Fatalf("runHome: %v", err)
		}
		if errOut.Len() != 0 || len(p.data) != 2 {
			t.Errorf("stderr = %q, displays = %d", errOut.String(), len(p.data))
		}
	})

	t.Run("quit ends the loop", func(t *testing.T) {
		t.Parallel()

		var ran []*cobra.Command
		commit := sub(nil, &ran)
		cmd, _, _ := homeCmd(t, commit)
		p := &scriptedHomePrompter{}
		d, _, _ := rigDeps(t, p)

		if err := runHome(cmd, "git-zf", []*cobra.Command{commit}, d); err != nil {
			t.Fatalf("runHome: %v", err)
		}
		if len(p.data) != 1 || len(ran) != 0 {
			t.Errorf("displays = %d, commit ran %d times", len(p.data), len(ran))
		}
	})

	t.Run("refresh gathers again", func(t *testing.T) {
		t.Parallel()

		var ran []*cobra.Command
		commit := sub(nil, &ran)
		cmd, _, errOut := homeCmd(t, commit)
		p := &scriptedHomePrompter{picks: []tui.HomePick{{Kind: tui.HomeRefresh}}}
		d, _, _ := rigDeps(t, p)

		if err := runHome(cmd, "git-zf", []*cobra.Command{commit}, d); err != nil {
			t.Fatalf("runHome: %v", err)
		}
		if len(p.data) != 2 || len(ran) != 0 || errOut.Len() != 0 || p.cursors[1] != "" {
			t.Errorf("displays = %d, ran = %d, stderr = %q, cursors = %q", len(p.data), len(ran), errOut.String(), p.cursors)
		}
	})

	t.Run("a command that changes the branch shows on the next display", func(t *testing.T) {
		t.Parallel()

		p := &scriptedHomePrompter{picks: []tui.HomePick{run}}
		d, r, _ := rigDeps(t, p)
		commit := &cobra.Command{Use: "commit", RunE: func(*cobra.Command, []string) error {
			gitIn(t, r.dir, "checkout", "-q", "-b", "elsewhere")

			return nil
		}}
		cmd, _, _ := homeCmd(t, commit)

		if err := runHome(cmd, "git-zf", []*cobra.Command{commit}, d); err != nil {
			t.Fatalf("runHome: %v", err)
		}
		if len(p.data) != 2 || p.data[0].Branch != "main" || p.data[1].Branch != "elsewhere" {
			t.Errorf("branches shown = %+v", p.data)
		}
	})

	t.Run("a command that removes the repository under the shell ends quietly", func(t *testing.T) {
		t.Parallel()

		var ran []*cobra.Command
		commit := sub(nil, &ran)
		cmd, _, errOut := homeCmd(t, commit)
		p := &scriptedHomePrompter{picks: []tui.HomePick{run}}
		d, _, opens := rigDeps(t, p)
		open := d.open
		d.open = func(c *cobra.Command) (*git.Client, error) {
			if *opens >= 1 { // a closed issue removed the worktree the shell stands in
				return nil, errors.New("not a git repository")
			}

			return open(c)
		}

		if err := runHome(cmd, "git-zf", []*cobra.Command{commit}, d); err != nil {
			t.Fatalf("runHome: %v", err)
		}
		if len(p.data) != 1 || errOut.String() != "this directory is no longer a git repository\n" {
			t.Errorf("displays = %d, stderr = %q", len(p.data), errOut.String())
		}
	})

	t.Run("a pick that names no command is an error", func(t *testing.T) {
		t.Parallel()

		cmd, _, _ := homeCmd(t)
		p := &scriptedHomePrompter{picks: []tui.HomePick{{Kind: tui.HomeRun, Name: "bogus"}}}
		d, _, _ := rigDeps(t, p)

		if err := runHome(cmd, "git-zf", nil, d); err == nil || !strings.Contains(err.Error(), "bogus") {
			t.Errorf("err = %v, want one naming the pick", err)
		}
	})
}
