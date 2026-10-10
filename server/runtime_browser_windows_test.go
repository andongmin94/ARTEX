package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/internal/toolruntime"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"golang.org/x/sys/windows"
)

func browserRendererCannotCrossBoundary(t *testing.T, pid, otherPID int, paths []string) {
	t.Helper()
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	var token, duplicate windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &token); err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	if err := windows.DuplicateTokenEx(token, windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE|windows.TOKEN_IMPERSONATE, nil, windows.SecurityImpersonation, windows.TokenImpersonation, &duplicate); err != nil {
		t.Fatal(err)
	}
	defer duplicate.Close()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.SetThreadToken(nil, duplicate); err != nil {
		t.Fatal(err)
	}
	defer windows.RevertToSelf()
	for _, access := range []uint32{windows.PROCESS_VM_READ, windows.PROCESS_DUP_HANDLE, windows.PROCESS_TERMINATE, windows.PROCESS_QUERY_LIMITED_INFORMATION} {
		handle, err := windows.OpenProcess(access, false, uint32(otherPID))
		if err == nil {
			windows.CloseHandle(handle)
			t.Fatalf("renderer opened other task PID with access %#x", access)
		}
	}
	for _, path := range paths {
		if _, err := os.ReadFile(path); err == nil {
			t.Fatal("renderer read a task workspace or host credential fixture")
		}
	}
}

type browserTestTransport struct {
	t     *testing.T
	inner http.RoundTripper
}

func (r browserTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := r.inner.RoundTrip(request)
	if err != nil {
		r.t.Logf("browser control HTTP failure: %v", err)
	}
	return response, err
}

// This opt-in integration uses the actual product broker, Go HTTP routes,
// modernc SQLite and live Windows renderer token. The executable must be a
// freshly copied public Electron runtime with only the product's AAP RX grant.
func TestWindowsBrowserBrokerIntegration(t *testing.T) {
	executable := os.Getenv("ARTEX_TEST_BROWSER_EXECUTABLE")
	if executable == "" {
		t.Skip("set ARTEX_TEST_BROWSER_EXECUTABLE to run the real Windows Electron integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	home := t.TempDir()
	t.Setenv("ARTEX_HOME", home)
	m, err := NewManager(filepath.Join(home, "data"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	s, err := New(ctx, m, home, m.dir, home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	s.desktopSession = []byte(strings.Repeat("ab", 32))
	s.browser = newBrowserRuntime(s)
	s.browser.http.Transport = browserTestTransport{t: t, inner: s.browser.http.Transport}
	s.browser.executable = executable
	appFixture := browserPolicyHTTPServer(t, http.NotFoundHandler())
	appListener := appFixture.Listener
	if err := s.ConfigureDesktopListener(appListener.Addr()); err != nil {
		t.Fatal(err)
	}
	appFixture.Config.Handler = s.Handler()
	origin := appFixture.URL
	var requestsMu sync.Mutex
	requests := []string{}
	target := browserPolicyHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestsMu.Lock()
		requests = append(requests, r.URL.Path+" "+r.Header.Get("Cookie"))
		requestsMu.Unlock()
		switch r.URL.Path {
		case "/":
			w.Header().Add("Set-Cookie", "first=one; Path=/; HttpOnly; Expires=Wed, 09 Jun 2027 10:18:14 GMT")
			w.Header().Add("Set-Cookie", "second=two; Path=/; Expires=Wed, 09 Jun 2027 10:18:14 GMT")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<html><title>Browser fixture</title><body><label for="name">이름</label><input id="name"><button onclick="document.getElementById('result').textContent=document.getElementById('name').value">저장</button><p id="result">준비</p></body></html>`)
		case "/echo":
			_, _ = io.WriteString(w, r.Header.Get("Cookie"))
		case "/worker.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = io.WriteString(w, `onmessage=async()=>postMessage(await(await fetch('/echo')).text())`)
		case "/redirect":
			http.Redirect(w, r, "http://127.0.0.2:"+strings.Split(r.Host, ":")[1]+"/forbidden", http.StatusFound)
		case "/forbidden-status":
			w.WriteHeader(403)
			_, _ = io.WriteString(w, "fixture target forbidden")
		default:
			_, _ = io.WriteString(w, "fixture")
		}
	}))
	defer target.Close()
	task, err := m.pg.CreateTask("가상 브라우저 검사", "승인 범위 검사", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.assets.AddAgentScope(task.ID, "ip", "127.0.0.1", "가상 대상", "manual"); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(m.workspaceDir(), "tasks", strconv.FormatInt(task.ID, 10))
	if err := os.MkdirAll(work, 0700); err != nil {
		t.Fatal(err)
	}
	brokerModule, err := filepath.Abs("../desktop/src/browser.cjs")
	if err != nil {
		t.Fatal(err)
	}
	moduleJSON, _ := json.Marshal(brokerModule)
	fixture := filepath.Join(home, "electron-main.cjs")
	main := `const {app}=require('electron'); const {BrowserBroker}=require(` + string(moduleJSON) + `);
app.setPath('userData',process.env.ARTEX_TEST_HOME); app.enableSandbox(); app.commandLine.appendSwitch('enable-features','RendererAppContainer'); app.commandLine.appendSwitch('host-resolver-rules','MAP * ~NOTFOUND, EXCLUDE 127.0.0.1'); app.commandLine.appendSwitch('disable-quic'); app.on('window-all-closed',()=>{});
let broker; app.whenReady().then(async()=>{broker=new BrowserBroker({token:()=>process.env.ARTEX_TEST_TOKEN,backendOrigin:()=>process.env.ARTEX_TEST_ORIGIN,available:()=>true}); const dispatch=broker.dispatch.bind(broker);broker.dispatch=async(method,params,signal)=>{if(method==='fixture/quit'){setTimeout(async()=>{await broker.stop();console.log('ARTEX_BROWSER_CLOSED '+JSON.stringify({workers:broker.workers.size}));app.quit()},50);return {}};if(method==='fixture/status')return [...broker.workers.entries()].map(([id,s])=>({id,pid:s.window.webContents.getOSProcessId(),storagePath:s.ses.storagePath,webrtc:s.window.webContents.getWebRTCIPHandlingPolicy()}));if(method==='fixture/processes')return app.getAppMetrics().map(p=>p.pid);return dispatch(method,params,signal)};const state=await broker.start(); console.log('ARTEX_BROWSER_READY '+JSON.stringify(state));}).catch(e=>{console.error(e.stack);app.exit(1)});`
	if err := os.WriteFile(fixture, []byte(main), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, executable, fixture)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "ELECTRON_RUN_AS_NODE=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "ARTEX_TEST_HOME="+filepath.Join(home, "profile"), "ARTEX_TEST_TOKEN="+string(s.desktopSession), "ARTEX_TEST_ORIGIN="+origin)
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(filepath.Join(home, "electron-stderr.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	t.Cleanup(func() {
		data, _ := os.ReadFile(logFile.Name())
		_ = os.WriteFile(filepath.Join(filepath.Dir(filepath.Dir(executable)), "electron-integration-stderr.log"), data, 0600)
	})
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	completed := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(completed) }()
	defer func() {
		select {
		case <-completed:
			return
		default:
		}
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_, _ = s.browser.rpc(closeCtx, "fixture/quit", map[string]any{})
		select {
		case <-completed:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-completed
		}
	}()
	ready := make(chan string, 1)
	closed := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			t.Logf("Electron fixture: %s", line)
			if strings.HasPrefix(line, "ARTEX_BROWSER_READY ") {
				ready <- strings.TrimPrefix(line, "ARTEX_BROWSER_READY ")
			}
			if strings.HasPrefix(line, "ARTEX_BROWSER_CLOSED ") {
				closed <- line
			}
		}
	}()
	select {
	case raw := <-ready:
		var state struct {
			URL string `json:"url"`
		}
		if json.Unmarshal([]byte(raw), &state) != nil {
			t.Fatal(raw)
		}
		s.browser.control, _ = url.Parse(state.URL)
	case <-ctx.Done():
		t.Fatal("Electron did not become ready")
	case <-completed:
		logs, _ := os.ReadFile(logFile.Name())
		t.Fatalf("Electron exited: %v %s", waitErr, logs)
	}
	if ok, message := s.browser.health(ctx); !ok {
		select {
		case <-completed:
			t.Logf("Electron exited during health: %v (exitCode=%d)", waitErr, cmd.ProcessState.ExitCode())
		case <-time.After(500 * time.Millisecond):
			t.Log("Electron is still running after failed health")
		}
		logs, _ := os.ReadFile(logFile.Name())
		t.Fatalf("live renderer health failed: %s %s", message, logs)
	}
	for _, provided := range []string{strings.Repeat("가", 64), strings.Repeat("a", 65), strings.Repeat("a", 64)} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.browser.control.String()+"/rpc", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
		req.Header.Set(desktopSessionHeader, provided)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != 403 {
			t.Fatalf("malformed/main token accepted: %d", response.StatusCode)
		}
	}
	callCtx := agent.WithRunInfo(ctx, agent.RunInfo{TaskID: task.ID})
	client := &browserMCPClient{b: s.browser, ctx: callCtx, taskID: task.ID}
	defer client.Close()
	list, err := client.Tools(callCtx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list {
		if d := tool.CheckPermissions(callCtx, json.RawMessage(`{}`), permission.Context{}); d.Behavior != permission.Ask {
			t.Fatalf("%s did not require approval: %v", tool.Name(), d)
		}
	}
	call := func(name string, args any, wantError bool) actool.Result {
		t.Helper()
		raw, _ := json.Marshal(args)
		result := client.run(callCtx, name, raw, &actool.ToolContext{WorkingDir: work, MaxOutputChars: 200000})
		if result.IsError != wantError {
			logs, _ := os.ReadFile(logFile.Name())
			t.Fatalf("%s isError=%v, want=%v: %s\n%s", name, result.IsError, wantError, result.Flatten(), logs)
		}
		t.Logf("%s: %s", name, result.Flatten())
		return result
	}
	call("browser_navigate", map[string]any{"url": target.URL}, false)
	call("browser_evaluate", map[string]any{"function": `()=>'x'.repeat(100001)`}, true)
	call("browser_navigate", map[string]any{"url": target.URL}, false)
	rawStatus, err := s.browser.rpc(ctx, "fixture/status", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var status []struct {
		PID         int     `json:"pid"`
		StoragePath *string `json:"storagePath"`
	}
	if json.Unmarshal(rawStatus, &status) != nil || len(status) != 1 || status[0].StoragePath != nil {
		t.Fatalf("worker is not memory-only: %s", rawStatus)
	}
	native, err := toolruntime.VerifyBrowserRenderer(status[0].PID, executable)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("live post-navigation OS renderer: %+v", native)
	call("browser_type", map[string]any{"ref": "e1", "text": "한국어 입력"}, false)
	call("browser_click", map[string]any{"ref": "e2"}, false)
	if result := call("browser_wait_for", map[string]any{"text": "한국어 입력"}, false); !strings.Contains(result.Flatten(), "한국어 입력") {
		t.Fatal("real click/input did not change page")
	}
	call("browser_press_key", map[string]any{"key": "Tab"}, false)
	call("browser_snapshot", map[string]any{}, false)
	result := call("browser_evaluate", map[string]any{"function": `async()=>({node:typeof require, process:typeof process, cookie:document.cookie, echo:await(await fetch('/echo')).text(), worker:await new Promise(r=>{const w=new Worker('/worker.js');w.onmessage=e=>{w.terminate();r(e.data)};w.postMessage('start')})})`}, false)
	if !strings.Contains(result.Flatten(), `"node":"undefined"`) || !strings.Contains(result.Flatten(), "first=one") || !strings.Contains(result.Flatten(), "second=two") {
		t.Fatalf("sandbox/cookie/worker failure: %s", result.Flatten())
	}
	call("browser_screenshot", map[string]any{}, false)
	files, _ := filepath.Glob(filepath.Join(work, "browser-*.png"))
	if len(files) != 1 {
		t.Fatalf("screenshot did not use task workspace: %v", files)
	}
	image, _ := os.ReadFile(files[0])
	if err := os.WriteFile(filepath.Join(filepath.Dir(filepath.Dir(executable)), "browser-fixture.png"), image, 0600); err != nil {
		t.Fatal(err)
	}
	// A second task receives an independent memory profile even for the exact
	// same origin. Neither cookie nor localStorage transfers between workers.
	secondTask, err := m.pg.CreateTask("두 번째 가상 작업", "메모리 격리", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.assets.AddAgentScope(secondTask.ID, "ip", "127.0.0.1", "가상 대상", "manual"); err != nil {
		t.Fatal(err)
	}
	secondCtx := agent.WithRunInfo(ctx, agent.RunInfo{TaskID: secondTask.ID})
	secondClient := &browserMCPClient{b: s.browser, ctx: secondCtx, taskID: secondTask.ID}
	defer secondClient.Close()
	secondWork := filepath.Join(m.workspaceDir(), "tasks", strconv.FormatInt(secondTask.ID, 10))
	if err := os.MkdirAll(secondWork, 0700); err != nil {
		t.Fatal(err)
	}
	secondCall := func(name string, args any) actool.Result {
		t.Helper()
		raw, _ := json.Marshal(args)
		out := secondClient.run(secondCtx, name, raw, &actool.ToolContext{WorkingDir: secondWork, MaxOutputChars: 200000})
		if out.IsError {
			t.Fatal(out.Flatten())
		}
		return out
	}
	call("browser_evaluate", map[string]any{"function": `()=>{localStorage.setItem('owner','first');return true}`}, false)
	secondCall("browser_navigate", map[string]any{"url": target.URL + "/echo"})
	isolation := secondCall("browser_evaluate", map[string]any{"function": `()=>({cookie:document.cookie,owner:localStorage.getItem('owner'),text:document.body.innerText})`})
	if !strings.Contains(isolation.Flatten(), `"cookie":""`) || !strings.Contains(isolation.Flatten(), `"owner":null`) || !strings.Contains(isolation.Flatten(), `"text":""`) {
		t.Fatalf("cross-task profile leak: %s", isolation.Flatten())
	}
	rawStatus, err = s.browser.rpc(ctx, "fixture/status", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(rawStatus, &status) != nil || len(status) != 2 {
		t.Fatalf("two live task workers missing: %s", rawStatus)
	}
	markerA := filepath.Join(work, "marker.txt")
	markerB := filepath.Join(secondWork, "marker.txt")
	for _, path := range []string{markerA, markerB} {
		if err := os.WriteFile(path, []byte("synthetic task secret"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	browserRendererCannotCrossBoundary(t, status[0].PID, status[1].PID, []string{markerA, markerB, filepath.Join(home, "jwt.key")})
	browserRendererCannotCrossBoundary(t, status[1].PID, status[0].PID, []string{markerA, markerB, filepath.Join(home, "jwt.key")})
	t.Log("both actual lockdown renderer tokens denied other task process memory/handles/termination and both task files/host JWT fixture")
	if out := client.run(secondCtx, "browser_snapshot", json.RawMessage(`{}`), &actool.ToolContext{WorkingDir: work}); !out.IsError {
		t.Fatal("cross-task call owner was accepted")
	}
	secondCall("browser_close", map[string]any{})
	call("browser_evaluate", map[string]any{"function": `async()=>{const r=await fetch('/oversized',{method:'POST',body:new Uint8Array((8<<20)+1)});return r.status}`}, false)
	call("browser_evaluate", map[string]any{"function": `()=>{setTimeout(()=>fetch('/late').catch(()=>{}),100);return true}`}, false)
	time.Sleep(250 * time.Millisecond)
	requestsMu.Lock()
	for _, request := range requests {
		if strings.HasPrefix(request, "/oversized ") || strings.HasPrefix(request, "/late ") {
			t.Fatal("oversized or expired-lease request reached target")
		}
	}
	requestsMu.Unlock()
	call("browser_navigate", map[string]any{"url": target.URL + "/forbidden-status"}, false)
	call("browser_navigate", map[string]any{"url": target.URL + "/redirect"}, true)
	// Error closes the worker. A fresh call must reacquire its own verified
	// session; navigating to the app backend remains forbidden despite IP scope.
	call("browser_navigate", map[string]any{"url": origin + "/api/health"}, true)
	call("browser_navigate", map[string]any{"url": target.URL}, false)
	var udpPackets, turnConnections atomic.Int64
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	go func() {
		buffer := make([]byte, 4096)
		for {
			if _, _, err := udp.ReadFrom(buffer); err != nil {
				return
			}
			udpPackets.Add(1)
		}
	}()
	turn, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer turn.Close()
	go func() {
		for {
			connection, err := turn.Accept()
			if err != nil {
				return
			}
			turnConnections.Add(1)
			_ = connection.Close()
		}
	}()
	udpPort := udp.LocalAddr().(*net.UDPAddr).Port
	turnPort := turn.Addr().(*net.TCPAddr).Port
	rtc := `async()=>{const pc=new RTCPeerConnection({iceServers:[{urls:'stun:127.0.0.1:` + strconv.Itoa(udpPort) + `'},{urls:'turn:127.0.0.1:` + strconv.Itoa(turnPort) + `?transport=tcp',username:'fixture',credential:'fixture'}]});const candidates=[];pc.onicecandidate=e=>{if(e.candidate)candidates.push(e.candidate.candidate)};pc.createDataChannel('fixture');await pc.setLocalDescription(await pc.createOffer());await new Promise(r=>setTimeout(r,3000));pc.close();let ws=await new Promise(r=>{const s=new WebSocket('ws://127.0.0.1:` + strconv.Itoa(turnPort) + `');s.onerror=()=>r('blocked');s.onopen=()=>{s.close();r('OPEN')}});return {candidates,ws};}`
	result = call("browser_evaluate", map[string]any{"function": rtc}, false)
	if udpPackets.Load() != 0 || turnConnections.Load() != 0 || !strings.Contains(result.Flatten(), `"candidates":[]`) || !strings.Contains(result.Flatten(), `"ws":"blocked"`) {
		t.Fatalf("direct socket bypass: udp=%d turn=%d result=%s", udpPackets.Load(), turnConnections.Load(), result.Flatten())
	}
	call("browser_close", map[string]any{}, false)
	rawProcesses, err := s.browser.rpc(ctx, "fixture/processes", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	pids := []int{}
	if json.Unmarshal(rawProcesses, &pids) != nil {
		t.Fatal("process list invalid")
	}
	requestsMu.Lock()
	t.Logf("actual target exchanges: %v", requests)
	requestsMu.Unlock()
	if _, err := s.browser.rpc(ctx, "fixture/quit", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-closed:
		if !strings.Contains(message, `"workers":0`) {
			t.Fatal(message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("broker workers did not close")
	}
	select {
	case <-completed:
		if waitErr != nil {
			t.Fatal(waitErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Electron main process did not exit")
	}
	for _, pid := range pids {
		handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if err != nil {
			continue
		}
		var code uint32
		err = windows.GetExitCodeProcess(handle, &code)
		windows.CloseHandle(handle)
		if err == nil && code == 259 {
			t.Fatalf("Electron child PID %d remains running after main cleanup", pid)
		}
	}
	t.Logf("Electron main/utility/renderer cleanup: %d owned PIDs exited", len(pids))
}
