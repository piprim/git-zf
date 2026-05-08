package cmd

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/piprim/git-zf/cmd/branch"
	"github.com/piprim/git-zf/cmd/commit"
	"github.com/piprim/git-zf/cmd/completion"
	cfgcmd "github.com/piprim/git-zf/cmd/config"
	"github.com/piprim/git-zf/cmd/install"
	"github.com/piprim/git-zf/cmd/issue"
	"github.com/piprim/git-zf/cmd/uninstall"
	"github.com/piprim/git-zf/cmd/version"
	"github.com/piprim/git-zf/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// const configFileExt = "toml"

// Version and Name are injected at build time via -ldflags.
var (
	Version = "none"
	Name    string

	isDebug   bool
	appConfig *config.AppConfig
)

// GetRootCmd builds and returns the root Cobra command.
func GetRootCmd() (*cobra.Command, error) {
	if err := initConfig(); err != nil {
		return nil, err
	}

	rootCmd := &cobra.Command{
		Use:  appConfig.ProgName,
		Long: `Command line utility to standardize git commit messages, golang version.`,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
		},
	}

	rootCmd.PersistentFlags().BoolVarP(&isDebug, "debug", "d", false,
		"debug mode, output debug info to debug.log")

	ir := issue.New(appConfig)
	br := branch.New(appConfig)
	co := commit.New(appConfig)
	cp := completion.New(appConfig)
	in := install.New(appConfig)
	uin := uninstall.New(appConfig)
	vs := version.New(Version, Name)
	cf := cfgcmd.New(appConfig)

	rootCmd.AddCommand(
		cp.GetRootCmd(),
		co.GetRootCmd(),
		ir.GetRootCmd(),
		br.GetRootCmd(),
		vs.GetRootCmd(),
		in.GetRootCmd(),
		uin.GetRootCmd(),
		cf.GetRootCmd(),
	)

	return rootCmd, nil
}

// initConfig sets up logging, loads the .git-zf.toml config file via Viper, then
// parses the full AppConfig. Not being inside a git repo is not a fatal error —
// git zf version/install must work anywhere.
func initConfig() error {
	if !isDebug {
		log.SetOutput(io.Discard)
	} else {
		f, err := os.OpenFile("debug.log", os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("failed to open debug.log: %w", err)
		}

		log.SetFlags(log.Lshortfile | log.LstdFlags)
		log.SetOutput(f)
	}

	// viper.SetConfigType(configFileExt)

	// Phase 1: load global config from home directory.
	err := loadGlobalConfig()
	if err != nil {
		return err
	}

	// Phase 2: merge repo-local config on top (repo values win).
	if repoPath := config.RepoPath(); repoPath != "" {
		err := loadRepoConfig(repoPath)
		if err != nil {
			return nil
		}
	}

	appConfig, err = config.Load()
	if err != nil {
		return fmt.Errorf("failed to load app config: %w", err)
	}

	return nil
}

func loadGlobalConfig() error {
	homePath, err := config.HomePath()
	if err != nil {
		return fmt.Errorf("get home config path: %w", err)
	}

	viper.SetConfigFile(homePath)

	if err := viper.ReadInConfig(); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("could not read global config %s: %w", homePath, err)
		}

		log.Println("no global config file found")
	}

	log.Printf("loaded global config: %s", homePath)

	return nil
}

func loadRepoConfig(repoPath string) error {
	viper.SetConfigFile(repoPath)

	if err := viper.MergeInConfig(); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("could not merge repo config %s: %w", repoPath, err)
		}

		log.Println("no repo config file found")
	}

	log.Printf("merged repo config: %s", repoPath)

	return nil
}
