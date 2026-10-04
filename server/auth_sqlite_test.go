package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/internal/sqlitedb"
	"golang.org/x/crypto/bcrypt"
)

// Exercise the existing HTTP auth handlers and real SQLite setting methods.
// This is intentionally NOT server.New/NewManager or full application boot:
// those still depend on the unported business schema.
type authSQLiteFixture struct {
	server *Server
	http   *httptest.Server
	store  *db.DB
}

func newAuthSQLiteFixture(t *testing.T, home string) *authSQLiteFixture {
	t.Helper()
	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	d, err := sqlitedb.Open(t.Context(), filepath.Join(dataDir, "auth-fixture.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY NOT NULL,value TEXT NOT NULL,updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	key, err := loadOrCreateJWTKey(home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	store := &db.DB{DB: d}
	s := &Server{m: &Manager{pg: store}, jwtKey: key}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/auth/status", s.authStatus)
	mux.HandleFunc("POST /api/auth/init", s.authInit)
	mux.HandleFunc("POST /api/auth/login", s.authLogin)
	mux.HandleFunc("POST /api/auth/change-password", s.authChangePassword)
	mux.HandleFunc("GET /api/protected", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]bool{"ok": true}) })
	h := httptest.NewServer(s.requireAuth(mux))
	t.Cleanup(h.Close)
	return &authSQLiteFixture{server: s, http: h, store: store}
}

func (f *authSQLiteFixture) close() error { f.http.Close(); return f.store.Close() }

type authResponse struct {
	status int
	body   map[string]any
	err    error
}

func authRequest(f *authSQLiteFixture, method, path string, payload map[string]string, token string) authResponse {
	raw, err := json.Marshal(payload)
	if err != nil {
		return authResponse{err: err}
	}
	req, err := http.NewRequest(method, f.http.URL+path, bytes.NewReader(raw))
	if err != nil {
		return authResponse{err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := f.http.Client().Do(req)
	if err != nil {
		return authResponse{err: err}
	}
	defer resp.Body.Close()
	body := map[string]any{}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body)
	return authResponse{status: resp.StatusCode, body: body, err: err}
}

func requireAuthStatus(t *testing.T, r authResponse, status int) map[string]any {
	t.Helper()
	if r.err != nil || r.status != status {
		t.Fatalf("status=%d want=%d body=%v err=%v", r.status, status, r.body, r.err)
	}
	if status != 200 {
		if _, ok := r.body["token"]; ok {
			t.Fatal("error response leaked a token")
		}
	}
	return r.body
}

func TestAuthSQLiteInitializeLoginReopenChangePassword(t *testing.T) {
	home := filepath.Join(t.TempDir(), "한글 설정 # 100%")
	f := newAuthSQLiteFixture(t, home)
	status := requireAuthStatus(t, authRequest(f, "GET", "/api/auth/status", nil, ""), 200)
	if status["initialized"] != false {
		t.Fatal("new DB reported initialized")
	}
	init := requireAuthStatus(t, authRequest(f, "POST", "/api/auth/init", map[string]string{"password": "처음-비밀번호-123"}, ""), 200)
	token, ok := init["token"].(string)
	if !ok || !verifyJWT(token, f.server.jwtKey) {
		t.Fatal("invalid setup token")
	}
	requireAuthStatus(t, authRequest(f, "GET", "/api/protected", nil, ""), 401)
	requireAuthStatus(t, authRequest(f, "GET", "/api/protected", nil, token), 200)
	requireAuthStatus(t, authRequest(f, "POST", "/api/auth/init", map[string]string{"password": "overwrite"}, ""), 403)
	hash, ok, err := f.store.GetSetting(authPassKey)
	if err != nil || !ok {
		t.Fatal("password not stored")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("처음-비밀번호-123")) != nil {
		t.Fatal("stored hash did not match")
	}
	if _, err := os.Stat(filepath.Join(home, "data", jwtKeyFilename)); !os.IsNotExist(err) {
		t.Fatalf("JWT key in workspace: %v", err)
	}
	oldKey := append([]byte(nil), f.server.jwtKey...)
	if err := f.close(); err != nil {
		t.Fatal(err)
	}
	f = newAuthSQLiteFixture(t, home)
	if !bytes.Equal(oldKey, f.server.jwtKey) {
		t.Fatal("JWT key changed on reopen")
	}
	status = requireAuthStatus(t, authRequest(f, "GET", "/api/auth/status", nil, ""), 200)
	if status["initialized"] != true {
		t.Fatal("password lost on reopen")
	}
	requireAuthStatus(t, authRequest(f, "POST", "/api/auth/login", map[string]string{"username": "ARTEX", "password": "wrong"}, ""), 401)
	login := requireAuthStatus(t, authRequest(f, "POST", "/api/auth/login", map[string]string{"username": "ARTEX", "password": "처음-비밀번호-123"}, ""), 200)
	token = login["token"].(string)
	change := map[string]string{"old_password": "처음-비밀번호-123", "new_password": "새-비밀번호-456"}
	requireAuthStatus(t, authRequest(f, "POST", "/api/auth/change-password", change, ""), 401)
	requireAuthStatus(t, authRequest(f, "POST", "/api/auth/change-password", change, token), 200)
	if err := f.close(); err != nil {
		t.Fatal(err)
	}
	f = newAuthSQLiteFixture(t, home)
	requireAuthStatus(t, authRequest(f, "POST", "/api/auth/login", map[string]string{"username": "ARTEX", "password": "처음-비밀번호-123"}, ""), 401)
	requireAuthStatus(t, authRequest(f, "POST", "/api/auth/login", map[string]string{"username": "ARTEX", "password": "새-비밀번호-456"}, ""), 200)
}

func TestAuthSQLiteConcurrentInitialization(t *testing.T) {
	f := newAuthSQLiteFixture(t, t.TempDir())
	const n = 8
	responses := make([]authResponse, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range responses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			responses[i] = authRequest(f, "POST", "/api/auth/init", map[string]string{"password": fmt.Sprintf("candidate-%d", i)}, "")
		}(i)
	}
	close(start)
	wg.Wait()
	winner := -1
	for i, r := range responses {
		if r.status == 200 {
			requireAuthStatus(t, r, 200)
			if winner != -1 {
				t.Fatal("multiple setup winners")
			}
			winner = i
		} else {
			requireAuthStatus(t, r, 403)
		}
	}
	if winner < 0 {
		t.Fatal("no setup winner")
	}
	for i := range responses {
		want := 401
		if i == winner {
			want = 200
		}
		requireAuthStatus(t, authRequest(f, "POST", "/api/auth/login", map[string]string{"username": "ARTEX", "password": fmt.Sprintf("candidate-%d", i)}, ""), want)
	}
}

func TestAuthSQLiteReadFailuresNeverBecomeSetup(t *testing.T) {
	for _, failure := range []string{"closed", "missing-table", "empty-hash"} {
		t.Run(failure, func(t *testing.T) {
			f := newAuthSQLiteFixture(t, t.TempDir())
			token, err := signJWT(f.server.jwtKey)
			if err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "closed":
				err = f.store.Close()
			case "missing-table":
				_, err = f.store.Exec(`DROP TABLE settings`)
			case "empty-hash":
				err = f.store.SetSetting(authPassKey, "")
			}
			if err != nil {
				t.Fatal(err)
			}
			cases := []struct {
				method, path string
				data         map[string]string
			}{
				{"GET", "/api/auth/status", nil},
				{"POST", "/api/auth/init", map[string]string{"password": "must-not-save"}},
				{"POST", "/api/auth/login", map[string]string{"username": "ARTEX", "password": "anything"}},
				{"POST", "/api/auth/change-password", map[string]string{"old_password": "anything", "new_password": "must-not-save"}},
			}
			for _, tc := range cases {
				body := requireAuthStatus(t, authRequest(f, tc.method, tc.path, tc.data, token), 500)
				text := fmt.Sprint(body)
				if strings.Contains(text, "database is closed") || strings.Contains(text, "no such table") {
					t.Fatal("raw storage error leaked")
				}
				if body["initialized"] == false {
					t.Fatal("storage failure advertised uninitialized state")
				}
			}
			if failure == "empty-hash" {
				v, ok, err := f.store.GetSetting(authPassKey)
				if err != nil || !ok || v != "" {
					t.Fatal("empty corrupt hash silently replaced")
				}
			}
		})
	}
}

func TestAuthSQLiteConcurrentPasswordChanges(t *testing.T) {
	f := newAuthSQLiteFixture(t, t.TempDir())
	body := requireAuthStatus(t, authRequest(f, "POST", "/api/auth/init", map[string]string{"password": "original-password"}, ""), 200)
	token := body["token"].(string)
	const n = 4
	responses := make([]authResponse, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range responses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			responses[i] = authRequest(f, "POST", "/api/auth/change-password", map[string]string{"old_password": "original-password", "new_password": fmt.Sprintf("replacement-%d", i)}, token)
		}(i)
	}
	close(start)
	wg.Wait()
	winner := -1
	for i, r := range responses {
		if r.status == 200 {
			requireAuthStatus(t, r, 200)
			if winner != -1 {
				t.Fatal("multiple password-change winners")
			}
			winner = i
		} else if r.status == 409 {
			requireAuthStatus(t, r, 409)
		} else {
			requireAuthStatus(t, r, 401)
		}
	}
	if winner < 0 {
		t.Fatal("no password-change winner")
	}
	requireAuthStatus(t, authRequest(f, "POST", "/api/auth/login", map[string]string{"username": "ARTEX", "password": fmt.Sprintf("replacement-%d", winner)}, ""), 200)
}
