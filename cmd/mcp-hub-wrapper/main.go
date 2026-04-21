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
	"sync"
	"syscall"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

const (
	defaultProfile        = "codex"
	defaultHubURL         = "wss://mcp.flexinfer.ai/ws"
	defaultConnectTimeout = 10 * time.Second
	replayTimeout         = 10 * time.Second
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

	connectHub := func(ctx context.Context) (mcp.Transport, error) {
		return mcp.NewWebSocketTransport(ctx, wsCfg, serverName)
	}

	hubTransport, err := connectHub(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "error: connect hub transport: %v\n", err)
		return 1
	}

	stdioTransport := mcp.NewStdioTransport(stdin, stdout)
	defer stdioTransport.Close()

	if err := bridge(ctx, stdioTransport, hubTransport, connectHub); err != nil {
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

type hubConnector func(context.Context) (mcp.Transport, error)

type pingTransport interface {
	Ping(context.Context) error
}

type reconnectingBridge struct {
	ctx     context.Context
	stdio   mcp.Transport
	connect hubConnector

	hubMu      sync.RWMutex
	hub        mcp.Transport
	closedHubs map[mcp.Transport]struct{}
	hubReady   chan struct{}

	reconnectMu sync.Mutex

	requestPermit chan struct{}

	inflightMu      sync.Mutex
	inflightID      any
	inflightHub     mcp.Transport
	inflightRequest *mcp.Message
	inflightRetried bool

	initMu      sync.RWMutex
	initRequest *mcp.Message
	initNotify  *mcp.Message
}

func bridge(ctx context.Context, stdio mcp.Transport, hub mcp.Transport, connect hubConnector) error {
	if connect == nil {
		connect = func(context.Context) (mcp.Transport, error) { return hub, nil }
	}
	bridgeCtx, cancel := context.WithCancel(ctx)
	b := &reconnectingBridge{
		ctx:           bridgeCtx,
		stdio:         stdio,
		connect:       connect,
		hub:           hub,
		closedHubs:    make(map[mcp.Transport]struct{}),
		hubReady:      make(chan struct{}, 1),
		requestPermit: make(chan struct{}, 1),
	}
	b.requestPermit <- struct{}{}
	defer b.closeHub()
	defer cancel()

	errCh := make(chan error, 1)
	go b.recvLoop(errCh)

	for {
		select {
		case err := <-errCh:
			return err
		default:
		}

		msg, err := stdio.Recv(bridgeCtx)
		if err != nil {
			return fmt.Errorf("stdio recv: %w", err)
		}

		b.rememberInitialization(msg)

		if isRequestMessage(msg) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-b.requestPermit:
			}
		}

		if err := b.sendHubMessage(msg); err != nil {
			if isRequestMessage(msg) {
				b.releaseRequestPermit()
				if sendErr := stdio.Send(ctx, mcp.NewErrorResponse(msg.ID, mcp.InternalError, err.Error())); sendErr != nil {
					return fmt.Errorf("stdio send: %w", sendErr)
				}
				continue
			}
			return err
		}
	}
}

func (b *reconnectingBridge) recvLoop(errCh chan<- error) {
	for {
		if err := b.ctx.Err(); err != nil {
			errCh <- err
			return
		}

		hub := b.currentHub()
		if hub == nil {
			// No active hub. Reconnection is driven by new work from the
			// main loop (sendHubMessage) or by retryInFlight when an
			// in-flight request needs retrying. Wait for a new hub to
			// appear or for shutdown.
			select {
			case <-b.ctx.Done():
				errCh <- b.ctx.Err()
				return
			case <-b.hubReady:
			}
			continue
		}

		msg, err := hub.Recv(b.ctx)
		if err != nil {
			b.markHubFailed(hub)
			if b.retryInFlight(hub) {
				continue
			}
			b.failInFlight(hub, fmt.Errorf("hub recv: %w", err))
			continue
		}
		if err := b.stdio.Send(b.ctx, msg); err != nil {
			errCh <- fmt.Errorf("stdio send: %w", err)
			return
		}
		if isResponseMessage(msg) {
			if b.clearInFlightForHub(hub) {
				b.releaseRequestPermit()
			}
		}
	}
}

func (b *reconnectingBridge) sendHubMessage(msg *mcp.Message) error {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		hub, err := b.ensureHub()
		if err != nil {
			lastErr = err
			continue
		}
		if isRequestMessage(msg) {
			if err := pingHub(b.ctx, hub); err != nil {
				lastErr = fmt.Errorf("hub ping: %w", err)
				b.markHubFailed(hub)
				continue
			}
		}
		if isRequestMessage(msg) {
			b.setInFlight(msg.ID, hub, msg)
		}
		if err := hub.Send(b.ctx, msg); err != nil {
			if isRequestMessage(msg) {
				b.clearInFlightForHub(hub)
			}
			lastErr = fmt.Errorf("hub send: %w", err)
			b.markHubFailed(hub)
			continue
		}
		return nil
	}
	return lastErr
}

func pingHub(ctx context.Context, hub mcp.Transport) error {
	pinger, ok := hub.(pingTransport)
	if !ok {
		return nil
	}
	return pinger.Ping(ctx)
}

func (b *reconnectingBridge) ensureHub() (mcp.Transport, error) {
	if hub := b.currentHub(); hub != nil {
		return hub, nil
	}
	if err := b.reconnect(); err != nil {
		return nil, err
	}
	hub := b.currentHub()
	if hub == nil {
		return nil, fmt.Errorf("hub reconnect produced no transport")
	}
	return hub, nil
}

func (b *reconnectingBridge) reconnect() error {
	b.reconnectMu.Lock()
	defer b.reconnectMu.Unlock()

	if hub := b.currentHub(); hub != nil {
		return nil
	}
	if err := b.ctx.Err(); err != nil {
		return err
	}

	hub, err := b.connect(b.ctx)
	if err != nil {
		return fmt.Errorf("hub reconnect: %w", err)
	}
	if err := b.replayInitialization(hub); err != nil {
		_ = hub.Close()
		return err
	}

	b.hubMu.Lock()
	b.hub = hub
	b.hubMu.Unlock()
	select {
	case b.hubReady <- struct{}{}:
	default:
	}
	return nil
}

func (b *reconnectingBridge) replayInitialization(hub mcp.Transport) error {
	initReq, initNotify := b.initializationMessages()
	if initReq == nil {
		return nil
	}

	replayCtx, cancel := context.WithTimeout(b.ctx, replayTimeout)
	defer cancel()

	if err := hub.Send(replayCtx, initReq); err != nil {
		return fmt.Errorf("hub replay initialize send: %w", err)
	}
	resp, err := hub.Recv(replayCtx)
	if err != nil {
		return fmt.Errorf("hub replay initialize recv: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("hub replay initialize error: %s", resp.Error.Message)
	}
	if initNotify != nil {
		if err := hub.Send(replayCtx, initNotify); err != nil {
			return fmt.Errorf("hub replay initialized send: %w", err)
		}
	}
	return nil
}

func (b *reconnectingBridge) currentHub() mcp.Transport {
	b.hubMu.RLock()
	defer b.hubMu.RUnlock()
	return b.hub
}

func (b *reconnectingBridge) markHubFailed(hub mcp.Transport) {
	if hub == nil {
		return
	}

	shouldClose := false
	b.hubMu.Lock()
	if b.hub == hub {
		b.hub = nil
	}
	if _, ok := b.closedHubs[hub]; !ok {
		b.closedHubs[hub] = struct{}{}
		shouldClose = true
	}
	b.hubMu.Unlock()
	if shouldClose {
		_ = hub.Close()
	}
}

func (b *reconnectingBridge) closeHub() {
	hub := b.currentHub()
	b.markHubFailed(hub)
}

func (b *reconnectingBridge) rememberInitialization(msg *mcp.Message) {
	if msg == nil {
		return
	}
	b.initMu.Lock()
	defer b.initMu.Unlock()
	switch msg.Method {
	case "initialize":
		if msg.ID != nil {
			b.initRequest = msg
		}
	case "notifications/initialized":
		b.initNotify = msg
	}
}

func (b *reconnectingBridge) initializationMessages() (*mcp.Message, *mcp.Message) {
	b.initMu.RLock()
	defer b.initMu.RUnlock()
	return b.initRequest, b.initNotify
}

func (b *reconnectingBridge) retryInFlight(failedHub mcp.Transport) bool {
	req := b.inFlightRequestForRetry(failedHub)
	if req == nil {
		return false
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		hub, err := b.ensureHub()
		if err != nil {
			lastErr = err
			continue
		}
		if err := pingHub(b.ctx, hub); err != nil {
			lastErr = fmt.Errorf("hub ping: %w", err)
			b.markHubFailed(hub)
			continue
		}
		b.updateInFlightHub(req.ID, hub)
		if err := hub.Send(b.ctx, req); err != nil {
			lastErr = fmt.Errorf("hub retry send: %w", err)
			b.markHubFailed(hub)
			continue
		}
		return true
	}

	b.failCurrentInFlight(fmt.Errorf("hub retry failed: %w", lastErr))
	return true
}

func (b *reconnectingBridge) inFlightRequestForRetry(failedHub mcp.Transport) *mcp.Message {
	b.inflightMu.Lock()
	defer b.inflightMu.Unlock()
	if b.inflightHub != failedHub || b.inflightRequest == nil || b.inflightRetried {
		return nil
	}
	b.inflightRetried = true
	return b.inflightRequest
}

func (b *reconnectingBridge) setInFlight(id any, hub mcp.Transport, req *mcp.Message) {
	b.inflightMu.Lock()
	b.inflightID = id
	b.inflightHub = hub
	b.inflightRequest = req
	b.inflightRetried = false
	b.inflightMu.Unlock()
}

func (b *reconnectingBridge) updateInFlightHub(id any, hub mcp.Transport) {
	b.inflightMu.Lock()
	if b.inflightID == id {
		b.inflightHub = hub
	}
	b.inflightMu.Unlock()
}

func (b *reconnectingBridge) clearInFlightForHub(hub mcp.Transport) bool {
	b.inflightMu.Lock()
	defer b.inflightMu.Unlock()
	if b.inflightHub != hub {
		return false
	}
	b.inflightID = nil
	b.inflightHub = nil
	b.inflightRequest = nil
	b.inflightRetried = false
	return true
}

func (b *reconnectingBridge) failInFlight(hub mcp.Transport, err error) {
	b.inflightMu.Lock()
	if b.inflightHub != hub {
		b.inflightMu.Unlock()
		return
	}
	id := b.inflightID
	b.inflightID = nil
	b.inflightHub = nil
	b.inflightRequest = nil
	b.inflightRetried = false
	b.inflightMu.Unlock()
	if id == nil {
		return
	}

	_ = b.stdio.Send(b.ctx, mcp.NewErrorResponse(id, mcp.InternalError, err.Error()))
	b.releaseRequestPermit()
}

func (b *reconnectingBridge) failCurrentInFlight(err error) {
	b.inflightMu.Lock()
	id := b.inflightID
	b.inflightID = nil
	b.inflightHub = nil
	b.inflightRequest = nil
	b.inflightRetried = false
	b.inflightMu.Unlock()
	if id == nil {
		return
	}

	_ = b.stdio.Send(b.ctx, mcp.NewErrorResponse(id, mcp.InternalError, err.Error()))
	b.releaseRequestPermit()
}

func (b *reconnectingBridge) releaseRequestPermit() {
	select {
	case b.requestPermit <- struct{}{}:
	default:
	}
}

func isRequestMessage(msg *mcp.Message) bool {
	return msg != nil && msg.ID != nil && strings.TrimSpace(msg.Method) != ""
}

func isResponseMessage(msg *mcp.Message) bool {
	return msg != nil && msg.ID != nil && strings.TrimSpace(msg.Method) == ""
}
