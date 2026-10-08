// Package chatgpt implements the official public-client Sign in with ChatGPT
// flow. Credentials belong only to the supplied ARTEX home; this package never
// imports another application's credentials or falls back to an API key.
package chatgpt

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

var (
	ErrNotConnected             = errors.New("chatgpt_not_connected")
	ErrConsentRequired          = errors.New("chatgpt_plan_consent_required")
	ErrReauthenticationRequired = errors.New("chatgpt_reauthentication_required")
	ErrRevocationUnconfirmed    = errors.New("chatgpt_remote_revocation_unconfirmed")
	ErrClosed                   = errors.New("chatgpt_client_closed")
	errStorage                  = errors.New("chatgpt_credential_storage_failed")
	errInvalidIdentity          = errors.New("chatgpt_identity_validation_failed")
	errInvalidResponse          = errors.New("chatgpt_invalid_oauth_response")
	errUnavailable              = errors.New("chatgpt_service_unavailable")
)

const (
	issuer          = "https://auth.openai.com"
	resource        = "https://api.openai.com/v1"
	dynamicClientID = "dynamic_agent_client"
	requestedScopes = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"
)

// Status contains display information only. It deliberately excludes all
// credentials, registration identifiers, and authorization transaction values.
type Status struct {
	Connected bool       `json:"connected"`
	Pending   bool       `json:"pending"`
	Sharing   bool       `json:"sharing"`
	Email     string     `json:"email,omitempty"`
	Name      string     `json:"name,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	LastError string     `json:"last_error,omitempty"`
}

// Model uses the account-specific OAuth model catalog, not the API-key catalog.
type Model struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
}

type endpoints struct{ authorize, token, jwks, discovery, models, issuer, resource string }

type Client struct {
	opMu           sync.Mutex
	mu             sync.Mutex
	store          *credentialStore
	http           *http.Client
	endpoints      endpoints
	now            func() time.Time
	attemptTimeout time.Duration
	pending        *attempt
	status         Status
	closed         bool
	lifetime       context.Context
	cancelRequests context.CancelFunc
	keysMu         sync.Mutex
	keys           map[string]verificationKey
	keysAt         time.Time
}

// New performs only local initialization. Production endpoints are fixed and
// cannot be replaced through configuration, environment variables, or APIs.
func New(home string) (*Client, error) {
	return newClient(home, endpoints{
		authorize: issuer + "/api/accounts/authorize", token: issuer + "/api/accounts/oauth/token",
		jwks: issuer + "/.well-known/jwks.json", discovery: issuer + "/.well-known/openid-configuration",
		models: resource + "/models", issuer: issuer, resource: resource,
	})
}

func newClient(home string, ep endpoints) (*Client, error) {
	s, err := newCredentialStore(home)
	if err != nil {
		return nil, errStorage
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	c := &Client{store: s, endpoints: ep, now: time.Now, attemptTimeout: 5 * time.Minute,
		http: &http.Client{Timeout: 20 * time.Second, Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	c.lifetime, c.cancelRequests = context.WithCancel(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, errStorage
	}
	defer unlock()
	r, err := s.loadOrCreate()
	if err != nil {
		return nil, errStorage
	}
	c.setRecord(r)
	return c, nil
}

func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.status
	if s.ExpiresAt != nil {
		expiry := *s.ExpiresAt
		s.ExpiresAt = &expiry
	}
	return s
}

func sharing(scopes []string) bool {
	direct, invoke := false, false
	for _, s := range scopes {
		direct = direct || s == "chatgpt.tokens.use.direct"
		invoke = invoke || s == "resource.invoke"
	}
	return direct && invoke
}

func (c *Client) setRecord(r *credentialRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status.Connected = r.Subject != "" && r.IDToken != ""
	c.status.Sharing = c.status.Connected && sharing(r.Scopes)
	c.status.Email, c.status.Name = r.Email, r.Name
	c.status.ExpiresAt = nil
	if r.AccessToken != "" {
		t := r.ExpiresAt
		c.status.ExpiresAt = &t
	}
}

func (c *Client) setError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status.LastError = ""
	if err != nil {
		c.status.LastError = err.Error()
		if errors.Is(err, errStorage) {
			c.status.Connected = false
			c.status.Sharing = false
			c.status.ExpiresAt = nil
		}
	}
}

func (c *Client) requestContext(ctx context.Context) (context.Context, func()) {
	requestCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.lifetime, cancel)
	return requestCtx, func() { stop(); cancel() }
}

func (c *Client) checkOpen() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	return nil
}

// AccessToken serializes refreshes across clients/processes sharing one home.
// A failed or uncommitted rotation never returns either the old or new token.
func (c *Client) AccessToken(ctx context.Context) (string, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	c.opMu.Lock()
	defer c.opMu.Unlock()
	if err := c.checkOpen(); err != nil {
		return "", err
	}
	unlock, err := c.store.lock(ctx)
	if err != nil {
		c.setError(errStorage)
		return "", errStorage
	}
	defer unlock()
	r, err := c.store.load()
	if err != nil {
		c.setError(errStorage)
		return "", errStorage
	}
	c.setRecord(r)
	if r.Subject == "" || r.IDToken == "" {
		c.setError(ErrNotConnected)
		return "", ErrNotConnected
	}
	if !sharing(r.Scopes) {
		c.setError(ErrConsentRequired)
		return "", ErrConsentRequired
	}
	if c.now().Add(60 * time.Second).Before(r.ExpiresAt) {
		return r.AccessToken, nil
	}
	if r.RefreshToken == "" {
		c.setError(ErrReauthenticationRequired)
		return "", ErrReauthenticationRequired
	}
	updated, err := c.refresh(ctx, r)
	if err != nil {
		c.setError(err)
		return "", err
	}
	if err = c.store.save(updated); err != nil {
		c.setError(errStorage)
		return "", errStorage
	}
	c.setRecord(updated)
	c.setError(nil)
	if !sharing(updated.Scopes) {
		c.setError(ErrConsentRequired)
		return "", ErrConsentRequired
	}
	return updated.AccessToken, nil
}

func (c *Client) Models(ctx context.Context) ([]Model, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	token, err := c.AccessToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoints.models, nil)
	if err != nil {
		return nil, errUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errUnavailable
	}
	var payload struct {
		Models []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Visibility  string `json:"visibility"`
		} `json:"models"`
	}
	if err = readJSON(resp.Body, &payload); err != nil || payload.Models == nil {
		return nil, errInvalidResponse
	}
	models := make([]Model, 0, len(payload.Models))
	for _, m := range payload.Models {
		if m.Visibility == "list" {
			if m.Slug == "" || m.DisplayName == "" {
				return nil, errInvalidResponse
			}
			models = append(models, Model{m.Slug, m.DisplayName})
		}
	}
	return models, nil
}

// Close ends any loopback authorization listener without deleting credentials.
func (c *Client) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.cancelRequests()
	c.Cancel()
	c.opMu.Lock()
	defer c.opMu.Unlock()
	c.http.CloseIdleConnections()
	return nil
}

// Keep malformed network response text out of errors, logs, and public status.
func readJSON(body interface{ Read([]byte) (int, error) }, value any) error {
	return decodeBoundedJSON(body, value)
}
