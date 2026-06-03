package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/huh/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/runcrate/cli/internal/auth"
	"github.com/runcrate/cli/internal/config"
	"github.com/spf13/cobra"
)

type workspace struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	IsDefault bool   `json:"is_default"`
}

func fetchWorkspaces(cfg *config.Config) ([]workspace, error) {
	if auth.NeedsRefresh(cfg) {
		if err := auth.RefreshTokens(cfg); err != nil {
			return nil, fmt.Errorf("session expired — run: runcrate login")
		}
	}

	url := cfg.APIURL + "/api/v1/workspaces"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AccessToken)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching workspaces: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []workspace `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return result.Data, nil
}

var workspacesCmd = &cobra.Command{
	Use:     "workspaces",
	Aliases: []string{"ws"},
	Short:   "List workspaces",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if !cfg.IsOAuth() {
			return fmt.Errorf("workspace commands require OAuth login — run: runcrate login")
		}

		var workspaces []workspace
		_ = spinner.New().
			Title("  Loading workspaces...").
			Action(func() { workspaces, err = fetchWorkspaces(cfg) }).
			Run()
		if err != nil {
			return err
		}

		if len(workspaces) == 0 {
			fmt.Println("No workspaces found.")
			return nil
		}

		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
		active := lipgloss.NewStyle().Foreground(lipgloss.Color("36"))

		fmt.Println()
		for _, ws := range workspaces {
			if ws.ID == cfg.ProjectID {
				fmt.Printf("  %s %s  %s\n", active.Render("●"), ws.Name, dim.Render(ws.Role))
			} else {
				fmt.Printf("  %s %s  %s\n", dim.Render("○"), ws.Name, dim.Render(ws.Role))
			}
		}
		fmt.Println()

		return nil
	},
}

var workspacesSwitchCmd = &cobra.Command{
	Use:   "switch",
	Short: "Switch to a different workspace",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if !cfg.IsOAuth() {
			return fmt.Errorf("workspace commands require OAuth login — run: runcrate login")
		}

		var workspaces []workspace
		_ = spinner.New().
			Title("  Loading workspaces...").
			Action(func() { workspaces, err = fetchWorkspaces(cfg) }).
			Run()
		if err != nil {
			return err
		}

		if len(workspaces) == 0 {
			return fmt.Errorf("no workspaces found")
		}

		if len(workspaces) == 1 {
			fmt.Printf("Only one workspace available: %s\n", workspaces[0].Name)
			return nil
		}

		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

		options := make([]huh.Option[string], len(workspaces))
		for i, ws := range workspaces {
			label := fmt.Sprintf("%s  %s", ws.Name, dim.Render(ws.Role))
			options[i] = huh.NewOption(label, ws.ID)
		}

		var selectedID string
		err = huh.NewSelect[string]().
			Title("Switch workspace").
			Options(options...).
			Value(&selectedID).
			Run()
		if err != nil {
			return fmt.Errorf("cancelled")
		}

		for _, ws := range workspaces {
			if ws.ID == selectedID {
				cfg.ProjectID = ws.ID
				cfg.ProjectName = ws.Name
				cfg.Role = ws.Role
				cfg.Environment = ""

				if err := config.Save(cfg); err != nil {
					return fmt.Errorf("saving config: %w", err)
				}

				fmt.Printf("\n  Switched to %s %s\n\n", ws.Name, dim.Render("("+ws.Role+")"))
				return nil
			}
		}

		return nil
	},
}

func init() {
	workspacesCmd.AddCommand(workspacesSwitchCmd)
	rootCmd.AddCommand(workspacesCmd)
}
