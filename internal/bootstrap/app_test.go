package bootstrap

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestShutdownClosesSSEBeforeHTTPDrain(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Close() }()

	go func() {
		resp, err := http.Get("http://" + listener.Addr().String() + "/events")
		if err != nil {
			return
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE handler did not start")
	}

	var order []string
	listenerDone := make(chan struct{})
	close(listenerDone)
	app := &App{
		server: server,
		closeSSE: func() {
			order = append(order, "sse")
			close(release)
		},
		cancelListener: func() { order = append(order, "listener") },
		cancelWorkers:  func() { order = append(order, "workers") },
		listenerDone:   listenerDone,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := app.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if len(order) == 0 || order[0] != "sse" {
		t.Fatalf("shutdown order = %v, want sse first", order)
	}
}
