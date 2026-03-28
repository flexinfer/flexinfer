// mcp-filesystem is a server for safe filesystem access.
package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcpscaffold"
	"github.com/crb2nu/loom/pkg/pathsec"
	"github.com/crb2nu/loom/pkg/validate"
)

var version = "1.0.0"

const (
	maxReadSize = 50 * 1024 * 1024 // 50MB max file read
	maxResults  = 10000            // max search results
)

var allowedRoot string

func init() {
	// Determine allowed root directory
	allowedRoot = os.Getenv("FILESYSTEM_ROOT")
	if allowedRoot == "" {
		// Default to home directory if not specified
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			allowedRoot = home
		} else if cwd, err := os.Getwd(); err == nil {
			allowedRoot = cwd
		} else {
			allowedRoot = "/"
		}
	}
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	srv, cleanup, err := mcpscaffold.NewServer(ctx, "mcp-filesystem", version,
		mcpscaffold.WithInstructions("Safe filesystem access. Tools: list_directory, read_file, search_files"),
	)
	if err != nil {
		return err
	}
	defer func() { _ = cleanup(ctx) }()

	srv.Logger.Info("filesystem root", "root", allowedRoot)

	// list_directory
	srv.AddTracedTool(mcp.Tool{
		Name:        "list_directory",
		Description: "List contents of a directory",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute path to directory",
				},
			},
			Required: []string{"path"},
		},
	}, handleListDirectory)

	// read_file
	srv.AddTracedTool(mcp.Tool{
		Name:        "read_file",
		Description: "Read contents of a file",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute path to file",
				},
			},
			Required: []string{"path"},
		},
	}, handleReadFile)

	// search_files (simple glob)
	srv.AddTracedTool(mcp.Tool{
		Name:        "search_files",
		Description: "Search for files matching a glob pattern",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"root": map[string]any{
					"type":        "string",
					"description": "Root directory to search in",
				},
				"pattern": map[string]any{
					"type":        "string",
					"description": "Glob pattern (e.g. *.go)",
				},
			},
			Required: []string{"root", "pattern"},
		},
	}, handleSearchFiles)

	return srv.Run(ctx)
}

func handleListDirectory(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	path := v.Required("path")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Clean and validate path
	absPath, err := pathsec.CleanPath(path)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("invalid path: %w", err)), nil
	}

	if err := pathsec.ValidatePath(absPath, allowedRoot); err != nil {
		return mcp.ErrorResult(fmt.Errorf("access denied: %w", err)), nil
	}

	entries, err := os.ReadDir(absPath)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("read dir: %w", err)), nil
	}

	var result []map[string]any
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue // Skip entries we can't stat
		}
		result = append(result, map[string]any{
			"name":  entry.Name(),
			"isDir": entry.IsDir(),
			"size":  info.Size(),
			"mode":  info.Mode().String(),
		})
	}

	return mcp.JSONResult(map[string]any{
		"path":    absPath,
		"entries": result,
	})
}

func handleReadFile(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	path := v.Required("path")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Clean and validate path
	absPath, err := pathsec.CleanPath(path)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("invalid path: %w", err)), nil
	}

	if err := pathsec.ValidatePath(absPath, allowedRoot); err != nil {
		return mcp.ErrorResult(fmt.Errorf("access denied: %w", err)), nil
	}

	// Check file size before reading
	if err := pathsec.ValidateFileSize(absPath, maxReadSize); err != nil {
		return mcp.ErrorResult(fmt.Errorf("file too large: %w", err)), nil
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("read file: %w", err)), nil
	}

	// Detect if binary? For now assume text or return base64 if needed.
	// MCP protocol handles strings.
	return mcp.JSONResult(map[string]any{
		"path":    absPath,
		"content": string(data),
		"size":    len(data),
	})
}

func handleSearchFiles(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	root := v.String("root", ".")
	pattern := v.Required("pattern")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Clean and validate search root
	absRoot, err := pathsec.CleanPath(root)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("invalid root: %w", err)), nil
	}

	if err := pathsec.ValidatePath(absRoot, allowedRoot); err != nil {
		return mcp.ErrorResult(fmt.Errorf("search root denied: %w", err)), nil
	}

	var matches []string
	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err != nil {
			return nil // ignore errors
		}
		if d.IsDir() {
			return nil
		}

		// Limit results to prevent memory exhaustion
		if len(matches) >= maxResults {
			return filepath.SkipAll
		}

		matched, err := filepath.Match(pattern, d.Name())
		if err != nil {
			return err
		}
		if matched {
			matches = append(matches, path)
		}
		return nil
	})

	if err != nil && err != filepath.SkipAll {
		return mcp.ErrorResult(fmt.Errorf("walk dir: %w", err)), nil
	}

	truncated := len(matches) >= maxResults

	return mcp.JSONResult(map[string]any{
		"root":      absRoot,
		"pattern":   pattern,
		"matches":   matches,
		"truncated": truncated,
	})
}
