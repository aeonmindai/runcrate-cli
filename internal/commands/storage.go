package commands

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/runcrate/cli/internal/api"
	"github.com/spf13/cobra"
)

var storageCmd = &cobra.Command{
	Use:     "volumes",
	Aliases: []string{"vol", "storage"},
	Short:   "Manage volumes",
}

var storageListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all volumes",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		volumes, err := client.ListStorage()
		if err != nil {
			return fmt.Errorf("fetching volumes: %w", err)
		}

		if jsonOutput {
			return printJSON(volumes)
		}

		if len(volumes) == 0 {
			fmt.Println("No storage volumes. Create one with: runcrate storage create")
			return nil
		}

		w := newTable()
		fmt.Fprintf(w, "NAME\tSIZE\tREGION\tSTATUS\t$/HR\n")
		for _, v := range volumes {
			fmt.Fprintf(w, "%s\t%d GB\t%s\t%s\t$%.4f\n",
				v.Name, v.SizeGB, v.Region, v.Status, v.CostPerHr)
		}
		return w.Flush()
	},
}

var storageCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a storage volume",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		name, _ := cmd.Flags().GetString("name")
		size, _ := cmd.Flags().GetInt("size")
		region, _ := cmd.Flags().GetString("region")

		if name == "" {
			return fmt.Errorf("--name is required")
		}
		if size <= 0 {
			return fmt.Errorf("--size is required (in GB)")
		}

		vol, err := client.CreateStorage(api.CreateStorageRequest{
			Name:   name,
			SizeGB: size,
			Region: region,
		})
		if err != nil {
			return fmt.Errorf("creating volume: %w", err)
		}

		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
		fmt.Printf("\n  Created %s %s\n\n", vol.Name, dim.Render(fmt.Sprintf("(%d GB, %s)", vol.SizeGB, vol.Region)))

		return nil
	},
}

var storageDeleteCmd = &cobra.Command{
	Use:     "delete <id>",
	Aliases: []string{"rm"},
	Short:   "Delete a volume",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		if err := client.DeleteStorage(args[0]); err != nil {
			return fmt.Errorf("deleting volume: %w", err)
		}

		fmt.Println("Volume deleted.")
		return nil
	},
}

var storageResizeCmd = &cobra.Command{
	Use:   "resize <id>",
	Short: "Resize a storage volume",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		size, _ := cmd.Flags().GetInt("size")
		if size <= 0 {
			return fmt.Errorf("--size is required (new size in GB)")
		}

		if err := client.ResizeStorage(args[0], size); err != nil {
			return fmt.Errorf("resizing volume: %w", err)
		}

		fmt.Printf("Volume resized to %d GB.\n", size)
		return nil
	},
}

var storageRegionsCmd = &cobra.Command{
	Use:   "regions",
	Short: "List available storage regions",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		regions, err := client.ListStorageRegions()
		if err != nil {
			return fmt.Errorf("fetching regions: %w", err)
		}

		if len(regions) == 0 {
			fmt.Println("No storage regions available.")
			return nil
		}

		w := newTable()
		fmt.Fprintf(w, "ID\tREGION\tPROVIDER\n")
		for _, r := range regions {
			fmt.Fprintf(w, "%s\t%s\t%s\n", r.ID, r.Name, r.Provider)
		}
		return w.Flush()
	},
}

func init() {
	storageCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	storageCreateCmd.Flags().String("name", "", "Volume name (required)")
	storageCreateCmd.Flags().Int("size", 0, "Size in GB (required)")
	storageCreateCmd.Flags().String("region", "", "Region")
	storageResizeCmd.Flags().Int("size", 0, "New size in GB (required)")

	storageCmd.AddCommand(storageListCmd)
	storageCmd.AddCommand(storageCreateCmd)
	storageCmd.AddCommand(storageDeleteCmd)
	storageCmd.AddCommand(storageResizeCmd)
	storageCmd.AddCommand(storageRegionsCmd)
	rootCmd.AddCommand(storageCmd)
}
