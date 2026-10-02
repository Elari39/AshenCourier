package httpx

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestRunWaitsForInFlightRequest(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	started, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	srv := NewServer(ServerConfig{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusNoContent)
	})})
	done := make(chan error, 1)
	go func() { done <- srv.run(ctx, func() error { return srv.http.Serve(listener) }) }()
	go func() {
		res, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			res.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("handler never started")
	}
	cancel()
	select {
	case err := <-done:
		close(release)
		t.Fatalf("returned before handler completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not finish")
	}
}
