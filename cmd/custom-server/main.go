package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
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

type sseSession struct {
	id        string
	createdAt time.Time

	cancel context.CancelFunc

	cmd       *exec.Cmd
	transport *mcp.StdioTransport

	sendMu sync.Mutex

	closeOnce sync.Once
	done      chan struct{}
}

func (s *sseSession) Close() {
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		if s.transport != nil {
			_ = s.transport.Close()
		}
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		close(s.done)
	})
}

func (s *sseSession) Send(ctx context.Context, msg *mcp.Message) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if msg.JSONRPC == "" {
		msg.JSONRPC = mcp.JSONRPCVersion
	}
	return s.transport.Send(ctx, msg)
}

type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]*sseSession
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: make(map[string]*sseSession)}
}

func (st *sessionStore) Get(id string) *sseSession {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.sessions[id]
}

func (st *sessionStore) Put(s *sseSession) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.sessions[s.id] = s
}

func (st *sessionStore) Delete(id string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.sessions, id)
}

func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b[:]), nil
}

func writeSSE(w http.ResponseWriter, event string, data string) error {
	if event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
			return err
		}
	}
	// data may contain newlines; each line must be prefixed with "data: "
	for _, line := range strings.Split(data, "\n") {
		if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

func writeSSEComment(w http.ResponseWriter, comment string) error {
	if _, err := fmt.Fprintf(w, ": %s\n\n", comment); err != nil {
		return err
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

func startMCPProcess(ctx context.Context, serverName, command string) (*exec.Cmd, *mcp.StdioTransport, func(), error) {
	cmdName, cmdArgs, err := splitCommand(command)
	if err != nil {
		return nil, nil, nil, err
	}

	cmd := exec.CommandContext(ctx, cmdName, cmdArgs...)
	cmd.Env = append(os.Environ(), "MCP_TRANSPORT=stdio")
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("stdin pipe error: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, fmt.Errorf("stdout pipe error: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, nil, nil, fmt.Errorf("start error: %w", err)
	}

	transport := mcp.NewStdioTransport(stdout, stdin)

	closeOnce := sync.Once{}
	closeAll := func() {
		closeOnce.Do(func() {
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

	_ = serverName // reserved for future logging / tagging
	return cmd, transport, closeAll, nil
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

	sessions := newSessionStore()

	http.HandleFunc("/health", okHandler)
	http.HandleFunc("/ready", okHandler)

	// SSE transport (MCP SSE spec):
	// - client connects to GET /sse (text/event-stream)
	// - server emits an "endpoint" event containing the POST URL to send messages
	// - client POSTs JSON-RPC messages to /messages?session_id=<hex>
	http.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		sessionID, err := newSessionID()
		if err != nil {
			http.Error(w, "failed to generate session id", http.StatusInternalServerError)
			return
		}

		ctx, cancel := context.WithCancel(r.Context())

		cmd, transport, closeProc, err := startMCPProcess(ctx, serverName, command)
		if err != nil {
			cancel()
			http.Error(w, "failed to start mcp server", http.StatusInternalServerError)
			return
		}

		sess := &sseSession{
			id:        sessionID,
			createdAt: time.Now().UTC(),
			cancel:    cancel,
			cmd:       cmd,
			transport: transport,
			done:      make(chan struct{}),
		}

		sessions.Put(sess)
		defer func() {
			sessions.Delete(sessionID)
			sess.Close()
			closeProc()
		}()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		} else {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		// Build a relative endpoint for the client to POST messages.
		// Keep it consistent with the Python MCP SSE server transport.
		postPath := "/messages"
		q := url.Values{}
		q.Set("session_id", sessionID)
		postURI := postPath + "?" + q.Encode()

		if err := writeSSE(w, "endpoint", postURI); err != nil {
			return
		}

		backendCh := make(chan *mcp.Message, 8)
		go func() {
			defer close(backendCh)
			for {
				msg, err := transport.Recv(ctx)
				if err != nil {
					return
				}
				select {
				case backendCh <- msg:
				case <-ctx.Done():
					return
				}
			}
		}()

		keepalive := time.NewTicker(25 * time.Second)
		defer keepalive.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-sess.done:
				return
			case <-keepalive.C:
				_ = writeSSEComment(w, "ping")
			case msg, ok := <-backendCh:
				if !ok {
					return
				}
				b, err := json.Marshal(msg)
				if err != nil {
					continue
				}
				if err := writeSSE(w, "message", string(b)); err != nil {
					return
				}
			}
		}
	})

	http.HandleFunc("/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		sessionID := r.URL.Query().Get("session_id")
		if sessionID == "" {
			http.Error(w, "session_id is required", http.StatusBadRequest)
			return
		}

		sess := sessions.Get(sessionID)
		if sess == nil {
			http.Error(w, "unknown session_id", http.StatusNotFound)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()

		var msg mcp.Message
		if err := json.Unmarshal(body, &msg); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if msg.JSONRPC == "" {
			msg.JSONRPC = mcp.JSONRPCVersion
		}

		if err := sess.Send(r.Context(), &msg); err != nil {
			http.Error(w, "send failed", http.StatusBadGateway)
			return
		}

		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("OK"))
	})

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
