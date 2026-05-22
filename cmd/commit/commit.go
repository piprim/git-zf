package commit

import (
	"fmt"
	"log/slog"

	"github.com/piprim/git-zf/branch"
	commitpkg "github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

type Commit struct {
	appConfig *config.AppConfig
}

func New(appConfig *config.AppConfig) Commit {
	return Commit{appConfig: appConfig}
}

func (c Commit) GetRootCmd() *cobra.Command {
	var (
		skip       bool
		all        bool
		amend      bool
		noVerify   bool
		signoff    bool
		allowEmpty bool
		author     string
	)

	desc := "Open the " + c.appConfig.ProgName +
		" TUI to compose a standardised commit message, then commit using go-git."
	cmd := &cobra.Command{
		Use:   "commit",
		Short: "Record changes to the repository",
		Long:  desc,
	}

	f := cmd.Flags()
	f.BoolVarP(&skip, "yes", "y", false, "skip the commit options form and assume defaults")
	f.BoolVarP(&all, "all", "a", false, "stage all tracked modified/deleted files before committing")
	f.BoolVar(&amend, "amend", false, "replace the tip of the current branch")
	f.BoolVarP(&noVerify, "no-verify", "n", false, "bypass pre-commit and commit-msg hooks")
	f.BoolVarP(&signoff, "signoff", "s", false, "add Signed-off-by trailer to the commit message")
	f.BoolVar(&allowEmpty, "allow-empty", false, "allow a commit with no changes")
	f.StringVar(&author, "author", "", `override commit author as "Name <email>"`)

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return c.runE(cmd, tui.CommitOption{
			Skip: skip ||
				cmd.Flags().Changed("all") ||
				cmd.Flags().Changed("amend") ||
				cmd.Flags().Changed("no-verify") ||
				cmd.Flags().Changed("signoff") ||
				cmd.Flags().Changed("allow-empty") ||
				cmd.Flags().Changed("author"),
			All:        all,
			Amend:      amend,
			NoVerify:   noVerify,
			Signoff:    signoff,
			AllowEmpty: allowEmpty,
			Author:     author,
		})
	}

	return cmd
}

func (c Commit) runE(cmd *cobra.Command, flags tui.CommitOption) error {
	client, err := git.NewClient(&pkg.IO{
		In:  cmd.InOrStdin(),
		Out: cmd.OutOrStdout(),
		Err: cmd.ErrOrStderr(),
	})
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	authors, err := client.Authors()
	if err != nil {
		slog.Warn("could not load author list", "error", err)
		authors = []string{}
	}

	defaults := flags
	defaults.Authors = authors

	hint := issueHintFromClient(client)

	s, err := store.OpenRepo(cmd.Context())
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = s.Close() }()

	prefill := hint.Prefill(c.appConfig.CommitMessage.Items)

	msg, opts, err := commitpkg.FillOutForm(cmd.Context(), c.appConfig, defaults, s, prefill)
	if err != nil {
		return fmt.Errorf("failed to fill form: %w", err)
	}

	if err := client.Commit(cmd.Context(), msg, git.CommitOptions{
		All:        opts.All,
		Amend:      opts.Amend,
		NoVerify:   opts.NoVerify,
		Signoff:    opts.Signoff,
		AllowEmpty: opts.AllowEmpty,
		Author:     opts.Author,
	}); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	return nil
}

// issueHintFromClient detects whether the current branch is an issue branch
// and returns the corresponding IssueHint. All failure modes (detached HEAD,
// non-issue branch name) collapse to the zero value, leaving the form unchanged.
func issueHintFromClient(c *git.Client) commitpkg.IssueHint {
	name, err := c.CurrentBranch()
	if err != nil {
		return commitpkg.IssueHint{}
	}

	b, err := branch.Parse(name)
	if err != nil {
		return commitpkg.IssueHint{}
	}

	return commitpkg.IssueHint{IssueID: b.IssueID(), BranchType: b.Type()}
}
