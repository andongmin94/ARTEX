package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

func TestBrowserControlRedirectDoesNotForwardAppSession(t *testing.T) {
	var sourceRequests, targetRequests atomic.Int64
	target := browserPolicyHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetRequests.Add(1); w.WriteHeader(200) }))
	source := browserPolicyHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceRequests.Add(1)
		http.Redirect(w, r, target.URL+"/rpc", http.StatusTemporaryRedirect)
	}))
	b := newBrowserRuntime(&Server{desktopSession: []byte(strings.Repeat("ab", 32))})
	b.control, _ = url.Parse(source.URL)
	defer b.Close()
	if _, err := b.rpc(context.Background(), "tools/list", map[string]any{}); err == nil {
		t.Fatal("redirecting control endpoint was accepted")
	}
	if sourceRequests.Load() != 1 || targetRequests.Load() != 0 {
		t.Fatalf("app secret channel followed redirect: source=%d target=%d", sourceRequests.Load(), targetRequests.Load())
	}
}

func TestBrowserMetadataDoesNotAuthorizeGlobalTasklessExecution(t *testing.T) {
	control := browserPolicyHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("invalid metadata request")
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if request.Method != "tools/list" {
			t.Errorf("taskless metadata started renderer/network: %s", request.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"tools": []map[string]any{{"name": "browser_navigate", "description": "fixture", "inputSchema": map[string]any{"type": "object"}}}}})
	}))
	b := newBrowserRuntime(&Server{desktopSession: []byte(strings.Repeat("ab", 32))})
	b.control, _ = url.Parse(control.URL)
	defer b.Close()
	client := &browserMCPClient{b: b, ctx: context.Background()}
	tools, err := client.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].CheckPermissions(context.Background(), json.RawMessage(`{}`), permission.Context{}).Behavior != permission.Ask {
		t.Fatal("metadata tool weakened approval")
	}
	result := client.run(context.Background(), "browser_navigate", json.RawMessage(`{"url":"https://fixture.example"}`), &actool.ToolContext{WorkingDir: t.TempDir()})
	if !result.IsError || len(b.runs) != 0 {
		t.Fatal("global cached taskless browser tool acquired a run")
	}
}
