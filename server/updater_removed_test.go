package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBackendCannotUpdateItsOwnExecutable(t *testing.T) {
	s := &Server{jwtKey: []byte("fixture-only-signing-key")}
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	for _, test := range []struct{ method, path string }{
		{http.MethodGet, "/api/update/check"}, {http.MethodPost, "/api/update/apply"},
		{http.MethodPost, "/api/update/rollback"}, {http.MethodGet, "/api/update/stream"},
	} {
		req := httptest.NewRequest(test.method, test.path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("obsolete update endpoint %s returned %d", test.path, w.Code)
		}
	}
}
