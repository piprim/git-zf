package main

import (
	"log"

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
	log.SetOutput(ccmd.OutOrStderr())

	//nolint:revive,deep-exit // It's call by main only.
	log.Fatal(err)
}
