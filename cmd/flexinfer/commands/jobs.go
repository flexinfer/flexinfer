/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package commands

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

var (
	jobsPhase   string
	jobsModel   string
	jobsProxy   string
	jobsToken   string
	jobsOutFile string
)

var jobsCmd = &cobra.Command{
	Use:     "jobs",
	Aliases: []string{"job", "ijob"},
	Short:   "List and manage async InferenceJobs",
	Long: `Inspect and manage the async jobs the proxy runs (InferenceJob,
created by background requests and POST /v1/jobs).

Examples:
  # Queued and running jobs
  flexinfer jobs list --phase queued
  flexinfer jobs list --phase in_progress

  # One job's detail
  flexinfer jobs get job-3f9c0a1b2c3d4e5f60718293

  # Cancel a job
  flexinfer jobs cancel job-3f9c0a1b2c3d4e5f60718293

  # Download a completed job's output through the proxy
  flexinfer jobs content job-3f9c0a1b2c3d4e5f60718293 --proxy http://flexinfer-proxy.flexinfer-system.svc -o out.json`,
}

var jobsListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List InferenceJobs, newest first",
	Args:    cobra.NoArgs,
	RunE:    runJobsList,
}

var jobsGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Show one InferenceJob",
	Args:  cobra.ExactArgs(1),
	RunE:  runJobsGet,
}

var jobsCancelCmd = &cobra.Command{
	Use:   "cancel <id>",
	Short: "Cancel an InferenceJob (sets spec.cancel)",
	Args:  cobra.ExactArgs(1),
	RunE:  runJobsCancel,
}

var jobsDeleteCmd = &cobra.Command{
	Use:     "delete <id>",
	Aliases: []string{"rm"},
	Short:   "Delete an InferenceJob; the proxy deletes its stored output first",
	Args:    cobra.ExactArgs(1),
	RunE:    runJobsDelete,
}

var jobsContentCmd = &cobra.Command{
	Use:   "content <id>",
	Short: "Download a completed job's output through the proxy",
	Args:  cobra.ExactArgs(1),
	RunE:  runJobsContent,
}

func init() {
	jobsListCmd.Flags().StringVar(&jobsPhase, "phase", "", "Only jobs in this phase (queued, in_progress, completed, failed, cancelled, incomplete)")
	jobsListCmd.Flags().StringVar(&jobsModel, "model", "", "Only jobs for this model or lane")
	jobsContentCmd.Flags().StringVar(&jobsProxy, "proxy", os.Getenv("FLEXINFER_PROXY_URL"), "Proxy base URL (or FLEXINFER_PROXY_URL)")
	jobsContentCmd.Flags().StringVar(&jobsToken, "token", os.Getenv("FLEXINFER_PROXY_TOKEN"), "Proxy bearer token (or FLEXINFER_PROXY_TOKEN)")
	jobsContentCmd.Flags().StringVarP(&jobsOutFile, "output", "o", "", "Write the output to this file instead of stdout")
	jobsCmd.AddCommand(jobsListCmd, jobsGetCmd, jobsCancelCmd, jobsDeleteCmd, jobsContentCmd)
}

func jobPhase(j *aiv1alpha2.InferenceJob) string {
	if j.Status.Phase == "" {
		return string(aiv1alpha2.InferenceJobQueued)
	}
	return string(j.Status.Phase)
}

func jobAge(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return now.Sub(t).Round(time.Second).String()
}

func runJobsList(cmd *cobra.Command, _ []string) error {
	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	var list aiv1alpha2.InferenceJobList
	opts := []client.ListOption{}
	if ns := getNamespace(); ns != "" {
		opts = append(opts, client.InNamespace(ns))
	}
	if err := k8sClient.List(ctx(), &list, opts...); err != nil {
		return fmt.Errorf("failed to list InferenceJobs: %w", err)
	}
	items := list.Items
	sort.Slice(items, func(a, b int) bool { return items[a].CreationTimestamp.After(items[b].CreationTimestamp.Time) })
	out := cmd.OutOrStdout()
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tMODEL\tPATH\tPHASE\tPROGRESS\tLANE\tAGE\tERROR")
	now := time.Now()
	shown := 0
	for i := range items {
		j := &items[i]
		if jobsPhase != "" && jobPhase(j) != jobsPhase {
			continue
		}
		if jobsModel != "" && j.Spec.Model != jobsModel && j.Status.Lane != jobsModel {
			continue
		}
		errText := "-"
		if j.Status.Error != nil {
			errText = j.Status.Error.Code
		}
		lane := j.Status.Lane
		if lane == "" {
			lane = "-"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d%%\t%s\t%s\t%s\n", j.Name, j.Spec.Model, j.Spec.Path,
			jobPhase(j), j.Status.Progress, lane, jobAge(j.CreationTimestamp.Time, now), errText)
		shown++
	}
	if shown == 0 {
		_, _ = fmt.Fprintln(out, "No InferenceJobs found")
		return nil
	}
	return w.Flush()
}

func getJob(id string) (client.Client, *aiv1alpha2.InferenceJob, error) {
	k8sClient, err := getClient()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create client: %w", err)
	}
	ns := namespace
	j := &aiv1alpha2.InferenceJob{}
	if err := k8sClient.Get(ctx(), types.NamespacedName{Namespace: ns, Name: id}, j); err != nil {
		return nil, nil, fmt.Errorf("failed to get InferenceJob %s/%s: %w", ns, id, err)
	}
	return k8sClient, j, nil
}

func runJobsGet(cmd *cobra.Command, args []string) error {
	_, j, err := getJob(args[0])
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	line := func(k, v string) {
		if v != "" {
			_, _ = fmt.Fprintf(out, "%-14s %s\n", k+":", v)
		}
	}
	line("ID", j.Name)
	line("Model", j.Spec.Model)
	line("Path", j.Spec.Path)
	line("Phase", jobPhase(j))
	line("Progress", fmt.Sprintf("%d%%", j.Status.Progress))
	line("Lane", j.Status.Lane)
	line("Runner", j.Status.Runner)
	line("Attempts", fmt.Sprintf("%d", j.Status.Attempts))
	line("QoS class", j.Spec.QoSClass)
	if t := j.Status.QueuedAt; t != nil {
		line("Queued", t.UTC().Format(time.RFC3339))
	}
	if t := j.Status.StartedAt; t != nil {
		line("Started", t.UTC().Format(time.RFC3339))
	}
	if t := j.Status.CompletedAt; t != nil {
		line("Completed", t.UTC().Format(time.RFC3339))
	}
	if t := j.Status.ExpiresAt; t != nil {
		line("Expires", t.UTC().Format(time.RFC3339))
	}
	if o := j.Status.Output; o != nil {
		line("Output", fmt.Sprintf("%s (%s, %d bytes, sha256 %s)", o.URI, o.ContentType, o.Bytes, o.SHA256))
	}
	if e := j.Status.Error; e != nil {
		line("Error", fmt.Sprintf("%s (HTTP %d): %s", e.Code, e.Status, e.Message))
	}
	if wh := j.Spec.Webhook; wh != nil {
		delivered := "none"
		if st := j.Status.Webhook; st != nil && len(st.Delivered) > 0 {
			delivered = strings.Join(st.Delivered, ", ")
		}
		line("Webhook", wh.URL+" (delivered: "+delivered+")")
	}
	if j.Spec.Cancel && !j.Status.Phase.IsTerminal() {
		line("Cancel", "requested")
	}
	return nil
}

func runJobsCancel(cmd *cobra.Command, args []string) error {
	k8sClient, j, err := getJob(args[0])
	if err != nil {
		return err
	}
	if j.Status.Phase.IsTerminal() {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s is already %s\n", j.Name, j.Status.Phase)
		return nil
	}
	patch := client.MergeFrom(j.DeepCopy())
	j.Spec.Cancel = true
	if err := k8sClient.Patch(ctx(), j, patch); err != nil {
		return fmt.Errorf("failed to cancel %s: %w", j.Name, err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "cancel requested for %s\n", j.Name)
	return nil
}

func runJobsDelete(cmd *cobra.Command, args []string) error {
	k8sClient, j, err := getJob(args[0])
	if err != nil {
		return err
	}
	if err := k8sClient.Delete(ctx(), j); err != nil {
		return fmt.Errorf("failed to delete %s: %w", j.Name, err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", j.Name)
	return nil
}

func runJobsContent(cmd *cobra.Command, args []string) error {
	if jobsProxy == "" {
		return fmt.Errorf("--proxy (or FLEXINFER_PROXY_URL) is required")
	}
	req, err := http.NewRequestWithContext(ctx(), http.MethodGet, strings.TrimRight(jobsProxy, "/")+"/v1/jobs/"+args[0]+"/content", nil)
	if err != nil {
		return err
	}
	if jobsToken != "" {
		req.Header.Set("Authorization", "Bearer "+jobsToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetching job content: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("proxy answered %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	out := cmd.OutOrStdout()
	if jobsOutFile != "" {
		f, err := os.Create(jobsOutFile)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		out = f
	}
	n, err := io.Copy(out, resp.Body)
	if err != nil {
		return err
	}
	if jobsOutFile != "" {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "wrote %d bytes (%s) to %s\n", n, resp.Header.Get("Content-Type"), jobsOutFile)
	}
	return nil
}
