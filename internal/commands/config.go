package commands

import (
	"fmt"

	"github.com/runcrate/cli/internal/config"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage CLI configuration",
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a configuration value",
	Long: `Set a configuration value. Available keys:
  api_url      The Runcrate app/API base URL (aliases: domain, site_url, base_url, url)
  environment  Active environment name

The URL may be given without a scheme (https:// is assumed) and a trailing
slash is dropped, so all of these are equivalent:
  runcrate config set domain runcrate.ai
  runcrate config set api_url https://runcrate.ai/`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key, value := args[0], args[1]

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		switch key {
		case "api_url", "domain", "site_url", "base_url", "url":
			value = config.NormalizeURL(value)
			cfg.APIURL = value
			key = "api_url"
		case "environment":
			cfg.Environment = value
		default:
			return fmt.Errorf("unknown config key %q (available: api_url, environment)", key)
		}

		if err := config.Save(cfg); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}

		fmt.Printf("Set %s = %s\n", key, value)
		return nil
	},
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Display current configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		fmt.Printf("Config file:   %s\n", config.ConfigPath())
		fmt.Printf("API URL:       %s\n", cfg.APIURL)

		if cfg.IsOAuth() {
			fmt.Printf("Auth:          OAuth\n")
			fmt.Printf("Account:       %s\n", cfg.UserEmail)
			fmt.Printf("Workspace:     %s (%s)\n", cfg.ProjectName, cfg.Role)
			env := cfg.Environment
			if env == "" {
				env = "default"
			}
			fmt.Printf("Environment:   %s\n", env)
		} else if cfg.APIKey != "" {
			fmt.Printf("Auth:          API key (legacy)\n")
		} else {
			fmt.Printf("Auth:          Not authenticated (run: runcrate login)\n")
		}

		return nil
	},
}

func init() {
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configShowCmd)
	rootCmd.AddCommand(configCmd)
}
