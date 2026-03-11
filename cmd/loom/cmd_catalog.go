package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/sync"
)

type catalogEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Categories  []string `json:"categories,omitempty"`
	Command     string   `json:"command,omitempty"`
	Enabled     bool     `json:"enabled"`
}

func newCatalogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Browse available MCP servers from registry",
	}

	cmd.AddCommand(
		newCatalogListCmd(),
		newCatalogEnableCmd(),
		newCatalogDisableCmd(),
		newCatalogStatusCmd(),
	)
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

			cs, err := registry.LoadCatalogState()
			if err != nil {
				return fmt.Errorf("load catalog state: %w", err)
			}

			entries := buildCatalogEntries(reg, cs, targetProfile, categoryFilter)
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
				status := "+"
				if !entry.Enabled {
					status = "-"
				}
				fmt.Printf("%s %s", status, entry.Name)
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

func buildCatalogEntries(reg *registry.Registry, cs *registry.CatalogState, targetProfile, categoryFilter string) []catalogEntry {
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
			Enabled:    cs == nil || !cs.IsDisabled(srv.Name),
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

func newCatalogEnableCmd() *cobra.Command {
	var dryRun bool
	var noSync bool

	cmd := &cobra.Command{
		Use:   "enable <server>",
		Short: "Enable an MCP server in the catalog",
		Long:  "Enable a previously disabled server. Triggers loom sync to propagate the change.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			serverName := args[0]

			if err := validateServerExists(serverName); err != nil {
				return err
			}

			cs, err := registry.LoadCatalogState()
			if err != nil {
				return fmt.Errorf("load catalog state: %w", err)
			}

			if !cs.Enable(serverName) {
				fmt.Printf("Server %q is already enabled.\n", serverName)
				return nil
			}

			if dryRun {
				fmt.Printf("[dry-run] Would enable server %q.\n", serverName)
				return nil
			}

			if err := cs.Save(); err != nil {
				return fmt.Errorf("save catalog state: %w", err)
			}
			fmt.Printf("Enabled server %q.\n", serverName)

			if !noSync {
				return runPostCatalogSync()
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would change without writing")
	cmd.Flags().BoolVar(&noSync, "no-sync", false, "Skip automatic loom sync after change")
	return cmd
}

func newCatalogDisableCmd() *cobra.Command {
	var dryRun bool
	var noSync bool

	cmd := &cobra.Command{
		Use:   "disable <server>",
		Short: "Disable an MCP server in the catalog",
		Long:  "Disable a server so it is excluded from generated configs. Triggers loom sync to propagate.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			serverName := args[0]

			if err := validateServerExists(serverName); err != nil {
				return err
			}

			cs, err := registry.LoadCatalogState()
			if err != nil {
				return fmt.Errorf("load catalog state: %w", err)
			}

			if !cs.Disable(serverName) {
				fmt.Printf("Server %q is already disabled.\n", serverName)
				return nil
			}

			if dryRun {
				fmt.Printf("[dry-run] Would disable server %q.\n", serverName)
				return nil
			}

			if err := cs.Save(); err != nil {
				return fmt.Errorf("save catalog state: %w", err)
			}
			fmt.Printf("Disabled server %q.\n", serverName)

			if !noSync {
				return runPostCatalogSync()
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would change without writing")
	cmd.Flags().BoolVar(&noSync, "no-sync", false, "Skip automatic loom sync after change")
	return cmd
}

func newCatalogStatusCmd() *cobra.Command {
	var outputJSON bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show catalog enable/disable state",
		RunE: func(cmd *cobra.Command, args []string) error {
			cs, err := registry.LoadCatalogState()
			if err != nil {
				return fmt.Errorf("load catalog state: %w", err)
			}

			if outputJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"state_path":       registry.CatalogStatePath(),
					"disabled_servers": cs.DisabledServers,
					"disabled_count":   len(cs.DisabledServers),
				})
			}

			fmt.Printf("State file: %s\n", registry.CatalogStatePath())
			if len(cs.DisabledServers) == 0 {
				fmt.Println("All servers enabled.")
				return nil
			}

			fmt.Printf("Disabled servers (%d):\n", len(cs.DisabledServers))
			for _, name := range cs.DisabledServers {
				fmt.Printf("  - %s\n", name)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&outputJSON, "json", false, "Output in JSON format")
	return cmd
}

func validateServerExists(name string) error {
	workspaceRoot := findWorkspaceRootForChecks()
	regRes := resolveRegistryForDiagnostics(workspaceRoot)
	if !regRes.Found {
		return nil // Skip validation if registry not found
	}

	reg, err := registry.LoadWithDefaults(regRes.Path)
	if err != nil {
		return nil // Skip validation on load error
	}

	if reg.GetServer(name) == nil {
		return fmt.Errorf("server %q not found in registry (%s)", name, regRes.Path)
	}
	return nil
}

func runPostCatalogSync() error {
	cwd, _ := os.Getwd()
	mgr, err := sync.NewManager(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not initialize sync manager: %v\n", err)
		return nil
	}

	fmt.Println("Running loom sync all --regen...")
	loomBinary := ""
	if exe, lookErr := os.Executable(); lookErr == nil && exe != "" {
		loomBinary = exe
	}
	if err := mgr.SyncAll(true, true, false, false, "", true, loomBinary, nil, false); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: sync failed: %v\n", err)
	}
	return nil
}

func sortedCopy(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
