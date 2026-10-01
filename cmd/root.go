package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/piprim/git-zf/cmd/branch"
	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/cmd/commit"
	"github.com/piprim/git-zf/cmd/completion"
	cfgcmd "github.com/piprim/git-zf/cmd/config"
	init_cmd "github.com/piprim/git-zf/cmd/init"
	"github.com/piprim/git-zf/cmd/install"
	"github.com/piprim/git-zf/cmd/issue"
	"github.com/piprim/git-zf/cmd/review"
	"github.com/piprim/git-zf/cmd/uninstall"
	"github.com/piprim/git-zf/cmd/version"
	"github.com/piprim/git-zf/config"
	"github.com/spf13/cobra"
)

// Version is injected at build time via -ldflags.
var (
	Version = "none"

	isDebug   bool
	appConfig *config.AppConfig

	// rootMenu lists, in display order, the workflow commands offered by the
	// menu that `git zf` opens when run without a subcommand. Setup commands
	// (install, uninstall, init, config, completion, version) are deliberately
	// absent: they are one-shot and stay CLI-only, listed by --help.
	rootMenu = []string{"commit", "issue", "branch", "review"}
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
		Use:  appConfig.ProgName,
		Long: `Command line utility to standardize git commit messages, golang version.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmdutil.RunMenu(cmd, appConfig.ProgName+":", menuSubs(cmd), cmdutil.NewHuhMenuPrompter())
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

	return nil
}
