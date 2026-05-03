package issue

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
	"github.com/spf13/cobra"
)

type issueListFlags struct {
	status  string
	stdout  bool
	jsonOut bool
}

type issueListInfra struct {
	tracker tracker.Tracker
	store   *store.Store
	stderr  io.Writer
}

func (ir Issue) getIssueListCmd() *cobra.Command {
	var flags issueListFlags

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List issues",
	}

	f := cmd.Flags()
	f.StringVar(&flags.status, "status", "", "filter by status: open, closed, all")
	f.BoolVar(&flags.stdout, "stdout", false, "print table to stdout without TUI")
	f.BoolVar(&flags.jsonOut, "json", false, "print JSON array to stdout")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return ir.issueListRunE(cmd, flags)
	}

	return cmd
}

func (ir Issue) issueListRunE(cmd *cobra.Command, flags issueListFlags) error {
	client, err := git.NewClient()
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	root, err := client.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	s, err := store.Open(cmd.Context(), filepath.Join(root, ".git"))
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = s.Close() }()

	var t tracker.Tracker
	if ir.appConfig.IssueTracker.Type != "" {
		t, err = tracker.New(ir.appConfig.IssueTracker)
		if err != nil {
			fmt.Fprintf(cmd.OutOrStderr(), "warning: could not initialize tracker: %v\n", err)
		}
	}

	infra := issueListInfra{
		tracker: t,
		store:   s,
		stderr:  cmd.OutOrStderr(),
	}

	return runList(cmd.Context(), os.Stdout, infra, flags)
}
