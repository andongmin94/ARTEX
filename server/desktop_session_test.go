package server

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDesktopSessionValidatesSecretFormat(t *testing.T) {
	for _, value := range []string{"short", strings.Repeat("z", 64)} {
		t.Setenv("ARTEX_DESKTOP_SESSION", value)
		if _, err := readDesktopSession(); err == nil {
			t.Fatal("invalid desktop secret accepted")
		}
	}
	t.Setenv("ARTEX_DESKTOP_SESSION", strings.Repeat("ab", 32))
	if key, err := readDesktopSession(); err != nil || len(key) != 64 {
		t.Fatalf("valid secret rejected: %v", err)
	}
}

func TestDesktopSessionProtectsSetupHealthAndAuth(t *testing.T) {
	s := &Server{desktopSession: []byte(strings.Repeat("ab", 32)), jwtKey: []byte("fixture")}
	if err := s.ConfigureDesktopListener(&net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: 8787}); err == nil {
		t.Fatal("nonloopback desktop listener accepted")
	}
	if err := s.ConfigureDesktopListener(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8787}); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	for _, path := range []string{"/api/health", "/api/auth/status", "/api/auth/init", "/api/auth/login", "/"} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787"+path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s bypassed desktop gate: %d", path, w.Code)
		}
	}
	for _, test := range []struct {
		host, origin, key, path string
		status                  int
	}{
		{"127.0.0.1:8787", "", string(s.desktopSession), "/api/health", 200},
		{"127.0.0.1:8787", "http://127.0.0.1:8787", string(s.desktopSession), "/api/health", 200},
		{"127.0.0.1:8787", "http://attacker.invalid", string(s.desktopSession), "/api/health", 403},
		{"localhost:8787", "", string(s.desktopSession), "/api/health", 403},
		{"127.0.0.1:8788", "", string(s.desktopSession), "/api/health", 403},
		{"127.0.0.1:8787", "", strings.Repeat("cd", 32), "/api/health", 403},
		{"127.0.0.1:8787", "", string(s.desktopSession), "/api/stats", 401},
	} {
		req := httptest.NewRequest(http.MethodGet, "http://"+test.host+test.path, nil)
		req.Header.Set(desktopSessionHeader, test.key)
		if test.origin != "" {
			req.Header.Set("Origin", test.origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != test.status {
			t.Fatalf("host=%s origin=%s path=%s: %d want %d", test.host, test.origin, test.path, w.Code, test.status)
		}
	}
	req := httptest.NewRequest(http.MethodOptions, "http://127.0.0.1:8787/api/auth/init", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatal("CORS preflight bypassed desktop gate")
	}
}
