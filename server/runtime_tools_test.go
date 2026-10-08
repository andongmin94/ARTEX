package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

func TestDesktopCannotExecuteUnpreparedHostTools(t *testing.T) {
	t.Setenv("ARTEX_DESKTOP_SESSION", strings.Repeat("ab", 32))
	store, err := db.Open(testBusinessPath(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	oldResolve, oldBinding := agent.ToolResolve, agent.FindingTrafficBindingEnabled
	t.Cleanup(func() { agent.ToolResolve, agent.FindingTrafficBindingEnabled = oldResolve, oldBinding })
	if err := wireTools(store, nil); err != nil {
		t.Fatal(err)
	}
	called := false
	resolved := agent.ToolResolve(context.Background(), "worker", []actool.CoreTool{policyProbeTool{CoreTool: actool.NewBash(), called: &called}})
	if len(resolved) == 0 {
		t.Fatal("empty tools could restore SDK defaults")
	}
	for _, tool := range resolved {
		if tool.Name() != "Bash" {
			continue
		}
		result, err := tool.Call(context.Background(), json.RawMessage(`{"command":"echo fixture"}`), nil)
		if err != nil || !result.IsError || called {
			t.Fatalf("unprepared shell executed: called=%v error=%v", called, err)
		}
	}
	s := &Server{}
	for _, result := range []struct {
		kind    string
		execute func() (actool.Result, error)
	}{
		{"command", func() (actool.Result, error) { return s.runCommandTool(context.Background(), nil, nil, nil) }},
		{"script", func() (actool.Result, error) { return s.runScriptTool(context.Background(), "fixture", nil, nil, nil) }},
	} {
		output, err := result.execute()
		if err != nil || !output.IsError || !strings.Contains(output.Flatten(), "배포와 검증") {
			t.Fatalf("unprepared %s accepted: %+v %v", result.kind, output, err)
		}
	}
	if client, err := connectMCP(context.Background(), &db.MCPServer{Transport: "stdio", Command: "host-tool-not-verified"}); err == nil || client != nil || !strings.Contains(err.Error(), "배포와 검증") {
		t.Fatalf("unprepared stdio MCP accepted: %v %v", client, err)
	}
	w := httptest.NewRecorder()
	s.runtimeTools(w, httptest.NewRequest("GET", "/api/runtime/tools", nil))
	var state struct {
		Ready      bool
		Mode       string
		Components []struct{ Key, State string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Ready || state.Mode != "desktop" || len(state.Components) != 6 {
		t.Fatalf("host tools were reported prepared: %+v", state)
	}
	for _, component := range state.Components {
		if component.State != "not_prepared" {
			t.Fatal("component falsely ready", component)
		}
	}
}
