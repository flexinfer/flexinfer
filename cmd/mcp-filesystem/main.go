// mcp-filesystem is a server for safe filesystem access.
package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/pathsec"
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
			return
		}
	}()

	server := mcp.NewServer("mcp-filesystem", version)
	server.SetInstructions("Safe filesystem access. Tools: list_directory, read_file, search_files")

	// list_directory
	server.AddTool(mcp.Tool{
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
	server.AddTool(mcp.Tool{
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
	server.AddTool(mcp.Tool{
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

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func handleListDirectory(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("path is required")
	}

	// Clean and validate path
	absPath, err := pathsec.CleanPath(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	if err := pathsec.ValidatePath(absPath, allowedRoot); err != nil {
		return nil, fmt.Errorf("access denied: %w", err)
	}

	entries, err := os.ReadDir(absPath)
	if err != nil {
		return nil, fmt.Errorf("read dir: %w", err)
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
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("path is required")
	}

	// Clean and validate path
	absPath, err := pathsec.CleanPath(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	if err := pathsec.ValidatePath(absPath, allowedRoot); err != nil {
		return nil, fmt.Errorf("access denied: %w", err)
	}

	// Check file size before reading
	if err := pathsec.ValidateFileSize(absPath, maxReadSize); err != nil {
		return nil, fmt.Errorf("file too large: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
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
	root, _ := args["root"].(string)
	pattern, _ := args["pattern"].(string)

	if root == "" {
		root = "."
	}
	if pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}

	// Clean and validate search root
	absRoot, err := pathsec.CleanPath(root)
	if err != nil {
		return nil, fmt.Errorf("invalid root: %w", err)
	}

	if err := pathsec.ValidatePath(absRoot, allowedRoot); err != nil {
		return nil, fmt.Errorf("search root denied: %w", err)
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
		return nil, fmt.Errorf("walk dir: %w", err)
	}

	truncated := len(matches) >= maxResults

	return mcp.JSONResult(map[string]any{
		"root":      absRoot,
		"pattern":   pattern,
		"matches":   matches,
		"truncated": truncated,
	})
}
