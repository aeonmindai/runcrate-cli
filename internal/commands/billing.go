package commands

import (
	"fmt"
	"strconv"
	"strings"
	"time"

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
		if balance.ActiveUsageCost > 0 {
			fmt.Printf("  %s  $%.2f\n", dim.Render("Active usage"), balance.ActiveUsageCost)
		}
		fmt.Println()

		return nil
	},
}

// parsePeriodFrom converts a period like "7d", "24h", or "2w" into an RFC3339
// lower-bound timestamp plus a human label. Returns ("", "") for empty/invalid
// input (the server then reports all-time usage).
func parsePeriodFrom(period string) (from, label string) {
	period = strings.TrimSpace(period)
	if period == "" {
		return "", ""
	}
	n, err := strconv.Atoi(period[:len(period)-1])
	if err != nil || n <= 0 {
		return "", ""
	}
	var d time.Duration
	switch period[len(period)-1] {
	case 'h':
		d = time.Duration(n) * time.Hour
	case 'd':
		d = time.Duration(n) * 24 * time.Hour
	case 'w':
		d = time.Duration(n) * 7 * 24 * time.Hour
	default:
		return "", ""
	}
	return time.Now().Add(-d).UTC().Format(time.RFC3339), period
}

var billingUsageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Show inference usage summary",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		period, _ := cmd.Flags().GetString("period")
		from, label := parsePeriodFrom(period)

		usage, err := client.GetUsage(from)
		if err != nil {
			return fmt.Errorf("fetching usage: %w", err)
		}

		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
		bold := lipgloss.NewStyle().Bold(true)

		scope := "all time"
		if label != "" {
			scope = "last " + label
		}

		fmt.Println()
		fmt.Printf("  %s %s\n", bold.Render("Inference usage"), dim.Render("("+scope+")"))
		fmt.Printf("    %s  %d\n", dim.Render("Requests"), usage.TotalRequests)
		fmt.Printf("    %s  %d %s\n", dim.Render("Tokens  "), usage.TotalTokens,
			dim.Render(fmt.Sprintf("(%d prompt / %d completion)", usage.TotalPromptTokens, usage.TotalCompletionTokens)))
		fmt.Printf("    %s  %s\n", dim.Render("Cost    "), bold.Render(fmt.Sprintf("$%.4f", usage.TotalCost)))

		// Account context so this isn't only inference numbers. Compute/instance
		// charges show up against the credit balance + active usage.
		if balance, err := client.GetBalance(); err == nil {
			fmt.Println()
			fmt.Printf("  %s  %s\n", dim.Render("Credits     "), bold.Render(fmt.Sprintf("$%.2f", balance.Credits)))
			if balance.ActiveUsageCost > 0 {
				fmt.Printf("  %s  $%.2f\n", dim.Render("Active usage"), balance.ActiveUsageCost)
			}
		}
		fmt.Println()

		return nil
	},
}

func init() {
	billingUsageCmd.Flags().String("period", "", "Usage period (e.g., 24h, 7d, 2w)")
	billingCmd.AddCommand(billingBalanceCmd)
	billingCmd.AddCommand(billingUsageCmd)
	rootCmd.AddCommand(billingCmd)
}
