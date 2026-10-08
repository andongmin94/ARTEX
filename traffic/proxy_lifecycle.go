package traffic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	mproxy "github.com/lqqyt2423/go-mitmproxy/proxy"
)

// Start waits for the actual proxy to bind and serve its private local readiness
// response. go-mitmproxy owns net.Listen internally; a port precheck cannot prove
// that its listener started, and could accept an unrelated server on that port.
func (t *Traffic) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.startMu.Lock()
	if t.serveDone != nil || t.stopping() {
		t.startMu.Unlock()
		return errors.New("트래픽 프록시가 이미 시작되었거나 종료되었습니다")
	}
	host, port, err := net.SplitHostPort(t.addr)
	if err != nil {
		t.startMu.Unlock()
		return fmt.Errorf("프록시 수신 주소: %w", err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		t.startMu.Unlock()
		return errors.New("트래픽 프록시는 1~65535 사이의 명시적 포트가 필요합니다")
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if host == "::" {
		host = "::1"
	}
	t.readyPath = "/__artex_proxy_ready/" + uuid.NewString()
	done := make(chan error, 1)
	t.serveDone = done
	readyURL := "http://" + net.JoinHostPort(host, port) + t.readyPath
	t.startMu.Unlock()
	go func() {
		err := t.proxy.Start()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
		close(done)
	}()
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 200 * time.Millisecond}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			if err == nil {
				return errors.New("트래픽 프록시가 준비 전에 종료되었습니다")
			}
			return err
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, readyURL, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, 128))
			resp.Body.Close()
			if readErr == nil && resp.StatusCode == http.StatusOK && string(body) == t.readyPath && t.readySeen.Load() {
				return nil
			}
		}
		select {
		case err := <-done:
			if err == nil {
				return errors.New("트래픽 프록시가 준비 전에 종료되었습니다")
			}
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Done reports an unexpected proxy exit to the backend supervisor.
func (t *Traffic) Done() <-chan error {
	t.startMu.Lock()
	defer t.startMu.Unlock()
	return t.serveDone
}

type proxyLifecycleAddon struct {
	mproxy.BaseAddon
	t *Traffic
}

func (a *proxyLifecycleAddon) AccessProxyServer(req *http.Request, w http.ResponseWriter) {
	if req.Method == http.MethodGet && req.URL.Path == a.t.readyPath {
		a.t.readySeen.Store(true)
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, a.t.readyPath)
	}
}

func (a *proxyLifecycleAddon) ClientConnected(client *mproxy.ClientConn) {
	a.t.connsMu.Lock()
	if a.t.conns == nil {
		a.t.conns = make(map[net.Conn]struct{})
	}
	a.t.conns[client.Conn] = struct{}{}
	stopping := a.t.stopping()
	a.t.connsMu.Unlock()
	if stopping {
		_ = client.Conn.Close()
	}
}

func (a *proxyLifecycleAddon) ClientDisconnected(client *mproxy.ClientConn) {
	a.t.connsMu.Lock()
	delete(a.t.conns, client.Conn)
	a.t.connsMu.Unlock()
}
