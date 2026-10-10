package traffic

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Autumn-27/artex/internal/browserports"
	"github.com/Autumn-27/artex/internal/browserrelay"
)

func browserTrafficFixtureServer(t *testing.T, handler http.Handler) *httptest.Server {
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
		server := httptest.NewUnstartedServer(handler)
		_ = server.Listener.Close()
		server.Listener = listener
		server.Start()
		t.Cleanup(server.Close)
		return server
	}
	t.Fatal("fixture could not bind an allowed browser port")
	return nil
}

func TestBrowserRelayRecordsBodiesWithoutSecondTargetRequest(t *testing.T) {
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	var hits atomic.Int32
	target := browserTrafficFixtureServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Set-Cookie", "target=fixture; Path=/")
		_, _ = w.Write([]byte("응답 원문 fixture"))
	}))
	defer target.Close()
	relay, err := browserrelay.New(browserrelay.Options{Authorize: func(context.Context, *url.URL, []netip.Addr) error { return nil }, Record: tr.RecordBrowser})
	if err != nil {
		t.Fatal(err)
	}
	_, err = relay.Do(context.Background(), browserrelay.Request{URL: target.URL + "/notes", Method: "POST", Headers: http.Header{"Content-Type": {"text/plain"}, "X-Artex-Desktop-Session": {"never-target"}}, BodyBase64: base64.StdEncoding.EncodeToString([]byte("요청 원문 fixture"))})
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatal("capture reissued target request", hits.Load())
	}
	id := onlyExchangeID(t, tr)
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	request, response, err := reopened.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(request, "요청 원문 fixture") || !strings.Contains(response, "응답 원문 fixture") || !strings.Contains(response, "target=fixture") || strings.Contains(request, "never-target") {
		t.Fatal("record did not preserve target exchange or leaked app credential")
	}
}

func TestRecordRejectsBlobWriteFailureAndRecovers(t *testing.T) {
	tr, dir := openTraffic(t)
	blobs := filepath.Join(dir, "_blobs", "sha256")
	if err := os.WriteFile(blobs, []byte("fixture blocker"), 0600); err != nil {
		t.Fatal(err)
	}
	flow := newFlow("fixture.invalid", "GET", "/body", nil, []byte(strings.Repeat("fixture payload ", maxInlineBody)), withRespType("text/plain"))
	if err := tr.record(flow); err == nil {
		t.Fatal("blob write failure reported success")
	}
	var count int
	if err := tr.DB().QueryRow("SELECT COUNT(*) FROM exchanges").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed capture committed truncated metadata", count, err)
	}
	if body, err := os.ReadFile(blobs); err != nil || string(body) != "fixture blocker" {
		t.Fatal("existing file overwritten")
	}
	if err := os.Remove(blobs); err != nil {
		t.Fatal(err)
	}
	if err := tr.record(flow); err != nil {
		t.Fatal("capture did not recover", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tr.record(flow); err == nil {
		t.Fatal("closed recorder reported success")
	}
}

func TestBlobAtomicPublicationAndCorruptionRefusal(t *testing.T) {
	tr, dir := openTraffic(t)
	body := []byte(strings.Repeat("atomic fixture", 32768))
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	directory := filepath.Join(dir, "_blobs", "sha256", hash[:2])
	var writers sync.WaitGroup
	for range 8 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			if err := tr.storeBlob(directory, hash, body); err != nil {
				t.Error(err)
			}
		}()
	}
	writers.Wait()
	name := filepath.Join(directory, hash+".bin")
	if got, err := os.ReadFile(name); err != nil || string(got) != string(body) {
		t.Fatal("published blob was incomplete", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary blob files remained", len(entries), err)
	}
	corrupted := append([]byte(nil), body...)
	corrupted[0] ^= 1
	if err := os.WriteFile(name, corrupted, 0600); err != nil {
		t.Fatal(err)
	}
	if err := tr.storeBlob(directory, hash, body); err == nil {
		t.Fatal("equal-size corrupt blob accepted")
	}
	if got, err := os.ReadFile(name); err != nil || string(got) != string(corrupted) {
		t.Fatal("existing corrupt blob overwritten", err)
	}
	if err := os.WriteFile(name, []byte("preexisting short fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := tr.storeBlob(directory, hash, body); err == nil {
		t.Fatal("short existing blob accepted")
	}
}
