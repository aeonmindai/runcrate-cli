package commands

import (
	"fmt"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/huh/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/runcrate/cli/internal/api"
	"github.com/runcrate/cli/internal/auth"
	"github.com/runcrate/cli/internal/config"
	"github.com/spf13/cobra"
)

var (
	dimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	boldStyle = lipgloss.NewStyle().Bold(true)
)

var loginURL string

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with Runcrate via browser",
	Long: `Opens your browser to log in to Runcrate and select a workspace.

By default it targets the configured app URL (api_url, default https://runcrate.ai).
To authenticate against a different deployment — a Vercel preview or a local dev
server — pass --url, or set it persistently with "runcrate config set domain <url>"
or the RUNCRATE_API_URL environment variable.

  runcrate login --url my-branch.preview.runcrate.ai
  runcrate login --url http://localhost:3000`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			cfg = &config.Config{}
		}
		apiURL := cfg.APIURL
		if loginURL != "" {
			// --url overrides the configured/default target for this login only.
			apiURL = config.NormalizeURL(loginURL)
		}
		if apiURL == "" {
			apiURL = "https://runcrate.ai"
		}

		port, resultCh, shutdown := auth.StartCallbackServer(120 * time.Second)
		defer shutdown()

		if port == 0 {
			result := <-resultCh
			return result.Error
		}

		authURL := fmt.Sprintf("%s/auth/authorize?client=cli&port=%d", apiURL, port)

		// Always show the URL: OpenBrowser can report success even when no
		// browser actually appears (headless/SSH session, no display), so a
		// printed URL is the only thing that keeps a non-GUI terminal unstuck.
		fmt.Println()
		fmt.Printf("  Authorizing the Runcrate CLI at %s\n", dimStyle.Render(apiURL))
		fmt.Println("  Your browser should open automatically. If it doesn't, open this URL:")
		fmt.Println()
		fmt.Printf("  %s\n", boldStyle.Render(authURL))
		fmt.Println()
		if err := auth.OpenBrowser(authURL); err != nil {
			fmt.Printf("  %s\n\n", dimStyle.Render("(couldn't open a browser automatically: "+err.Error()+")"))
		}

		// Wait for browser callback with spinner
		var result auth.CallbackResult
		_ = spinner.New().
			Title("  Waiting for browser authentication...").
			Action(func() { result = <-resultCh }).
			Run()

		if result.Error != nil {
			return fmt.Errorf("login failed: %w", result.Error)
		}

		// Exchange code for tokens
		var token *api.OAuthTokenResponse
		err = spinner.New().
			Title("  Exchanging credentials...").
			Action(func() { token, err = api.ExchangeCodeOAuth(apiURL, result.Code) }).
			Run()
		if err != nil {
			return err
		}
		if token == nil {
			return fmt.Errorf("failed to exchange credentials")
		}

		// Pick workspace
		ws, err := pickWorkspace(token.Workspaces)
		if err != nil {
			return err
		}

		if err := auth.LoginWithOAuth(token, ws, apiURL); err != nil {
			return err
		}

		fmt.Println()
		fmt.Printf("  Logged in. %s %s\n", ws.Name, dimStyle.Render("("+ws.Role+")"))
		fmt.Println()

		return nil
	},
}

func pickWorkspace(workspaces []api.OAuthWorkspace) (*api.OAuthWorkspace, error) {
	if len(workspaces) == 0 {
		return nil, fmt.Errorf("no workspaces found for this account")
	}

	if len(workspaces) == 1 {
		return &workspaces[0], nil
	}

	options := make([]huh.Option[string], len(workspaces))
	defaultID := ""
	for i, ws := range workspaces {
		label := fmt.Sprintf("%s  %s", ws.Name, dimStyle.Render(ws.Role))
		options[i] = huh.NewOption(label, ws.ID)
		if ws.IsDefault {
			defaultID = ws.ID
		}
	}

	var selectedID string
	err := huh.NewSelect[string]().
		Title("Select a workspace").
		Options(options...).
		Value(&selectedID).
		Run()

	if err != nil {
		return nil, fmt.Errorf("workspace selection cancelled")
	}

	if selectedID == "" {
		selectedID = defaultID
	}

	for i := range workspaces {
		if workspaces[i].ID == selectedID {
			return &workspaces[i], nil
		}
	}

	return &workspaces[0], nil
}

func init() {
	loginCmd.Flags().StringVar(&loginURL, "url", "", "App URL to authenticate against (e.g. a preview deployment); overrides api_url for this login")
	rootCmd.AddCommand(loginCmd)
}
