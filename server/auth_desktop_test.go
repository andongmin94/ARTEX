package server

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthDesktopSessionLoginPreservesPassword(t *testing.T) {
	for _, name := range []string{"new-home", "existing-password", "empty-password", "malformed-password"} {
		t.Run(name, func(t *testing.T) {
			f := newAuthSQLiteFixture(t, t.TempDir())
			initialized := name != "new-home"
			switch name {
			case "existing-password":
				requireAuthStatus(t, authRequest(f, "POST", "/api/auth/init", map[string]string{"password": "keep-existing-password"}, ""), http.StatusOK)
			case "empty-password", "malformed-password":
				hash := ""
				if name == "malformed-password" {
					hash = "preserve-malformed-hash"
				}
				if err := f.store.SetSetting(authPassKey, hash); err != nil {
					t.Fatal(err)
				}
			}
			before, existsBefore, err := f.store.GetSetting(authPassKey)
			if err != nil {
				t.Fatal(err)
			}
			s := f.server
			s.desktopSession = []byte(strings.Repeat("ab", 32))
			if err := s.ConfigureDesktopListener(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8787}); err != nil {
				t.Fatal(err)
			}
			h := s.Handler()
			request := func(method, path, token string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(method, "http://127.0.0.1:8787"+path, nil)
				req.Header.Set(desktopSessionHeader, string(s.desktopSession))
				req.Header.Set("Origin", "http://127.0.0.1:8787")
				if token != "" {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)
				return w
			}
			status := request(http.MethodGet, "/api/auth/status", "")
			var statusBody struct {
				Initialized bool   `json:"initialized"`
				Mode        string `json:"mode"`
			}
			if err := json.Unmarshal(status.Body.Bytes(), &statusBody); err != nil || status.Code != http.StatusOK || statusBody.Mode != "desktop" || statusBody.Initialized != initialized {
				t.Fatalf("status=%d body=%s err=%v", status.Code, status.Body.String(), err)
			}
			login := request(http.MethodPost, "/api/auth/desktop-session", "")
			var body struct {
				Token string `json:"token"`
			}
			if err := json.Unmarshal(login.Body.Bytes(), &body); err != nil || login.Code != http.StatusOK || !verifyJWT(body.Token, s.jwtKey) {
				t.Fatalf("desktop login failed: status=%d err=%v", login.Code, err)
			}
			if w := request(http.MethodGet, "/api/runtime/tools", ""); w.Code != http.StatusUnauthorized {
				t.Fatalf("protected route accepted app key without JWT: %d", w.Code)
			}
			if w := request(http.MethodGet, "/api/runtime/tools", body.Token); w.Code != http.StatusOK {
				t.Fatalf("desktop JWT did not authenticate protected route: %d", w.Code)
			}
			if w := request(http.MethodGet, "/api/runtime/tools", "invalid"); w.Code != http.StatusUnauthorized {
				t.Fatalf("protected route accepted invalid JWT: %d", w.Code)
			}
			if w := request(http.MethodGet, "/api/auth/desktop-session", ""); w.Code != http.StatusMethodNotAllowed {
				t.Fatalf("desktop login accepted GET: %d", w.Code)
			}
			if w := request(http.MethodPost, "/api/auth/desktop-session/other", ""); w.Code != http.StatusUnauthorized {
				t.Fatalf("desktop login JWT exemption extended to a child path: %d", w.Code)
			}
			after, existsAfter, err := f.store.GetSetting(authPassKey)
			if err != nil || before != after || existsBefore != existsAfter {
				t.Fatalf("desktop login changed password state: exists=%v->%v err=%v", existsBefore, existsAfter, err)
			}
			if name == "existing-password" {
				requireAuthStatus(t, authRequest(f, "POST", "/api/auth/login", map[string]string{"username": "ARTEX", "password": "keep-existing-password"}, ""), http.StatusOK)
			}
		})
	}
}

func TestAuthDesktopStatusPropagatesStorageErrors(t *testing.T) {
	for _, failure := range []string{"closed", "missing-table"} {
		t.Run(failure, func(t *testing.T) {
			f := newAuthSQLiteFixture(t, t.TempDir())
			s := f.server
			s.desktopSession = []byte(strings.Repeat("ab", 32))
			if err := s.ConfigureDesktopListener(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8787}); err != nil {
				t.Fatal(err)
			}
			if failure == "closed" {
				if err := f.store.Close(); err != nil {
					t.Fatal(err)
				}
			} else if _, err := f.store.Exec(`DROP TABLE settings`); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "http://"+s.desktopHost+"/api/auth/status", nil)
			req.Header.Set(desktopSessionHeader, string(s.desktopSession))
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, req)
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("storage error hidden by desktop mode: %d", w.Code)
			}
		})
	}
}

func TestAuthDesktopSessionRejectsUntrustedRequests(t *testing.T) {
	s := &Server{desktopSession: []byte(strings.Repeat("ab", 32)), jwtKey: []byte("fixture")}
	if err := s.ConfigureDesktopListener(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8787}); err != nil {
		t.Fatal(err)
	}
	for _, direct := range []bool{false, true} {
		var h http.Handler = s.Handler()
		if direct {
			h = http.HandlerFunc(s.authDesktopSession)
		}
		for _, test := range []struct {
			name, host, origin string
			keys, origins      []string
		}{
			{name: "missing-key", host: s.desktopHost},
			{name: "wrong-key", host: s.desktopHost, keys: []string{strings.Repeat("cd", 32)}},
			{name: "duplicate-key", host: s.desktopHost, keys: []string{string(s.desktopSession), string(s.desktopSession)}},
			{name: "wrong-host", host: "localhost:8787", keys: []string{string(s.desktopSession)}},
			{name: "wrong-port", host: "127.0.0.1:8788", keys: []string{string(s.desktopSession)}},
			{name: "wrong-origin", host: s.desktopHost, origin: "http://attacker.invalid", keys: []string{string(s.desktopSession)}},
			{name: "duplicate-origin", host: s.desktopHost, origins: []string{"http://" + s.desktopHost, "http://" + s.desktopHost}, keys: []string{string(s.desktopSession)}},
		} {
			req := httptest.NewRequest(http.MethodPost, "http://"+test.host+"/api/auth/desktop-session", nil)
			for _, key := range test.keys {
				req.Header.Add(desktopSessionHeader, key)
			}
			if test.origin != "" {
				req.Header.Set("Origin", test.origin)
			}
			for _, origin := range test.origins {
				req.Header.Add("Origin", origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "\"token\"") {
				t.Fatalf("%s direct=%v: status=%d", test.name, direct, w.Code)
			}
		}
	}
	// A session JWT must still be presented through the app boundary.
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://"+s.desktopHost+"/api/runtime/tools", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("JWT bypassed app session boundary: %d", w.Code)
	}
}

func TestAuthDesktopSessionDeniedOutsideConfiguredDesktop(t *testing.T) {
	f := newAuthSQLiteFixture(t, t.TempDir())
	status := requireAuthStatus(t, authRequest(f, "GET", "/api/auth/status", nil, ""), http.StatusOK)
	if status["mode"] != "standalone" {
		t.Fatalf("standalone mode not explicit: %v", status)
	}
	token, err := signJWT(f.server.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, configuredKey := range []bool{false, true} {
		s := f.server
		if configuredKey {
			s.desktopSession = []byte(strings.Repeat("ab", 32))
		}
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/api/auth/desktop-session", nil)
		req.Header.Set(desktopSessionHeader, strings.Repeat("ab", 32))
		req.Header.Set("Origin", "http://127.0.0.1:8787")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "\"token\"") {
			t.Fatalf("unconfigured desktop key=%v: status=%d", configuredKey, w.Code)
		}
	}
}
