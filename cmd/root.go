package cmd

import (
	"fmt"
	"io"
	"log"
	"os"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	configFileName = ".git-zf"
	configFileExt  = "json"
	progName       = "git-zf"
)

var (
	isDebug   bool
	appConfig config.AppConfig
)

// GetRootCmd builds and returns the root Cobra command.
func GetRootCmd() (*cobra.Command, error) {
	rootCmd := &cobra.Command{
		Use:  progName,
		Long: `Command line utility to standardize git commit messages, golang version.`,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
		},
	}

	if err := initConfig(); err != nil {
		return nil, err
	}

	rootCmd.PersistentFlags().BoolVarP(&isDebug, "debug", "d", false,
		"debug mode, output debug info to debug.log")

	rootCmd.AddCommand(getCommitCmd(), getIssueCmd(), getBranchCmd(), getVersionCmd(), getInstallCmd())

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
	if client, err := git.NewClient(); err == nil {
		if root, err := client.WorkingTreeRoot(); err == nil && root != "" {
			viper.AddConfigPath(root)
		}
	}

	home, err := homedir.Dir()
	if err != nil {
		return fmt.Errorf("get home dir failed: %w", err)
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
