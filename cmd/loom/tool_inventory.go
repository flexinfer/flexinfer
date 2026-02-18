package main

import (
	"fmt"
	"strconv"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

const (
	defaultToolPageSize = 100
	minToolPageSize     = 10
	maxToolPageSize     = 500
)

type toolInventoryPage struct {
	Server     string     `json:"server"`
	Page       int        `json:"page"`
	PageSize   int        `json:"pageSize"`
	TotalTools int        `json:"totalTools"`
	TotalPages int        `json:"totalPages"`
	Tools      []mcp.Tool `json:"tools"`
}

func clampToolPageSize(pageSize int) int {
	if pageSize < minToolPageSize {
		return minToolPageSize
	}
	if pageSize > maxToolPageSize {
		return maxToolPageSize
	}
	return pageSize
}

func toolServerFromName(toolName string) string {
	server, _, ok := strings.Cut(toolName, "__")
	if !ok || server == "" {
		return ""
	}
	return server
}

func filterToolsByServer(tools []mcp.Tool, server string) ([]mcp.Tool, bool) {
	if server == "" {
		return tools, true
	}

	filtered := make([]mcp.Tool, 0, len(tools))
	serverExists := false
	for _, tool := range tools {
		if toolServerFromName(tool.Name) == server {
			serverExists = true
			filtered = append(filtered, tool)
		}
	}
	return filtered, serverExists
}

func buildToolInventoryPage(tools []mcp.Tool, server string, page, pageSize int, strictServer bool) (toolInventoryPage, error) {
	if page < 1 {
		return toolInventoryPage{}, fmt.Errorf("page must be >= 1")
	}
	if pageSize < 1 {
		return toolInventoryPage{}, fmt.Errorf("page size must be >= 1")
	}

	filtered, serverExists := filterToolsByServer(tools, server)
	if strictServer && server != "" && !serverExists {
		return toolInventoryPage{}, fmt.Errorf("unknown server: %s", server)
	}

	totalTools := len(filtered)
	totalPages := 0
	if totalTools > 0 {
		totalPages = (totalTools + pageSize - 1) / pageSize
	}

	if totalTools == 0 {
		if page != 1 {
			return toolInventoryPage{}, fmt.Errorf("page out of range: %d", page)
		}
		return toolInventoryPage{
			Server:     serverOrAll(server),
			Page:       1,
			PageSize:   pageSize,
			TotalTools: 0,
			TotalPages: 0,
			Tools:      []mcp.Tool{},
		}, nil
	}

	if page > totalPages {
		return toolInventoryPage{}, fmt.Errorf("page out of range: %d", page)
	}

	start := (page - 1) * pageSize
	end := start + pageSize
	if end > totalTools {
		end = totalTools
	}

	return toolInventoryPage{
		Server:     serverOrAll(server),
		Page:       page,
		PageSize:   pageSize,
		TotalTools: totalTools,
		TotalPages: totalPages,
		Tools:      filtered[start:end],
	}, nil
}

func parseLoomToolsInventoryURI(uri string) (server string, page int, ok bool, err error) {
	if uri == "loom://tools/index" {
		return "", 1, true, nil
	}

	if strings.HasPrefix(uri, "loom://tools/page/") {
		page, err = parsePositivePage(strings.TrimPrefix(uri, "loom://tools/page/"))
		if err != nil {
			return "", 0, true, err
		}
		return "", page, true, nil
	}

	if strings.HasPrefix(uri, "loom://tools/server/") {
		tail := strings.TrimPrefix(uri, "loom://tools/server/")
		parts := strings.Split(tail, "/")
		if len(parts) != 3 || parts[1] != "page" {
			return "", 0, true, fmt.Errorf("server URI must match loom://tools/server/{server}/page/{page}")
		}

		server = strings.TrimSpace(parts[0])
		if server == "" {
			return "", 0, true, fmt.Errorf("server must be non-empty")
		}

		page, err = parsePositivePage(parts[2])
		if err != nil {
			return "", 0, true, err
		}
		return server, page, true, nil
	}

	return "", 0, false, nil
}

func parsePositivePage(raw string) (int, error) {
	if raw == "" || strings.Contains(raw, "/") {
		return 0, fmt.Errorf("page must be a positive integer")
	}
	page, err := strconv.Atoi(raw)
	if err != nil || page < 1 {
		return 0, fmt.Errorf("page must be a positive integer")
	}
	return page, nil
}

func serverOrAll(server string) string {
	if server == "" {
		return "all"
	}
	return server
}
