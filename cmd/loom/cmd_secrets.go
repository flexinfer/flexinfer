package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/crb2nu/loom/pkg/secrets"
)

// newSecretsCmd creates the secrets command and its subcommands.
func newSecretsCmd(socketPath string) *cobra.Command {
	secretsCmd := &cobra.Command{
		Use:   "secrets",
		Short: "Manage secrets for MCP servers",
		Long: `Manage secrets used by MCP servers.

Secrets are stored securely and can be referenced in registry.yaml using ${secret:KEY} syntax.
The secret store supports multiple backends in priority order:
  1. Environment variables (read-only, allows override)
  2. macOS Keychain (if available)
  3. 1Password CLI (if configured)
  4. Encrypted file store (~/.config/loom/secrets.enc)`,
	}

	secretsSetCmd := &cobra.Command{
		Use:   "set KEY [VALUE]",
		Short: "Set a secret value",
		Long: `Set a secret value. If VALUE is not provided, prompts for secure input.

Examples:
  loom secrets set GITHUB_TOKEN ghp_xxxx
  loom secrets set API_KEY              # prompts for value`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			var value string

			if len(args) > 1 {
				value = args[1]
			} else {
				// Prompt for value securely
				fmt.Printf("Enter value for %s: ", key)
				if term.IsTerminal(int(os.Stdin.Fd())) {
					byteValue, err := term.ReadPassword(int(os.Stdin.Fd()))
					if err != nil {
						return fmt.Errorf("read password: %w", err)
					}
					fmt.Println() // newline after password input
					value = string(byteValue)
				} else {
					reader := bufio.NewReader(os.Stdin)
					line, err := reader.ReadString('\n')
					if err != nil {
						return fmt.Errorf("read input: %w", err)
					}
					value = strings.TrimSpace(line)
				}
			}

			mgr, err := secrets.DefaultManager()
			if err != nil {
				return fmt.Errorf("init secrets: %w", err)
			}

			if err := mgr.Set(key, value); err != nil {
				return fmt.Errorf("set secret: %w", err)
			}

			fmt.Printf("Secret '%s' stored in %s\n", key, mgr.PrimaryBackend().Name())
			reloadDaemonAfterSecretChange(socketPath, "secret update")
			return nil
		},
	}

	secretsGetCmd := &cobra.Command{
		Use:   "get KEY",
		Short: "Get a secret value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			showSource, _ := cmd.Flags().GetBool("source")

			mgr, err := secrets.DefaultManager()
			if err != nil {
				return fmt.Errorf("init secrets: %w", err)
			}

			value, source, err := mgr.Get(key)
			if err != nil {
				return fmt.Errorf("secret '%s' not found", key)
			}

			if showSource {
				fmt.Printf("%s (from %s)\n", value, source)
			} else {
				fmt.Println(value)
			}
			return nil
		},
	}
	secretsGetCmd.Flags().Bool("source", false, "Show which backend the secret came from")

	secretsListCmd := &cobra.Command{
		Use:   "list",
		Short: "List all secret keys",
		RunE: func(cmd *cobra.Command, args []string) error {
			showBackends, _ := cmd.Flags().GetBool("backends")

			mgr, err := secrets.DefaultManager()
			if err != nil {
				return fmt.Errorf("init secrets: %w", err)
			}

			if showBackends {
				fmt.Println("Configured backends:")
				for i, b := range mgr.Backends() {
					primary := ""
					if mgr.PrimaryBackend() == b {
						primary = " (primary)"
					}
					readonly := ""
					if b.ReadOnly() {
						readonly = " [read-only]"
					}
					fmt.Printf("  %d. %s%s%s\n", i+1, b.Name(), readonly, primary)
				}
				fmt.Println()
			}

			keys, err := mgr.List()
			if err != nil {
				return fmt.Errorf("list secrets: %w", err)
			}

			if len(keys) == 0 {
				fmt.Println("No secrets found")
				return nil
			}

			sort.Strings(keys)
			fmt.Printf("Secrets (%d):\n", len(keys))
			for _, k := range keys {
				fmt.Printf("  %s\n", k)
			}
			return nil
		},
	}
	secretsListCmd.Flags().Bool("backends", false, "Show configured backends")

	secretsDeleteCmd := &cobra.Command{
		Use:   "delete KEY",
		Short: "Delete a secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]

			mgr, err := secrets.DefaultManager()
			if err != nil {
				return fmt.Errorf("init secrets: %w", err)
			}

			if err := mgr.Delete(key); err != nil {
				return fmt.Errorf("delete secret: %w", err)
			}

			fmt.Printf("Secret '%s' deleted\n", key)
			reloadDaemonAfterSecretChange(socketPath, "secret delete")
			return nil
		},
	}

	secretsImportCmd := &cobra.Command{
		Use:   "import FILE",
		Short: "Import secrets from an env file",
		Long: `Import secrets from a .env file into the secret store.

The file should contain KEY=VALUE pairs, one per line.
Lines starting with # are ignored.
Export statements (export KEY=VALUE) are also supported.

Example:
  loom secrets import ~/.config/secrets/ai.env`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath := args[0]
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			// Expand ~ in path
			if strings.HasPrefix(filePath, "~") {
				home, _ := os.UserHomeDir()
				filePath = filepath.Join(home, filePath[1:])
			}

			file, err := os.Open(filePath)
			if err != nil {
				return fmt.Errorf("open file: %w", err)
			}
			defer file.Close()

			var mgr *secrets.Manager
			if !dryRun {
				mgr, err = secrets.DefaultManager()
				if err != nil {
					return fmt.Errorf("init secrets: %w", err)
				}
			}

			scanner := bufio.NewScanner(file)
			imported := 0
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())

				// Skip empty lines and comments
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}

				// Handle export prefix
				line = strings.TrimPrefix(line, "export ")

				// Parse KEY=VALUE
				idx := strings.Index(line, "=")
				if idx == -1 {
					continue
				}

				key := strings.TrimSpace(line[:idx])
				value := strings.TrimSpace(line[idx+1:])

				// Remove quotes from value
				if len(value) >= 2 {
					if (value[0] == '"' && value[len(value)-1] == '"') ||
						(value[0] == '\'' && value[len(value)-1] == '\'') {
						value = value[1 : len(value)-1]
					}
				}

				// Skip variable references like $VAR or ${VAR}
				if strings.Contains(value, "$") {
					continue
				}

				if dryRun {
					fmt.Printf("Would import: %s\n", key)
				} else {
					if err := mgr.Set(key, value); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: failed to set %s: %v\n", key, err)
						continue
					}
					fmt.Printf("Imported: %s\n", key)
				}
				imported++
			}

			if err := scanner.Err(); err != nil {
				return fmt.Errorf("read file: %w", err)
			}

			if dryRun {
				fmt.Printf("\nWould import %d secrets (dry-run)\n", imported)
			} else {
				fmt.Printf("\nImported %d secrets to %s\n", imported, mgr.PrimaryBackend().Name())
				if imported > 0 {
					reloadDaemonAfterSecretChange(socketPath, "secret import")
				}
			}
			return nil
		},
	}
	secretsImportCmd.Flags().Bool("dry-run", false, "Show what would be imported without storing")

	secretsCmd.AddCommand(secretsSetCmd, secretsGetCmd, secretsListCmd, secretsDeleteCmd, secretsImportCmd)
	return secretsCmd
}
