package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
)

// startHTTP binds synchronously so an occupied/invalid port is a startup error,
// not a log.Fatal in a goroutine that bypasses the manager's cleanup.
func startHTTP(srv *http.Server) (net.Listener, <-chan error, error) {
	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return nil, nil, err
	}
	done := make(chan error, 1)
	go func() {
		err := srv.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
		close(done)
	}()
	return listener, done, nil
}

// announceReady writes one machine-readable event after stores and the listener
// are ready. This is not a login token and does not bypass application auth.
func announceReady(out io.Writer, addr net.Addr, version string) error {
	return json.NewEncoder(out).Encode(struct {
		Event   string `json:"event"`
		URL     string `json:"url"`
		PID     int    `json:"pid"`
		Version string `json:"version"`
	}{
		Event:   "ready",
		URL:     (&url.URL{Scheme: "http", Host: addr.String()}).String(),
		PID:     os.Getpid(),
		Version: version,
	})
}

// waitForParent uses pipe EOF instead of Unix-only signals. A desktop parent
// closes the child's stdin to request normal shutdown, also when it crashes.
// Bytes on this pipe are ignored; it is not a command execution interface.
func waitForParent(input io.Reader, stop func()) {
	_, _ = io.Copy(io.Discard, input)
	stop()
}

// shutdownHTTP drains requests, then closes remaining connections on timeout.
func shutdownHTTP(ctx context.Context, srv *http.Server) error {
	if err := srv.Shutdown(ctx); err != nil {
		return errors.Join(err, srv.Close())
	}
	return nil
}
