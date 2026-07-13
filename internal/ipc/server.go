package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
)

const (
	CodeMethodNotFound = "METHOD_NOT_FOUND"
	FixMethodNotFound  = "simurgh doctor"

	MethodDaemonPing = "daemon.ping"
)

// HandlerFunc handles one decoded request and returns either a JSON result
// payload or a typed error triple (exactly one must be set by convention:
// non-nil *Error means an error response).
type HandlerFunc func(ctx context.Context, req Request) (json.RawMessage, *Error)

// Server accepts Unix-socket connections, decodes M0.2a request frames, and
// dispatches them through a method→handler map.
type Server struct {
	ln       net.Listener
	handlers map[string]HandlerFunc

	mu     sync.Mutex
	closed bool
}

// NewServer returns a Server that serves ln. Exactly one method is registered:
// daemon.ping.
func NewServer(ln net.Listener) *Server {
	s := &Server{
		ln:       ln,
		handlers: make(map[string]HandlerFunc),
	}
	s.handlers[MethodDaemonPing] = handleDaemonPing
	return s
}

// Serve accepts connections until ctx is cancelled or the listener fails.
// Each connection runs in its own goroutine. Serve returns nil when shutdown
// is due to ctx cancellation.
func (s *Server) Serve(ctx context.Context) error {
	var wg sync.WaitGroup
	defer wg.Wait()

	go func() {
		<-ctx.Done()
		s.closeListener()
	}()

	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if ctx.Err() != nil || s.isClosed() {
				return nil
			}
			return fmt.Errorf("ipc: accept: %w", err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.serveConn(ctx, conn)
		}()
	}
}

func (s *Server) closeListener() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	_ = s.ln.Close()
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	dec := NewDecoder(conn)
	for {
		if ctx.Err() != nil {
			return
		}
		req, err := dec.DecodeRequest()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			// Best-effort: malformed frames without a usable request id cannot
			// form a valid error envelope (id is required). Close the connection
			// so one bad client cannot affect the accept loop or peers.
			return
		}

		resp := s.dispatch(ctx, req)
		if err := Encode(conn, resp); err != nil {
			return
		}
	}
}

func (s *Server) dispatch(ctx context.Context, req Request) Response {
	h, ok := s.handlers[req.Method]
	if !ok {
		env, err := NewError(
			CodeMethodNotFound,
			fmt.Sprintf("unknown method %q", req.Method),
			FixMethodNotFound,
		)
		if err != nil {
			return Response{V: ProtocolVersion, ID: req.ID, OK: false, Error: &Error{
				Code: CodeMethodNotFound, Message: "unknown method", Fix: FixMethodNotFound,
			}}
		}
		return Response{V: ProtocolVersion, ID: req.ID, OK: false, Error: env}
	}

	result, herr := h(ctx, req)
	if herr != nil {
		return Response{V: ProtocolVersion, ID: req.ID, OK: false, Error: herr}
	}
	return Response{V: ProtocolVersion, ID: req.ID, OK: true, Result: result}
}

func handleDaemonPing(_ context.Context, _ Request) (json.RawMessage, *Error) {
	raw, err := json.Marshal(map[string]any{
		"pong": true,
		"pid":  os.Getpid(),
	})
	if err != nil {
		env, _ := NewError("INTERNAL", "failed to encode ping result", FixMethodNotFound)
		return nil, env
	}
	return raw, nil
}
