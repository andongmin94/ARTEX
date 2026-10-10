package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/internal/browserports"
	"github.com/Autumn-27/artex/internal/browserrelay"
)

// These tests use the full modernc business schema and the actual browser HTTP
// handler. The verified renderer is supplied at the main-process boundary; OS
// token inspection and Chromium execution are covered by the Electron tests.
type browserPolicyFixture struct {
	s    *Server
	db   *db.DB
	task *db.Task
}

func newBrowserPolicyFixture(t *testing.T) *browserPolicyFixture {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "browser-policy.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	task, err := store.CreateTask("로컬 브라우저 범위 검사", "승인된 fixture만 확인", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{m: &Manager{pg: store, assets: store.Assets()}, desktopSession: []byte(strings.Repeat("ab", 32)), jwtKey: []byte("browser-policy-fixture")}
	return &browserPolicyFixture{s: s, db: store, task: task}
}

func (f *browserPolicyFixture) scope(t *testing.T, kind, value string) db.TaskScope {
	t.Helper()
	scope, err := f.db.Assets().AddAgentScope(f.task.ID, kind, value, "명시적 검사 범위", "manual")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := f.db.Assets().ListTaskScope(f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range stored {
		if row.Kind == scope.Kind && row.Domain == scope.Domain && row.Net == scope.Net && row.Value == scope.Value {
			return row
		}
	}
	t.Fatal("new declared scope was not persisted")
	return db.TaskScope{}
}

func (f *browserPolicyFixture) authorize(t *testing.T, raw string, ips []netip.Addr, allowed bool) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	err = f.s.authorizeBrowserTarget(t.Context(), f.task.ID, u, ips)
	if (err == nil) != allowed {
		t.Fatalf("target=%s addresses=%v allowed=%v: %v", raw, ips, allowed, err)
	}
}

func TestBrowserPolicyUsesDeclaredScopeInsteadOfDerivedAssets(t *testing.T) {
	f := newBrowserPolicyFixture(t)
	const endpoint = "https://api.fixture.example/approved"
	if _, err := f.db.Assets().UpsertEndpoint(db.UpsertEndpointReq{URL: endpoint, Method: "GET", TaskID: f.task.ID}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Assets().AddAutoScope(f.task.ID, "endpoint", "", endpoint, ""); err != nil {
		t.Fatal(err)
	}
	assets, err := f.db.Assets().QueryByTask(f.task.ID, "root_domain", 10, 0)
	if err != nil || len(assets) != 1 {
		t.Fatalf("expected the ordinary derived root asset: assets=%v err=%v", assets, err)
	}
	public := []netip.Addr{netip.MustParseAddr("192.0.2.1")}
	f.authorize(t, endpoint, public, true)
	f.authorize(t, "https://sibling.fixture.example/private", public, false)
	f.authorize(t, "https://api.fixture.example.attacker.example/approved", public, false)
	f.scope(t, "root_domain", "fixture.example")
	f.authorize(t, "https://sibling.fixture.example/private", public, true)
}

func TestBrowserPolicyRechecksBlocksAllowsAndRevocations(t *testing.T) {
	for _, policy := range []string{"global-block", "task-block", "task-domain-block", "task-allow", "scope-revocation", "deleted-task"} {
		t.Run(policy, func(t *testing.T) {
			f := newBrowserPolicyFixture(t)
			scope := f.scope(t, "subdomain", "api.fixture.example")
			const target = "https://api.fixture.example/request"
			addresses := []netip.Addr{netip.MustParseAddr("192.0.2.1")}
			f.authorize(t, target, addresses, true)
			switch policy {
			case "global-block":
				if _, err := f.db.CreateAssetInterceptRule("exact_domain", "api.fixture.example", "동적 차단", true); err != nil {
					t.Fatal(err)
				}
			case "task-block":
				if _, err := f.db.Assets().CreateTaskInterceptRule(f.task.ID, "block", "exact_url", target, "동적 차단", true); err != nil {
					t.Fatal(err)
				}
			case "task-domain-block":
				if _, err := f.db.Assets().CreateTaskInterceptRule(f.task.ID, "block", "exact_domain", "api.fixture.example", "동적 도메인 차단", true); err != nil {
					t.Fatal(err)
				}
			case "task-allow":
				if _, err := f.db.Assets().CreateTaskInterceptRule(f.task.ID, "allow", "exact_url", "https://api.fixture.example/other", "허용 URL 한정", true); err != nil {
					t.Fatal(err)
				}
			case "scope-revocation":
				if deleted, err := f.db.Assets().DeleteTaskScope(f.task.ID, scope.ID); err != nil || !deleted {
					t.Fatalf("scope revoke deleted=%v err=%v", deleted, err)
				}
			case "deleted-task":
				if _, err := f.db.Exec(`UPDATE tasks SET deleted_at=CURRENT_TIMESTAMP WHERE id=?1`, f.task.ID); err != nil {
					t.Fatal(err)
				}
			}
			f.authorize(t, target, addresses, false)
			if policy == "global-block" || policy == "task-domain-block" {
				f.authorize(t, "https://api.fixture.example./request", nil, false)
				f.authorize(t, "https://api.fixture.example./request", addresses, false)
			}
		})
	}
}

func TestBrowserPolicyDNSAddressGateAndPrivateScope(t *testing.T) {
	f := newBrowserPolicyFixture(t)
	f.scope(t, "subdomain", "api.fixture.example")
	const target = "https://api.fixture.example/request"
	f.authorize(t, target, []netip.Addr{netip.MustParseAddr("127.0.0.1")}, false)
	f.scope(t, "ip", "127.0.0.1")
	f.authorize(t, target, []netip.Addr{netip.MustParseAddr("127.0.0.1")}, true)
	if _, err := f.db.Assets().CreateTaskInterceptRule(f.task.ID, "allow", "exact_ip", "192.0.2.2", "허용 IP 한정", true); err != nil {
		t.Fatal(err)
	}
	// Pre-DNS can determine host scope and blocks, but cannot yet determine an
	// exact-IP allow rule. Every actual address must pass after resolution.
	f.authorize(t, target, nil, true)
	f.authorize(t, target, []netip.Addr{netip.MustParseAddr("192.0.2.2")}, true)
	f.authorize(t, target, []netip.Addr{netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.2")}, false)
	f.authorize(t, target, []netip.Addr{netip.MustParseAddr("192.0.2.2"), netip.MustParseAddr("192.0.2.1")}, false)
	if _, err := f.db.CreateAssetInterceptRule("exact_ip", "192.0.2.2", "DNS 후 차단", true); err != nil {
		t.Fatal(err)
	}
	f.authorize(t, target, []netip.Addr{netip.MustParseAddr("192.0.2.2")}, false)
}

func TestBrowserPolicyReadsFailClosed(t *testing.T) {
	for _, failure := range []string{"closed", "task_scope", "asset_intercept_rules", "task_intercept_rules", "company_scope"} {
		t.Run(failure, func(t *testing.T) {
			f := newBrowserPolicyFixture(t)
			f.scope(t, "subdomain", "api.fixture.example")
			if failure == "company_scope" {
				companyID, _, err := f.db.Companies().UpsertCompany("가상 검사 기업", "")
				if err != nil {
					t.Fatal(err)
				}
				f.scope(t, "company", strconv.FormatInt(companyID, 10))
			}
			if failure == "closed" {
				if err := f.db.Close(); err != nil {
					t.Fatal(err)
				}
			} else if _, err := f.db.Exec(`DROP TABLE ` + failure); err != nil {
				t.Fatal(err)
			}
			f.authorize(t, "https://api.fixture.example/request", []netip.Addr{netip.MustParseAddr("192.0.2.1")}, false)
		})
	}
}

func browserPolicyHTTPServer(t *testing.T, handler http.Handler) *httptest.Server {
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
		if !browserports.Allowed(listener.Addr().(*net.TCPAddr).Port) {
			held = append(held, listener)
			continue
		}
		h := httptest.NewUnstartedServer(handler)
		_ = h.Listener.Close()
		h.Listener = listener
		h.Start()
		t.Cleanup(h.Close)
		return h
	}
	t.Fatal("fixture could not bind an allowed browser port")
	return nil
}

func TestBrowserPolicyRevokedDuringDNSNeverDials(t *testing.T) {
	for _, change := range []string{"block", "scope", "mixed-address-allow"} {
		t.Run(change, func(t *testing.T) {
			f := newBrowserPolicyFixture(t)
			hostScope := f.scope(t, "subdomain", "api.fixture.example")
			f.scope(t, "ip", "127.0.0.1")
			var hits atomic.Int32
			target := browserPolicyHTTPServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
			targetURL, err := url.Parse(target.URL)
			if err != nil {
				t.Fatal(err)
			}
			if change == "mixed-address-allow" {
				if _, err := f.db.Assets().CreateTaskInterceptRule(f.task.ID, "allow", "exact_ip", "127.0.0.1", "두 번째 주소만 허용", true); err != nil {
					t.Fatal(err)
				}
			}
			var lookups int
			relay, err := browserrelay.New(browserrelay.Options{
				Authorize: func(ctx context.Context, u *url.URL, ips []netip.Addr) error {
					return f.s.authorizeBrowserTarget(ctx, f.task.ID, u, ips)
				},
				LookupIP: func(context.Context, string, string) ([]netip.Addr, error) {
					lookups++
					switch change {
					case "block":
						if _, err := f.db.CreateAssetInterceptRule("exact_domain", "api.fixture.example", "DNS 도중 차단", true); err != nil {
							t.Fatal(err)
						}
					case "scope":
						if deleted, err := f.db.Assets().DeleteTaskScope(f.task.ID, hostScope.ID); err != nil || !deleted {
							t.Fatalf("DNS scope revoke deleted=%v err=%v", deleted, err)
						}
					case "mixed-address-allow":
						return []netip.Addr{netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("127.0.0.1")}, nil
					}
					return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := relay.Do(t.Context(), browserrelay.Request{URL: "http://api.fixture.example:" + targetURL.Port() + "/request", Method: "GET"}); err == nil {
				t.Fatal("policy changed during DNS, but the relay still succeeded")
			}
			if lookups != 1 || hits.Load() != 0 {
				t.Fatalf("post-DNS policy failed to stop socket dial: lookups=%d hits=%d", lookups, hits.Load())
			}
		})
	}
}

func TestBrowserRelayHTTPChecksOwnerLeasePolicyAndOwnListeners(t *testing.T) {
	f := newBrowserPolicyFixture(t)
	f.scope(t, "ip", "127.0.0.1")
	var targetHits atomic.Int32
	target := browserPolicyHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		_, _ = w.Write([]byte("허용된 로컬 fixture"))
	}))
	control := browserPolicyHTTPServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("browser relay reached its control listener") }))
	controlURL, _ := url.Parse(control.URL)
	denyProxy := browserPolicyHTTPServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("browser relay reached its deny-proxy listener") }))
	denyProxyURL, _ := url.Parse(denyProxy.URL)
	lease, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	const sessionID = "0123456789abcdef0123456789abcdef"
	run := &browserRun{taskID: f.task.ID, lease: lease, verified: true}
	f.s.browser = &browserRuntime{s: f.s, control: controlURL, denyProxy: netip.MustParseAddrPort(denyProxyURL.Host), runs: map[string]*browserRun{sessionID: run}}
	app := browserPolicyHTTPServer(t, f.s.Handler())
	if err := f.s.ConfigureDesktopListener(app.Listener.Addr()); err != nil {
		t.Fatal(err)
	}
	call := func(id string, input browserrelay.Request, mutate func(*http.Request)) (int, []byte) {
		t.Helper()
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, app.URL+"/api/runtime/browser/relay/"+id, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set(desktopSessionHeader, string(f.s.desktopSession))
		if mutate != nil {
			mutate(request)
		}
		response, err := app.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		output, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, output
	}
	input := browserrelay.Request{URL: target.URL + "/request", Method: "GET"}
	status, body := call(sessionID, input, nil)
	var result browserrelay.Response
	if status != http.StatusOK || json.Unmarshal(body, &result) != nil || result.Status != http.StatusOK {
		t.Fatalf("allowed HTTP relay: status=%d body=%s", status, body)
	}
	decoded, err := base64.StdEncoding.DecodeString(result.BodyBase64)
	if err != nil || string(decoded) != "허용된 로컬 fixture" || targetHits.Load() != 1 {
		t.Fatalf("unexpected target response/hits: body=%q hits=%d err=%v", decoded, targetHits.Load(), err)
	}
	jwt, err := signJWT(f.s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		id     string
		input  browserrelay.Request
		mutate func(*http.Request)
	}{
		{name: "unknown-owner", id: "ffffffffffffffffffffffffffffffff", input: input},
		{name: "renderer-origin", id: sessionID, input: input, mutate: func(r *http.Request) { r.Header.Set("Origin", app.URL); r.Header.Set("Authorization", "Bearer "+jwt) }},
		{name: "cookie-owner", id: sessionID, input: input, mutate: func(r *http.Request) {
			r.Header.Set("Cookie", "fixture=true")
			r.Header.Set("Authorization", "Bearer "+jwt)
		}},
		{name: "missing-key", id: sessionID, input: input, mutate: func(r *http.Request) { r.Header.Del(desktopSessionHeader) }},
		{name: "own-app", id: sessionID, input: browserrelay.Request{URL: app.URL + "/api/health", Method: "GET"}},
		{name: "mapped-app", id: sessionID, input: browserrelay.Request{URL: strings.Replace(app.URL, "127.0.0.1", "[::ffff:127.0.0.1]", 1) + "/api/health", Method: "GET"}},
		{name: "own-control", id: sessionID, input: browserrelay.Request{URL: control.URL, Method: "GET"}},
		{name: "own-deny-proxy", id: sessionID, input: browserrelay.Request{URL: denyProxy.URL, Method: "GET"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, body := call(test.id, test.input, test.mutate)
			if status != http.StatusForbidden {
				t.Fatalf("unapproved HTTP relay: status=%d body=%s", status, body)
			}
		})
	}
	block, err := f.db.CreateAssetInterceptRule("exact_ip", "127.0.0.1", "실행 중 변경한 차단", true)
	if err != nil {
		t.Fatal(err)
	}
	if status, body := call(sessionID, input, nil); status != http.StatusForbidden {
		t.Fatalf("late block ignored: status=%d body=%s", status, body)
	}
	if err := f.db.DeleteAssetInterceptRule(block.ID); err != nil {
		t.Fatal(err)
	}
	cancel()
	if status, body := call(sessionID, input, nil); status != http.StatusForbidden {
		t.Fatalf("expired lease accepted: status=%d body=%s", status, body)
	}
	if targetHits.Load() != 1 {
		t.Fatalf("denied target requests escaped: hits=%d", targetHits.Load())
	}
}
