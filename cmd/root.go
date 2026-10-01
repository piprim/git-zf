package cmd

import (
	"errors"
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
	"github.com/spf13/viper"
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

// initConfig loads the .git-zf.toml config file via Viper, then parses the full
// AppConfig. A fresh viper instance is created per call and threaded through the
// load explicitly, so there is no hidden dependency on package-global viper
// state and no load-order coupling between commands. Not being inside a git repo
// is not a fatal error — git zf version/install must work anywhere.
func initConfig() error {
	v := viper.New()

	// Phase 1: load global config from home directory.
	if err := loadGlobalConfig(v); err != nil {
		return err
	}

	// Phase 2: merge repo-local config on top (repo values win).
	if repoPath := config.RepoPath(); repoPath != "" {
		if err := loadRepoConfig(v, repoPath); err != nil {
			return err
		}
	}

	var err error
	appConfig, err = config.Load(v)
	if err != nil {
		return fmt.Errorf("failed to load app config: %w", err)
	}

	return nil
}

func loadGlobalConfig(v *viper.Viper) error {
	homePath, err := config.HomePath()
	if err != nil {
		return fmt.Errorf("get home config path: %w", err)
	}

	v.SetConfigFile(homePath)

	if err := v.ReadInConfig(); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("could not read global config %s: %w", homePath, err)
		}

		slog.Info("no global config file found")

		return nil
	}

	slog.Debug("loaded global config", "path", homePath)

	return nil
}

func loadRepoConfig(v *viper.Viper, repoPath string) error {
	v.SetConfigFile(repoPath)

	if err := v.MergeInConfig(); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("could not merge repo config %s: %w", repoPath, err)
		}

		slog.Info("no repo config file found")

		return nil
	}

	slog.Debug("merged repo config", "path", repoPath)

	return nil
}
