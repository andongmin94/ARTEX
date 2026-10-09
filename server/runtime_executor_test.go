package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

func managedBundleEnvironment(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("실제 Windows 관리 도구 검사")
	}
	root := os.Getenv("ARTEX_TEST_TOOL_ROOT")
	if root == "" {
		t.Skip("공식 관리 도구 번들 검사 경로 미지정")
	}
	digest := os.Getenv("ARTEX_TEST_TOOL_MANIFEST_SHA256")
	if digest == "" {
		t.Fatal("관리 도구 검사 매니페스트 SHA256이 없습니다")
	}
	t.Setenv("ARTEX_TOOL_ROOT", root)
	t.Setenv("ARTEX_TOOL_MANIFEST_SHA256", digest)
	t.Setenv("ARTEX_DESKTOP_SESSION", strings.Repeat("ab", 32))
	home := t.TempDir()
	t.Setenv("ARTEX_HOME", home)
	work := filepath.Join(home, "data", "workspace", "sessions", "fixture")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	return work
}

func TestManagedWorkspaceRejectsDataRootAndAdjacentHomes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ARTEX_HOME", home)
	for _, work := range []string{home, filepath.Join(home, "data"), filepath.Join(home, "data", "tasks"), filepath.Join(home, "data", "tasks-other", "1"), filepath.Join(home, "data", "sessions", "..", "artex.sqlite"), "relative"} {
		if err := validateManagedWorkspace(work); err == nil {
			t.Fatal("unsafe workspace accepted", work)
		}
	}
	for _, kind := range []string{"tasks", "sessions", "tool-workspaces"} {
		if err := validateManagedWorkspace(filepath.Join(home, "data", "workspace", kind, "fixture")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestManagedDesktopPythonUsesVerifiedRuntimeAndWorkspace(t *testing.T) {
	work := managedBundleEnvironment(t)
	outside := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	code := `import json, sys, os
p=json.load(sys.stdin)
print("stdin="+p["value"])
print("env="+os.environ["TOOL_VALUE"])
try:
 open(p["outside"]).read()
 print("OUTSIDE-EXPOSED")
except PermissionError:
 print("outside-denied")
print("session="+os.environ.get("ARTEX_DESKTOP_SESSION","clean"))
`
	execRaw, _ := json.Marshal(scriptExec{Code: code, TimeoutMs: 45000})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	result, err := (&Server{}).runScriptTool(ctx, "fixture", execRaw, map[string]any{"value": "한글 fixture", "outside": outside}, &actool.ToolContext{WorkingDir: work})
	if err != nil || result.IsError {
		t.Fatalf("managed Python failed %+v %v", result, err)
	}
	for _, want := range []string{"stdin=한글 fixture", "env=한글 fixture", "outside-denied", "session=clean"} {
		if !strings.Contains(result.Flatten(), want) {
			t.Fatal("missing result", want, result.Flatten())
		}
	}
	if strings.Contains(result.Flatten(), "OUTSIDE-EXPOSED") {
		t.Fatal("outside file exposed")
	}
}

func TestManagedDesktopMCPUsesNodeAndPreservesApproval(t *testing.T) {
	_ = managedBundleEnvironment(t)
	code := `const readline=require('node:readline');
let initialized=false;
const lines=readline.createInterface({input:process.stdin});
lines.on('line',line=>{
 const m=JSON.parse(line); if(m.method==='notifications/initialized'){initialized=true;return;}
 let result={};
 if(m.method==='initialize')result={protocolVersion:'2025-06-18',capabilities:{tools:{}},serverInfo:{name:'fixture',version:'1'}};
 else if(m.method==='tools/list'){
  if(m.params.cursor===undefined)result={tools:[{name:'echo',description:'Local fixture',inputSchema:{type:'object',properties:{value:{type:'string'}},required:['value']}}],nextCursor:'last'};
  else if(m.params.cursor==='last')result={tools:[{name:'last',description:'Final page without nextCursor',inputSchema:{type:'object'}}]};
  else throw new Error('unexpected cursor');
 }
 else if(m.method==='tools/call'){
  if(m.params.name==='last')result={content:[{type:'text',text:'image fixture text'},{type:'image',mimeType:'image/png',data:'Zml4dHVyZQ=='}],isError:false};
  else result={content:[{type:'text',text:'managed:'+m.params.arguments.value+':'+initialized+':'+(process.env.ARTEX_DESKTOP_SESSION||'clean')}],isError:false};
 }
 process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:m.id,result})+'\n');
});`
	args, _ := json.Marshal([]string{"-e", code})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client, err := connectMCP(ctx, &db.MCPServer{ID: 123, Name: "fixture", Transport: "stdio", Command: "node", Args: args})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tools, err := client.Tools(ctx)
	if err != nil || len(tools) != 2 {
		t.Fatal(err, len(tools))
	}
	input := json.RawMessage(`{"value":"한글"}`)
	if decision := tools[0].CheckPermissions(ctx, input, permission.Context{}); decision.Behavior != permission.Ask {
		t.Fatal("MCP permission bypass", decision)
	}
	result, err := tools[0].Call(ctx, input, nil)
	if err != nil || result.IsError || !strings.Contains(result.Flatten(), "managed:한글:true:clean") {
		t.Fatalf("MCP call failed %+v %v", result, err)
	}
	imageResult, err := tools[1].Call(ctx, json.RawMessage(`{}`), nil)
	if err != nil || !imageResult.IsError || !strings.Contains(imageResult.Flatten(), "image fixture text") || !strings.Contains(imageResult.Flatten(), `"image"`) {
		t.Fatal("MCP image was silently discarded", imageResult, err)
	}
	if client, err := connectMCP(ctx, &db.MCPServer{Transport: "stdio", Command: "host-unverified-command"}); err == nil || client != nil {
		t.Fatal("host command accepted")
	}
}

func TestManagedMCPDoesNotDiscardUnsupportedContent(t *testing.T) {
	result := managedMCPResult(json.RawMessage(`{"content":[{"type":"text","text":"retained"},{"type":"image","data":"Zml4dHVyZQ==","mimeType":"image/png"}],"isError":false}`), nil)
	if !result.IsError || !strings.Contains(result.Flatten(), "retained") || !strings.Contains(result.Flatten(), `"image"`) {
		t.Fatal(result)
	}
	text := managedMCPResult(json.RawMessage(`{"content":[{"type":"text","text":"plain"}],"isError":false}`), nil)
	if text.IsError || text.Flatten() != "plain" {
		t.Fatal(text)
	}
}

func TestRuntimeToolsRejectsUnpinnedManifest(t *testing.T) {
	root := t.TempDir()
	raw := []byte(`{"schema":1,"platform":"` + runtime.GOOS + `-` + runtime.GOARCH + `","components":[]}`)
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	t.Setenv("ARTEX_DESKTOP_SESSION", "fixture")
	t.Setenv("ARTEX_TOOL_ROOT", root)
	t.Setenv("ARTEX_TOOL_MANIFEST_SHA256", hex.EncodeToString(digest[:]))
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), append(raw, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	(&Server{}).runtimeTools(w, httptest.NewRequest("GET", "/api/runtime/tools", nil))
	var state struct {
		Ready   bool   `json:"ready"`
		Message string `json:"message"`
	}
	if json.Unmarshal(w.Body.Bytes(), &state) != nil || state.Ready || !strings.Contains(state.Message, "SHA256") {
		t.Fatal(w.Body.String())
	}
}

func TestDesktopSettingsCannotOverrideManagedInterpreter(t *testing.T) {
	t.Setenv("ARTEX_DESKTOP_SESSION", "fixture")
	w := httptest.NewRecorder()
	(&Server{}).putSettings(w, httptest.NewRequest("PUT", "/api/settings", strings.NewReader(`{"python_interpreter":"host-unverified-python"}`)))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "매니페스트") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestManagedDesktopPTYAndBackgroundLifecycle(t *testing.T) {
	work := managedBundleEnvironment(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	m := newManagedToolSessions(ctx)
	defer m.Close()
	tc := &actool.ToolContext{WorkingDir: work, AgentID: "fixture-agent"}
	call := func(name string, input any) actool.Result {
		t.Helper()
		raw, _ := json.Marshal(input)
		result, err := m.call(ctx, name, raw, tc)
		if err != nil || result.IsError {
			t.Fatalf("%s failed %s %v", name, result.Flatten(), err)
		}
		return result
	}
	result := call("Bash", map[string]any{"command": "Write-Output 'foreground-한글'"})
	if !strings.Contains(result.Flatten(), "foreground-한글") {
		t.Fatal(result.Flatten())
	}
	opened := call("shell_open", map[string]any{"command": "$x=Read-Host; Write-Output ('pty-result-'+$x)", "mode": "hands-free"})
	var session struct {
		ID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(opened.Flatten()), &session); err != nil || session.ID == "" {
		t.Fatal(opened.Flatten())
	}
	sent := call("shell_send", map[string]any{"session_id": session.ID, "text": "한글 fixture", "submit": true, "quiet_ms": 2000, "timeout_ms": 10000})
	var received struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal([]byte(sent.Flatten()), &received); err != nil || !strings.Contains(received.Output, "pty-result-한글 fixture") {
		t.Fatal(sent.Flatten())
	}
	call("shell_read", map[string]any{"session_id": session.ID, "view": "screen"})
	call("shell_close", map[string]any{"session_id": session.ID})
	background := call("Bash", map[string]any{"command": "Write-Output 'background-fixture'; Start-Sleep -Seconds 30", "run_in_background": true})
	if !strings.Contains(background.Flatten(), "task_") {
		t.Fatal(background.Flatten())
	}
	m.mu.Lock()
	var task *managedToolSession
	for _, s := range m.entries {
		if !s.terminal {
			task = s
		}
	}
	m.mu.Unlock()
	if task == nil {
		t.Fatal("background task missing")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		output := call("TaskOutput", map[string]any{"task_id": task.id})
		if strings.Contains(output.Flatten(), "background-fixture") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(output.Flatten())
		}
		time.Sleep(50 * time.Millisecond)
	}
	otherTC := &actool.ToolContext{WorkingDir: work, AgentID: "another-agent"}
	if denied, err := m.call(ctx, "TaskOutput", json.RawMessage(fmt.Sprintf(`{"task_id":%q}`, task.id)), otherTC); err != nil || !denied.IsError {
		t.Fatal("cross-agent task accepted", denied, err)
	}
	call("TaskList", map[string]any{})
	m.Close()
	m.Close()
	select {
	case <-task.done:
	case <-time.After(5 * time.Second):
		t.Fatal("owned background process survived cleanup")
	}
	if denied, err := m.call(ctx, "TaskList", json.RawMessage(`{}`), tc); err != nil || !denied.IsError {
		t.Fatal("closed owner accepted a call", denied, err)
	}
}

func TestManagedRuntimeReportsSupportedExecutionAndBlockedBrowser(t *testing.T) {
	managedBundleEnvironment(t)
	w := httptest.NewRecorder()
	(&Server{}).runtimeTools(w, httptest.NewRequest("GET", "/api/runtime/tools", nil))
	var status struct {
		Ready      bool `json:"ready"`
		Components []struct {
			Key       string `json:"key"`
			State     string `json:"state"`
			Execution string `json:"execution"`
		} `json:"components"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err, w.Body.String())
	}
	if status.Ready || len(status.Components) != 6 {
		t.Fatal(w.Body.String())
	}
	for _, component := range status.Components {
		expected := "available"
		if component.Key == "browser" {
			expected = "blocked"
		}
		if component.State != "verified" || component.Execution != expected {
			t.Fatal(w.Body.String())
		}
	}
}

func TestManagedWrapperPreservesPermissionFloor(t *testing.T) {
	t.Setenv("AGENT_CORE_DISABLE_BACKGROUND_TASKS", "1")
	wrapped := managedDesktopTool{CoreTool: actool.NewBash()}
	for _, input := range []json.RawMessage{json.RawMessage(`{"command":"Write-Output 'fixture'"}`), json.RawMessage(`{"command":"rm -rf /"}`)} {
		original := wrapped.CoreTool.CheckPermissions(context.Background(), input, permission.Context{})
		managed := wrapped.CheckPermissions(context.Background(), input, permission.Context{})
		if original.Behavior != managed.Behavior || original.Message != managed.Message {
			t.Fatal("permission behavior changed", original, managed)
		}
	}
	if wrapped.InputSchema()["properties"].(map[string]any)["run_in_background"] == nil {
		t.Fatal("managed background input missing")
	}
}
