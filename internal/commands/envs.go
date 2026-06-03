package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/runcrate/cli/internal/config"
	"github.com/spf13/cobra"
)

type environment struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
}

var envsCmd = &cobra.Command{
	Use:     "envs",
	Aliases: []string{"environments"},
	Short:   "List environments in the current workspace",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		cfg, _ := config.Load()

		resp, err := client.DoRaw("GET", "/api/v1/environments", nil)
		if err != nil {
			return fmt.Errorf("fetching environments: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		if resp.StatusCode != 200 {
			return fmt.Errorf("API error (HTTP %d): %s", resp.StatusCode, string(body))
		}

		var result struct {
			Data []environment `json:"data"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return fmt.Errorf("parsing response: %w", err)
		}

		if len(result.Data) == 0 {
			fmt.Println("No environments found.")
			return nil
		}

		activeEnv := ""
		if cfg != nil {
			activeEnv = cfg.Environment
		}

		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
		active := lipgloss.NewStyle().Foreground(lipgloss.Color("36"))

		fmt.Println()
		for _, env := range result.Data {
			isActive := strings.EqualFold(env.Name, activeEnv) || (activeEnv == "" && env.IsDefault)
			if isActive {
				fmt.Printf("  %s %s\n", active.Render("●"), env.Name)
			} else {
				fmt.Printf("  %s %s\n", dim.Render("○"), env.Name)
			}
		}
		fmt.Println()

		return nil
	},
}

var envsSwitchCmd = &cobra.Command{
	Use:   "switch [name]",
	Short: "Switch active environment",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		if len(args) == 1 {
			cfg.Environment = args[0]
			if err := config.Save(cfg); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}
			fmt.Printf("Switched to environment: %s\n", args[0])
			return nil
		}

		// Interactive picker
		client, err := loadClient()
		if err != nil {
			return err
		}

		resp, err := client.DoRaw("GET", "/api/v1/environments", nil)
		if err != nil {
			return fmt.Errorf("fetching environments: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		var result struct {
			Data []environment `json:"data"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return err
		}

		if len(result.Data) == 0 {
			return fmt.Errorf("no environments found")
		}

		options := make([]huh.Option[string], len(result.Data))
		for i, env := range result.Data {
			options[i] = huh.NewOption(env.Name, env.Name)
		}

		var selected string
		err = huh.NewSelect[string]().
			Title("Switch environment").
			Options(options...).
			Value(&selected).
			Run()
		if err != nil {
			return fmt.Errorf("cancelled")
		}

		cfg.Environment = selected
		if err := config.Save(cfg); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}

		fmt.Printf("\n  Switched to environment: %s\n\n", selected)
		return nil
	},
}

var envsCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new environment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		body := fmt.Sprintf(`{"name":"%s"}`, args[0])
		resp, err := client.DoRaw("POST", "/api/v1/environments", strings.NewReader(body))
		if err != nil {
			return fmt.Errorf("creating environment: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			b, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("failed (HTTP %d): %s", resp.StatusCode, string(b))
		}

		fmt.Printf("Created environment: %s\n", args[0])
		return nil
	},
}

var envsDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete an environment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		// Resolve environment name to ID
		resp, err := client.DoRaw("GET", "/api/v1/environments", nil)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		var result struct {
			Data []environment `json:"data"`
		}
		json.Unmarshal(body, &result)

		var envID string
		for _, env := range result.Data {
			if strings.EqualFold(env.Name, args[0]) {
				envID = env.ID
				break
			}
		}
		if envID == "" {
			return fmt.Errorf("environment %q not found", args[0])
		}

		delResp, err := client.DoRaw("DELETE", "/api/v1/environments/"+envID, nil)
		if err != nil {
			return fmt.Errorf("deleting environment: %w", err)
		}
		defer delResp.Body.Close()

		if delResp.StatusCode >= 400 {
			b, _ := io.ReadAll(delResp.Body)
			return fmt.Errorf("failed (HTTP %d): %s", delResp.StatusCode, string(b))
		}

		fmt.Printf("Deleted environment: %s\n", args[0])
		return nil
	},
}

func init() {
	envsCmd.AddCommand(envsSwitchCmd)
	envsCmd.AddCommand(envsCreateCmd)
	envsCmd.AddCommand(envsDeleteCmd)
	rootCmd.AddCommand(envsCmd)
}
