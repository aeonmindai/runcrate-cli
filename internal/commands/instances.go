package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/runcrate/cli/internal/api"
	"github.com/spf13/cobra"
)

// parseGPUFlag parses the --gpu flag value which may contain a colon-separated
// count (e.g. "H100:2"). Returns the GPU type and count. If --gpu-count was
// explicitly set alongside colon syntax, returns an error.
func parseGPUFlag(gpuFlag string, gpuCountFlag int, gpuCountChanged bool) (string, int, error) {
	if gpuFlag == "" {
		return "", gpuCountFlag, nil
	}

	parts := strings.SplitN(gpuFlag, ":", 2)
	if len(parts) == 2 {
		// Colon syntax: "H100:2"
		if gpuCountChanged {
			return "", 0, fmt.Errorf("cannot use both --gpu %s (colon syntax) and --gpu-count; pick one", gpuFlag)
		}
		count, err := strconv.Atoi(parts[1])
		if err != nil || count < 1 {
			return "", 0, fmt.Errorf("invalid GPU count in %q — expected a positive integer after the colon", gpuFlag)
		}
		return parts[0], count, nil
	}

	// No colon — use gpuCountFlag as-is (defaults to 1 for create, 0 for types)
	return gpuFlag, gpuCountFlag, nil
}

var jsonOutput bool

var instancesCmd = &cobra.Command{
	Use:     "instances",
	Aliases: []string{"i"},
	Short:   "Manage GPU instances",
}

// ── list ────────────────────────────────────────────────────────────────────

var instancesListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all instances",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		search, _ := cmd.Flags().GetString("search")
		instances, err := client.ListInstances(search)
		if err != nil {
			return fmt.Errorf("fetching instances: %w", err)
		}

		if jsonOutput {
			return printJSON(instances)
		}

		quiet, _ := cmd.Flags().GetBool("quiet")
		if quiet {
			for _, inst := range instances {
				fmt.Println(inst.ID)
			}
			return nil
		}

		if len(instances) == 0 {
			fmt.Println("No instances found. Create one with: runcrate instances create")
			return nil
		}

		w := newTable()
		fmt.Fprintf(w, "NAME\tSTATUS\tGPU\tIP\t$/HR\n")
		for _, inst := range instances {
			gpu := inst.GPUType
			if inst.GPUCount > 1 {
				gpu = fmt.Sprintf("%dx %s", inst.GPUCount, inst.GPUType)
			}
			ip := inst.IP
			if ip == "" {
				ip = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t$%.2f\n",
				truncate(inst.Name, 24),
				statusColor(inst.Status),
				gpu, ip, inst.CostPerHour,
			)
		}
		return w.Flush()
	},
}

// ── types ───────────────────────────────────────────────────────────────────

var instancesTypesCmd = &cobra.Command{
	Use:   "types",
	Short: "List available GPU types and pricing",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		gpuRaw, _ := cmd.Flags().GetString("gpu")
		region, _ := cmd.Flags().GetString("region")
		gpuCountRaw, _ := cmd.Flags().GetInt("gpu-count")

		gpuType, gpuCount, err := parseGPUFlag(gpuRaw, gpuCountRaw, cmd.Flags().Changed("gpu-count"))
		if err != nil {
			return err
		}

		types, err := client.ListInstanceTypes(gpuType, region, gpuCount)
		if err != nil {
			return fmt.Errorf("fetching instance types: %w", err)
		}

		if jsonOutput {
			return printJSON(types)
		}

		if len(types) == 0 {
			fmt.Println("No GPU types available matching your filters.")
			return nil
		}

		w := newTable()
		fmt.Fprintf(w, "ID\tGPU\tCOUNT\tCPU\tRAM\tSTORAGE\tREGION\t$/HR\n")
		for _, t := range types {
			fmt.Fprintf(w, "%s\t%s\t%d\t%d cores\t%d GB\t%d GB\t%s\t$%.2f\n",
				t.ID,
				t.GPUType, t.GPUCount,
				t.CPUCores, t.MemoryGB, t.StorageGB,
				t.Region, t.HourlyRate,
			)
		}
		return w.Flush()
	},
}

// ── create ──────────────────────────────────────────────────────────────────

var instancesCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create and deploy a new instance",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		name, _ := cmd.Flags().GetString("name")
		gpuRaw, _ := cmd.Flags().GetString("gpu")
		gpuCountRaw, _ := cmd.Flags().GetInt("gpu-count")
		region, _ := cmd.Flags().GetString("region")
		template, _ := cmd.Flags().GetString("template")
		typeID, _ := cmd.Flags().GetString("type-id")

		gpuType, gpuCount, err := parseGPUFlag(gpuRaw, gpuCountRaw, cmd.Flags().Changed("gpu-count"))
		if err != nil {
			return err
		}

		if name == "" {
			return fmt.Errorf("--name is required")
		}
		if gpuType == "" && typeID == "" {
			return fmt.Errorf("provide --gpu for auto-deploy or --type-id from 'runcrate instances types'")
		}

		// Check for duplicate active instance name
		existing, _ := client.ListInstances("")
		for _, inst := range existing {
			if strings.EqualFold(inst.Name, name) && isActiveInstance(inst.Status) {
				return fmt.Errorf("instance %q already exists (ID: %s, status: %s). Use a different name or delete it first", name, inst.ID, inst.Status)
			}
		}

		req := api.CreateInstanceRequest{
			Name:           name,
			GPUType:        gpuType,
			GPUCount:       gpuCount,
			Region:         region,
			Template:       template,
			InstanceTypeID: typeID,
		}

		instance, err := client.CreateInstance(req)
		if err != nil {
			return fmt.Errorf("creating instance: %w", err)
		}

		if jsonOutput {
			return printJSON(instance)
		}

		gpu := instance.GPUType
		if instance.GPUCount > 1 {
			gpu = fmt.Sprintf("%dx %s", instance.GPUCount, instance.GPUType)
		}

		fmt.Println()
		fmt.Printf("  Instance queued: %s (%s)\n", instance.Name, gpu)
		fmt.Printf("  ID:    %s\n", instance.ID)
		fmt.Printf("  Cost:  $%.2f/hr\n", instance.CostPerHour)
		fmt.Println()
		fmt.Println("  Provisioning in the background. Track with:")
		fmt.Printf("    runcrate instances status %s\n", instance.Name)
		fmt.Printf("    runcrate instances status %s\n", instance.ID)
		fmt.Println()
		fmt.Println("  Once running, connect with:")
		fmt.Printf("    runcrate ssh %s\n", instance.Name)
		fmt.Println()
		return nil
	},
}

// ── info ────────────────────────────────────────────────────────────────────

var instancesInfoCmd = &cobra.Command{
	Use:   "info <name-or-id>",
	Short: "Show instance details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		id, err := resolveInstanceID(client, args[0])
		if err != nil {
			return err
		}

		instance, err := client.GetInstance(id)
		if err != nil {
			return fmt.Errorf("fetching instance: %w", err)
		}

		if jsonOutput {
			return printJSON(instance)
		}

		fmt.Println()
		fmt.Printf("  Name:      %s\n", instance.Name)
		fmt.Printf("  ID:        %s\n", instance.ID)
		fmt.Printf("  Status:    %s\n", statusColor(instance.Status))
		fmt.Printf("  GPU:       %dx %s\n", instance.GPUCount, instance.GPUType)
		fmt.Printf("  CPU:       %d cores\n", instance.CPUCores)
		fmt.Printf("  Memory:    %d GB\n", instance.Memory)
		fmt.Printf("  Storage:   %d GB\n", instance.Storage)
		fmt.Printf("  Region:    %s\n", instance.Region)
		if instance.IP != "" {
			fmt.Printf("  IP:        %s\n", instance.IP)
		}
		fmt.Printf("  Image:     %s\n", instance.OSImage)
		fmt.Printf("  Cost:      $%.2f/hr\n", instance.CostPerHour)
		if instance.DeployedAt != "" {
			fmt.Printf("  Deployed:  %s\n", instance.DeployedAt)
		}
		fmt.Printf("  Created:   %s\n", instance.CreatedAt)
		fmt.Println()
		return nil
	},
}

// ── status ──────────────────────────────────────────────────────────────────

var instancesStatusCmd = &cobra.Command{
	Use:   "status <name-or-id>",
	Short: "Check live instance status",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		id, err := resolveInstanceID(client, args[0])
		if err != nil {
			return err
		}

		status, err := client.GetInstanceStatus(id)
		if err != nil {
			return fmt.Errorf("fetching status: %w", err)
		}

		if jsonOutput {
			return printJSON(status)
		}

		fmt.Printf("  ID:      %s\n", status.ID)
		fmt.Printf("  Status:  %s\n", statusColor(status.Status))
		if status.IP != "" {
			fmt.Printf("  IP:      %s\n", status.IP)
		}
		return nil
	},
}

// ── delete ──────────────────────────────────────────────────────────────────

var instancesDeleteCmd = &cobra.Command{
	Use:     "delete <name-or-id>",
	Aliases: []string{"rm"},
	Short:   "Terminate an instance",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		id, err := resolveInstanceID(client, args[0])
		if err != nil {
			return err
		}

		fmt.Printf("Terminating instance %s...\n", args[0])
		if err := client.DeleteInstance(id); err != nil {
			return fmt.Errorf("deleting instance: %w", err)
		}

		fmt.Println("Instance terminated.")
		return nil
	},
}

// resolveInstanceID resolves a name or ID to an instance UUID.
func resolveInstanceID(client *api.Client, ref string) (string, error) {
	// Try direct ID lookup first
	inst, err := client.GetInstance(ref)
	if err == nil {
		return inst.ID, nil
	}

	// Fall back to name search
	instances, listErr := client.ListInstances("")
	if listErr != nil {
		return "", fmt.Errorf("fetching instances: %w", listErr)
	}

	var matches []api.Instance
	for _, inst := range instances {
		if strings.EqualFold(inst.Name, ref) {
			matches = append(matches, inst)
		}
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("instance %q not found", ref)
	}
	if len(matches) > 1 {
		fmt.Printf("Multiple instances named %q found:\n", ref)
		for _, m := range matches {
			fmt.Printf("  %s  %s  %s\n", m.ID, statusColor(m.Status), m.GPUType)
		}
		return "", fmt.Errorf("use the instance ID instead of the name")
	}

	return matches[0].ID, nil
}

// isActiveInstance returns true if the instance is not terminated/failed.
func isActiveInstance(status string) bool {
	s := strings.ToLower(status)
	return s != "terminated" && s != "failed"
}

// ── init ────────────────────────────────────────────────────────────────────

func init() {
	// Global --json flag on parent
	instancesCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output as JSON")

	// list
	instancesListCmd.Flags().String("search", "", "Filter instances by name")
	instancesListCmd.Flags().BoolP("quiet", "q", false, "Only show instance IDs")

	// types
	instancesTypesCmd.Flags().String("gpu", "", "Filter by GPU type (e.g., A100 or A100:4)")
	instancesTypesCmd.Flags().String("region", "", "Filter by region (e.g., us-east)")
	instancesTypesCmd.Flags().Int("gpu-count", 0, "Filter by GPU count")

	// create
	instancesCreateCmd.Flags().String("name", "", "Instance name (required)")
	instancesCreateCmd.Flags().String("gpu", "", "GPU type (e.g., H100 or H100:2 for multi-GPU)")
	instancesCreateCmd.Flags().Int("gpu-count", 1, "Number of GPUs (alternative to colon syntax)")
	instancesCreateCmd.Flags().String("region", "", "Preferred region")
	instancesCreateCmd.Flags().String("template", "", "OS template")
	instancesCreateCmd.Flags().String("type-id", "", "Instance type ID from 'runcrate instances types'")

	// Wire subcommands
	instancesCmd.AddCommand(instancesListCmd)
	instancesCmd.AddCommand(instancesTypesCmd)
	instancesCmd.AddCommand(instancesCreateCmd)
	instancesCmd.AddCommand(instancesInfoCmd)
	instancesCmd.AddCommand(instancesStatusCmd)
	instancesCmd.AddCommand(instancesDeleteCmd)
	rootCmd.AddCommand(instancesCmd)
}
