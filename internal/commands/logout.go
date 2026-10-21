package commands

import (
	"fmt"
	"os"

	"github.com/runcrate/cli/internal/auth"
	"github.com/runcrate/cli/internal/config"
	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Sign out and clear stored credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		// The stored agent token is signed out on Runcrate first, so a copy of it stops
		// working too. Read from the file itself: a token given in RUNCRATE_ACCESS_TOKEN is
		// not stored, is left alone, and must not hide the stored one.
		if cfg, err := config.LoadFile(); err == nil && auth.IsAgentToken(cfg.AccessToken) {
			if err := auth.RevokeAgentToken(cfg.APIURL, cfg.AccessToken); err != nil {
				fmt.Fprintf(os.Stderr, "Could not sign out on Runcrate (%v). Sign this CLI out on the API keys page of the dashboard.\n", err)
			}
		}
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
