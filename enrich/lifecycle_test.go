package enrich

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCloseCancelsAndDrainsHTTPWorker(t *testing.T) {
	entered := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	}))
	defer target.Close()
	engine := New(nil, nil, 1)
	engine.ProbeSite(1, target.URL)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("local fixture was not reached")
	}
	done := make(chan struct{})
	go func() { engine.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("close did not cancel and drain the HTTP worker")
	}
	engine.Close()
}
