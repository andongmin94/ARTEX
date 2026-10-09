package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"github.com/Autumn-27/artex/internal/browserports"
)

// startHTTP binds synchronously so an occupied/invalid port is a startup error,
// not a log.Fatal in a goroutine that bypasses the manager's cleanup.
func startHTTP(srv *http.Server, validate func(net.Addr) error) (net.Listener, <-chan error, error) {
	listener, err := listenBrowserHTTP(srv.Addr, net.Listen)
	if err != nil {
		return nil, nil, err
	}
	if validate != nil {
		if err := validate(listener.Addr()); err != nil {
			return nil, nil, errors.Join(err, listener.Close())
		}
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

// Keep the accepted socket bound until Serve takes ownership. Port probing and
// closing a safe socket before rebinding would create a startup race.
func listenBrowserHTTP(address string, listen func(string, string) (net.Listener, error)) (listener net.Listener, err error) {
	_, portString, splitErr := net.SplitHostPort(address)
	if splitErr != nil {
		return nil, splitErr
	}
	requestedPort, portErr := strconv.Atoi(portString)
	if portErr == nil && requestedPort != 0 && !browserports.Allowed(requestedPort) {
		return nil, fmt.Errorf("HTTP port %d is restricted by the bundled browser", requestedPort)
	}
	ephemeral := portErr == nil && requestedPort == 0
	var rejected []net.Listener
	defer func() {
		for _, candidate := range rejected {
			err = errors.Join(err, candidate.Close())
		}
		if err != nil && listener != nil {
			err = errors.Join(err, listener.Close())
			listener = nil
		}
	}()
	for attempt := 0; attempt < browserports.BindAttempts(); attempt++ {
		candidate, bindErr := listen("tcp", address)
		if bindErr != nil {
			return nil, bindErr
		}
		bound, ok := candidate.Addr().(*net.TCPAddr)
		if ok && browserports.Allowed(bound.Port) {
			return candidate, nil
		}
		rejected = append(rejected, candidate)
		if !ok || !ephemeral {
			return nil, fmt.Errorf("HTTP listener address %s is not accepted by the bundled browser", candidate.Addr())
		}
	}
	return nil, fmt.Errorf("no browser-safe HTTP port after %d ephemeral bindings", browserports.BindAttempts())
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
