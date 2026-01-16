package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

var upgrader = websocket.Upgrader{
	HandshakeTimeout: 10 * time.Second,
	CheckOrigin:      func(r *http.Request) bool { return true },
}

func main() {
	command := strings.TrimSpace(os.Getenv("MCP_SERVER_COMMAND"))
	if command == "" {
		log.Fatal("MCP_SERVER_COMMAND is required")
	}

	wsPort := strings.TrimSpace(os.Getenv("MCP_WS_PORT"))
	if wsPort == "" {
		wsPort = "8080"
	}
	addr := wsPort
	if !strings.HasPrefix(addr, ":") {
		addr = ":" + addr
	}

	wsPath := strings.TrimSpace(os.Getenv("MCP_WS_PATH"))
	if wsPath == "" {
		wsPath = "/ws"
	}

	serverName := strings.TrimSpace(os.Getenv("MCP_SERVER_NAME"))
	if serverName == "" {
		serverName = "custom-server"
	}

	http.HandleFunc("/health", okHandler)
	http.HandleFunc("/ready", okHandler)
	http.HandleFunc(wsPath, func(w http.ResponseWriter, r *http.Request) {
		handleWS(w, r, serverName, command)
	})

	srv := &http.Server{
		Addr:              addr,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("custom-server listening on %s (ws=%s, server=%s)", addr, wsPath, serverName)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("ListenAndServe: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func okHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func handleWS(w http.ResponseWriter, r *http.Request, serverName, command string) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	cmdName, cmdArgs, err := splitCommand(command)
	if err != nil {
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, err.Error()))
		return
	}

	cmd := exec.CommandContext(ctx, cmdName, cmdArgs...)
	cmd.Env = append(os.Environ(), "MCP_TRANSPORT=stdio")
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "stdin pipe error"))
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "stdout pipe error"))
		return
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "start error"))
		return
	}

	transport := mcp.NewStdioTransport(stdout, stdin)

	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			cancel()
			_ = conn.Close()
			_ = stdin.Close()
			_ = stdout.Close()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			done := make(chan struct{})
			go func() {
				_ = cmd.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
		})
	}
	defer closeAll()

	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					closeAll()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	var wg sync.WaitGroup
	var writeMu sync.Mutex

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			msg, err := transport.Recv(ctx)
			if err != nil {
				closeAll()
				return
			}
			b, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			writeMu.Lock()
			err = conn.WriteMessage(websocket.TextMessage, b)
			writeMu.Unlock()
			if err != nil {
				closeAll()
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			mt, data, err := conn.ReadMessage()
			if err != nil {
				closeAll()
				return
			}
			if mt != websocket.TextMessage && mt != websocket.BinaryMessage {
				continue
			}
			var msg mcp.Message
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			if msg.JSONRPC == "" {
				msg.JSONRPC = mcp.JSONRPCVersion
			}
			if err := transport.Send(ctx, &msg); err != nil {
				closeAll()
				return
			}
		}
	}()

	wg.Wait()
}

func splitCommand(s string) (string, []string, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return "", nil, fmt.Errorf("empty MCP_SERVER_COMMAND")
	}
	return fields[0], fields[1:], nil
}
