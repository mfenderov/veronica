package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
)

type CallbackResult struct {
	Code  string
	State string
}

type CallbackServer struct {
	bindAddr string
	listener net.Listener
	server   *http.Server
	codeChan chan CallbackResult
	mu       sync.Mutex
}

func NewCallbackServer(bindAddr string) *CallbackServer {
	if bindAddr == "" {
		bindAddr = "127.0.0.1:9091"
	}
	return &CallbackServer{
		bindAddr: bindAddr,
		codeChan: make(chan CallbackResult, 1),
	}
}

func (s *CallbackServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ln, err := net.Listen("tcp", s.bindAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.bindAddr, err)
	}
	s.listener = ln

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/callback", s.handleCallback)

	s.server = &http.Server{Handler: mux}
	go func() {
		_ = s.server.Serve(ln)
	}()

	return nil
}

func (s *CallbackServer) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func (s *CallbackServer) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

func (s *CallbackServer) WaitForCode(ctx context.Context) (CallbackResult, error) {
	select {
	case res := <-s.codeChan:
		return res, nil
	case <-ctx.Done():
		return CallbackResult{}, ctx.Err()
	}
}

func (s *CallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" {
		http.Error(w, "missing code parameter", http.StatusBadRequest)
		return
	}

	select {
	case s.codeChan <- CallbackResult{Code: code, State: state}:
	default:
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>Veronica Authorization</title></head>
<body style="font-family: system-ui, sans-serif; text-align: center; padding: 40px;">
  <h2>Authorization Successful!</h2>
  <p>Veronica has received the credentials. You can close this tab and return to your AI agent.</p>
</body>
</html>`))
}

var ErrCallbackTimeout = errors.New("timed out waiting for authorization callback")
