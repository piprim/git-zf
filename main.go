package main

import (
	"log/slog"
	"os"

	"github.com/piprim/git-zf/cmd"
	"github.com/spf13/cobra"
)

func main() {
	rootCmd, err := cmd.GetRootCmd()
	if err != nil {
		fatalError(rootCmd, err)
	}

	err = rootCmd.Execute()
	if err != nil {
		fatalError(rootCmd, err)
	}
}

func fatalError(ccmd *cobra.Command, err error) {
	h := slog.NewTextHandler(ccmd.ErrOrStderr(), &slog.HandlerOptions{Level: slog.LevelError})
	slog.New(h).Error(err.Error())

	//nolint:revive // It's call by main only.
	os.Exit(1)
}
