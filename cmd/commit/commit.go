package commit

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/cmd/pushflow"
	commitpkg "github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/convert"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

// Committer is the subset of *git.Client the commit command consumes: load the
// author list for the form, detect the current branch for the issue-hint
// prefill, and write the commit. Declared here (the consuming package) rather
// than in package git so the command owns its git surface and tests can
// substitute a fake — mirrors the pruner / BranchClient pattern elsewhere in
// cmd/.
type Committer interface {
	Authors(ctx context.Context) ([]string, error)
	Commit(ctx context.Context, msg []byte, opts git.CommitOptions) error
	CurrentBranch() (string, error)
}

// Compile-time check that the production client satisfies the role. Catches
// accidental signature drift on *git.Client.
var _ Committer = (*git.Client)(nil)

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
	pushflow.AddFlags(cmd)

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
	client, err := cmdutil.NewClientForCmd(cmd, c.appConfig)
	if err != nil {
		return err
	}

	authors, err := client.Authors(cmd.Context())
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

	prefill := hint.Prefill(c.appConfig.CommitMessage)

	msg, opts, err := commitpkg.FillOutForm(cmd.Context(), c.appConfig, defaults, s, prefill)
	if err != nil {
		return fmt.Errorf("failed to fill form: %w", err)
	}

	if err := client.Commit(cmd.Context(), msg, convert.CommitOptionsFromTUI(opts)); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	return proposeCommitPush(cmd, client, s, c.appConfig)
}

// proposeCommitPush offers to push the current branch after a successful commit,
// enriched (on a git-zf issue branch) with a merge-vs-parent preview.
func proposeCommitPush(cmd *cobra.Command, client *git.Client, s *store.Store, cfg *config.AppConfig) error {
	push, noPush := pushflow.ReadFlags(cmd)
	skip, auto, err := pushflow.ResolveFlags(push, noPush, cfg.Push.Propose)
	if err != nil {
		return err
	}

	branchName, err := client.CurrentBranch()
	if err != nil {
		return nil // detached/unknown HEAD → nothing to offer
	}

	parent, includeMerge := resolveCommitMergeParent(cmd.Context(), client, s, branchName, cfg.Branch.Base)

	yes, _ := cmd.Flags().GetBool("yes")

	return pushflow.Propose(cmd.Context(), client, pushflow.Opts{
		Branch:              branchName,
		Skip:                skip,
		AutoConfirm:         auto,
		NonInteractive:      yes,
		IncludeMergePreview: includeMerge,
		Parent:              parent,
	}, pushflow.NewHuhConfirm())
}

// resolveCommitMergeParent returns the parent integration branch for the
// merge-vs-parent preview (as a ref IsAncestor/MergeDryRun can use), and whether
// to show the preview at all. include is false when the current branch is not a
// git-zf issue branch, when no distinct parent/base resolves, or on any error —
// in those cases commit shows the push preview only.
func resolveCommitMergeParent(
	ctx context.Context,
	client *git.Client,
	s *store.Store, currentBranch,
	cfgBase string) (string, bool) {
	parsed, err := branch.Parse(currentBranch)
	if err != nil {
		return "", false // not a git-zf issue branch
	}

	parentBranch, err := issueflow.ResolveParentBranch(ctx, s, client, parsed.IssueID(), cfgBase)
	if err != nil || parentBranch == "" || parentBranch == currentBranch {
		return "", false
	}

	// Resolve to a ref the read-only preview primitives can read: the local head,
	// or <remote>/<parent> when the parent is remote-only.
	return client.LocalOrRemoteRef(parentBranch), true
}

// issueHintFromClient detects whether the current branch is an issue branch
// (feature "<id>@<type>@<slug>" or review "<id>@review") and returns the
// corresponding IssueHint. All other cases (detached HEAD, non-issue branch
// name) collapse to the zero value, leaving the form unchanged. It accepts the
// Committer role (it reads CurrentBranch) so it can be unit-tested with a fake.
func issueHintFromClient(c Committer) commitpkg.IssueHint {
	name, err := c.CurrentBranch()
	if err != nil {
		return commitpkg.IssueHint{}
	}

	// Review branches ("<issueSlug>@review") are not feature-branch shaped, so
	// branch.Parse rejects them. Recognise the suffix and prefill the issue ID
	// from the slug; the reviewer chooses the commit type themselves.
	if slug, ok := strings.CutSuffix(name, "@review"); ok {
		return commitpkg.IssueHint{IssueID: slug}
	}

	b, err := branch.Parse(name)
	if err != nil {
		return commitpkg.IssueHint{}
	}

	return commitpkg.IssueHint{IssueID: b.IssueID(), BranchType: b.Type()}
}
