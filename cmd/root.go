package cmd

import (
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
	"github.com/piprim/git-zf/cmd/version"
	"github.com/piprim/git-zf/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	configFileName = ".git-zf"
	configFileExt  = "json"
)

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
	vs := version.New(Version, Name)
	cf := cfgcmd.New(appConfig)

	rootCmd.AddCommand(
		cp.GetRootCmd(),
		co.GetRootCmd(),
		ir.GetRootCmd(),
		br.GetRootCmd(),
		vs.GetRootCmd(),
		in.GetRootCmd(),
		cf.GetRootCmd(),
	)

	return rootCmd, nil
}

// initConfig sets up logging, loads the .git-zf.json config file via Viper, then
// parses the full AppConfig. Not being inside a git repo is not a fatal error —
// git zf version/install must work anywhere.
func initConfig() error {
	if !isDebug {
		log.SetOutput(io.Discard)
	} else {
		f, err := os.OpenFile("debug.log", os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("failed to open debug.log: %w", err)
		}

		log.SetFlags(log.Lshortfile | log.LstdFlags)
		log.SetOutput(f)
	}

	viper.SetConfigName(configFileName)
	viper.SetConfigType(configFileExt)

	// Repo-root config takes priority over home: add it first so Viper searches it first.
	if root := config.RepoDir(); root != "" {
		viper.AddConfigPath(root)
	}

	home, err := config.HomeDir()
	if err != nil {
		return fmt.Errorf("get home config dir failed: %w", err)
	}
	viper.AddConfigPath(home)

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return fmt.Errorf("read config failed: %w", err)
		}

		log.Println("can not find config file")
	} else {
		log.Println("read config success")
	}

	appConfig, err = config.Load()
	if err != nil {
		return fmt.Errorf("failed to load app config: %w", err)
	}

	return nil
}
