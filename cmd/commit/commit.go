package commit

import (
	"fmt"
	"log"

	commitpkg "github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
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
	f.BoolVarP(&all, "all", "a", false, "stage all tracked modified/deleted files before committing")
	f.BoolVar(&amend, "amend", false, "replace the tip of the current branch")
	f.BoolVarP(&noVerify, "no-verify", "n", false, "bypass pre-commit and commit-msg hooks")
	f.BoolVarP(&signoff, "signoff", "s", false, "add Signed-off-by trailer to the commit message")
	f.BoolVar(&allowEmpty, "allow-empty", false, "allow a commit with no changes")
	f.StringVar(&author, "author", "", `override commit author as "Name <email>"`)

	cmd.RunE = func(_ *cobra.Command, _ []string) error {
		return c.runE(tui.CommitOption{
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

func (c Commit) runE(flags tui.CommitOption) error {
	client, err := git.NewClient()
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	authors, err := client.Authors()
	if err != nil {
		log.Printf("could not load author list: %v", err)
		authors = []string{}
	}

	defaults := flags
	defaults.Authors = authors

	msg, opts, err := commitpkg.FillOutForm(c.appConfig, defaults)
	if err != nil {
		return fmt.Errorf("failed to fill form: %w", err)
	}

	summary, err := client.Commit(msg, git.CommitOptions{
		All:        opts.All,
		Amend:      opts.Amend,
		NoVerify:   opts.NoVerify,
		Signoff:    opts.Signoff,
		AllowEmpty: opts.AllowEmpty,
		Author:     opts.Author,
	})
	if err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	printCommitSummary(&summary)

	return nil
}

func printCommitSummary(s *git.CommitSummary) {
	if s == nil {
		return
	}

	ref := s.Branch
	if s.IsRoot {
		ref += " (root-commit)"
	}

	fmt.Printf("[%s %s] %s\n", ref, s.ShortHash, s.Subject)

	if s.Files == 0 {
		return
	}

	fileWord := "files"
	if s.Files == 1 {
		fileWord = "file"
	}

	line := fmt.Sprintf(" %d %s changed", s.Files, fileWord)

	if s.Additions > 0 {
		word := "insertions"
		if s.Additions == 1 {
			word = "insertion"
		}

		line += fmt.Sprintf(", %d %s(+)", s.Additions, word)
	}

	if s.Deletions > 0 {
		word := "deletions"
		if s.Deletions == 1 {
			word = "deletion"
		}

		line += fmt.Sprintf(", %d %s(-)", s.Deletions, word)
	}

	fmt.Println(line)
}
