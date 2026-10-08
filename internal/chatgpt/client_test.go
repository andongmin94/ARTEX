package chatgpt

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type oauthFixture struct {
	t                                     *testing.T
	server                                *httptest.Server
	client                                *Client
	private                               *rsa.PrivateKey
	mu                                    sync.Mutex
	authorize                             url.Values
	tokenCalls, refreshCalls, revokeCalls int
	codeResponse                          func(*tokenResponse)
	refreshResponse                       func(*tokenResponse)
	tokenHook                             func()
	errorCode                             string
	revokeStatus                          int
	modelAuth                             string
	modelBody                             string
	identity                              func(*identityClaims)
	clock                                 atomic.Int64
}

func newFixture(t *testing.T) *oauthFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &oauthFixture{t: t, private: key, revokeStatus: http.StatusOK,
		modelBody: `{"models":[{"slug":"hidden","display_name":"Hidden","visibility":"hidden"},{"slug":"first","display_name":"첫 모델","visibility":"list"},{"slug":"second","display_name":"둘째 모델","visibility":"list"}]}`}
	f.clock.Store(time.Now().UTC().Truncate(time.Second).Unix())
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(f.private.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(f.private.E)).Bytes())
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []jwk{{Kty: "RSA", Kid: "fixture-key", Use: "sig", Alg: "RS256", N: n, E: e}}})
	})
	mux.HandleFunc("/token", f.token)
	mux.HandleFunc("/discovery", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": issuer, "revocation_endpoint": f.server.URL + "/revoke"})
	})
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("token_type_hint") != "refresh_token" || r.Form.Get("client_id") != "oaiapp_fixture" {
			t.Error("incorrect revocation form")
		}
		f.mu.Lock()
		f.revokeCalls++
		status := f.revokeStatus
		f.mu.Unlock()
		w.WriteHeader(status)
	})
	mux.HandleFunc("/models", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.modelAuth = r.Header.Get("Authorization")
		body := f.modelBody
		f.mu.Unlock()
		_, _ = io.WriteString(w, body)
	})
	f.server = httptest.NewServer(mux)
	c, err := newClient(t.TempDir(), f.endpoints())
	if err != nil {
		t.Fatal(err)
	}
	f.client = c
	c.now = f.now
	c.http.Timeout = 2 * time.Second
	t.Cleanup(func() { _ = c.Close(); f.server.Close() })
	return f
}

func (f *oauthFixture) endpoints() endpoints {
	return endpoints{authorize: f.server.URL + "/authorize", token: f.server.URL + "/token", jwks: f.server.URL + "/jwks", discovery: f.server.URL + "/discovery", models: f.server.URL + "/models", issuer: issuer, resource: resource}
}
func (f *oauthFixture) now() time.Time { return time.Unix(f.clock.Load(), 0).UTC() }

func (f *oauthFixture) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		f.t.Error(err)
	}
	if r.Form.Get("client_id") != "oaiapp_fixture" || r.Form.Get("resource") != resource {
		f.t.Error("incorrect token client/resource")
	}
	f.mu.Lock()
	f.tokenCalls++
	errorCode := f.errorCode
	hook := f.tokenHook
	refresh := r.Form.Get("grant_type") == "refresh_token"
	if refresh {
		f.refreshCalls++
	}
	auth := f.authorize
	mutation := f.codeResponse
	if refresh {
		mutation = f.refreshResponse
	}
	identityMutation := f.identity
	f.mu.Unlock()
	if errorCode != "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": errorCode, "error_description": "secret-access secret-refresh secret-id"})
		return
	}
	tr := &tokenResponse{AccessToken: "secret-access", RefreshToken: "secret-refresh", TokenType: "Bearer", ExpiresIn: 3600, Scope: requestedScopes}
	if refresh {
		if r.Form.Has("scope") || r.Form.Get("refresh_token") != "secret-refresh" {
			f.t.Error("incorrect rotating refresh form")
		}
		tr.AccessToken = "rotated-access"
		tr.RefreshToken = "rotated-refresh"
	} else {
		challenge := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if auth.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(challenge[:]) || r.Form.Get("redirect_uri") != auth.Get("redirect_uri") {
			f.t.Error("PKCE or callback URI mismatch")
		}
		claims := &identityClaims{RegisteredClaims: jwt.RegisteredClaims{Issuer: issuer, Subject: "verified-subject", Audience: jwt.ClaimStrings{"oaiapp_fixture"}, ExpiresAt: jwt.NewNumericDate(f.now().Add(time.Hour)), IssuedAt: jwt.NewNumericDate(f.now())}, Nonce: auth.Get("nonce"), Email: "fixture@example.invalid", Name: "검사 계정"}
		if identityMutation != nil {
			identityMutation(claims)
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		token.Header["kid"] = "fixture-key"
		signed, err := token.SignedString(f.private)
		if err != nil {
			f.t.Error(err)
		}
		tr.IDToken = signed
	}
	if mutation != nil {
		mutation(tr)
	}
	if hook != nil {
		hook()
	}
	_ = json.NewEncoder(w).Encode(tr)
}

func (f *oauthFixture) begin() url.Values {
	f.t.Helper()
	link, err := f.client.Begin(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	u, err := url.Parse(link)
	if err != nil {
		f.t.Fatal(err)
	}
	q := u.Query()
	f.mu.Lock()
	f.authorize = q
	f.mu.Unlock()
	return q
}

func (f *oauthFixture) callback(q url.Values, modify func(url.Values)) int {
	f.t.Helper()
	params := url.Values{"code": {"fixture-code"}, "state": {q.Get("state")}, "client_id": {"oaiapp_fixture"}}
	if modify != nil {
		modify(params)
	}
	resp, err := http.Get(q.Get("redirect_uri") + "?" + params.Encode())
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func (f *oauthFixture) signIn() {
	f.t.Helper()
	if status := f.callback(f.begin(), nil); status != http.StatusOK {
		f.t.Fatalf("callback status %d, public status %+v", status, f.client.Status())
	}
}

func stored(t *testing.T, c *Client) *credentialRecord {
	t.Helper()
	r, err := c.store.load()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func expireSoon(t *testing.T, c *Client) {
	t.Helper()
	r := stored(t, c)
	r.ExpiresAt = c.now().Add(30 * time.Second)
	if err := c.store.save(r); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrationTokenValidationAndStorage(t *testing.T) {
	f := newFixture(t)
	q := f.begin()
	for k, v := range map[string]string{"client_id": dynamicClientID, "agent_name_hint": "ARTEX", "code_challenge_method": "S256", "resource": resource, "response_type": "code", "scope": requestedScopes} {
		if q.Get(k) != v {
			t.Fatalf("authorization %s mismatch", k)
		}
	}
	if len(q.Get("state")) < 43 || len(q.Get("nonce")) < 43 || q.Has("id_token_hint") || q.Has("login_hint") {
		t.Fatal("unsafe or weak authorization parameters")
	}
	u, _ := url.Parse(q.Get("redirect_uri"))
	if u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "/auth/callback" {
		t.Fatal("incorrect loopback callback")
	}
	connection, err := net.DialTimeout("tcp", u.Host, time.Second)
	if err != nil {
		t.Fatal("listener was not started before authorization URL returned")
	}
	connection.Close()
	if f.callback(q, nil) != http.StatusOK {
		t.Fatal("sign-in failed")
	}
	token, err := f.client.AccessToken(context.Background())
	if err != nil || token != "secret-access" {
		t.Fatalf("token inaccessible: %v", err)
	}
	s := f.client.Status()
	if !s.Connected || !s.Sharing || s.Pending || s.Email != "fixture@example.invalid" {
		t.Fatalf("status %+v", s)
	}
	r := stored(t, f.client)
	if r.ClientID != "oaiapp_fixture" || r.Subject != "verified-subject" || r.Issuer != issuer || !strings.HasPrefix(r.HostID, "urn:uuid:") {
		t.Fatal("registration was not bound to verified identity")
	}
	public, _ := json.Marshal(s)
	for _, secret := range []string{r.AccessToken, r.RefreshToken, r.IDToken, r.HostID, r.ClientID, r.Subject, q.Get("nonce"), q.Get("state")} {
		if strings.Contains(string(public), secret) {
			t.Fatal("public status leaked private values")
		}
	}
	encoded, err := os.ReadFile(f.client.store.path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		for _, secret := range []string{r.AccessToken, r.RefreshToken, r.IDToken} {
			if strings.Contains(string(encoded), secret) {
				t.Fatal("DPAPI storage exposed token plaintext")
			}
		}
	} else {
		info, err := os.Stat(f.client.store.path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("credentials are not private")
		}
	}
	c2, err := newClient(strings.TrimSuffix(f.client.store.dir, string(os.PathSeparator)+"chatgpt"), f.endpoints())
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if !c2.Status().Connected || stored(t, c2).HostID != r.HostID {
		t.Fatal("protected credentials did not survive restart")
	}
	models, err := f.client.Models(context.Background())
	if err != nil || len(models) != 2 || models[0].Slug != "first" || models[1].DisplayName != "둘째 모델" {
		t.Fatalf("models %+v: %v", models, err)
	}
	f.mu.Lock()
	modelAuth := f.modelAuth
	f.mu.Unlock()
	if modelAuth != "Bearer secret-access" {
		t.Fatal("model request did not use this app's access token")
	}
}

func TestInvalidCallbackStateCannotExchangeCode(t *testing.T) {
	f := newFixture(t)
	q := f.begin()
	for _, modify := range []func(url.Values){func(v url.Values) { v.Del("state") }, func(v url.Values) { v.Set("state", "wrong") }, func(v url.Values) { v.Add("state", q.Get("state")) }} {
		if f.callback(q, modify) != http.StatusBadRequest {
			t.Fatal("invalid state accepted")
		}
	}
	f.mu.Lock()
	calls := f.tokenCalls
	f.mu.Unlock()
	if calls != 0 {
		t.Fatal("invalid callback exchanged code")
	}
	f.client.mu.Lock()
	pending := f.client.pending
	f.client.mu.Unlock()
	r := httptest.NewRequest(http.MethodGet, q.Get("redirect_uri")+"?state="+url.QueryEscape(q.Get("state")), nil)
	r.Host = "attacker.invalid"
	r.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	f.client.callback(pending, w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("callback accepted hostile Host")
	}
	r.Host = strings.TrimPrefix(strings.TrimSuffix(q.Get("redirect_uri"), "/auth/callback"), "http://")
	r.Header.Set("Origin", "https://attacker.invalid")
	w = httptest.NewRecorder()
	f.client.callback(pending, w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("callback accepted hostile Origin")
	}
	if f.callback(q, nil) != http.StatusOK {
		t.Fatal("valid callback failed after invalid state")
	}
}

func TestCallbackReplayAndCancellation(t *testing.T) {
	f := newFixture(t)
	q := f.begin()
	entered := make(chan struct{})
	release := make(chan struct{})
	f.mu.Lock()
	f.tokenHook = func() { close(entered); <-release }
	f.mu.Unlock()
	result := make(chan int, 1)
	go func() { result <- f.callback(q, nil) }()
	<-entered
	if f.callback(q, nil) != http.StatusConflict {
		t.Fatal("callback was consumed twice")
	}
	close(release)
	if <-result != http.StatusOK {
		t.Fatal("original callback failed")
	}
	f.mu.Lock()
	calls := f.tokenCalls
	f.mu.Unlock()
	if calls != 1 {
		t.Fatal("replay exchanged code")
	}
	q = f.begin()
	f.client.mu.Lock()
	p := f.client.pending
	f.client.mu.Unlock()
	f.client.Cancel()
	select {
	case <-p.done:
	case <-time.After(time.Second):
		t.Fatal("cancel left listener open")
	}
	if f.client.Status().Pending {
		t.Fatal("cancel left pending status")
	}
	u, _ := url.Parse(q.Get("redirect_uri"))
	conn, err := net.DialTimeout("tcp", u.Host, 100*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Fatal("cancelled callback still listening")
	}
}

func TestIdentityValidationFailsClosed(t *testing.T) {
	cases := map[string]func(*identityClaims){
		"issuer":            func(c *identityClaims) { c.Issuer = "https://wrong.invalid" },
		"audience":          func(c *identityClaims) { c.Audience = jwt.ClaimStrings{"wrong-client"} },
		"expiry":            func(c *identityClaims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute)) },
		"missing_expiry":    func(c *identityClaims) { c.ExpiresAt = nil },
		"missing_issued_at": func(c *identityClaims) { c.IssuedAt = nil },
		"subject":           func(c *identityClaims) { c.Subject = "" },
		"nonce":             func(c *identityClaims) { c.Nonce = "wrong-nonce" },
	}
	for name, mutation := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.identity = mutation
			if f.callback(f.begin(), nil) != http.StatusBadRequest {
				t.Fatal("invalid identity accepted")
			}
			if f.client.Status().Connected {
				t.Fatal("invalid identity activated account")
			}
			if token, err := f.client.AccessToken(context.Background()); err == nil || token != "" {
				t.Fatal("invalid identity supplied token")
			}
		})
	}
	t.Run("signature", func(t *testing.T) {
		f := newFixture(t)
		f.codeResponse = func(r *tokenResponse) {
			parts := strings.Split(r.IDToken, ".")
			sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
			sig[0] ^= 1
			parts[2] = base64.RawURLEncoding.EncodeToString(sig)
			r.IDToken = strings.Join(parts, ".")
		}
		if f.callback(f.begin(), nil) != http.StatusBadRequest || f.client.Status().Connected {
			t.Fatal("invalid signature accepted")
		}
	})
}

func TestMissingConsentRetainsIdentityAndBlocksRequests(t *testing.T) {
	f := newFixture(t)
	f.codeResponse = func(r *tokenResponse) {
		r.Scope = "openid email profile"
		r.AccessToken = ""
		r.RefreshToken = ""
		r.TokenType = ""
		r.ExpiresIn = 0
	}
	f.signIn()
	s := f.client.Status()
	if !s.Connected || s.Sharing {
		t.Fatalf("consent status %+v", s)
	}
	if token, err := f.client.AccessToken(context.Background()); token != "" || !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("missing consent used token: %v", err)
	}
	if _, err := f.client.Models(context.Background()); !errors.Is(err, ErrConsentRequired) {
		t.Fatal("missing consent requested models")
	}
	q := f.begin()
	if q.Get("client_id") != "oaiapp_fixture" || q.Get("prompt") != "consent" || q.Has("agent_name_hint") || q.Has("id_token_hint") {
		t.Fatal("consent reconnection did not reuse registration safely")
	}
}

func TestIssuedClientIDAndReturningIdentityPreserved(t *testing.T) {
	t.Run("missing_first_issued_id", func(t *testing.T) {
		f := newFixture(t)
		q := f.begin()
		if f.callback(q, func(v url.Values) { v.Del("client_id") }) != http.StatusBadRequest {
			t.Fatal("registration without issued ID succeeded")
		}
		if stored(t, f.client).ClientID != "" {
			t.Fatal("missing ID persisted")
		}
	})
	t.Run("dynamic_id_cannot_be_issued_id", func(t *testing.T) {
		f := newFixture(t)
		if f.callback(f.begin(), func(v url.Values) { v.Set("client_id", dynamicClientID) }) != http.StatusBadRequest {
			t.Fatal("dynamic registration entrypoint was saved")
		}
	})
	t.Run("different_returned_id", func(t *testing.T) {
		f := newFixture(t)
		f.signIn()
		before := stored(t, f.client)
		q := f.begin()
		if f.callback(q, func(v url.Values) { v.Set("client_id", "oaiapp_other") }) != http.StatusBadRequest {
			t.Fatal("returning callback replaced issued ID")
		}
		after := stored(t, f.client)
		if before.ClientID != after.ClientID || before.AccessToken != after.AccessToken {
			t.Fatal("mismatched callback replaced credentials")
		}
	})
	t.Run("different_subject", func(t *testing.T) {
		f := newFixture(t)
		f.signIn()
		before := stored(t, f.client)
		f.identity = func(c *identityClaims) { c.Subject = "different-subject" }
		q := f.begin()
		if f.callback(q, func(v url.Values) { v.Del("client_id") }) != http.StatusBadRequest {
			t.Fatal("returning identity mismatch succeeded")
		}
		after := stored(t, f.client)
		if before.Subject != after.Subject || before.IDToken != after.IDToken {
			t.Fatal("wrong identity replaced credentials")
		}
	})
	t.Run("code_failure_retains_issued_id", func(t *testing.T) {
		f := newFixture(t)
		f.errorCode = "invalid_grant"
		if f.callback(f.begin(), nil) != http.StatusBadRequest {
			t.Fatal("invalid code accepted")
		}
		r := stored(t, f.client)
		if r.ClientID != "oaiapp_fixture" || r.AccessToken != "" {
			t.Fatal("registration was lost or activated")
		}
		q := f.begin()
		if q.Get("client_id") != r.ClientID || q.Has("agent_name_hint") {
			t.Fatal("retry dynamically registered again")
		}
	})
}

func TestRefreshRotatesAtomicallyAcrossClients(t *testing.T) {
	f := newFixture(t)
	f.signIn()
	expireSoon(t, f.client)
	c2, err := newClient(strings.TrimSuffix(f.client.store.dir, string(os.PathSeparator)+"chatgpt"), f.endpoints())
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	c2.now = f.now
	var wg sync.WaitGroup
	results := make(chan string, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			c := f.client
			if index%2 != 0 {
				c = c2
			}
			token, err := c.AccessToken(context.Background())
			results <- token
			errs <- err
		}(i)
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for token := range results {
		if token != "rotated-access" {
			t.Fatal("uncommitted or stale access token returned")
		}
	}
	f.mu.Lock()
	calls := f.refreshCalls
	f.mu.Unlock()
	if calls != 1 {
		t.Fatalf("rotating session was refreshed %d times", calls)
	}
	r := stored(t, f.client)
	if r.RefreshToken != "rotated-refresh" || r.AccessToken != "rotated-access" {
		t.Fatal("rotation not persisted")
	}
}

func TestRefreshFailureDoesNotFallback(t *testing.T) {
	for _, code := range []string{"temporarily_unavailable", "invalid_grant", "refresh_token_reused"} {
		t.Run(code, func(t *testing.T) {
			f := newFixture(t)
			f.signIn()
			expireSoon(t, f.client)
			f.errorCode = code
			token, err := f.client.AccessToken(context.Background())
			if err == nil || token != "" {
				t.Fatal("refresh failure fell back to an old token")
			}
			r := stored(t, f.client)
			if code == "temporarily_unavailable" {
				if r.RefreshToken != "secret-refresh" || r.AccessToken != "secret-access" {
					t.Fatal("temporary error erased credentials")
				}
			} else {
				if !errors.Is(err, ErrReauthenticationRequired) || r.AccessToken != "" || r.RefreshToken != "" || r.ClientID != "oaiapp_fixture" || r.Subject != "verified-subject" {
					t.Fatal("terminal error did not clear only unusable tokens")
				}
			}
			if strings.Contains(f.client.Status().LastError, "secret-") {
				t.Fatal("OAuth diagnostic leaked secrets")
			}
		})
	}
	t.Run("missing_rotation", func(t *testing.T) {
		f := newFixture(t)
		f.signIn()
		expireSoon(t, f.client)
		f.refreshResponse = func(r *tokenResponse) { r.RefreshToken = "" }
		if token, err := f.client.AccessToken(context.Background()); err == nil || token != "" {
			t.Fatal("incomplete rotation accepted")
		}
		if stored(t, f.client).RefreshToken != "secret-refresh" {
			t.Fatal("failed rotation replaced stored credentials")
		}
	})
	t.Run("storage_failure", func(t *testing.T) {
		f := newFixture(t)
		f.signIn()
		expireSoon(t, f.client)
		f.tokenHook = func() {
			if err := os.Remove(f.client.store.path); err != nil {
				t.Error(err)
			}
			if err := os.Mkdir(f.client.store.path, 0700); err != nil {
				t.Error(err)
			}
		}
		if token, err := f.client.AccessToken(context.Background()); err == nil || token != "" {
			t.Fatal("uncommitted rotation was returned")
		}
	})
}

func TestLogoutClearsCredentialsAndRetainsRegistration(t *testing.T) {
	for _, remoteStatus := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(remoteStatus), func(t *testing.T) {
			f := newFixture(t)
			f.signIn()
			before := stored(t, f.client)
			f.revokeStatus = remoteStatus
			err := f.client.Logout()
			if remoteStatus == http.StatusOK && err != nil {
				t.Fatal(err)
			}
			if remoteStatus != http.StatusOK && !errors.Is(err, ErrRevocationUnconfirmed) {
				t.Fatalf("unconfirmed revocation not surfaced: %v", err)
			}
			r := stored(t, f.client)
			if r.AccessToken != "" || r.RefreshToken != "" || r.IDToken != "" || r.HostID != before.HostID || r.ClientID != before.ClientID || r.Subject != before.Subject {
				t.Fatal("logout lost mapping or kept tokens")
			}
			if f.client.Status().Connected || f.client.Status().Sharing {
				t.Fatal("logout retained active session")
			}
			q := f.begin()
			if q.Get("client_id") != before.ClientID || q.Get("ext_agent_host_id") != before.HostID || q.Has("agent_name_hint") || q.Has("id_token_hint") {
				t.Fatal("logout reconnect did not preserve registration")
			}
		})
	}
}

func TestCorruptStorageFailsClosedWithoutOverwrite(t *testing.T) {
	f := newFixture(t)
	f.signIn()
	path := f.client.store.path
	corrupt := []byte("not-a-credential-record")
	if err := os.WriteFile(path, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	if c, err := newClient(strings.TrimSuffix(f.client.store.dir, string(os.PathSeparator)+"chatgpt"), f.endpoints()); err == nil {
		c.Close()
		t.Fatal("corrupt storage accepted")
	}
	if token, err := f.client.AccessToken(context.Background()); err == nil || token != "" {
		t.Fatal("cached credentials bypassed corruption")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(corrupt) {
		t.Fatal("corrupt data was silently replaced")
	}
}

func TestCallbackTimeoutContextAndClose(t *testing.T) {
	for _, mode := range []string{"timeout", "context", "close"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "timeout" {
				f.client.attemptTimeout = 20 * time.Millisecond
			}
			if _, err := f.client.Begin(ctx); err != nil {
				t.Fatal(err)
			}
			f.client.mu.Lock()
			p := f.client.pending
			f.client.mu.Unlock()
			switch mode {
			case "context":
				cancel()
			case "close":
				if err := f.client.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-p.done:
			case <-time.After(time.Second):
				t.Fatal("callback listener survived lifecycle end")
			}
			deadline := time.Now().Add(time.Second)
			for f.client.Status().Pending && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if f.client.Status().Pending {
				t.Fatal("listener ended but status stayed pending")
			}
			if mode == "close" {
				if _, err := f.client.Begin(context.Background()); !errors.Is(err, ErrClosed) {
					t.Fatal("closed client started sign-in")
				}
			}
		})
	}
}

func TestProductionEndpointsCannotBeOverridden(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "http://attacker.invalid")
	t.Setenv("OPENAI_API_KEY", "unrelated-api-key")
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.endpoints.authorize != issuer+"/api/accounts/authorize" || c.endpoints.token != issuer+"/api/accounts/oauth/token" || c.endpoints.models != resource+"/models" {
		t.Fatal("production endpoints were overridden")
	}
	if c.http.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("OAuth inherited host proxy configuration")
	}
	if _, err = c.AccessToken(context.Background()); !errors.Is(err, ErrNotConnected) {
		t.Fatal("environment/API key fallback occurred")
	}
}

func TestRefreshScopeRemovalBlocksRequests(t *testing.T) {
	f := newFixture(t)
	f.signIn()
	expireSoon(t, f.client)
	f.refreshResponse = func(r *tokenResponse) { r.Scope = "" }
	if token, err := f.client.AccessToken(context.Background()); token != "" || !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("empty granted scopes retained permission: %v", err)
	}
	if f.client.Status().Sharing || !f.client.Status().Connected {
		t.Fatal("scope removal status did not retain identity while disabling plan usage")
	}
}

func TestRefreshScopeOmissionRetainsGrant(t *testing.T) {
	r := &credentialRecord{Scopes: strings.Fields(requestedScopes)}
	var response tokenResponse
	if err := json.Unmarshal([]byte(`{"access_token":"new","refresh_token":"rotated","expires_in":3600,"token_type":"Bearer"}`), &response); err != nil {
		t.Fatal(err)
	}
	if err := applyTokenResponse(r, &response, time.Now(), true); err != nil || !sharing(r.Scopes) {
		t.Fatalf("omitted scope did not retain the authorized grant: %v", err)
	}
}

func TestCredentialRedirectsAreNeverFollowed(t *testing.T) {
	for _, operation := range []string{"token", "jwks", "models"} {
		t.Run(operation, func(t *testing.T) {
			f := newFixture(t)
			if operation == "models" {
				f.signIn()
			}
			var destinationCalls atomic.Int64
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationCalls.Add(1); w.WriteHeader(http.StatusOK) }))
			defer destination.Close()
			redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
			}))
			defer redirect.Close()
			switch operation {
			case "token":
				f.client.endpoints.token = redirect.URL
			case "jwks":
				f.client.endpoints.jwks = redirect.URL
			case "models":
				f.client.endpoints.models = redirect.URL
			}
			if operation == "models" {
				if _, err := f.client.Models(context.Background()); err == nil {
					t.Fatal("redirecting model request succeeded")
				}
			} else if f.callback(f.begin(), nil) != http.StatusBadRequest {
				t.Fatal("redirecting OAuth request succeeded")
			}
			if destinationCalls.Load() != 0 {
				t.Fatal("private OAuth/API request followed a redirect")
			}
		})
	}
}

func TestCloseCancelsActiveRefresh(t *testing.T) {
	f := newFixture(t)
	f.signIn()
	expireSoon(t, f.client)
	entered, release := make(chan struct{}), make(chan struct{})
	f.tokenHook = func() { close(entered); <-release }
	result := make(chan error, 1)
	go func() {
		token, err := f.client.AccessToken(context.Background())
		if token != "" {
			t.Error("closed refresh returned token")
		}
		result <- err
	}()
	<-entered
	closed := make(chan struct{})
	go func() { _ = f.client.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("Close did not cancel active network request")
	}
	close(release)
	if err := <-result; err == nil {
		t.Fatal("cancelled refresh succeeded")
	}
}

func TestCredentialLockAcrossProcesses(t *testing.T) {
	f := newFixture(t)
	unlock, err := f.client.store.lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCredentialLockChild$")
	cmd.Env = append(os.Environ(), "ARTEX_CHATGPT_LOCK_TEST_HOME="+strings.TrimSuffix(f.client.store.dir, string(os.PathSeparator)+"chatgpt"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child process bypassed credential lock: %v; %s", err, output)
	}
}

func TestCredentialLockChild(t *testing.T) {
	home := os.Getenv("ARTEX_CHATGPT_LOCK_TEST_HOME")
	if home == "" {
		return
	}
	s, err := newCredentialStore(home)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	unlock, err := s.lock(ctx)
	if err == nil {
		unlock()
		t.Fatal("parent's exclusive credential lock was ignored")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected lock failure: %v", err)
	}
}
