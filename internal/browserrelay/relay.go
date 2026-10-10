// Package browserrelay forwards browser HTTP requests through an owner-supplied
// authorization boundary. It never delegates target DNS to the browser or proxy.
package browserrelay

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/internal/browserports"
	"golang.org/x/net/http/httpguts"
	"golang.org/x/net/proxy"
)

const (
	MaxRequestBytes  = 8 << 20
	MaxResponseBytes = 32 << 20
	maxHeaderBytes   = 64 << 10
)

type Request struct {
	URL        string      `json:"url"`
	Method     string      `json:"method"`
	Headers    http.Header `json:"headers,omitempty"`
	BodyBase64 string      `json:"body_base64,omitempty"`
}

type Response struct {
	Status     int         `json:"status"`
	Headers    http.Header `json:"headers"`
	BodyBase64 string      `json:"body_base64"`
}

// Authorize is called before DNS with a nil address slice, then again with the
// exact DNS answers that may be dialed. Both calls must succeed. The owner must
// check current task/run ownership and policy on every call; a URL alone is not
// an approval. A nil callback denies all requests.
type Authorize func(context.Context, *url.URL, []netip.Addr) error

type Options struct {
	Authorize       Authorize
	LookupIP        func(context.Context, string, string) ([]netip.Addr, error)
	ProxyURL        string
	RootCAs         *x509.CertPool
	DeniedEndpoints []netip.AddrPort // Includes the application's own listeners.
	Record          func(*http.Request, []byte, *http.Response, []byte) error
}

type Relay struct {
	options Options
	proxy   *url.URL
}

// Keep peer-supplied status lines, DNS details and filesystem paths out of the
// browser/model-facing diagnostic, while preserving cancellation/error identity.
type relayError struct {
	message string
	cause   error
}

func (e *relayError) Error() string { return e.message }
func (e *relayError) Unwrap() error { return e.cause }

func New(options Options) (*Relay, error) {
	if options.Authorize == nil {
		return nil, errors.New("브라우저 요청의 실행 소유자와 대상 승인 정책이 없습니다")
	}
	if options.LookupIP == nil {
		options.LookupIP = net.DefaultResolver.LookupNetIP
	}
	var upstream *url.URL
	if options.ProxyURL != "" {
		var err error
		upstream, err = url.Parse(options.ProxyURL)
		if err != nil || upstream.Hostname() == "" || upstream.Path != "" && upstream.Path != "/" || upstream.RawQuery != "" || upstream.Fragment != "" {
			return nil, errors.New("브라우저 중계 프록시 주소가 유효하지 않습니다")
		}
		var defaultPort string
		switch upstream.Scheme {
		case "http":
			defaultPort = "80"
		case "https":
			defaultPort = "443"
		case "socks5":
			defaultPort = "1080"
		default:
			return nil, errors.New("브라우저 중계 프록시는 http/https/socks5만 지원합니다")
		}
		if upstream.Port() == "" {
			upstream.Host = net.JoinHostPort(upstream.Hostname(), defaultPort)
		}
		port, err := strconv.Atoi(upstream.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, errors.New("브라우저 중계 프록시 포트가 유효하지 않습니다")
		}
	}
	options.DeniedEndpoints = append([]netip.AddrPort(nil), options.DeniedEndpoints...)
	return &Relay{options: options, proxy: upstream}, nil
}

func targetURL(raw string) (*url.URL, int, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.Hostname() == "" || u.User != nil || strings.Contains(u.Hostname(), "%") {
		return nil, 0, errors.New("브라우저 대상 주소가 유효하지 않습니다")
	}
	port := 80
	switch u.Scheme {
	case "http":
	case "https":
		port = 443
	default:
		return nil, 0, errors.New("브라우저 요청은 승인된 http/https 주소만 허용합니다")
	}
	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil {
			return nil, 0, errors.New("브라우저 대상 포트가 유효하지 않습니다")
		}
	}
	if !browserports.Allowed(port) {
		return nil, 0, errors.New("브라우저가 제한하는 대상 포트입니다")
	}
	u.Fragment, u.RawFragment = "", ""
	return u, port, nil
}

func (r *Relay) resolve(ctx context.Context, target *url.URL, port int) ([]netip.Addr, error) {
	if err := r.options.Authorize(ctx, target, nil); err != nil {
		return nil, err
	}
	var addresses []netip.Addr
	if literal, err := netip.ParseAddr(target.Hostname()); err == nil {
		addresses = []netip.Addr{literal.Unmap()}
	} else {
		var err error
		addresses, err = r.options.LookupIP(ctx, "ip", target.Hostname())
		if err != nil {
			return nil, &relayError{message: "브라우저 대상 주소 확인 실패", cause: err}
		}
	}
	if len(addresses) == 0 {
		return nil, errors.New("브라우저 대상의 IP 주소가 없습니다")
	}
	for i, addr := range addresses {
		addr = addr.Unmap()
		if !addr.IsValid() || addr.IsUnspecified() || addr.IsMulticast() || addr.Zone() != "" {
			return nil, errors.New("브라우저 대상의 IP 주소가 유효하지 않습니다")
		}
		addresses[i] = addr
		for _, denied := range r.options.DeniedEndpoints {
			if denied.Addr().Unmap() == addr && int(denied.Port()) == port {
				return nil, errors.New("브라우저에서 앱 내부 통신 주소에 접근할 수 없습니다")
			}
		}
	}
	if err := r.options.Authorize(ctx, target, append([]netip.Addr(nil), addresses...)); err != nil {
		return nil, err
	}
	return addresses, nil
}

func (r *Relay) Do(ctx context.Context, input Request) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	target, port, err := targetURL(input.URL)
	if err != nil {
		return Response{}, err
	}
	switch input.Method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
	default:
		return Response{}, errors.New("지원하지 않는 브라우저 HTTP 메서드입니다")
	}
	if len(input.BodyBase64) > base64.StdEncoding.EncodedLen(MaxRequestBytes) {
		return Response{}, errors.New("브라우저 요청 본문이 제한을 초과했습니다")
	}
	body, err := base64.StdEncoding.Strict().DecodeString(input.BodyBase64)
	if err != nil || len(body) > MaxRequestBytes {
		return Response{}, errors.New("브라우저 요청 본문이 유효하지 않거나 제한을 초과했습니다")
	}
	headers, err := safeHeaders(input.Headers)
	if err != nil {
		return Response{}, err
	}
	addresses, err := r.resolve(ctx, target, port)
	if err != nil {
		return Response{}, err
	}
	// Keep the URL hostname for origin/cookies/TLS, but dial only this approved
	// IP. A fresh transport prevents cross-owner connection or proxy reuse.
	address := net.JoinHostPort(addresses[0].String(), strconv.Itoa(port))
	transport := &http.Transport{
		Proxy:             nil, // Do not inherit host HTTP_PROXY / credentials.
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: r.options.RootCAs, ServerName: target.Hostname()},
		DisableKeepAlives: true, MaxResponseHeaderBytes: maxHeaderBytes,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return r.dial(ctx, network, address)
		},
	}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, input.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return Response{}, errors.New("브라우저 요청을 만들 수 없습니다")
	}
	request.Header = headers
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err // Do not put query credentials in diagnostic errors.
		}
		message := "브라우저 대상 통신 실패"
		if errors.Is(err, context.Canceled) {
			message = "브라우저 대상 통신이 취소됐습니다"
		} else if errors.Is(err, context.DeadlineExceeded) {
			message = "브라우저 대상 통신 제한 시간을 초과했습니다"
		}
		return Response{}, &relayError{message: message, cause: err}
	}
	defer response.Body.Close()
	output, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if err != nil {
		return Response{}, &relayError{message: "브라우저 응답 읽기 실패", cause: err}
	}
	if len(output) > MaxResponseBytes {
		return Response{}, errors.New("브라우저 응답 본문이 제한을 초과했습니다")
	}
	if r.options.Record != nil {
		if err := r.options.Record(request, body, response, output); err != nil {
			return Response{}, &relayError{message: "브라우저 트래픽 기록 실패", cause: err}
		}
	}
	responseHeaders, err := safeHeaders(response.Header)
	if err != nil {
		return Response{}, err
	}
	// The browser follows 3xx responses through this relay again; Go never
	// follows them behind the owner's back. Do not advertise HTTP/3 or an
	// alternate network endpoint outside the intercepted HTTP protocols.
	responseHeaders.Del("Alt-Svc")
	return Response{Status: response.StatusCode, Headers: responseHeaders, BodyBase64: base64.StdEncoding.EncodeToString(output)}, nil
}

func safeHeaders(source http.Header) (http.Header, error) {
	out := make(http.Header)
	blocked := map[string]bool{"host": true, "content-length": true, "connection": true, "proxy-connection": true, "keep-alive": true, "transfer-encoding": true, "trailer": true, "upgrade": true, "te": true, "proxy-authorization": true, "proxy-authenticate": true, "x-artex-desktop-session": true}
	for name, values := range source {
		if strings.EqualFold(name, "Connection") {
			for _, connection := range values {
				for _, name := range strings.Split(connection, ",") {
					blocked[strings.ToLower(strings.TrimSpace(name))] = true
				}
			}
		}
	}
	count := 0
	for name, values := range source {
		if !httpguts.ValidHeaderFieldName(name) {
			return nil, errors.New("브라우저 헤더 이름이 유효하지 않습니다")
		}
		for _, value := range values {
			count += len(name) + len(value)
			if count > maxHeaderBytes || !httpguts.ValidHeaderFieldValue(value) {
				return nil, errors.New("브라우저 헤더가 유효하지 않거나 제한을 초과했습니다")
			}
			if !blocked[strings.ToLower(name)] {
				out.Add(name, value)
			}
		}
	}
	return out, nil
}

func (r *Relay) dial(ctx context.Context, network, target string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if r.proxy == nil {
		return dialer.DialContext(ctx, network, target)
	}
	if r.proxy.Scheme == "socks5" {
		upstream, err := proxy.FromURL(r.proxy, dialer)
		if err != nil {
			return nil, errors.New("브라우저 SOCKS 프록시 준비 실패")
		}
		contextDialer, ok := upstream.(proxy.ContextDialer)
		if !ok {
			return nil, errors.New("브라우저 SOCKS 프록시가 실행 취소를 지원하지 않습니다")
		}
		return contextDialer.DialContext(ctx, network, target)
	}
	connection, err := dialer.DialContext(ctx, network, r.proxy.Host)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			connection.Close()
		}
	}()
	if deadline, exists := ctx.Deadline(); exists {
		if err := connection.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}
	plainConnection := connection
	stop := context.AfterFunc(ctx, func() { plainConnection.Close() })
	defer stop()
	if r.proxy.Scheme == "https" {
		secured := tls.Client(connection, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: r.proxy.Hostname(), RootCAs: r.options.RootCAs})
		if err := secured.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		connection = secured
	}
	connect := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
	if r.proxy.User != nil {
		password, _ := r.proxy.User.Password()
		connect.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(r.proxy.User.Username()+":"+password)))
	}
	if err := connect.Write(connection); err != nil {
		return nil, err
	}
	// Transport's target header limit does not cover this manual CONNECT.
	// Bound the handshake while preserving any prefetched tunnel bytes.
	bounded := &io.LimitedReader{R: connection, N: maxHeaderBytes + 1}
	reader := bufio.NewReader(bounded)
	response, err := http.ReadResponse(reader, connect)
	if bounded.N == 0 {
		return nil, errors.New("브라우저 프록시 응답 헤더가 너무 큽니다")
	}
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("브라우저 프록시 연결이 거부됐습니다: %d", response.StatusCode)
	}
	bounded.N = 1<<63 - 1
	if err := connection.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	ok = true
	return &bufferedConn{Conn: connection, reader: reader}, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
