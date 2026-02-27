package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

const (
	defaultProfile        = "codex"
	defaultHubURL         = "wss://mcp.flexinfer.ai/ws"
	defaultConnectTimeout = 10 * time.Second
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printUsage(stderr)
		return 0
	}

	serverName := strings.TrimSpace(args[0])
	if serverName == "" {
		fmt.Fprintln(stderr, "error: missing server name")
		printUsage(stderr)
		return 2
	}

	fs := flag.NewFlagSet("mcp-hub-wrapper", flag.ContinueOnError)
	fs.SetOutput(stderr)
	profile := fs.String("profile", defaultProfile, "Hub profile name")
	hubURL := fs.String("hub-url", defaultHubURL, "Hub WebSocket URL")
	connectTimeout := fs.Duration("connect-timeout", defaultConnectTimeout, "Hub connect timeout")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "error: unexpected positional arguments: %s\n", strings.Join(fs.Args(), " "))
		printUsage(stderr)
		return 2
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	wsCfg := mcp.WebSocketConfig{
		URL:            strings.TrimSpace(*hubURL),
		Profile:        strings.TrimSpace(*profile),
		ConnectTimeout: *connectTimeout,
		ClientInfo: mcp.ClientInfo{
			Name:    "loom-mcp-hub-wrapper",
			Version: "1.0.0",
		},
	}

	token := strings.TrimSpace(os.Getenv("MCP_HUB_TOKEN"))
	if token != "" {
		wsCfg.Headers = map[string]string{
			"Authorization": "Bearer " + token,
		}
	}

	wsCfg.CFAccessClientID = strings.TrimSpace(os.Getenv("CF_ACCESS_CLIENT_ID"))
	wsCfg.CFAccessClientSecret = strings.TrimSpace(os.Getenv("CF_ACCESS_CLIENT_SECRET"))

	hubTransport, err := mcp.NewWebSocketTransport(ctx, wsCfg, serverName)
	if err != nil {
		fmt.Fprintf(stderr, "error: connect hub transport: %v\n", err)
		return 1
	}
	defer hubTransport.Close()

	stdioTransport := mcp.NewStdioTransport(stdin, stdout)
	defer stdioTransport.Close()

	if err := bridge(ctx, stdioTransport, hubTransport); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
			return 0
		}
		fmt.Fprintf(stderr, "error: bridge failed: %v\n", err)
		return 1
	}

	return 0
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  mcp-hub-wrapper <server> [--profile <profile>] [--hub-url <url>] [--connect-timeout <duration>]")
}

func bridge(ctx context.Context, stdio mcp.Transport, hub mcp.Transport) error {
	requestPermit := make(chan struct{}, 1)
	requestPermit <- struct{}{}

	errCh := make(chan error, 1)

	go func() {
		for {
			msg, err := hub.Recv(ctx)
			if err != nil {
				errCh <- fmt.Errorf("hub recv: %w", err)
				return
			}
			if err := stdio.Send(ctx, msg); err != nil {
				errCh <- fmt.Errorf("stdio send: %w", err)
				return
			}
			if isResponseMessage(msg) {
				select {
				case requestPermit <- struct{}{}:
				default:
				}
			}
		}
	}()

	for {
		select {
		case err := <-errCh:
			return err
		default:
		}

		msg, err := stdio.Recv(ctx)
		if err != nil {
			return fmt.Errorf("stdio recv: %w", err)
		}

		if isRequestMessage(msg) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-requestPermit:
			}
		}

		if err := hub.Send(ctx, msg); err != nil {
			return fmt.Errorf("hub send: %w", err)
		}
	}
}

func isRequestMessage(msg *mcp.Message) bool {
	return msg != nil && msg.ID != nil && strings.TrimSpace(msg.Method) != ""
}

func isResponseMessage(msg *mcp.Message) bool {
	return msg != nil && msg.ID != nil && strings.TrimSpace(msg.Method) == ""
}
