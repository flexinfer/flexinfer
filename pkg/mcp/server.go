// Package mcp provides MCP server framework.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// ToolHandler is a function that handles a tool call.
type ToolHandler func(ctx context.Context, args map[string]any) (*CallToolResult, error)

// Server is an MCP server that handles tool calls.
type Server struct {
	name         string
	version      string
	instructions string
	tools        []Tool
	handlers     map[string]ToolHandler
	transport    *StdioTransport
}

// NewServer creates a new MCP server.
func NewServer(name, version string) *Server {
	return &Server{
		name:     name,
		version:  version,
		tools:    []Tool{},
		handlers: make(map[string]ToolHandler),
	}
}

// SetInstructions sets the server instructions.
func (s *Server) SetInstructions(instructions string) {
	s.instructions = instructions
}

// AddTool registers a tool with the server.
func (s *Server) AddTool(tool Tool, handler ToolHandler) {
	s.tools = append(s.tools, tool)
	s.handlers[tool.Name] = handler
}

// Run starts the server on stdio.
func (s *Server) Run(ctx context.Context) error {
	s.transport = NewStdioTransport(os.Stdin, os.Stdout)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		msg, err := s.transport.Recv(ctx)
		if err != nil {
			return nil // Client disconnected
		}

		resp, err := s.handleMessage(ctx, msg)
		if err != nil {
			resp = NewErrorResponse(msg.ID, InternalError, err.Error())
		}

		if resp != nil {
			if err := s.transport.Send(ctx, resp); err != nil {
				return fmt.Errorf("send response: %w", err)
			}
		}
	}
}

func (s *Server) handleMessage(ctx context.Context, msg *Message) (*Message, error) {
	switch msg.Method {
	case "initialize":
		return s.handleInitialize(msg)
	case "notifications/initialized":
		return nil, nil // No response for notifications
	case "tools/list":
		return s.handleToolsList(msg)
	case "tools/call":
		return s.handleToolsCall(ctx, msg)
	case "resources/list":
		return s.handleResourcesList(msg)
	case "prompts/list":
		return s.handlePromptsList(msg)
	default:
		return NewErrorResponse(msg.ID, MethodNotFound, fmt.Sprintf("unknown method: %s", msg.Method)), nil
	}
}

func (s *Server) handleInitialize(msg *Message) (*Message, error) {
	result := InitializeResult{
		ProtocolVersion: ProtocolVersion,
		Capabilities: Capabilities{
			Tools: &ToolsCapability{},
		},
		ServerInfo: ServerInfo{
			Name:    s.name,
			Version: s.version,
		},
		Instructions: s.instructions,
	}
	return NewResponse(msg.ID, result)
}

func (s *Server) handleToolsList(msg *Message) (*Message, error) {
	return NewResponse(msg.ID, ToolsListResult{Tools: s.tools})
}

func (s *Server) handleToolsCall(ctx context.Context, msg *Message) (*Message, error) {
	var params CallToolParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return NewErrorResponse(msg.ID, InvalidParams, err.Error()), nil
	}

	handler, ok := s.handlers[params.Name]
	if !ok {
		return NewErrorResponse(msg.ID, InvalidParams, fmt.Sprintf("unknown tool: %s", params.Name)), nil
	}

	result, err := handler(ctx, params.Arguments)
	if err != nil {
		return NewResponse(msg.ID, &CallToolResult{
			Content: []Content{{Type: "text", Text: err.Error()}},
			IsError: true,
		})
	}

	return NewResponse(msg.ID, result)
}

func (s *Server) handleResourcesList(msg *Message) (*Message, error) {
	return NewResponse(msg.ID, ResourcesListResult{Resources: []Resource{}})
}

func (s *Server) handlePromptsList(msg *Message) (*Message, error) {
	return NewResponse(msg.ID, PromptsListResult{Prompts: []Prompt{}})
}

// TextResult creates a simple text result.
func TextResult(text string) *CallToolResult {
	return &CallToolResult{
		Content: []Content{{Type: "text", Text: text}},
	}
}

// JSONResult creates a JSON result.
func JSONResult(v any) (*CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return &CallToolResult{
		Content: []Content{{Type: "text", Text: string(b)}},
	}, nil
}

// ErrorResult creates an error result.
func ErrorResult(err error) *CallToolResult {
	return &CallToolResult{
		Content: []Content{{Type: "text", Text: err.Error()}},
		IsError: true,
	}
}
