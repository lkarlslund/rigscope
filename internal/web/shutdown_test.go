package web

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestListenAndServeStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)

	go func() {
		errc <- (&Server{}).ListenAndServe(ctx, "127.0.0.1:0")
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ListenAndServe() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ListenAndServe() did not stop after cancellation")
	}
}
