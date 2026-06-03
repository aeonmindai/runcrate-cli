package commands

import (
	"fmt"

	"github.com/runcrate/cli/internal/update"
	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update runcrate to the latest version",
	RunE: func(cmd *cobra.Command, args []string) error {
		if update.IsHomebrew() {
			fmt.Println("runcrate was installed via Homebrew.")
			fmt.Println("Run: brew upgrade runcrate")
			return nil
		}

		fmt.Printf("Current version: %s\n", versionStr)
		fmt.Println("Checking for updates...")

		info, err := update.CheckLatestVersion()
		if err != nil {
			return fmt.Errorf("checking for updates: %w", err)
		}

		if !update.IsNewer(versionStr, info.Version) {
			fmt.Println("Already up to date.")
			return nil
		}

		fmt.Printf("Updating to %s...\n", info.Version)
		if err := update.DownloadAndReplace(info); err != nil {
			return fmt.Errorf("update failed: %w", err)
		}

		fmt.Printf("Updated to %s.\n", info.Version)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)
}
