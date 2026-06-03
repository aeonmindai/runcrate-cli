package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("runcrate %s\n", versionStr)
		fmt.Printf("  commit:  %s\n", commitStr)
		fmt.Printf("  built:   %s\n", buildDate)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
