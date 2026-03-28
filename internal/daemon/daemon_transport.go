// daemon_transport.go contains Unix socket accept loop and connection handling.
package daemon

import (
	"context"
	"net"
	gosync "sync"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func (d *Daemon) acceptLoop(ctx context.Context) {
	defer d.wg.Done()

	for {
		select {
		case <-d.done:
			return
		default:
		}

		conn, err := d.listener.Accept()
		if err != nil {
			select {
			case <-d.done:
				return
			default:
				d.logger.Error("accept error", "error", err)
				continue
			}
		}

		d.wg.Add(1)
		go d.handleConnection(ctx, conn)
	}
}

func (d *Daemon) handleConnection(ctx context.Context, conn net.Conn) {
	defer d.wg.Done()
	defer conn.Close()

	remoteAddr := ""
	if conn.RemoteAddr() != nil {
		remoteAddr = conn.RemoteAddr().String()
	}

	ctx, connSpan := d.daemonTracer().Start(ctx, "daemon.connection",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("network.transport", "unix"),
			attribute.String("loom.client_addr", remoteAddr),
		),
	)
	messageCount := 0
	defer func() {
		connSpan.SetAttributes(attribute.Int("loom.message_count", messageCount))
		connSpan.End()
	}()

	d.logger.Debug("client connected", "addr", remoteAddr)

	transport := mcp.NewStdioTransport(conn, conn)

	// Subscribe to EventBus for tool/resource change notifications.
	var writeMu gosync.Mutex
	connCtx, connCancel := context.WithCancel(ctx)
	defer connCancel()

	if d.eventBus != nil {
		subID, events := d.eventBus.Subscribe()
		defer d.eventBus.Unsubscribe(subID)

		go d.forwardNotifications(connCtx, events, transport, &writeMu, remoteAddr)
	}

	// Read messages in a dedicated goroutine so client disconnects are
	// detected even while handleMessage blocks (e.g., long tool calls).
	// On disconnect, connCancel fires and propagates to in-flight calls,
	// releasing the per-server call lock immediately instead of waiting
	// for the full routing timeout.
	type recvResult struct {
		msg *mcp.Message
		err error
	}
	msgCh := make(chan recvResult, 1)
	go func() {
		defer close(msgCh)
		for {
			msg, err := transport.Recv(connCtx)
			if err != nil {
				connCancel() // cancel in-flight calls on disconnect
				select {
				case msgCh <- recvResult{nil, err}:
				case <-connCtx.Done():
				}
				return
			}
			select {
			case msgCh <- recvResult{msg, nil}:
			case <-connCtx.Done():
				return
			}
		}
	}()

	for {
		select {
		case <-d.done:
			return
		case <-connCtx.Done():
			return
		case recv, ok := <-msgCh:
			if !ok {
				return
			}
			if recv.err != nil {
				d.logger.Debug("client disconnected", "error", recv.err)
				connSpan.AddEvent("client_disconnected", trace.WithAttributes(attribute.String("error", recv.err.Error())))
				return
			}
			messageCount++

			resp, err := d.handleMessage(connCtx, recv.msg)
			if err != nil {
				if connCtx.Err() != nil {
					// Client disconnected during handling; skip response.
					d.logger.Debug("client disconnected during message handling", "error", err)
					return
				}
				d.logger.Error("handle message error", "error", err)
				connSpan.RecordError(err)
				resp = mcp.NewErrorResponse(recv.msg.ID, mcp.InternalError, err.Error())
			}

			if resp != nil {
				writeMu.Lock()
				sendErr := transport.Send(connCtx, resp)
				writeMu.Unlock()
				if sendErr != nil {
					d.logger.Error("send response error", "error", sendErr)
					connSpan.RecordError(sendErr)
					connSpan.SetStatus(codes.Error, sendErr.Error())
					return
				}
			}
		}
	}
}

// forwardNotifications reads events from the EventBus subscription and writes
// MCP notifications to the transport. It exits when ctx is cancelled or the
// events channel is closed.
func (d *Daemon) forwardNotifications(ctx context.Context, events <-chan Event, transport mcp.Transport, writeMu *gosync.Mutex, remoteAddr string) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			notif := eventToMCPNotification(event)
			if notif == nil {
				continue
			}
			writeMu.Lock()
			err := transport.Send(ctx, notif)
			writeMu.Unlock()
			if err != nil {
				d.logger.Debug("failed to send notification to client",
					"addr", remoteAddr, "event", event.Type, "error", err)
				return
			}
		}
	}
}

// eventToMCPNotification converts a daemon event to an MCP notification message.
// Returns nil for event types that do not map to MCP notifications.
func eventToMCPNotification(event Event) *mcp.Message {
	switch event.Type {
	case EventToolsChanged:
		return &mcp.Message{JSONRPC: "2.0", Method: "notifications/tools/list_changed"}
	case EventResourcesChanged:
		return &mcp.Message{JSONRPC: "2.0", Method: "notifications/resources/list_changed"}
	default:
		return nil
	}
}
