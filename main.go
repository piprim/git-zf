package main

import (
	"log"
	"os"

	"github.com/piprim/git-zf/cmd"
)

func main() {
	rootCmd, err := cmd.GetRootCmd()
	if err != nil {
		fatalError(err)
	}

	err = rootCmd.Execute()
	if err != nil {
		fatalError(err)
	}

	os.Exit(0)
}

func fatalError(err error) {
	log.SetOutput(os.Stderr)

	//nolint:revive // It's call by main only.
	log.Fatal(err)
}
