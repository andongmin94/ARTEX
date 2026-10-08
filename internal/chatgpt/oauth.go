package chatgpt

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type attempt struct {
	ctx                                                         context.Context
	cancel                                                      context.CancelFunc
	server                                                      *http.Server
	done                                                        chan struct{}
	state, nonce, verifier, redirect, clientID, subject, hostID string
	consumed                                                    bool
}

func randomValue(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Begin starts its listener before returning the authorization URL. The caller
// opens that URL in the system browser. ctx controls the whole attempt: pass
// the application lifetime context rather than a short-lived HTTP request.
// No token hints are put in the URL; only public OAuth parameters are returned.
func (c *Client) Begin(ctx context.Context) (string, error) {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	if err := c.checkOpen(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	c.Cancel()
	unlock, err := c.store.lock(ctx)
	if err != nil {
		return "", errStorage
	}
	r, err := c.store.load()
	unlock()
	if err != nil {
		c.setError(errStorage)
		return "", errStorage
	}
	c.setRecord(r)
	state, err := randomValue(32)
	if err != nil {
		return "", errUnavailable
	}
	nonce, err := randomValue(32)
	if err != nil {
		return "", errUnavailable
	}
	verifier, err := randomValue(64)
	if err != nil {
		return "", errUnavailable
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", errUnavailable
	}
	attemptCtx, cancel := context.WithTimeout(ctx, c.attemptTimeout)
	p := &attempt{ctx: attemptCtx, cancel: cancel, done: make(chan struct{}), state: state, nonce: nonce, verifier: verifier,
		redirect: "http://" + listener.Addr().String() + "/auth/callback", clientID: r.ClientID, subject: r.Subject, hostID: r.HostID}
	p.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { c.callback(p, w, req) }), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8192}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()
		listener.Close()
		return "", ErrClosed
	}
	c.pending = p
	c.status.Pending = true
	c.status.LastError = ""
	c.mu.Unlock()
	go func() { _ = p.server.Serve(listener); close(p.done) }()
	go func() {
		select {
		case <-p.ctx.Done():
			_ = p.server.Close()
			c.finish(p, errors.New("chatgpt_sign_in_expired_or_cancelled"))
		case <-p.done:
			p.cancel()
		}
	}()
	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {r.ClientID}, "ext_agent_host_id": {r.HostID}, "response_type": {"code"}, "redirect_uri": {p.redirect},
		"scope": {requestedScopes}, "resource": {c.endpoints.resource}, "state": {state}, "nonce": {nonce}, "code_challenge_method": {"S256"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}}
	if r.ClientID == "" {
		q.Set("client_id", dynamicClientID)
		q.Set("agent_name_hint", "ARTEX")
	}
	if r.IDToken != "" && !sharing(r.Scopes) {
		q.Set("prompt", "consent")
	}
	if err = p.ctx.Err(); err != nil {
		c.Cancel()
		return "", err
	}
	return c.endpoints.authorize + "?" + q.Encode(), nil
}

func (c *Client) Cancel() {
	c.mu.Lock()
	p := c.pending
	c.pending = nil
	c.status.Pending = false
	c.mu.Unlock()
	if p != nil {
		p.cancel()
		_ = p.server.Close()
	}
}

func (c *Client) finish(p *attempt, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending != p {
		return
	}
	c.pending = nil
	c.status.Pending = false
	c.status.LastError = ""
	if err != nil {
		c.status.LastError = err.Error()
	}
}

func (c *Client) callback(p *attempt, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	expected, _ := url.Parse(p.redirect)
	remote, _, remoteErr := net.SplitHostPort(r.RemoteAddr)
	if r.Method != http.MethodGet || r.URL.Path != "/auth/callback" || r.Host != expected.Host || remoteErr != nil || remote != "127.0.0.1" {
		http.Error(w, "요청을 허용할 수 없습니다.", http.StatusForbidden)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != c.endpoints.issuer {
		http.Error(w, "요청 출처를 허용할 수 없습니다.", http.StatusForbidden)
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(r.URL.RawQuery) > 16384 || len(q["state"]) != 1 || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(p.state)) != 1 {
		http.Error(w, "로그인 상태가 일치하지 않습니다.", http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	if c.pending != p || p.consumed || p.ctx.Err() != nil {
		c.mu.Unlock()
		http.Error(w, "만료되거나 처리된 로그인입니다.", http.StatusConflict)
		return
	}
	p.consumed = true
	c.mu.Unlock()
	defer func() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = p.server.Shutdown(ctx)
			p.cancel()
		}()
	}()
	if q.Get("error") != "" {
		c.finish(p, errors.New("chatgpt_sign_in_declined"))
		http.Error(w, "로그인이 취소되었습니다.", http.StatusBadRequest)
		return
	}
	if len(q["code"]) != 1 || q.Get("code") == "" || len(q["client_id"]) > 1 {
		c.finish(p, errInvalidResponse)
		http.Error(w, "로그인 응답을 확인할 수 없습니다.", http.StatusBadRequest)
		return
	}
	clientID := p.clientID
	if clientID == "" {
		clientID = q.Get("client_id")
	} else if supplied := q.Get("client_id"); supplied != "" && supplied != clientID {
		c.finish(p, errInvalidIdentity)
		http.Error(w, "계정 등록 정보가 일치하지 않습니다.", http.StatusBadRequest)
		return
	}
	if clientID == "" || clientID == dynamicClientID || len(clientID) > 1024 || strings.ContainsAny(clientID, " \t\r\n") {
		c.finish(p, errInvalidResponse)
		http.Error(w, "계정 등록이 완료되지 않았습니다.", http.StatusBadRequest)
		return
	}
	err = c.complete(p, q.Get("code"), clientID)
	c.finish(p, err)
	if err != nil {
		http.Error(w, "ChatGPT 연결을 완료하지 못했습니다. ARTEX에서 상태를 확인해 주세요.", http.StatusBadRequest)
		return
	}
	_, _ = io.WriteString(w, "ChatGPT 연결이 완료되었습니다. ARTEX로 돌아가세요.")
}

func (c *Client) complete(p *attempt, code, clientID string) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	if err := c.checkOpen(); err != nil {
		return err
	}
	unlock, err := c.store.lock(p.ctx)
	if err != nil {
		return errStorage
	}
	defer unlock()
	r, err := c.store.load()
	if err != nil {
		return errStorage
	}
	if r.HostID != p.hostID || r.ClientID != p.clientID || r.Subject != p.subject {
		return errInvalidIdentity
	}
	if r.ClientID == "" {
		r.ClientID = clientID
		if err = p.ctx.Err(); err != nil {
			return errors.New("chatgpt_sign_in_cancelled")
		}
		if err = c.store.save(r); err != nil {
			return errStorage
		}
	}
	tr, err := c.tokenRequest(p.ctx, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "code_verifier": {p.verifier}, "redirect_uri": {p.redirect}, "resource": {c.endpoints.resource}})
	if err != nil {
		return err
	}
	identity, err := c.verifyIdentity(p.ctx, tr.IDToken, clientID, p.nonce, true)
	if err != nil {
		return err
	}
	if p.subject != "" && identity.Subject != p.subject {
		return errInvalidIdentity
	}
	if tr.ClientID != "" && tr.ClientID != clientID {
		return errInvalidIdentity
	}
	r.clearTokens()
	r.Issuer = c.endpoints.issuer
	r.Subject = identity.Subject
	r.Email = identity.Email
	r.Name = identity.Name
	r.Nonce = p.nonce
	if err = applyTokenResponse(r, tr, c.now(), false); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending != p || c.closed || p.ctx.Err() != nil {
		return errors.New("chatgpt_sign_in_cancelled")
	}
	if err = c.store.save(r); err != nil {
		return errStorage
	}
	c.status.Connected = true
	c.status.Sharing = sharing(r.Scopes)
	c.status.Email = r.Email
	c.status.Name = r.Name
	c.status.ExpiresAt = nil
	if r.AccessToken != "" {
		expiry := r.ExpiresAt
		c.status.ExpiresAt = &expiry
	}
	return nil
}

type tokenResponse struct {
	AccessToken       string          `json:"access_token"`
	RefreshToken      string          `json:"refresh_token"`
	IDToken           string          `json:"id_token"`
	ClientID          string          `json:"client_id"`
	TokenType         string          `json:"token_type"`
	ExpiresIn         int64           `json:"expires_in"`
	Scope             string          `json:"scope"`
	EarliestRefreshAt json.RawMessage `json:"earliest_refresh_at"`
	scopePresent      bool
}

func (tr *tokenResponse) UnmarshalJSON(data []byte) error {
	type response tokenResponse
	var parsed response
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*tr = tokenResponse(parsed)
	_, tr.scopePresent = fields["scope"]
	return nil
}

type oauthFailure struct{ code string }

func (e *oauthFailure) Error() string { return "chatgpt_oauth_request_failed" }

func (c *Client) tokenRequest(ctx context.Context, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoints.token, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errUnavailable
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var body struct {
			Error json.RawMessage `json:"error"`
		}
		var code string
		if readJSON(resp.Body, &body) == nil {
			if json.Unmarshal(body.Error, &code) != nil {
				var nested struct {
					Code string `json:"code"`
				}
				if json.Unmarshal(body.Error, &nested) == nil {
					code = nested.Code
				}
			}
		}
		return nil, &oauthFailure{code: code}
	}
	var result tokenResponse
	if readJSON(resp.Body, &result) != nil {
		return nil, errInvalidResponse
	}
	return &result, nil
}

func applyTokenResponse(r *credentialRecord, tr *tokenResponse, now time.Time, refresh bool) error {
	if tr.AccessToken != "" {
		if !strings.EqualFold(tr.TokenType, "Bearer") || tr.ExpiresIn <= 0 || tr.ExpiresIn > 86400*7 || strings.ContainsAny(tr.AccessToken, "\r\n") {
			return errInvalidResponse
		}
		r.AccessToken = tr.AccessToken
		r.TokenType = "Bearer"
		r.ExpiresAt = now.Add(time.Duration(tr.ExpiresIn) * time.Second)
	} else if refresh || sharing(strings.Fields(tr.Scope)) {
		return errInvalidResponse
	}
	if refresh && tr.RefreshToken == "" {
		return errInvalidResponse
	}
	if !refresh && tr.AccessToken != "" && tr.RefreshToken == "" {
		for _, scope := range strings.Fields(tr.Scope) {
			if scope == "offline_access" {
				return errInvalidResponse
			}
		}
	}
	r.RefreshToken = tr.RefreshToken
	if tr.IDToken != "" {
		r.IDToken = tr.IDToken
	}
	if !refresh || tr.scopePresent || tr.Scope != "" {
		r.Scopes = strings.Fields(tr.Scope)
	}
	r.EarliestRefreshAt = tr.EarliestRefreshAt
	return nil
}

func (c *Client) refresh(ctx context.Context, r *credentialRecord) (*credentialRecord, error) {
	tr, err := c.tokenRequest(ctx, url.Values{"grant_type": {"refresh_token"}, "client_id": {r.ClientID}, "refresh_token": {r.RefreshToken}, "resource": {c.endpoints.resource}})
	if err != nil {
		var failure *oauthFailure
		if errors.As(err, &failure) {
			switch failure.code {
			case "invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused":
				r.clearTokens()
				if saveErr := c.store.save(r); saveErr != nil {
					return nil, errStorage
				}
				c.setRecord(r)
				return nil, ErrReauthenticationRequired
			}
		}
		return nil, errUnavailable
	}
	if tr.ClientID != "" && tr.ClientID != r.ClientID {
		return nil, errInvalidIdentity
	}
	if tr.IDToken != "" {
		identity, err := c.verifyIdentity(ctx, tr.IDToken, r.ClientID, r.Nonce, false)
		if err != nil {
			return nil, err
		}
		if identity.Subject != r.Subject {
			return nil, errInvalidIdentity
		}
	}
	updated := *r
	if err = applyTokenResponse(&updated, tr, c.now(), true); err != nil {
		return nil, err
	}
	return &updated, nil
}

// Logout retains the host and verified account/client registration while
// clearing credentials even if remote revocation cannot be confirmed.
func (c *Client) Logout() error {
	c.Cancel()
	c.opMu.Lock()
	defer c.opMu.Unlock()
	if err := c.checkOpen(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(c.lifetime, 8*time.Second)
	defer cancel()
	unlock, err := c.store.lock(ctx)
	if err != nil {
		return errStorage
	}
	defer unlock()
	r, err := c.store.load()
	if err != nil {
		return errStorage
	}
	confirmed := r.RefreshToken == "" || c.revoke(ctx, r) == nil
	r.clearTokens()
	if err = c.store.save(r); err != nil {
		c.setError(errStorage)
		return errStorage
	}
	c.setRecord(r)
	c.setError(nil)
	if !confirmed {
		c.setError(ErrRevocationUnconfirmed)
		return ErrRevocationUnconfirmed
	}
	return nil
}

func (c *Client) revoke(ctx context.Context, r *credentialRecord) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoints.discovery, nil)
	if err != nil {
		return errUnavailable
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return errUnavailable
	}
	var discovery struct {
		Issuer             string `json:"issuer"`
		RevocationEndpoint string `json:"revocation_endpoint"`
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return errUnavailable
	}
	err = readJSON(resp.Body, &discovery)
	resp.Body.Close()
	if err != nil || discovery.Issuer != c.endpoints.issuer {
		return errUnavailable
	}
	u, err := url.Parse(discovery.RevocationEndpoint)
	trusted, _ := url.Parse(c.endpoints.token)
	if err != nil || u.Scheme != trusted.Scheme || u.Host != trusted.Host || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errUnavailable
	}
	form := url.Values{"token": {r.RefreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {r.ClientID}}
	for attempt := 0; attempt < 2; attempt++ {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(form.Encode()))
		if err != nil {
			return errUnavailable
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err = c.http.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			if resp.StatusCode < 500 {
				return errUnavailable
			}
		}
		if attempt == 0 {
			select {
			case <-ctx.Done():
				return errUnavailable
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	return errUnavailable
}
