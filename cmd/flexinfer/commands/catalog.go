/*
Copyright 2025.

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
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/registry"
)

func init() {
	_ = aiv1alpha2.AddToScheme(scheme)
}

var catalogCmd = &cobra.Command{
	Use:   "catalog",
	Short: "Manage the model catalog (list, search, pull)",
	Long: `Catalog commands provide access to models from configured registries.

Examples:
  # List all models in the catalog
  flexinfer catalog list

  # Search for models
  flexinfer catalog search llama

  # Pull a model from the catalog
  flexinfer catalog pull HF://meta-llama/Llama-2-7b-chat-hf`,
}

var catalogListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all models in the catalog",
	RunE:  runCatalogList,
}

var catalogSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search for models by name or tag",
	Args:  cobra.ExactArgs(1),
	RunE:  runCatalogSearch,
}

var catalogPullCmd = &cobra.Command{
	Use:   "pull <reference>",
	Short: "Pull model artifacts from a registry",
	Args:  cobra.ExactArgs(1),
	RunE:  runCatalogPull,
}

var catalogSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Trigger a manual catalog sync",
	RunE:  runCatalogSync,
}

func init() {
	catalogCmd.AddCommand(catalogListCmd)
	catalogCmd.AddCommand(catalogSearchCmd)
	catalogCmd.AddCommand(catalogPullCmd)
	catalogCmd.AddCommand(catalogSyncCmd)
}

func runCatalogList(cmd *cobra.Command, _ []string) error {
	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("connect to cluster: %w", err)
	}

	catalogs := &aiv1alpha2.ModelCatalogList{}
	if err := k8sClient.List(ctx(), catalogs, client.InNamespace(getNamespace())); err != nil {
		return fmt.Errorf("list catalogs: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tREGISTRY\tREFERENCE\tSIZE")

	for _, cat := range catalogs.Items {
		for _, entry := range cat.Status.Entries {
			size := ""
			if entry.Size > 0 {
				size = formatSize(entry.Size)
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", entry.Name, entry.Registry, entry.Reference, size)
		}
	}

	return w.Flush()
}

func runCatalogSearch(cmd *cobra.Command, args []string) error {
	query := args[0]

	// Search across all registered registry types
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tREGISTRY\tREFERENCE")

	for _, regType := range registry.Types() {
		reg, err := registry.Get(regType)
		if err != nil {
			continue
		}

		entries, err := reg.List(ctx(), registry.ListFilter{Query: query, Limit: 10})
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s search failed: %v\n", regType, err)
			continue
		}

		for _, e := range entries {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", e.Name, e.Registry, e.Reference)
		}
	}

	return w.Flush()
}

func runCatalogPull(cmd *cobra.Command, args []string) error {
	ref := args[0]

	// Determine registry type from reference prefix
	regType := inferRegistryType(ref)
	reg, err := registry.Get(regType)
	if err != nil {
		return fmt.Errorf("unknown registry type for ref %q: %w", ref, err)
	}

	// Resolve metadata first
	meta, err := reg.Resolve(ctx(), ref)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", ref, err)
	}

	fmt.Printf("Resolved: %s (format=%s, digest=%s)\n", meta.Name, meta.Format, meta.Digest)
	fmt.Println("Note: To cache this model, create a ModelCache CR instead of pulling directly.")
	return nil
}

func runCatalogSync(_ *cobra.Command, _ []string) error {
	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("connect to cluster: %w", err)
	}

	catalogs := &aiv1alpha2.ModelCatalogList{}
	if err := k8sClient.List(ctx(), catalogs, client.InNamespace(getNamespace())); err != nil {
		return fmt.Errorf("list catalogs: %w", err)
	}

	if len(catalogs.Items) == 0 {
		fmt.Println("No ModelCatalog resources found. Create one to enable registry sync.")
		return nil
	}

	for _, cat := range catalogs.Items {
		// Touch the annotation to trigger re-reconciliation
		if cat.Annotations == nil {
			cat.Annotations = make(map[string]string)
		}
		cat.Annotations["flexinfer.ai/sync-trigger"] = fmt.Sprintf("%d", cat.Generation+1)
		if err := k8sClient.Update(ctx(), &cat); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to trigger sync for %s: %v\n", cat.Name, err)
			continue
		}
		fmt.Printf("Triggered sync for catalog %s\n", cat.Name)
	}

	return nil
}

func inferRegistryType(ref string) string {
	switch {
	case strings.HasPrefix(ref, "HF://"), strings.HasPrefix(ref, "huggingface://"):
		return "huggingface"
	case strings.HasPrefix(ref, "ollama://"):
		return "ollama"
	case strings.HasPrefix(ref, "oci://"), strings.HasPrefix(ref, "oras://"):
		return "oci"
	default:
		return "huggingface"
	}
}

func formatSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1fGi", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1fMi", float64(bytes)/float64(1<<20))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
