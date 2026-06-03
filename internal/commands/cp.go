package commands

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

var cpCmd = &cobra.Command{
	Use:   "cp <source> <destination>",
	Short: "Copy files to/from an instance",
	Long: `Copy files between your local machine and a running instance via SCP.
Uses ephemeral SSH certificates — no key management needed.

Use instance-name:/path to refer to remote paths.

Examples:
  runcrate cp ./data.tar.gz my-gpu:/workspace/
  runcrate cp my-gpu:/workspace/results.tar.gz ./
  runcrate cp -r ./project my-gpu:/workspace/`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		src := args[0]
		dst := args[1]
		recursive, _ := cmd.Flags().GetBool("recursive")

		// Parse which side is remote
		srcInst, srcPath, srcIsRemote := parseRemotePath(src)
		dstInst, dstPath, dstIsRemote := parseRemotePath(dst)

		if srcIsRemote && dstIsRemote {
			return fmt.Errorf("cannot copy between two remote instances — one side must be local")
		}
		if !srcIsRemote && !dstIsRemote {
			return fmt.Errorf("one side must be an instance path (e.g., my-gpu:/workspace/)")
		}

		// Resolve the remote instance
		var instanceRef string
		if srcIsRemote {
			instanceRef = srcInst
		} else {
			instanceRef = dstInst
		}

		instanceID, ip, err := resolveInstance(client, instanceRef)
		if err != nil {
			return err
		}

		// Generate ephemeral key pair and get certificate
		pubKey, privKey, err := generateEphemeralKeyPair()
		if err != nil {
			return fmt.Errorf("generating ephemeral key: %w", err)
		}

		cert, err := client.GetSSHCertificate(instanceID, pubKey)
		if err != nil {
			return fmt.Errorf("getting SSH certificate: %w", err)
		}

		if cert.IP != "" {
			ip = cert.IP
		}

		// Write temp files
		tempDir, keyPath, certPath, err := writeTempSSHFiles(privKey, cert.Certificate)
		if err != nil {
			return fmt.Errorf("writing temp SSH files: %w", err)
		}
		defer os.RemoveAll(tempDir)

		user := cert.User
		if user == "" {
			user = "root"
		}
		port := fmt.Sprintf("%d", cert.Port)
		if cert.Port == 0 {
			port = "22"
		}

		// Build SCP args
		scpArgs := []string{"scp"}
		scpArgs = append(scpArgs, "-o", "StrictHostKeyChecking=no")
		scpArgs = append(scpArgs, "-o", "UserKnownHostsFile=/dev/null")
		scpArgs = append(scpArgs, "-o", "CertificateFile="+certPath)
		scpArgs = append(scpArgs, "-i", keyPath)
		scpArgs = append(scpArgs, "-P", port)
		if recursive {
			scpArgs = append(scpArgs, "-r")
		}

		if srcIsRemote {
			scpArgs = append(scpArgs, fmt.Sprintf("%s@%s:%s", user, ip, srcPath), dstPath)
		} else {
			scpArgs = append(scpArgs, srcPath, fmt.Sprintf("%s@%s:%s", user, ip, dstPath))
		}

		// Find scp binary
		scpBin, err := exec.LookPath("scp")
		if err != nil {
			return fmt.Errorf("scp not found in PATH")
		}

		return execOrRun(scpBin, scpArgs)
	},
}

// parseRemotePath splits "instance-name:/remote/path" into parts.
func parseRemotePath(s string) (string, string, bool) {
	idx := strings.Index(s, ":")
	if idx <= 0 {
		return "", s, false
	}
	if idx == 1 && len(s) > 2 && s[2] == '\\' {
		return "", s, false
	}
	return s[:idx], s[idx+1:], true
}

func init() {
	cpCmd.Flags().BoolP("recursive", "r", false, "Copy directories recursively")
	rootCmd.AddCommand(cpCmd)
}
