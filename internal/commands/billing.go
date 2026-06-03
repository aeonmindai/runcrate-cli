package commands

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var billingCmd = &cobra.Command{
	Use:   "billing",
	Short: "View billing and usage",
}

var billingBalanceCmd = &cobra.Command{
	Use:   "balance",
	Short: "Show current credit balance",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		balance, err := client.GetBalance()
		if err != nil {
			return fmt.Errorf("fetching balance: %w", err)
		}

		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
		bold := lipgloss.NewStyle().Bold(true)

		fmt.Println()
		fmt.Printf("  %s  %s\n", dim.Render("Credits"), bold.Render(fmt.Sprintf("$%.2f", balance.Credits)))
		if balance.AutoRecharge {
			fmt.Printf("  %s  enabled\n", dim.Render("Auto-recharge"))
		}
		fmt.Println()

		return nil
	},
}

var billingUsageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Show usage summary",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		period, _ := cmd.Flags().GetString("period")
		usage, err := client.GetUsage(period)
		if err != nil {
			return fmt.Errorf("fetching usage: %w", err)
		}

		return printJSON(usage)
	},
}

func init() {
	billingUsageCmd.Flags().String("period", "", "Usage period (e.g., 7d, 30d)")
	billingCmd.AddCommand(billingBalanceCmd)
	billingCmd.AddCommand(billingUsageCmd)
	rootCmd.AddCommand(billingCmd)
}
