// cmd_hive.go implements `loom hive` subcommands. The hive lives in-cluster
// (cmd/loom-hive-operator running on k3s), so these commands are pure HTTP
// clients — there is no socket fallback because the canonical store and
// reconciler are never local. The Mac CLI authenticates with an admin token
// when one is configured; for the initial slice (1.2 stub) the operator
// returns the status payload without auth.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// defaultHiveOperatorURL is the cluster-internal address used when the user
// hasn't overridden via flag/env. Setting LOOM_HIVE_OPERATOR_URL to the public
// ingress (e.g. https://hive.flexinfer.ai) is the recommended setup once the
// service is exposed; localhost is for `kubectl port-forward` workflows.
const defaultHiveOperatorURL = "http://localhost:8090"

// newHiveCmd returns the `loom hive` command group.
func newHiveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hive",
		Short: "Loom Hive control plane (cluster operator: council + pipeline)",
		Long: `Talk to the in-cluster loom-hive-operator (council scheduler + pipeline reconciler).

The operator is single-source-of-truth for Loom Hive state. Configure the URL
via LOOM_HIVE_OPERATOR_URL (default: ` + defaultHiveOperatorURL + `).
Set LOOM_HIVE_TOKEN for admin-token-gated endpoints once they ship.`,
	}
	cmd.PersistentFlags().String("operator-url", "", "Operator base URL (default: $LOOM_HIVE_OPERATOR_URL or "+defaultHiveOperatorURL+")")
	cmd.PersistentFlags().Duration("timeout", 10*time.Second, "Per-request timeout")
	cmd.PersistentFlags().Bool("json", false, "Emit raw JSON instead of the human-readable summary")

	cmd.AddCommand(newHiveStatusCmd())
	return cmd
}

// hiveClient resolves the operator URL + admin token from flags/env and
// returns an HTTP client tuned for the hive surface.
type hiveClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func resolveHiveClient(cmd *cobra.Command) (*hiveClient, error) {
	urlFlag, _ := cmd.Flags().GetString("operator-url")
	timeout, _ := cmd.Flags().GetDuration("timeout")

	base := strings.TrimSpace(urlFlag)
	if base == "" {
		base = strings.TrimSpace(os.Getenv("LOOM_HIVE_OPERATOR_URL"))
	}
	if base == "" {
		base = defaultHiveOperatorURL
	}
	base = strings.TrimRight(base, "/")
	return &hiveClient{
		baseURL: base,
		token:   strings.TrimSpace(os.Getenv("LOOM_HIVE_TOKEN")),
		http:    &http.Client{Timeout: timeout},
	}, nil
}

// get performs an authenticated GET against path (relative to the operator
// base URL) and decodes the JSON body into out.
func (c *hiveClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("connect %s%s: %w", c.baseURL, path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MiB cap
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("operator returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s: %w (body=%q)", path, err, truncateForError(body, 200))
	}
	return nil
}

// hiveStatus mirrors the operator's stub shape (slice 1.2 / cmd/loom-hive-operator/server.go).
// Fields not yet populated by the operator surface as nil/zero — the CLI
// renders them as "—" for the human-readable view. Slice 2.4 fills the rest.
type hiveStatus struct {
	DBOK               bool   `json:"db_ok"`
	PolicyEnabled      bool   `json:"policy_enabled"`
	PolicyVersion      int    `json:"policy_version"`
	QueueDepth         *int   `json:"queue_depth,omitempty"`
	LastCouncilAt      string `json:"last_council_at,omitempty"`
	ActivePipelineRuns *int   `json:"active_pipeline_runs,omitempty"`
	Slice              string `json:"slice,omitempty"`
}

func newHiveStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the hive's current state (operator health, policy, queue, last council)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := resolveHiveClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			emitJSON, _ := cmd.Flags().GetBool("json")
			if emitJSON {
				// Pass-through mode: fetch + reprint without re-marshaling so
				// extra fields the operator might add stay visible to scripts.
				var raw json.RawMessage
				if err := client.get(ctx, "/api/hive/status", &raw); err != nil {
					return wrapHiveErr(client, err)
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return err
			}

			var st hiveStatus
			if err := client.get(ctx, "/api/hive/status", &st); err != nil {
				return wrapHiveErr(client, err)
			}
			return renderHiveStatus(cmd.OutOrStdout(), client.baseURL, st)
		},
	}
}

// renderHiveStatus prints the human-readable status block.
func renderHiveStatus(w io.Writer, base string, st hiveStatus) error {
	enabled := "off"
	if st.PolicyEnabled {
		enabled = "on"
	}
	dbOK := "ok"
	if !st.DBOK {
		dbOK = "FAIL"
	}
	queue := "—"
	if st.QueueDepth != nil {
		queue = fmt.Sprintf("%d", *st.QueueDepth)
	}
	active := "—"
	if st.ActivePipelineRuns != nil {
		active = fmt.Sprintf("%d", *st.ActivePipelineRuns)
	}
	last := "—"
	if st.LastCouncilAt != "" {
		last = st.LastCouncilAt
	}
	slice := st.Slice
	if slice == "" {
		slice = "(unknown)"
	}
	_, err := fmt.Fprintf(w,
		"Loom Hive @ %s\n  policy:           %s (v%d)\n  store:            %s\n  queue depth:      %s\n  active pipelines: %s\n  last council run: %s\n  operator slice:   %s\n",
		base, enabled, st.PolicyVersion, dbOK, queue, active, last, slice,
	)
	return err
}

// wrapHiveErr decorates connection errors with the friendly hint the CLI's
// users will most often need: how to point at the right operator URL.
func wrapHiveErr(c *hiveClient, err error) error {
	if err == nil {
		return nil
	}
	var ne interface{ Timeout() bool }
	if errors.As(err, &ne) && ne.Timeout() {
		return fmt.Errorf("%w\nhint: operator at %s is not responding within timeout — check the deployment is Ready or override --operator-url / LOOM_HIVE_OPERATOR_URL", err, c.baseURL)
	}
	if isConnRefused(err) {
		return fmt.Errorf("%w\nhint: nothing answering at %s — set LOOM_HIVE_OPERATOR_URL or use `kubectl port-forward -n loom-hive svc/loom-hive-operator 8090:8090`", err, c.baseURL)
	}
	return err
}

func isConnRefused(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "dial tcp")
}

func truncateForError(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
