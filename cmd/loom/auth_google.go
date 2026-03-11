package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/crb2nu/loom/pkg/googleworkspace"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/secrets"
)

func newGoogleAuthCmd(socketPath string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "google",
		Short: "Manage Google Workspace OAuth credentials for Loom MCP tools",
	}

	loginCmd := &cobra.Command{
		Use:   "login",
		Short: "Run a browser-based Google OAuth login and store the session in Loom secrets",
		RunE: func(cmd *cobra.Command, args []string) error {
			scopeInput, _ := cmd.Flags().GetString("scopes")
			clientJSONPath, _ := cmd.Flags().GetString("client-json")
			listenHost, _ := cmd.Flags().GetString("listen-host")
			noBrowser, _ := cmd.Flags().GetBool("no-browser")
			timeout, _ := cmd.Flags().GetDuration("timeout")

			mgr, err := secrets.DefaultManager()
			if err != nil {
				return fmt.Errorf("init secrets: %w", err)
			}

			scopes, err := googleworkspace.ParseScopes(scopeInput)
			if err != nil {
				return err
			}

			creds, err := loadGoogleClientCredentials(mgr, clientJSONPath)
			if err != nil {
				return err
			}
			creds.Scopes = scopes

			if err := googleworkspace.SaveClientCredentials(mgr, creds); err != nil {
				return fmt.Errorf("store Google client credentials: %w", err)
			}

			baseHTTPClient := httpclient.NewDefault().HTTP()
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			existingRefresh := ""
			if existing, existingErr := googleworkspace.LoadRuntimeCredentials(mgr); existingErr == nil {
				existingRefresh = existing.RefreshToken
			}

			token, err := runGoogleLoopbackLogin(ctx, baseHTTPClient, creds, listenHost, !noBrowser)
			if err != nil {
				return err
			}

			refreshToken := strings.TrimSpace(token.RefreshToken)
			if refreshToken == "" {
				refreshToken = existingRefresh
			}
			if refreshToken == "" {
				return fmt.Errorf("google did not return a refresh token; re-run login with a fresh consent grant")
			}

			authenticated := *creds
			authenticated.RefreshToken = refreshToken

			accountEmail := ""
			userInfo, err := googleworkspace.FetchUserInfo(ctx, baseHTTPClient, &authenticated)
			if err == nil {
				accountEmail = strings.TrimSpace(userInfo.Email)
			}
			if accountEmail == "" {
				accountEmail = creds.AccountEmail
			}

			if err := googleworkspace.SaveSession(mgr, refreshToken, scopes, accountEmail); err != nil {
				return fmt.Errorf("store Google Workspace session: %w", err)
			}

			fmt.Printf("Google Workspace session stored in %s\n", mgr.PrimaryBackend().Name())
			if accountEmail != "" {
				fmt.Printf("Account: %s\n", accountEmail)
			}
			fmt.Printf("Scopes: %s\n", googleworkspace.FormatScopes(scopes))
			fmt.Println()
			fmt.Println("Registry-backed MCP usage will now resolve:")
			fmt.Printf("  %s\n", googleworkspace.SecretClientID)
			fmt.Printf("  %s\n", googleworkspace.SecretClientSecret)
			fmt.Printf("  %s\n", googleworkspace.SecretRefreshToken)
			fmt.Printf("  %s\n", googleworkspace.SecretScopes)
			reloadDaemonAfterSecretChange(socketPath, "Google Workspace auth update")
			return nil
		},
	}
	loginCmd.Flags().String("client-json", "", "Path to a Google OAuth desktop client JSON file (stored into Loom secrets before login)")
	loginCmd.Flags().String("scopes", "full", "Scope preset(s) or comma-delimited scopes (e.g. full, readonly, gmail,calendar)")
	loginCmd.Flags().String("listen-host", "127.0.0.1", "Loopback host to bind for the OAuth callback")
	loginCmd.Flags().Bool("no-browser", false, "Print the auth URL without attempting to open a browser")
	loginCmd.Flags().Duration("timeout", 2*time.Minute, "Maximum time to wait for the OAuth browser flow")

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show the stored Google Workspace auth status",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := secrets.DefaultManager()
			if err != nil {
				return fmt.Errorf("init secrets: %w", err)
			}

			clientCreds, clientErr := googleworkspace.LoadClientCredentials(mgr)
			runtimeCreds, runtimeErr := googleworkspace.LoadRuntimeCredentials(mgr)
			if clientErr != nil && runtimeErr != nil {
				return fmt.Errorf("google Workspace is not configured (run: loom auth google login)")
			}

			if clientErr == nil {
				fmt.Printf("Client ID: %s\n", maskSuffix(clientCreds.ClientID, 12))
			}

			if runtimeErr != nil {
				fmt.Println("Session: not authorized")
				return nil
			}

			baseHTTPClient := httpclient.NewDefault().HTTP()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			token, tokenErr := runtimeCreds.AccessToken(ctx, baseHTTPClient)
			userInfo, infoErr := googleworkspace.FetchUserInfo(ctx, baseHTTPClient, runtimeCreds)

			if runtimeCreds.AccountEmail != "" {
				fmt.Printf("Account: %s\n", runtimeCreds.AccountEmail)
			}
			if infoErr == nil && userInfo.Email != "" && userInfo.Email != runtimeCreds.AccountEmail {
				fmt.Printf("Userinfo email: %s\n", userInfo.Email)
			}
			fmt.Printf("Scopes: %s\n", googleworkspace.FormatScopes(runtimeCreds.Scopes))
			if tokenErr == nil && token != nil {
				fmt.Printf("Access token expiry: %s\n", token.Expiry.Format(time.RFC3339))
			} else if tokenErr != nil {
				fmt.Printf("Access token refresh: error: %v\n", tokenErr)
			}
			if infoErr != nil {
				fmt.Printf("Userinfo: error: %v\n", infoErr)
			}
			return nil
		},
	}

	logoutCmd := &cobra.Command{
		Use:   "logout",
		Short: "Delete the stored Google Workspace session",
		RunE: func(cmd *cobra.Command, args []string) error {
			clearClient, _ := cmd.Flags().GetBool("clear-client")
			localOnly, _ := cmd.Flags().GetBool("local-only")

			mgr, err := secrets.DefaultManager()
			if err != nil {
				return fmt.Errorf("init secrets: %w", err)
			}

			if !localOnly {
				if creds, loadErr := googleworkspace.LoadRuntimeCredentials(mgr); loadErr == nil {
					if revokeErr := revokeGoogleToken(context.Background(), httpclient.NewDefault(), creds.RefreshToken); revokeErr != nil {
						fmt.Fprintf(os.Stderr, "Warning: remote revoke failed: %v\n", revokeErr)
					}
				}
			}

			if err := googleworkspace.DeleteSession(mgr, clearClient); err != nil {
				return err
			}
			fmt.Println("Google Workspace session deleted")
			if clearClient {
				fmt.Println("Stored Google OAuth client credentials also removed")
			}
			reloadDaemonAfterSecretChange(socketPath, "Google Workspace auth delete")
			return nil
		},
	}
	logoutCmd.Flags().Bool("clear-client", false, "Also delete the stored Google OAuth client ID and secret")
	logoutCmd.Flags().Bool("local-only", false, "Delete local secrets without attempting Google token revocation")

	cmd.AddCommand(loginCmd, statusCmd, logoutCmd)
	return cmd
}

func loadGoogleClientCredentials(mgr *secrets.Manager, clientJSONPath string) (*googleworkspace.Credentials, error) {
	if strings.TrimSpace(clientJSONPath) == "" {
		creds, err := googleworkspace.LoadClientCredentials(mgr)
		if err != nil {
			return nil, fmt.Errorf("load Google OAuth client: %w (or pass --client-json)", err)
		}
		return creds, nil
	}

	if strings.HasPrefix(clientJSONPath, "~") {
		home, _ := os.UserHomeDir()
		clientJSONPath = filepath.Join(home, strings.TrimPrefix(clientJSONPath, "~"))
	}
	data, err := os.ReadFile(clientJSONPath)
	if err != nil {
		return nil, fmt.Errorf("read client JSON: %w", err)
	}
	creds, err := googleworkspace.ParseClientCredentialsJSON(data)
	if err != nil {
		return nil, err
	}
	return creds, nil
}

func runGoogleLoopbackLogin(ctx context.Context, baseHTTPClient *http.Client, creds *googleworkspace.Credentials, listenHost string, openBrowser bool) (*oauth2.Token, error) {
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", net.JoinHostPort(listenHost, "0"))
	if err != nil {
		return nil, fmt.Errorf("listen for Google OAuth callback: %w", err)
	}
	defer listener.Close()

	redirectURL := "http://" + listener.Addr().String() + "/oauth2/callback"
	config := creds.OAuthConfig(redirectURL)
	state, err := randomHex(16)
	if err != nil {
		return nil, fmt.Errorf("generate OAuth state: %w", err)
	}
	verifier := oauth2.GenerateVerifier()
	authURL := config.AuthCodeURL(
		state,
		oauth2.AccessTypeOffline,
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("prompt", "consent"),
		oauth2.SetAuthURLParam("include_granted_scopes", "true"),
	)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if gotState := r.URL.Query().Get("state"); gotState != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			select {
			case errCh <- fmt.Errorf("OAuth state mismatch"):
			default:
			}
			return
		}
		if oauthErr := r.URL.Query().Get("error"); oauthErr != "" {
			description := r.URL.Query().Get("error_description")
			http.Error(w, "OAuth login failed", http.StatusBadRequest)
			select {
			case errCh <- fmt.Errorf("OAuth login failed: %s %s", oauthErr, description):
			default:
			}
			return
		}
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		if code == "" {
			http.Error(w, "missing authorization code", http.StatusBadRequest)
			select {
			case errCh <- fmt.Errorf("missing authorization code"):
			default:
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<html><body><h3>Google Workspace authorization complete.</h3><p>You can close this window and return to Loom.</p></body></html>")
		select {
		case codeCh <- code:
		default:
		}
	})

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			select {
			case errCh <- serveErr:
			default:
			}
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	fmt.Println("Authorize Loom with Google Workspace by visiting:")
	fmt.Println(authURL)
	if openBrowser {
		openBrowserURL(authURL)
	}

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for Google OAuth callback: %w", ctx.Err())
	case err := <-errCh:
		return nil, err
	case code := <-codeCh:
		exchangeCtx := context.WithValue(ctx, oauth2.HTTPClient, baseHTTPClient)
		token, exchangeErr := config.Exchange(
			exchangeCtx,
			code,
			oauth2.VerifierOption(verifier),
		)
		if exchangeErr != nil {
			return nil, fmt.Errorf("exchange Google authorization code: %w", exchangeErr)
		}
		return token, nil
	}
}

func revokeGoogleToken(ctx context.Context, client *httpclient.Client, token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	values := url.Values{}
	values.Set("token", token)
	resp, err := client.Post(ctx, "https://oauth2.googleapis.com/revoke", "application/x-www-form-urlencoded", strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func openBrowserURL(target string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", target)
	case "linux":
		cmd = exec.CommandContext(ctx, "xdg-open", target)
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cancel()
		return
	}
	go func() {
		defer cancel()
		_ = cmd.Run()
	}()
}

func randomHex(byteLen int) (string, error) {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func maskSuffix(value string, suffixLen int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= suffixLen {
		return value
	}
	return strings.Repeat("*", len(value)-suffixLen) + value[len(value)-suffixLen:]
}
