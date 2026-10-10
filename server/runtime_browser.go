package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/internal/browserrelay"
	"github.com/Autumn-27/artex/internal/toolruntime"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

type browserRun struct {
	taskID   int64
	lease    context.Context
	verified bool
}

type browserRuntime struct {
	s          *Server
	control    *url.URL
	executable string
	http       *http.Client
	mu         sync.Mutex
	healthMu   sync.Mutex
	runs       map[string]*browserRun
	ready      bool
	message    string
	nextID     int
	closed     bool
	denyProxy  netip.AddrPort
}

type browserMCPClient struct {
	b      *browserRuntime
	ctx    context.Context
	taskID int64
	id     string
	gate   sync.Mutex
	closed bool
	stop   func() bool
}

func newBrowserRuntime(s *Server) *browserRuntime {
	b := &browserRuntime{s: s, runs: make(map[string]*browserRun), http: &http.Client{Transport: &http.Transport{Proxy: nil, MaxIdleConns: 4, IdleConnTimeout: time.Second}, Timeout: 35 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, message: "브라우저 메인 중계가 준비되지 않았습니다"}
	raw := os.Getenv("ARTEX_BROWSER_CONTROL_URL")
	u, err := url.Parse(raw)
	if len(s.desktopSession) == 64 && err == nil && u.Scheme == "http" && u.Hostname() == "127.0.0.1" && u.Port() != "" && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.User == nil && filepath.IsAbs(os.Getenv("ARTEX_BROWSER_EXECUTABLE")) {
		b.control = u
		b.executable = os.Getenv("ARTEX_BROWSER_EXECUTABLE")
	}
	return b
}

func browserID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}

func (b *browserRuntime) rpc(ctx context.Context, method string, params any) (json.RawMessage, error) {
	b.mu.Lock()
	if b.control == nil || b.closed {
		b.mu.Unlock()
		return nil, errors.New("브라우저 메인 중계가 준비되지 않았습니다")
	}
	b.nextID++
	id := b.nextID
	endpoint := b.control.ResolveReference(&url.URL{Path: "/rpc"}).String()
	b.mu.Unlock()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(desktopSessionHeader, string(b.s.desktopSession))
	res, err := b.http.Do(req)
	if err != nil {
		return nil, errors.New("브라우저 메인 중계 호출이 종료되거나 실패했습니다")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, errors.New("브라우저 메인 중계가 앱 세션을 거부했습니다")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (48<<20)+1))
	if err != nil || len(raw) > 48<<20 {
		return nil, errors.New("브라우저 메인 중계 응답 크기 또는 읽기 오류")
	}
	var result struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &result) != nil || result.JSONRPC != "2.0" || result.ID != id {
		return nil, errors.New("브라우저 JSON-RPC 응답이 유효하지 않습니다")
	}
	if result.Error != nil {
		return nil, errors.New(result.Error.Message)
	}
	return result.Result, nil
}

// health launches an about:blank-only worker and verifies its live Windows
// token through the same main-only channel used by actual task calls. This
// probe has taskID=0 and can never acquire a network lease.
func (b *browserRuntime) health(ctx context.Context) (bool, string) {
	b.healthMu.Lock()
	defer b.healthMu.Unlock()
	b.mu.Lock()
	ready, message, control := b.ready, b.message, b.control
	b.mu.Unlock()
	if ready || control == nil {
		return ready, message
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	id := browserID()
	b.mu.Lock()
	b.runs[id] = &browserRun{}
	b.mu.Unlock()
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = b.rpc(closeCtx, "sessions/close", map[string]any{"session_id": id})
		b.mu.Lock()
		delete(b.runs, id)
		b.mu.Unlock()
	}()
	raw, err := b.rpc(ctx, "sandbox/probe", map[string]any{"session_id": id, "task_id": 0})
	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		b.message = "브라우저 격리 실제 검증 실패: " + err.Error()
		return false, b.message
	}
	if run := b.runs[id]; run == nil || !run.verified {
		b.message = "브라우저 renderer의 OS 토큰을 검증하지 못했습니다"
		return false, b.message
	}
	var probe struct {
		DenyProxyPort uint16 `json:"deny_proxy_port"`
	}
	if json.Unmarshal(raw, &probe) != nil || probe.DenyProxyPort == 0 {
		b.message = "브라우저 네트워크 차단 중계를 확인할 수 없습니다"
		return false, b.message
	}
	b.denyProxy = netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), probe.DenyProxyPort)
	b.ready = true
	b.message = "Chromium lockdown AppContainer·메모리 프로필·작업 승인 대상 중계를 사용합니다"
	return true, b.message
}

func (b *browserRuntime) Close() {
	b.mu.Lock()
	ids := make([]string, 0, len(b.runs))
	for id := range b.runs {
		ids = append(ids, id)
	}
	b.mu.Unlock()
	for _, id := range ids {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = b.rpc(ctx, "sessions/close", map[string]any{"session_id": id})
		cancel()
	}
	b.mu.Lock()
	b.closed = true
	clear(b.runs)
	b.mu.Unlock()
	b.http.CloseIdleConnections()
}

func (s *Server) connectMCP(ctx context.Context, m *db.MCPServer) (mcpClient, error) {
	if desktopToolSession() && m.Name == browserMCPName {
		if m.Transport != "stdio" {
			return nil, errors.New("앱 브라우저 MCP는 메인 중계 전용입니다")
		}
		if s.browser == nil || s.browser.control == nil {
			return nil, errors.New("브라우저 메인 중계가 준비되지 않았습니다")
		}
		c := &browserMCPClient{b: s.browser, ctx: ctx, taskID: agent.RunInfoFrom(ctx).TaskID}
		c.stop = context.AfterFunc(ctx, func() { _ = c.Close() })
		return c, nil
	}
	return connectMCP(ctx, m)
}

func (c *browserMCPClient) Tools(ctx context.Context) ([]actool.CoreTool, error) {
	raw, err := c.b.rpc(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var listing struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if json.Unmarshal(raw, &listing) != nil || len(listing.Tools) == 0 || len(listing.Tools) > 100 {
		return nil, errors.New("브라우저 MCP 도구 목록이 유효하지 않습니다")
	}
	out := make([]actool.CoreTool, 0, len(listing.Tools))
	seen := map[string]bool{}
	for _, item := range listing.Tools {
		name := item.Name
		if !strings.HasPrefix(name, "browser_") || seen[name] || item.InputSchema == nil {
			return nil, errors.New("브라우저 MCP 도구 이름 또는 스키마가 유효하지 않습니다")
		}
		seen[name] = true
		full := "mcp__browser__" + name
		out = append(out, actool.Build(actool.Spec{Name: full, Description: item.Description, Schema: item.InputSchema, Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.AskUser("브라우저 도구 실행: " + full)
		}, Run: func(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
			return c.run(ctx, name, in, tc), nil
		}}))
	}
	return out, nil
}

func (c *browserMCPClient) run(ctx context.Context, name string, in json.RawMessage, tc *actool.ToolContext) actool.Result {
	c.gate.Lock()
	defer c.gate.Unlock()
	if c.closed || c.taskID <= 0 || agent.RunInfoFrom(ctx).TaskID != c.taskID || tc == nil {
		return actool.Errorf("브라우저 실행은 승인된 작업의 실행 문맥에서만 가능합니다")
	}
	if err := ctx.Err(); err != nil {
		return actool.Errorf(err.Error())
	}
	work := filepath.Join(c.b.s.m.workspaceDir(), "tasks", strconv.FormatInt(c.taskID, 10))
	rel, err := filepath.Rel(work, tc.WorkingDir)
	if err != nil || (!filepath.IsLocal(rel) && rel != ".") || validateManagedWorkspace(tc.WorkingDir) != nil {
		return actool.Errorf("브라우저 도구의 작업 공간 소유자가 유효하지 않습니다")
	}
	if ok, message := c.b.health(ctx); !ok {
		return actool.Errorf(message)
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if c.id == "" {
		c.id = browserID()
		c.b.mu.Lock()
		c.b.runs[c.id] = &browserRun{taskID: c.taskID}
		c.b.mu.Unlock()
		if _, err := c.b.rpc(callCtx, "sessions/open", map[string]any{"session_id": c.id, "task_id": c.taskID}); err != nil {
			c.b.mu.Lock()
			delete(c.b.runs, c.id)
			c.b.mu.Unlock()
			c.id = ""
			return actool.Errorf(err.Error())
		}
	}
	id := c.id
	c.b.mu.Lock()
	run := c.b.runs[id]
	verified := run != nil && run.verified
	if verified {
		run.lease = callCtx
	}
	c.b.mu.Unlock()
	if !verified {
		return actool.Errorf("브라우저 renderer의 OS 토큰이 검증되지 않았습니다")
	}
	defer func() {
		c.b.mu.Lock()
		if run := c.b.runs[id]; run != nil {
			run.lease = nil
		}
		c.b.mu.Unlock()
	}()
	var args any
	if len(in) > 0 && json.Unmarshal(in, &args) != nil {
		return actool.Errorf("브라우저 입력 JSON이 유효하지 않습니다")
	}
	raw, err := c.b.rpc(callCtx, "tools/call", map[string]any{"session_id": id, "name": name, "arguments": args})
	if err != nil {
		c.closeSession()
		return actool.Errorf(err.Error())
	}
	if name == "browser_close" {
		c.closeSession()
	}
	var result struct {
		Content []struct {
			Type     string `json:"type"`
			Data     string `json:"data"`
			MimeType string `json:"mimeType"`
			Text     string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return actool.Errorf("브라우저 MCP 응답이 유효하지 않습니다")
	}
	var text strings.Builder
	for _, part := range result.Content {
		if part.Type == "text" {
			text.WriteString(part.Text)
			continue
		}
		if part.Type != "image" || part.MimeType != "image/png" {
			return actool.Errorf("지원되지 않는 브라우저 MCP 콘텐츠입니다")
		}
		data, err := base64.StdEncoding.DecodeString(part.Data)
		if err != nil || len(data) > 32<<20 || len(data) < 8 || !bytes.Equal(data[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
			return actool.Errorf("브라우저 PNG 응답이 유효하지 않습니다")
		}
		path, err := c.b.s.wsPath(filepath.Join("tasks", strconv.FormatInt(c.taskID, 10), "browser-"+browserID()+".png"), true)
		if err == nil {
			err = c.b.s.wsSave(path, bytes.NewReader(data))
		}
		if err != nil {
			return actool.Errorf("브라우저 스크린샷 작업 파일 저장 실패: " + err.Error())
		}
		fmt.Fprintf(&text, "스크린샷 저장: %s\n", filepath.Join(c.b.s.m.workspaceDir(), path))
	}
	out := actool.Text(actool.Capture(tc, text.String()))
	out.IsError = result.IsError
	return out
}

func (c *browserMCPClient) closeSession() {
	if c.id == "" {
		return
	}
	id := c.id
	c.id = ""
	c.b.mu.Lock()
	delete(c.b.runs, id)
	c.b.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = c.b.rpc(ctx, "sessions/close", map[string]any{"session_id": id})
}
func (c *browserMCPClient) Close() error {
	c.gate.Lock()
	defer c.gate.Unlock()
	if !c.closed {
		c.closed = true
		c.closeSession()
		if c.stop != nil {
			c.stop()
		}
	}
	return nil
}

func (s *Server) browserMainRequest(r *http.Request) bool {
	return s.browser != nil && s.validDesktopSession(r) && len(r.Header.Values("Origin")) == 0 && len(r.Header.Values("Cookie")) == 0 && r.URL.RawQuery == ""
}

func (s *Server) browserVerify(w http.ResponseWriter, r *http.Request) {
	if !s.browserMainRequest(r) {
		writeErr(w, 403, "브라우저 메인 전용 요청입니다")
		return
	}
	var input struct {
		PID int `json:"pid"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if decode(r, &input) != nil {
		writeErr(w, 400, "브라우저 PID가 유효하지 않습니다")
		return
	}
	id := r.PathValue("session")
	s.browser.mu.Lock()
	run := s.browser.runs[id]
	s.browser.mu.Unlock()
	if run == nil {
		writeErr(w, 403, "브라우저 실행 소유자가 없습니다")
		return
	}
	status, err := toolruntime.VerifyBrowserRenderer(input.PID, s.browser.executable)
	if err != nil {
		s.browser.mu.Lock()
		s.browser.ready = false
		s.browser.message = "브라우저 renderer의 OS 격리 실제 검증이 실패했습니다"
		s.browser.mu.Unlock()
		writeErr(w, 403, err.Error())
		return
	}
	s.browser.mu.Lock()
	if current := s.browser.runs[id]; current == run {
		run.verified = true
	}
	s.browser.mu.Unlock()
	writeJSON(w, 200, status)
}

func (s *Server) browserRelay(w http.ResponseWriter, r *http.Request) {
	if !s.browserMainRequest(r) {
		writeErr(w, 403, "브라우저 메인 전용 요청입니다")
		return
	}
	id := r.PathValue("session")
	s.browser.mu.Lock()
	run := s.browser.runs[id]
	var lease context.Context
	var taskID int64
	if run != nil && run.verified {
		lease, taskID = run.lease, run.taskID
	}
	s.browser.mu.Unlock()
	if lease == nil || lease.Err() != nil || taskID <= 0 {
		writeErr(w, 403, "승인된 브라우저 호출이 종료됐습니다")
		return
	}
	var input browserrelay.Request
	r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
	if decode(r, &input) != nil {
		writeErr(w, 400, "브라우저 요청이 유효하지 않습니다")
		return
	}
	options := browserrelay.Options{ProxyURL: s.m.GlobalProxy(), Authorize: func(ctx context.Context, u *url.URL, ips []netip.Addr) error {
		s.browser.mu.Lock()
		current := s.browser.runs[id]
		allowed := current == run && current.lease == lease && lease.Err() == nil
		s.browser.mu.Unlock()
		if !allowed {
			return errors.New("승인된 브라우저 호출이 종료됐습니다")
		}
		return s.authorizeBrowserTarget(ctx, taskID, u, ips)
	}}
	for _, host := range []string{s.desktopHost, s.browser.control.Host} {
		if endpoint, err := netip.ParseAddrPort(host); err == nil {
			options.DeniedEndpoints = append(options.DeniedEndpoints, endpoint)
		}
	}
	s.browser.mu.Lock()
	if s.browser.denyProxy.IsValid() {
		options.DeniedEndpoints = append(options.DeniedEndpoints, s.browser.denyProxy)
	}
	s.browser.mu.Unlock()
	if s.m.traffic != nil {
		if proxy, err := url.Parse(s.m.traffic.ProxyAddr()); err == nil {
			if endpoint, err := netip.ParseAddrPort(proxy.Host); err == nil {
				options.DeniedEndpoints = append(options.DeniedEndpoints, endpoint)
			}
		}
	}
	if s.m.traffic != nil && s.m.TrafficEnabled() {
		options.Record = s.m.traffic.RecordBrowser
	}
	relay, err := browserrelay.New(options)
	if err != nil {
		writeErr(w, 403, err.Error())
		return
	}
	ctx, cancel := context.WithCancel(lease)
	defer cancel()
	stop := context.AfterFunc(r.Context(), cancel)
	defer stop()
	result, err := relay.Do(ctx, input)
	if err != nil {
		writeErr(w, 403, err.Error())
		return
	}
	type browserCookie struct {
		Name        string `json:"name"`
		Value       string `json:"value"`
		Domain      string `json:"domain,omitempty"`
		Path        string `json:"path,omitempty"`
		Secure      bool   `json:"secure"`
		HTTPOnly    bool   `json:"http_only"`
		SameSite    int    `json:"same_site"`
		Expires     int64  `json:"expires,omitempty"`
		MaxAge      int    `json:"max_age"`
		Partitioned bool   `json:"partitioned"`
	}
	cookies := []browserCookie{}
	for _, cookie := range (&http.Response{Header: result.Headers}).Cookies() {
		if cookie.Valid() != nil {
			continue
		}
		parsed := browserCookie{Name: cookie.Name, Value: cookie.Value, Domain: cookie.Domain, Path: cookie.Path, Secure: cookie.Secure, HTTPOnly: cookie.HttpOnly, SameSite: int(cookie.SameSite), MaxAge: cookie.MaxAge, Partitioned: cookie.Partitioned}
		if !cookie.Expires.IsZero() {
			parsed.Expires = cookie.Expires.Unix()
		}
		cookies = append(cookies, parsed)
	}
	writeJSON(w, 200, struct {
		browserrelay.Response
		Cookies []browserCookie `json:"cookies"`
	}{result, cookies})
}

func browserDomain(host, domain string, root bool) bool {
	host, domain = strings.ToLower(strings.TrimSuffix(host, ".")), strings.ToLower(strings.TrimSuffix(domain, "."))
	return domain != "" && (host == domain || (root && strings.HasSuffix(host, "."+domain)))
}

func (s *Server) authorizeBrowserTarget(ctx context.Context, taskID int64, u *url.URL, ips []netip.Addr) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	task, err := s.m.pg.GetTask(taskID)
	if err != nil || task == nil {
		return errors.New("브라우저 작업이 삭제됐거나 범위 저장소를 읽지 못했습니다")
	}
	scopes, err := s.m.assets.ListTaskScope(taskID)
	if err != nil || len(scopes) > 100000 {
		return errors.New("브라우저 작업 범위를 읽지 못했습니다")
	}
	blocks, err := s.m.assets.ListAssetInterceptRules()
	if err != nil {
		return errors.New("브라우저 자산 차단 정책을 읽지 못했습니다")
	}
	taskBlocks, allows, err := s.m.assets.TaskInterceptRulesSplit(taskID)
	if err != nil {
		return errors.New("브라우저 작업 허용·차단 정책을 읽지 못했습니다")
	}
	blocks = append(blocks, taskBlocks...)
	hostAllowed := false
	allowedIPs := []netip.Prefix{}
	addIP := func(value string) {
		if addr, err := netip.ParseAddr(value); err == nil {
			addr = addr.Unmap()
			allowedIPs = append(allowedIPs, netip.PrefixFrom(addr, addr.BitLen()))
		} else if prefix, err := netip.ParsePrefix(value); err == nil {
			allowedIPs = append(allowedIPs, prefix)
		}
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	// Only declared task_scope rows authorize requests. Derived root assets
	// created while linking an endpoint are facts, not scope expansion.
	for _, scope := range scopes {
		switch scope.Kind {
		case "root_domain", "subdomain":
			if browserDomain(host, scope.Domain, scope.Kind == "root_domain") {
				hostAllowed = true
			}
		case "ip", "cidr":
			addIP(scope.Net)
		case "company":
			if scope.CompanyID == nil {
				return errors.New("브라우저 기업 범위가 유효하지 않습니다")
			}
			rules, err := s.m.assets.Companies().GetScope(*scope.CompanyID)
			if err != nil {
				return errors.New("브라우저 기업 범위를 읽지 못했습니다")
			}
			for _, rule := range rules {
				if rule.Domain != "" && browserDomain(host, rule.Domain, true) {
					hostAllowed = true
				}
				if rule.Net != "" {
					addIP(rule.Net)
				}
			}
		}
	}
	if literal, err := netip.ParseAddr(host); err == nil {
		for _, prefix := range allowedIPs {
			if prefix.Contains(literal.Unmap()) {
				hostAllowed = true
			}
		}
	}
	if !hostAllowed {
		return errors.New("브라우저 대상이 이 작업의 명시적 범위에 없습니다")
	}
	domains, urls := []string{host}, []string{u.String()}
	knownIPs := []string{}
	if literal, err := netip.ParseAddr(host); err == nil {
		knownIPs = append(knownIPs, literal.Unmap().String())
	}
	for _, ip := range ips {
		knownIPs = append(knownIPs, ip.Unmap().String())
	}
	if gate := db.EvaluateAssetGate(blocks, nil, domains, knownIPs, urls); !gate.Allowed {
		return errors.New("브라우저 요청이 자산 차단 정책에 의해 거부됐습니다")
	}
	// DNS may be needed to evaluate an IP allow rule. Never accept one allowed
	// answer as approval for another answer that the transport could dial.
	if ips != nil {
		for _, ip := range ips {
			if gate := db.EvaluateAssetGate(nil, allows, domains, []string{ip.Unmap().String()}, urls); !gate.Allowed {
				return errors.New("브라우저 요청이 작업 허용 정책에 포함되지 않습니다")
			}
		}
	}
	for _, ip := range ips {
		if !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() {
			return errors.New("브라우저 DNS 주소가 허용되지 않습니다")
		}
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			explicit := false
			for _, prefix := range allowedIPs {
				if prefix.Contains(ip) {
					explicit = true
				}
			}
			if !explicit {
				return errors.New("브라우저의 로컬·사설 DNS 주소는 작업의 명시적 IP 범위가 필요합니다")
			}
		}
	}
	return nil
}
