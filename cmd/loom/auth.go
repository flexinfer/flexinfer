package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/pkg/secrets"
)

const defaultHTTPTokenKey = "LOOM_HTTP_TOKEN"

func newAuthCmd() *cobra.Command {
	authCmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication for Streamable HTTP",
	}

	tokenGenerateCmd := &cobra.Command{
		Use:   "token-generate",
		Short: "Generate a new bearer token for HTTP authentication",
		Long: `Generate a cryptographically random bearer token and store it in the secret store.

The token is stored under the key LOOM_HTTP_TOKEN (or a custom key with --key).
Configure the daemon to use it by setting http.auth.type: token in config.yaml.

Example:
  loom auth token-generate
  loom auth token-generate --key MY_CUSTOM_TOKEN`,
		RunE: func(cmd *cobra.Command, args []string) error {
			key, _ := cmd.Flags().GetString("key")
			if key == "" {
				key = defaultHTTPTokenKey
			}

			// Generate 32 bytes of randomness → 64-char hex string with "loom_" prefix
			b := make([]byte, 32)
			if _, err := rand.Read(b); err != nil {
				return fmt.Errorf("generate random token: %w", err)
			}
			token := "loom_" + hex.EncodeToString(b)

			mgr, err := secrets.DefaultManager()
			if err != nil {
				return fmt.Errorf("init secrets: %w", err)
			}

			if err := mgr.Set(key, token); err != nil {
				return fmt.Errorf("store token: %w", err)
			}

			fmt.Printf("Token: %s\n", token)
			fmt.Printf("Stored as: %s (in %s)\n", key, mgr.PrimaryBackend().Name())
			fmt.Println()
			fmt.Println("Configure daemon:")
			fmt.Println("  http:")
			fmt.Println("    auth:")
			fmt.Println("      type: token")
			fmt.Printf("      token_secret_key: %s\n", key)
			return nil
		},
	}
	tokenGenerateCmd.Flags().String("key", defaultHTTPTokenKey, "Secret store key for the token")

	tokenShowCmd := &cobra.Command{
		Use:   "token-show",
		Short: "Display the current HTTP bearer token",
		RunE: func(cmd *cobra.Command, args []string) error {
			key, _ := cmd.Flags().GetString("key")
			if key == "" {
				key = defaultHTTPTokenKey
			}

			mgr, err := secrets.DefaultManager()
			if err != nil {
				return fmt.Errorf("init secrets: %w", err)
			}

			value, source, err := mgr.Get(key)
			if err != nil {
				return fmt.Errorf("token not found (generate with: loom auth token-generate)")
			}

			fmt.Printf("Token: %s\n", value)
			fmt.Printf("Source: %s\n", source)
			return nil
		},
	}
	tokenShowCmd.Flags().String("key", defaultHTTPTokenKey, "Secret store key for the token")

	tokenRevokeCmd := &cobra.Command{
		Use:   "token-revoke",
		Short: "Delete the HTTP bearer token",
		RunE: func(cmd *cobra.Command, args []string) error {
			key, _ := cmd.Flags().GetString("key")
			if key == "" {
				key = defaultHTTPTokenKey
			}

			mgr, err := secrets.DefaultManager()
			if err != nil {
				return fmt.Errorf("init secrets: %w", err)
			}

			if err := mgr.Delete(key); err != nil {
				return fmt.Errorf("delete token: %w", err)
			}

			fmt.Printf("Token '%s' revoked\n", key)
			return nil
		},
	}
	tokenRevokeCmd.Flags().String("key", defaultHTTPTokenKey, "Secret store key for the token")

	authCmd.AddCommand(tokenGenerateCmd, tokenShowCmd, tokenRevokeCmd)
	return authCmd
}
