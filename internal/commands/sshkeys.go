package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/runcrate/cli/internal/api"
	"github.com/spf13/cobra"
)

var sshKeysCmd = &cobra.Command{
	Use:     "ssh-keys",
	Aliases: []string{"keys"},
	Short:   "Manage SSH keys",
}

var sshKeysListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List SSH keys",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		keys, err := client.ListSSHKeys()
		if err != nil {
			return fmt.Errorf("fetching SSH keys: %w", err)
		}

		if len(keys) == 0 {
			fmt.Println("No SSH keys. Add one with: runcrate ssh-keys add")
			return nil
		}

		w := newTable()
		fmt.Fprintf(w, "NAME\tTYPE\tFINGERPRINT\tADDED\n")
		for _, k := range keys {
			added := k.CreatedAt
			if len(added) >= 10 {
				added = added[:10]
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", k.Name, k.Type, k.Fingerprint, added)
		}
		return w.Flush()
	},
}

var sshKeysAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add an SSH key",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		name, _ := cmd.Flags().GetString("name")
		keyFile, _ := cmd.Flags().GetString("file")
		pubKey, _ := cmd.Flags().GetString("key")

		if name == "" {
			return fmt.Errorf("--name is required")
		}

		if keyFile != "" {
			data, err := os.ReadFile(keyFile)
			if err != nil {
				return fmt.Errorf("reading key file: %w", err)
			}
			pubKey = strings.TrimSpace(string(data))
		}

		if pubKey == "" {
			return fmt.Errorf("provide --key or --file with the public key")
		}

		key, err := client.AddSSHKey(api.AddSSHKeyRequest{
			Name:      name,
			PublicKey: pubKey,
		})
		if err != nil {
			return fmt.Errorf("adding SSH key: %w", err)
		}

		fmt.Printf("Added SSH key: %s\n", key.Name)
		return nil
	},
}

var sshKeysDeleteCmd = &cobra.Command{
	Use:     "delete <id>",
	Aliases: []string{"rm"},
	Short:   "Delete an SSH key",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		if err := client.DeleteSSHKey(args[0]); err != nil {
			return fmt.Errorf("deleting SSH key: %w", err)
		}

		fmt.Println("SSH key deleted.")
		return nil
	},
}

func init() {
	sshKeysAddCmd.Flags().String("name", "", "Key name (required)")
	sshKeysAddCmd.Flags().String("key", "", "Public key string")
	sshKeysAddCmd.Flags().String("file", "", "Path to public key file (e.g., ~/.ssh/id_ed25519.pub)")

	sshKeysCmd.AddCommand(sshKeysListCmd)
	sshKeysCmd.AddCommand(sshKeysAddCmd)
	sshKeysCmd.AddCommand(sshKeysDeleteCmd)
	rootCmd.AddCommand(sshKeysCmd)
}
