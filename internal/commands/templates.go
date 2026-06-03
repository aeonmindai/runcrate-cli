package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

var templatesCmd = &cobra.Command{
	Use:   "templates",
	Short: "List available templates",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		templates, err := client.ListTemplates()
		if err != nil {
			return fmt.Errorf("fetching templates: %w", err)
		}

		if len(templates) == 0 {
			fmt.Println("No templates available.")
			return nil
		}

		w := newTable()
		fmt.Fprintf(w, "ID\tNAME\tCATEGORY\n")
		for _, t := range templates {
			fmt.Fprintf(w, "%s\t%s\t%s\n", t.ID, t.Name, t.Category)
		}
		return w.Flush()
	},
}

func init() {
	rootCmd.AddCommand(templatesCmd)
}
