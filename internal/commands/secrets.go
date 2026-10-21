package commands

import (
	"fmt"
	"io"
	"os"

	"github.com/runcrate/cli/internal/api"
	"github.com/spf13/cobra"
)

// secretsDashboardURL is where secrets are added, changed and removed: the CLI
// only ever sees their names.
const secretsDashboardURL = "https://runcrate.ai/dashboard/settings/secrets"

var secretsCmd = &cobra.Command{
	Use:     "secrets",
	Aliases: []string{"secret"},
	Short:   "See which secrets machines in this environment get",
	Long: "Secrets are environment variables every instance and job launched in an environment gets.\n" +
		"The CLI lists their names only. Values are set in the dashboard, and a value given for one\n" +
		"launch (runcrate run --env NAME=value) wins over a secret of the same name.\n\n" +
		"Add or change secrets at " + secretsDashboardURL,
}

var secretsListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List the names of the environment's secrets (never values)",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}
		list, err := client.ListSecrets()
		if err != nil {
			return fmt.Errorf("fetching secrets: %w", err)
		}

		if jsonOutput {
			return printJSON(list)
		}
		quiet, _ := cmd.Flags().GetBool("quiet")
		return renderSecrets(os.Stdout, list, quiet)
	},
}

// renderSecrets prints the secrets as a table, or one name per line when quiet
// (for scripts: runcrate secrets ls -q | grep -qx HF_TOKEN).
func renderSecrets(w io.Writer, list []api.Secret, quiet bool) error {
	if quiet {
		for _, s := range list {
			fmt.Fprintln(w, s.Key)
		}
		return nil
	}
	if len(list) == 0 {
		fmt.Fprintf(w, "No secrets in this environment. Add them in the dashboard: %s\n", secretsDashboardURL)
		return nil
	}
	t := newTableTo(w)
	fmt.Fprintf(t, "NAME\tSENSITIVE\tUPDATED\n")
	for _, s := range list {
		sensitive := "no"
		if s.Sensitive {
			sensitive = "yes"
		}
		updated := s.UpdatedAt
		if len(updated) >= 10 {
			updated = updated[:10]
		}
		fmt.Fprintf(t, "%s\t%s\t%s\n", s.Key, sensitive, updated)
	}
	return t.Flush()
}

func init() {
	secretsCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	secretsListCmd.Flags().BoolP("quiet", "q", false, "Only show secret names")

	secretsCmd.AddCommand(secretsListCmd)
	rootCmd.AddCommand(secretsCmd)
}
