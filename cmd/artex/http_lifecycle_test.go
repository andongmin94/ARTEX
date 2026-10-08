package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func awaitServe(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP serving goroutine did not stop")
	}
}

func TestHTTPReadyAndGracefulShutdown(t *testing.T) {
	srv := &http.Server{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "healthy")
	})}
	listener, done, err := startHTTP(srv, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	var out bytes.Buffer
	if err := announceReady(&out, listener.Addr(), "test-version"); err != nil {
		t.Fatal(err)
	}
	var event struct {
		Event, URL, Version string
		PID                 int
	}
	if err := json.Unmarshal(out.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event.Event != "ready" || event.PID != os.Getpid() || event.Version != "test-version" || strings.HasSuffix(event.URL, ":0") {
		t.Fatalf("invalid ready event: %+v", event)
	}
	transport := &http.Transport{Proxy: nil}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Timeout: 2 * time.Second, Transport: transport}
	response, err := client.Get(event.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(body) != "healthy" {
		t.Fatalf("response = %q, %v", body, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := shutdownHTTP(ctx, srv); err != nil {
		t.Fatal(err)
	}
	awaitServe(t, done)
	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err == nil {
		_ = conn.Close()
		t.Fatal("listener survived shutdown")
	}
}

func TestHTTPPortConflictIsSynchronous(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	srv := &http.Server{Addr: occupied.Addr().String()}
	listener, done, err := startHTTP(srv, nil)
	if err == nil {
		_ = srv.Close()
		t.Fatal("expected port conflict")
	}
	if listener != nil || done != nil {
		t.Fatal("failed startup returned a running server")
	}
}

func TestHTTPShutdownTimeoutClosesActiveConnection(t *testing.T) {
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	srv := &http.Server{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(cancelled)
	})}
	listener, done, err := startHTTP(srv, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("request never reached handler")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := shutdownHTTP(ctx, srv); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout, got %v", err)
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("active request not cancelled after forced close")
	}
	awaitServe(t, done)
}

func TestParentPipeShutdown(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	stopped := make(chan struct{})
	go waitForParent(reader, func() { close(stopped) })
	if _, err := io.WriteString(writer, "not a command"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
		t.Fatal("bytes triggered shutdown before EOF")
	default:
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("parent EOF did not stop backend")
	}
}

type brokenPipe struct{}

func (brokenPipe) Read([]byte) (int, error)  { return 0, io.ErrClosedPipe }
func (brokenPipe) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestParentPipeErrorStops(t *testing.T) {
	stopped := false
	waitForParent(brokenPipe{}, func() { stopped = true })
	if !stopped {
		t.Fatal("broken parent pipe did not stop backend")
	}
}

func TestReadyPipeFailureIsReported(t *testing.T) {
	addr := &net.TCPAddr{IP: net.ParseIP("::1"), Port: 8787}
	if err := announceReady(brokenPipe{}, addr, "test"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("expected readiness error, got %v", err)
	}
	var out bytes.Buffer
	if err := announceReady(&out, addr, "test"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "http://[::1]:8787") {
		t.Fatalf("IPv6 address incorrectly encoded: %s", out.String())
	}
}
