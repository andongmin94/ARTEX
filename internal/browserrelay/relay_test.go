package browserrelay

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/internal/browserports"
)

func allow(context.Context, *url.URL, []netip.Addr) error { return nil }

func browserFixtureListener(t *testing.T) net.Listener {
	t.Helper()
	var held []net.Listener
	defer func() {
		for _, listener := range held {
			_ = listener.Close()
		}
	}()
	for i := 0; i < browserports.BindAttempts(); i++ {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		if browserports.Allowed(listener.Addr().(*net.TCPAddr).Port) {
			return listener
		}
		held = append(held, listener)
	}
	t.Fatal("fixture could not bind an allowed browser port")
	return nil
}

func browserFixtureServer(t *testing.T, handler http.Handler, tls bool) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	_ = server.Listener.Close()
	server.Listener = browserFixtureListener(t)
	if tls {
		server.StartTLS()
	} else {
		server.Start()
	}
	t.Cleanup(server.Close)
	return server
}

func TestPinnedDNSPreservesHostAndFiltersCredentials(t *testing.T) {
	var hits atomic.Int32
	target := browserFixtureServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Host != "fixture.invalid:"+strings.Split(r.Host, ":")[1] || r.URL.Path != "/request" || r.URL.RawQuery != "q=value" {
			t.Error("original URL/host changed", r.Host, r.URL)
		}
		for _, name := range []string{"X-Artex-Desktop-Session", "Proxy-Authorization", "X-Hop"} {
			if r.Header.Get(name) != "" {
				t.Errorf("private/hop header leaked: %s", name)
			}
		}
		if r.Header.Get("Authorization") != "Bearer target-fixture" || r.Header.Get("Cookie") != "site=fixture" {
			t.Error("target authorization/cookie stripped")
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Alt-Svc", `h3=":443"`)
		w.Header().Set("Set-Cookie", "next=value; Path=/")
		w.WriteHeader(201)
		_, _ = w.Write(body)
	}), false)
	defer target.Close()
	u, _ := url.Parse(target.URL)
	var resolutions atomic.Int32
	var authorizations atomic.Int32
	relay, err := New(Options{
		LookupIP: func(context.Context, string, string) ([]netip.Addr, error) {
			resolutions.Add(1)
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
		Authorize: func(_ context.Context, target *url.URL, addresses []netip.Addr) error {
			if target.Hostname() != "fixture.invalid" {
				t.Error("unexpected authorization host")
			}
			if authorizations.Add(1) == 1 && addresses != nil {
				t.Error("DNS occurred before initial approval")
			}
			if addresses != nil {
				if len(addresses) != 1 || addresses[0] != netip.MustParseAddr("127.0.0.1") {
					t.Error("unexpected DNS answers")
				}
				addresses[0] = netip.MustParseAddr("192.0.2.1") // Callback cannot change dial destination.
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := relay.Do(context.Background(), Request{URL: "http://fixture.invalid:" + u.Port() + "/request?q=value#ignored", Method: "POST", BodyBase64: base64.StdEncoding.EncodeToString([]byte("한글 fixture")), Headers: http.Header{
		"x-artex-desktop-session": {"never-target"}, "proxy-authorization": {"never-target"}, "connection": {"x-hop"}, "x-hop": {"never-target"},
		"Authorization": {"Bearer target-fixture"}, "Cookie": {"site=fixture"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := base64.StdEncoding.DecodeString(result.BodyBase64)
	if result.Status != 201 || string(body) != "한글 fixture" || result.Headers.Get("Alt-Svc") != "" || result.Headers.Get("Set-Cookie") == "" {
		t.Fatal("unexpected relay response", result.Status, string(body), result.Headers)
	}
	if resolutions.Load() != 1 || authorizations.Load() != 2 || hits.Load() != 1 {
		t.Fatal("unexpected request count")
	}
}

func TestAuthorizationAndOwnListenersDenyBeforeNetwork(t *testing.T) {
	var network atomic.Int32
	target := browserFixtureServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { network.Add(1) }), false)
	defer target.Close()
	u, _ := url.Parse(target.URL)
	endpoint := netip.MustParseAddrPort(u.Host)
	for _, phase := range []string{"before-dns", "after-dns", "app-alias", "app-mapped-ip"} {
		t.Run(phase, func(t *testing.T) {
			var dns int
			options := Options{Authorize: allow, LookupIP: func(context.Context, string, string) ([]netip.Addr, error) {
				dns++
				return []netip.Addr{endpoint.Addr()}, nil
			}}
			if strings.HasPrefix(phase, "app-") {
				options.DeniedEndpoints = []netip.AddrPort{endpoint}
			} else {
				options.Authorize = func(_ context.Context, _ *url.URL, addresses []netip.Addr) error {
					if (phase == "before-dns") == (addresses == nil) {
						return errors.New("fixture denied")
					}
					return nil
				}
			}
			relay, _ := New(options)
			raw := "http://alias.invalid:" + u.Port() + "/"
			if phase == "app-mapped-ip" {
				raw = "http://[::ffff:127.0.0.1]:" + u.Port() + "/"
			}
			if _, err := relay.Do(context.Background(), Request{URL: raw, Method: "GET"}); err == nil {
				t.Fatal("denied request sent")
			}
			if phase == "before-dns" && dns != 0 {
				t.Fatal("unapproved host resolved")
			}
		})
	}
	if network.Load() != 0 {
		t.Fatal("denied target received network request")
	}
}

func TestRedirectRequiresNewAuthorization(t *testing.T) {
	var redirected atomic.Int32
	forbidden := browserFixtureServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }), false)
	defer forbidden.Close()
	target := browserFixtureServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, forbidden.URL+"/private", 302) }), false)
	defer target.Close()
	relay, _ := New(Options{Authorize: allow})
	result, err := relay.Do(context.Background(), Request{URL: target.URL, Method: "GET"})
	if err != nil || result.Status != 302 || result.Headers.Get("Location") != forbidden.URL+"/private" {
		t.Fatal(result, err)
	}
	if redirected.Load() != 0 {
		t.Fatal("Go followed unapproved redirect")
	}
}

func TestTLSValidatesOriginalHostname(t *testing.T) {
	target := browserFixtureServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("TLS fixture")) }), true)
	defer target.Close()
	pool := x509.NewCertPool()
	pool.AddCert(target.Certificate())
	u, _ := url.Parse(target.URL)
	relay, _ := New(Options{Authorize: allow, RootCAs: pool, LookupIP: func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}})
	// httptest's certificate has example.com SAN, but the OS never resolves this synthetic target.
	result, err := relay.Do(context.Background(), Request{URL: "https://example.com:" + u.Port(), Method: "GET"})
	if err != nil || result.Status != 200 {
		t.Fatal(result, err)
	}
	if _, err := relay.Do(context.Background(), Request{URL: "https://untrusted.invalid:" + u.Port(), Method: "GET"}); err == nil {
		t.Fatal("hostname mismatch accepted")
	}
	untrusted, _ := New(Options{Authorize: allow})
	if _, err := untrusted.Do(context.Background(), Request{URL: target.URL, Method: "GET"}); err == nil {
		t.Fatal("untrusted certificate accepted")
	}
}

func TestHTTPAndHTTPSProxyConnectToPinnedIP(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP", true: "HTTPS"}[secure], func(t *testing.T) {
			var hit atomic.Int32
			target := browserFixtureServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hit.Add(1)
				if r.Header.Get("Proxy-Authorization") != "" {
					t.Error("proxy credential reached target")
				}
				_, _ = w.Write([]byte(r.Host))
			}), false)
			defer target.Close()
			u, _ := url.Parse(target.URL)
			var connects atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "CONNECT" || r.Host != u.Host || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("fixture:secret")) {
					t.Error("CONNECT was not pinned/authenticated", r.Method, r.Host)
					w.WriteHeader(403)
					return
				}
				connects.Add(1)
				upstream, err := net.DialTimeout("tcp", r.Host, time.Second)
				if err != nil {
					w.WriteHeader(502)
					return
				}
				defer upstream.Close()
				client, buffer, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer client.Close()
				_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
				_ = buffer.Flush()
				done := make(chan struct{})
				go func() { _, _ = io.Copy(upstream, buffer); _ = upstream.Close(); close(done) }()
				_, _ = io.Copy(client, upstream)
				_ = client.Close()
				<-done
			})
			var proxyServer *httptest.Server
			if secure {
				proxyServer = browserFixtureServer(t, handler, true)
			} else {
				proxyServer = browserFixtureServer(t, handler, false)
			}
			defer proxyServer.Close()
			proxyURL, _ := url.Parse(proxyServer.URL)
			proxyURL.User = url.UserPassword("fixture", "secret")
			pool := x509.NewCertPool()
			if secure {
				pool.AddCert(proxyServer.Certificate())
			}
			relay, err := New(Options{Authorize: allow, ProxyURL: proxyURL.String(), RootCAs: pool, LookupIP: func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := relay.Do(context.Background(), Request{URL: "http://fixture.invalid:" + u.Port() + "/", Method: "GET", Headers: http.Header{"Proxy-Authorization": {"untrusted"}}})
			if err != nil || result.Status != 200 || connects.Load() != 1 || hit.Load() != 1 {
				t.Fatal(result, err, connects.Load(), hit.Load())
			}
		})
	}
}

func TestCancellationAndInputLimits(t *testing.T) {
	started := make(chan struct{})
	target := browserFixtureServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }), false)
	defer target.Close()
	relay, _ := New(Options{Authorize: allow})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := relay.Do(ctx, Request{URL: target.URL, Method: "GET"}); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not stop target request")
	}
	for _, input := range []Request{
		{URL: "file:///fixture", Method: "GET"}, {URL: "http://user:password@fixture.invalid", Method: "GET"}, {URL: "http://127.0.0.1:25", Method: "GET"},
		{URL: target.URL, Method: "CONNECT"}, {URL: target.URL, Method: "GET", BodyBase64: "%%%"},
		{URL: target.URL, Method: "GET", Headers: http.Header{"X-Fixture": {"first\r\nInjected: header"}}},
		{URL: target.URL, Method: "POST", BodyBase64: strings.Repeat("A", base64.StdEncoding.EncodedLen(MaxRequestBytes)+1)},
	} {
		if _, err := relay.Do(context.Background(), input); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
	if _, err := New(Options{}); err == nil {
		t.Fatal("missing policy accepted")
	}
}

func TestResponseBodyLimit(t *testing.T) {
	target := browserFixtureServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.Copy(w, io.LimitReader(strings.NewReader(strings.Repeat("x", MaxResponseBytes+1)), MaxResponseBytes+1))
	}), false)
	defer target.Close()
	relay, _ := New(Options{Authorize: allow})
	if _, err := relay.Do(context.Background(), Request{URL: target.URL, Method: "GET"}); err == nil {
		t.Fatal("oversized response accepted")
	}
}

func TestSOCKSProxyReceivesPinnedIPAndOnlyProxyCredentials(t *testing.T) {
	target := browserFixtureServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("SOCKS credentials leaked to target")
		}
		_, _ = w.Write([]byte("SOCKS fixture"))
	}), false)
	defer target.Close()
	u, _ := url.Parse(target.URL)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		client, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer client.Close()
		_ = client.SetDeadline(time.Now().Add(3 * time.Second))
		read := func(n int) ([]byte, error) { b := make([]byte, n); _, err := io.ReadFull(client, b); return b, err }
		greeting, err := read(2)
		if err != nil {
			done <- err
			return
		}
		methods, err := read(int(greeting[1]))
		if err != nil || greeting[0] != 5 || !strings.Contains(string(methods), string([]byte{2})) {
			done <- errors.New("SOCKS auth greeting missing")
			return
		}
		_, _ = client.Write([]byte{5, 2})
		auth, err := read(2)
		if err != nil {
			done <- err
			return
		}
		username, err := read(int(auth[1]))
		if err != nil {
			done <- err
			return
		}
		plen, err := read(1)
		if err != nil {
			done <- err
			return
		}
		password, err := read(int(plen[0]))
		if err != nil || auth[0] != 1 || string(username) != "fixture" || string(password) != "secret" {
			done <- errors.New("SOCKS proxy credentials incorrect")
			return
		}
		_, _ = client.Write([]byte{1, 0})
		header, err := read(4)
		if err != nil || string(header) != string([]byte{5, 1, 0, 1}) {
			done <- errors.New("SOCKS target was not pinned IPv4")
			return
		}
		address, err := read(6)
		if err != nil {
			done <- err
			return
		}
		p, _ := strconv.Atoi(u.Port())
		if string(address) != string([]byte{127, 0, 0, 1, byte(p >> 8), byte(p)}) {
			done <- errors.New("SOCKS received wrong target")
			return
		}
		upstream, err := net.DialTimeout("tcp", u.Host, time.Second)
		if err != nil {
			done <- err
			return
		}
		defer upstream.Close()
		_, _ = client.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
		var streams sync.WaitGroup
		streams.Add(1)
		go func() { defer streams.Done(); _, _ = io.Copy(upstream, client); _ = upstream.Close() }()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
		streams.Wait()
		done <- nil
	}()
	relay, err := New(Options{Authorize: allow, ProxyURL: "socks5://fixture:secret@" + listener.Addr().String(), LookupIP: func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := relay.Do(context.Background(), Request{URL: "http://fixture.invalid:" + u.Port(), Method: "GET"})
	if err != nil || result.Status != 200 {
		t.Fatal(result, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestProxyHandshakeCancellationAndHostProxyIgnored(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	started := make(chan struct{})
	closed := make(chan struct{})
	go func() {
		c, err := listener.Accept()
		if err != nil {
			close(started)
			close(closed)
			return
		}
		defer c.Close()
		header := make([]byte, 1024)
		_, _ = c.Read(header)
		close(started)
		_, _ = c.Read(header)
		close(closed)
	}()
	relay, _ := New(Options{Authorize: allow, ProxyURL: "http://" + listener.Addr().String()})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := relay.Do(ctx, Request{URL: "http://127.0.0.1:8080/", Method: "GET"}); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancel succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("proxy handshake did not cancel")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("canceled proxy socket remained open")
	}
	var proxyHits atomic.Int32
	hostProxy := browserFixtureServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { proxyHits.Add(1); w.WriteHeader(502) }), false)
	defer hostProxy.Close()
	t.Setenv("HTTP_PROXY", hostProxy.URL)
	t.Setenv("HTTPS_PROXY", hostProxy.URL)
	t.Setenv("NO_PROXY", "")
	target := browserFixtureServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), false)
	defer target.Close()
	u, _ := url.Parse(target.URL)
	direct, _ := New(Options{Authorize: allow, LookupIP: func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}})
	result, err := direct.Do(context.Background(), Request{URL: "http://fixture.invalid:" + u.Port(), Method: "GET"})
	if err != nil || result.Status != 204 || proxyHits.Load() != 0 {
		t.Fatal("relay inherited host proxy", result, err)
	}
}

func TestDiagnosticsDoNotExposeTargetQueryOrPeerStatusLine(t *testing.T) {
	listener := browserFixtureListener(t)
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := listener.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(time.Second))
		b := make([]byte, 4096)
		_, _ = c.Read(b)
		_, _ = c.Write([]byte("target-secret-from-query\r\n\r\n"))
	}()
	relay, _ := New(Options{Authorize: allow})
	_, err := relay.Do(context.Background(), Request{URL: "http://" + listener.Addr().String() + "/?key=target-secret-from-query", Method: "GET"})
	if err == nil || strings.Contains(err.Error(), "target-secret-from-query") {
		t.Fatal("target diagnostic leaked secret", err)
	}
	<-done
}

func TestProxyHandshakeHeaderBoundAndBufferedTunnel(t *testing.T) {
	for _, variant := range []string{"status", "header", "buffered-tunnel"} {
		t.Run(variant, func(t *testing.T) {
			listener := browserFixtureListener(t)
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				request, err := http.ReadRequest(bufio.NewReader(conn))
				if err != nil || request.Method != "CONNECT" {
					t.Error("proxy fixture did not receive CONNECT", err)
					return
				}
				switch variant {
				case "status":
					_, _ = io.WriteString(conn, "HTTP/1.1 200 "+strings.Repeat("fixture", 65536)+"\r\n\r\n")
				case "header":
					_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nX-Fixture: "+strings.Repeat("fixture", 65536)+"\r\n\r\n")
				case "buffered-tunnel":
					_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\n\r\nHTTP/1.1 200 OK\r\nContent-Length: 7\r\n\r\nfixture")
					_, _ = http.ReadRequest(bufio.NewReader(conn))
				}
			}()
			relay, err := New(Options{Authorize: allow, ProxyURL: "http://" + listener.Addr().String()})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if variant == "buffered-tunnel" {
				// Inspect the CONNECT stream directly. A target HTTP Transport
				// correctly rejects an unsolicited response before its request.
				conn, err := relay.dial(ctx, "tcp", "127.0.0.1:8080")
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				_, _ = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: 127.0.0.1:8080\r\n\r\n")
				response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "GET"})
				if err != nil {
					t.Fatal("CONNECT discarded prefetched tunnel bytes", err)
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				if err != nil || string(body) != "fixture" {
					t.Fatal("CONNECT changed prefetched tunnel body", string(body), err)
				}
			} else {
				_, err := relay.Do(ctx, Request{URL: "http://127.0.0.1:8080/", Method: "GET"})
				cause := errors.Unwrap(err)
				if err == nil || errors.Is(err, context.DeadlineExceeded) || cause == nil || !strings.Contains(cause.Error(), "브라우저 프록시 응답 헤더가 너무 큽니다") {
					t.Fatal("oversized proxy header was not rejected before timeout", err)
				}
			}
			<-done
		})
	}
}
