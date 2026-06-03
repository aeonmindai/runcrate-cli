package main

import (
	"os"

	"github.com/runcrate/cli/internal/commands"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	commands.SetVersionInfo(version, commit, buildDate)
	if err := commands.Execute(); err != nil {
		os.Exit(1)
	}
}
