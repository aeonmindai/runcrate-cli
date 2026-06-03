package commands

import (
	"fmt"

	"github.com/runcrate/cli/internal/auth"
	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Clear stored credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := auth.Logout(); err != nil {
			return fmt.Errorf("logout failed: %w", err)
		}
		fmt.Println("Logged out. Credentials cleared.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(logoutCmd)
}
