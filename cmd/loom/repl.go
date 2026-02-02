// repl.go contains the interactive REPL for exploring MCP tools.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// runRepl runs an interactive REPL for exploring MCP tools
func runRepl(socketPath string) error {
	// Check if daemon is running
	if _, err := call(socketPath, "loom/status", nil); err != nil {
		return fmt.Errorf("daemon not running (start with: loom start)")
	}

	fmt.Println("Loom REPL - Interactive MCP Tool Explorer")
	fmt.Println("Type 'help' for commands, 'exit' to quit")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("loom> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil // EOF or error, exit gracefully
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		cmd := parts[0]
		args := parts[1:]

		switch cmd {
		case "exit", "quit", "q":
			fmt.Println("Goodbye!")
			return nil

		case "help", "h", "?":
			if len(args) > 0 {
				// Show help for specific tool
				if err := replShowToolHelp(socketPath, args[0]); err != nil {
					fmt.Printf("Error: %v\n", err)
				}
			} else {
				fmt.Println("Commands:")
				fmt.Println("  list [pattern]     - List tools (optionally filtered)")
				fmt.Println("  call <tool> <json> - Call a tool with JSON arguments")
				fmt.Println("  help <tool>        - Show tool description and schema")
				fmt.Println("  servers            - List available servers")
				fmt.Println("  status             - Show daemon status")
				fmt.Println("  exit               - Exit the REPL")
			}

		case "list", "ls", "l":
			pattern := ""
			if len(args) > 0 {
				pattern = args[0]
			}
			if err := replListTools(socketPath, pattern); err != nil {
				fmt.Printf("Error: %v\n", err)
			}

		case "call", "c":
			if len(args) < 1 {
				fmt.Println("Usage: call <tool-name> [json-args]")
				continue
			}
			toolName := args[0]
			jsonArgs := "{}"
			if len(args) > 1 {
				jsonArgs = strings.Join(args[1:], " ")
			}
			if err := replCallTool(socketPath, toolName, jsonArgs); err != nil {
				fmt.Printf("Error: %v\n", err)
			}

		case "servers", "s":
			if err := replListServers(socketPath); err != nil {
				fmt.Printf("Error: %v\n", err)
			}

		case "status":
			if err := showStatus(socketPath); err != nil {
				fmt.Printf("Error: %v\n", err)
			}

		default:
			fmt.Printf("Unknown command: %s (type 'help' for commands)\n", cmd)
		}
	}
}

func replListTools(socketPath, pattern string) error {
	result, err := call(socketPath, "loom/tools", nil)
	if err != nil {
		return err
	}

	var tools struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}

	if err := json.Unmarshal(result, &tools); err != nil {
		return err
	}

	patternLower := strings.ToLower(pattern)
	count := 0
	for _, t := range tools.Tools {
		if pattern == "" || strings.Contains(strings.ToLower(t.Name), patternLower) ||
			strings.Contains(strings.ToLower(t.Description), patternLower) {
			desc := t.Description
			if len(desc) > 50 {
				desc = desc[:47] + "..."
			}
			fmt.Printf("  %-40s %s\n", t.Name, desc)
			count++
		}
	}
	fmt.Printf("\n%d tools\n", count)
	return nil
}

func replShowToolHelp(socketPath, toolName string) error {
	result, err := call(socketPath, "loom/tools", nil)
	if err != nil {
		return err
	}

	var tools struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema,omitempty"`
		} `json:"tools"`
	}

	if err := json.Unmarshal(result, &tools); err != nil {
		return err
	}

	for _, t := range tools.Tools {
		if t.Name == toolName {
			fmt.Printf("Tool: %s\n", t.Name)
			fmt.Printf("Description: %s\n", t.Description)
			if len(t.InputSchema) > 0 {
				fmt.Println("\nInput Schema:")
				var schema interface{}
				if err := json.Unmarshal(t.InputSchema, &schema); err == nil {
					pretty, _ := json.MarshalIndent(schema, "  ", "  ")
					fmt.Println("  " + string(pretty))
				}
			}
			return nil
		}
	}

	return fmt.Errorf("tool not found: %s", toolName)
}

func replCallTool(socketPath, toolName, jsonArgs string) error {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(jsonArgs), &args); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}

	result, err := call(socketPath, "tools/call", map[string]interface{}{
		"name":      toolName,
		"arguments": args,
	})
	if err != nil {
		return err
	}

	var prettyResult interface{}
	if err := json.Unmarshal(result, &prettyResult); err == nil {
		prettyBytes, _ := json.MarshalIndent(prettyResult, "", "  ")
		fmt.Println(string(prettyBytes))
	} else {
		fmt.Println(string(result))
	}
	return nil
}

func replListServers(socketPath string) error {
	result, err := call(socketPath, "loom/servers", nil)
	if err != nil {
		return err
	}

	var resp struct {
		Servers []struct {
			Name        string `json:"name"`
			Description string `json:"description,omitempty"`
			Running     bool   `json:"running"`
		} `json:"servers"`
	}

	if err := json.Unmarshal(result, &resp); err != nil {
		return err
	}

	fmt.Printf("%-20s %-8s %s\n", "NAME", "STATUS", "DESCRIPTION")
	for _, s := range resp.Servers {
		status := "idle"
		if s.Running {
			status = "running"
		}
		desc := s.Description
		if len(desc) > 50 {
			desc = desc[:47] + "..."
		}
		fmt.Printf("%-20s %-8s %s\n", s.Name, status, desc)
	}
	return nil
}
