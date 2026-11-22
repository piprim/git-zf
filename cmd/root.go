package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/piprim/git-zf/cmd/branch"
	"github.com/piprim/git-zf/cmd/commit"
	"github.com/piprim/git-zf/cmd/completion"
	cfgcmd "github.com/piprim/git-zf/cmd/config"
	init_cmd "github.com/piprim/git-zf/cmd/init"
	"github.com/piprim/git-zf/cmd/install"
	"github.com/piprim/git-zf/cmd/issue"
	logcmd "github.com/piprim/git-zf/cmd/log"
	"github.com/piprim/git-zf/cmd/review"
	"github.com/piprim/git-zf/cmd/uninstall"
	"github.com/piprim/git-zf/cmd/version"
	"github.com/piprim/git-zf/config"
	_ "github.com/piprim/git-zf/tracker/forgejo" // registers forgejo + gitea adapters
	_ "github.com/piprim/git-zf/tracker/github"  // registers github adapter
	_ "github.com/piprim/git-zf/tracker/redmine" // registers redmine adapter
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

// Version is injected at build time via -ldflags.
var (
	Version = "none"

	isDebug   bool
	appConfig *config.AppConfig

	// rootMenu lists, in display order, the workflow commands offered by the
	// home screen that `git zf` opens when run without a subcommand; each
	// needs a key in homeKeys. Setup commands
	// (install, uninstall, init, config, completion, version) are deliberately
	// absent: they are one-shot and stay CLI-only, listed by --help.
	rootMenu = []string{"commit", "issue", "branch", "review", "log"}
)

// GetRootCmd builds and returns the root Cobra command.
func GetRootCmd() (*cobra.Command, error) {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	})))

	if err := initConfig(); err != nil {
		return nil, err
	}

	rootCmd := &cobra.Command{
		Use:   appConfig.ProgName,
		Short: "Git Zen workFlow: issue → branch → commit → review → close",
		Long: `A TUI powered CLI that wraps a git-flow workflow (issue → branch → commit →
review → close) with optional issue-tracker integration.

Every command is interactive by default: it asks what it needs in a form.
Passing a flag skips the matching prompt, and --help lists the flags of any
command.

Without a subcommand, git zf opens a home screen. Its header says where you
are: the current branch and its issue, the files staged, modified and
untracked, the commits ahead of and behind the upstream, the state of the
review. Under it are the workflow commands, each with a key and what waits
there: c commit, i issue, b branch, v review, l log. A key runs its command
with its interactive defaults, enter runs the one under the cursor. s shows
the working tree as git status does, r fetches the remote and reads
everything again, q quits. Esc does nothing on the home screen.

Leaving a command returns to the home screen, refreshed: q, or Esc once
there is nothing left to dismiss, closes the issue table, the branch table
and the log. git zf review opens a menu of its own that works the same way;
git zf issue and git zf branch open their tables.

These screens stay in the top half of the terminal. The bottom half is an
ordinary terminal: the forms open there, and what a commit, a push or a
merge prints stays there, the questions of a git hook included. On quit that
output is left at the top. The [ui] section of the configuration sets the
size of the two parts (size) or gives every screen the whole terminal
(split = false).

When stdin is not a terminal (a script, CI) this help is printed instead.
Outside a git repository it is printed too, under a line saying so. The
setup commands (init, install, uninstall, config, completion, version) are
not on the home screen.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHome(cmd, appConfig.ProgName, menuSubs(cmd), newHomeDeps())
		},
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true

			if isDebug {
				slog.SetDefault(slog.New(slog.NewTextHandler(cmd.OutOrStdout(), &slog.HandlerOptions{
					Level: slog.LevelDebug,
				})))
			}
		},
	}

	rootCmd.PersistentFlags().BoolVarP(&isDebug, "debug", "d", false,
		"debug mode, print debug info to stdout")

	ir := issue.New(appConfig)
	br := branch.New(appConfig)
	co := commit.New(appConfig)
	vs := version.New(Version)
	cf := cfgcmd.New(appConfig)
	rv := review.New(appConfig)
	it := init_cmd.New()
	lg := logcmd.New(appConfig)

	rootCmd.AddCommand(
		completion.Cmd(),
		co.GetRootCmd(),
		ir.GetRootCmd(),
		br.GetRootCmd(),
		vs.GetRootCmd(),
		install.Cmd(),
		uninstall.Cmd(),
		cf.GetRootCmd(),
		rv.GetRootCmd(),
		it.GetRootCmd(),
		lg.GetRootCmd(),
	)

	return rootCmd, nil
}

// menuSubs resolves rootMenu against root's registered subcommands, keeping
// rootMenu's order. Names that are not registered are skipped rather than
// failing, so the menu can never reference a command the CLI does not have.
func menuSubs(root *cobra.Command) []*cobra.Command {
	subs := make([]*cobra.Command, 0, len(rootMenu))
	for _, name := range rootMenu {
		for _, c := range root.Commands() {
			if c.Name() == name {
				subs = append(subs, c)

				break
			}
		}
	}

	return subs
}

// initConfig loads the global ($HOME) and repo-local .git-zf.toml files on top
// of the built-in defaults; repo values win. Not being inside a git repo is not
// a fatal error — git zf version/install must work anywhere.
func initConfig() error {
	homePath, err := config.HomePath()
	if err != nil {
		return fmt.Errorf("get home config path: %w", err)
	}

	paths := []string{homePath}
	if repoPath := config.RepoPath(); repoPath != "" {
		paths = append(paths, repoPath)
	}

	appConfig, err = config.Load(paths...)
	if err != nil {
		return fmt.Errorf("failed to load app config: %w", err)
	}
	tui.Configure(appConfig.UI.Split, appConfig.UI.Size)

	return nil
}
