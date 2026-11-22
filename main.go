package main

import (
	"log/slog"
	"os"

	"github.com/piprim/git-zf/cmd"
	"github.com/piprim/git-zf/tui"
)

func main() {
	// A screen may have split the terminal: give it back whole.
	defer tui.CloseScreen()

	rootCmd, err := cmd.GetRootCmd()
	if err != nil {
		fatalError(err)
	}

	err = rootCmd.Execute()
	if err != nil {
		fatalError(err)
	}
}

func fatalError(err error) {
	tui.CloseScreen() // os.Exit runs no defer
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})
	slog.New(h).Error(err.Error())

	//nolint:revive // It's call by main only.
	os.Exit(1)
}
