package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/pkg/registry"
)

type catalogEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Categories  []string `json:"categories,omitempty"`
	Command     string   `json:"command,omitempty"`
}

func newCatalogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Browse available MCP servers from registry",
	}

	cmd.AddCommand(newCatalogListCmd())
	return cmd
}

func newCatalogListCmd() *cobra.Command {
	var targetProfile string
	var categoryFilter string
	var outputJSON bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available MCP servers from registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			workspaceRoot := findWorkspaceRootForChecks()
			regRes := resolveRegistryForDiagnostics(workspaceRoot)
			if !regRes.Found {
				return fmt.Errorf("registry.yaml not found (checked: %s)", strings.Join(regRes.Precedence, " -> "))
			}

			reg, err := registry.LoadWithDefaults(regRes.Path)
			if err != nil {
				return fmt.Errorf("load registry %q: %w", regRes.Path, err)
			}

			entries := buildCatalogEntries(reg, targetProfile, categoryFilter)
			if outputJSON {
				payload := map[string]any{
					"registry_path": regRes.Path,
					"profile":       targetProfile,
					"category":      categoryFilter,
					"count":         len(entries),
					"servers":       entries,
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(payload)
			}

			fmt.Printf("Registry: %s\n", regRes.Path)
			fmt.Printf("Profile: %s\n", targetProfile)
			if categoryFilter != "" {
				fmt.Printf("Category filter: %s\n", categoryFilter)
			}
			fmt.Printf("Servers: %d\n\n", len(entries))

			for _, entry := range entries {
				fmt.Printf("- %s", entry.Name)
				if entry.Command != "" {
					fmt.Printf(" (%s)", entry.Command)
				}
				fmt.Println()
				if entry.Description != "" {
					fmt.Printf("  %s\n", entry.Description)
				}
				if len(entry.Categories) > 0 {
					fmt.Printf("  categories: %s\n", strings.Join(entry.Categories, ", "))
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&targetProfile, "target", "codex", "Target profile for resolved commands/env")
	cmd.Flags().StringVar(&categoryFilter, "category", "", "Only include servers with this category")
	cmd.Flags().BoolVar(&outputJSON, "json", false, "Output in JSON format")
	return cmd
}

func buildCatalogEntries(reg *registry.Registry, targetProfile, categoryFilter string) []catalogEntry {
	if reg == nil {
		return nil
	}

	needle := strings.TrimSpace(strings.ToLower(categoryFilter))
	entries := make([]catalogEntry, 0, len(reg.Servers))
	for _, srv := range reg.Servers {
		if srv == nil {
			continue
		}
		if needle != "" && !serverHasCategory(srv, needle) {
			continue
		}

		entry := catalogEntry{
			Name:       srv.Name,
			Categories: sortedCopy(srv.Categories),
		}

		if spec, err := reg.GetServerSpec(srv.Name, targetProfile); err == nil && spec != nil {
			entry.Description = strings.TrimSpace(spec.Description)
			entry.Command = strings.TrimSpace(spec.Command)
		}
		if entry.Description == "" && srv.Common != nil {
			entry.Description = strings.TrimSpace(srv.Common.Description)
		}
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}

func serverHasCategory(srv *registry.Server, categoryLower string) bool {
	for _, c := range srv.Categories {
		if strings.EqualFold(strings.TrimSpace(c), categoryLower) {
			return true
		}
	}
	return false
}

func sortedCopy(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
