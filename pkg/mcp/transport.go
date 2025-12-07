// Package mcp implements the Model Context Protocol (MCP) transport layer.
//
// MCP STDIO TRANSPORT SPECIFICATION:
// The MCP stdio transport uses newline-delimited JSON-RPC 2.0 messages.
// This is different from LSP which uses Content-Length headers.
//
// Message format:
//   - Each message is a single JSON object on one line
//   - Messages are terminated by newline (\n)
//   - Messages MUST NOT contain embedded newlines
//   - UTF-8 encoding is required
//
// Example message:
//
//	{"jsonrpc":"2.0","id":1,"method":"initialize","params":{...}}\n
//
// Reference: https://modelcontextprotocol.io/specification/2025-06-18/basic/transports
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Transport defines the interface for MCP message transport.
type Transport interface {
	// Send sends a message over the transport.
	Send(ctx context.Context, msg *Message) error
	// Recv receives a message from the transport.
	Recv(ctx context.Context) (*Message, error)
	// Close closes the transport.
	Close() error
}

// StdioTransport implements MCP transport over stdio using newline-delimited JSON.
type StdioTransport struct {
	reader *bufio.Reader
	writer io.Writer
	mu     sync.Mutex
}

// NewStdioTransport creates a new stdio transport.
func NewStdioTransport(r io.Reader, w io.Writer) *StdioTransport {
	return &StdioTransport{
		reader: bufio.NewReader(r),
		writer: w,
	}
}

// Send sends a message.
// We use newline-delimited JSON for compatibility with clients that don't support LSP-style framing.
func (t *StdioTransport) Send(ctx context.Context, msg *Message) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	// Use newline-delimited JSON
	if _, err := t.writer.Write(data); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	if _, err := t.writer.Write([]byte("\n")); err != nil {
		return fmt.Errorf("write newline: %w", err)
	}

	return nil
}

// Recv receives a message.
// Supports both Content-Length framing and newline-delimited JSON.
func (t *StdioTransport) Recv(ctx context.Context) (*Message, error) {
	// Peek at the first few bytes to detect framing
	// If we can't peek enough bytes, it might be a short line or empty.
	// We fall through to recvLine to handle it (including any errors).
	peek, _ := t.reader.Peek(14) // "Content-Length" is 14 chars

	// Check for Content-Length header
	if strings.HasPrefix(string(peek), "Content-Length") {
		return t.recvContentLength(ctx)
	}

	// Fallback to newline-delimited JSON
	return t.recvLine(ctx)
}

func (t *StdioTransport) recvContentLength(ctx context.Context) (*Message, error) {
	// Read headers
	var contentLength int64
	for {
		line, err := t.reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read header: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")

		if line == "" {
			break // End of headers
		}

		if strings.HasPrefix(line, "Content-Length: ") {
			if _, err := fmt.Sscanf(line, "Content-Length: %d", &contentLength); err != nil {
				return nil, fmt.Errorf("parse content length: %w", err)
			}
		}
	}

	if contentLength <= 0 {
		return nil, fmt.Errorf("invalid content length: %d", contentLength)
	}

	// Read body
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(t.reader, body); err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var msg Message
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal message: %w", err)
	}

	return &msg, nil
}

func (t *StdioTransport) recvLine(ctx context.Context) (*Message, error) {
	line, err := t.reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read message: %w", err)
	}

	line = strings.TrimSpace(line)
	if line == "" {
		// Skip empty lines and try again
		return t.Recv(ctx)
	}

	// Ignore non-JSON lines (like debug output) unless they look like JSON
	if !strings.HasPrefix(line, "{") {
		// Log or ignore? For now, recurse
		return t.Recv(ctx)
	}

	var msg Message
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return nil, fmt.Errorf("unmarshal message: %w", err)
	}

	return &msg, nil
}

// Close is a no-op for stdio transport.
func (t *StdioTransport) Close() error {
	return nil
}

// PipeTransport is an in-memory transport for testing.
type PipeTransport struct {
	incoming chan *Message
	outgoing chan *Message
	closed   bool
	mu       sync.Mutex
}

// NewPipeTransport creates a pair of connected pipe transports.
func NewPipeTransport() (*PipeTransport, *PipeTransport) {
	ch1 := make(chan *Message, 16)
	ch2 := make(chan *Message, 16)

	t1 := &PipeTransport{incoming: ch1, outgoing: ch2}
	t2 := &PipeTransport{incoming: ch2, outgoing: ch1}

	return t1, t2
}

// Send sends a message to the connected transport.
func (t *PipeTransport) Send(ctx context.Context, msg *Message) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return fmt.Errorf("transport closed")
	}
	t.mu.Unlock()

	select {
	case t.outgoing <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Recv receives a message from the connected transport.
func (t *PipeTransport) Recv(ctx context.Context) (*Message, error) {
	select {
	case msg, ok := <-t.incoming:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close closes the transport.
func (t *PipeTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		t.closed = true
		close(t.outgoing)
	}
	return nil
}
