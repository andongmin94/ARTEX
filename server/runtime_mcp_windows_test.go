package server

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"golang.org/x/sys/windows"
)

func TestManagedMCPCallCancellationInterruptsPipeWriteAndGateWait(t *testing.T) {
	for _, scenario := range []string{"write", "gate"} {
		t.Run(scenario, func(t *testing.T) {
			managedBundleEnvironment(t)
			code := `const readline=require('node:readline'); const childProcess=require('node:child_process');
const lines=readline.createInterface({input:process.stdin});
lines.on('line',line=>{
 const m=JSON.parse(line); let result={};
 if(m.method==='initialize')result={protocolVersion:'2025-06-18',capabilities:{tools:{}},serverInfo:{name:'blocked',version:'1'}};
 else if(m.method==='tools/list'){
  const child=childProcess.spawn(process.execPath,['-e','setInterval(()=>{},10000)'],{stdio:'ignore',detached:true});
  result={tools:[{name:'blocked',description:'grandchild:'+child.pid,inputSchema:{type:'object'}}]};
  process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:m.id,result})+'\n');
  lines.close(); process.stdin.pause(); process.stdin.removeAllListeners('data'); return;
 } else if(m.id===undefined)return;
 process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:m.id,result})+'\n');
}); setInterval(()=>{},10000);`
			args, _ := json.Marshal([]string{"-e", code})
			ownerCtx, ownerCancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer ownerCancel()
			client, err := connectMCP(ownerCtx, &db.MCPServer{ID: 456, Name: "blocked", Transport: "stdio", Command: "node", Args: args})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			c := client.(*managedMCPClient)
			tools, err := client.Tools(ownerCtx)
			if err != nil || len(tools) != 1 {
				t.Fatal(err, len(tools))
			}
			childPID, err := strconv.Atoi(strings.TrimPrefix(tools[0].Description(), "grandchild:"))
			if err != nil {
				t.Fatal(err, tools[0].Description())
			}
			large := map[string]any{"name": "blocked", "arguments": map[string]any{"payload": strings.Repeat("x", 4<<20)}}
			var writerDone chan error
			if scenario == "gate" {
				writerDone = make(chan error, 1)
				go func() { _, err := c.call(ownerCtx, "tools/call", large); writerDone <- err }()
				deadline := time.Now().Add(3 * time.Second)
				for len(c.writeGate) != 0 {
					if time.Now().After(deadline) {
						t.Fatal("first writer did not acquire gate")
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			callCtx, callCancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer callCancel()
			params := large
			if scenario == "gate" {
				params = map[string]any{"name": "blocked", "arguments": map[string]any{}}
			}
			started := time.Now()
			_, err = c.call(callCtx, "tools/call", params)
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 5*time.Second {
				t.Fatalf("call did not promptly cancel: elapsed=%s err=%v", time.Since(started), err)
			}
			if ownerCtx.Err() != nil {
				t.Fatal("test canceled the connection owner instead of the individual call")
			}
			if writerDone != nil {
				select {
				case err := <-writerDone:
					if err == nil {
						t.Fatal("blocked writer succeeded after connection cancellation")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("blocked writer goroutine survived")
				}
			}
			for name, done := range map[string]<-chan struct{}{"reader": c.readDone, "stderr": c.stderrDone, "waiter": c.waitDone} {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal(name + " goroutine survived")
				}
			}
			c.mu.Lock()
			pendingCount := len(c.pending)
			closed := c.closed
			c.mu.Unlock()
			if !closed || pendingCount != 0 {
				t.Fatalf("connection retained pending requests: closed=%t pending=%d", closed, pendingCount)
			}
			for _, pid := range []int{c.process.PID, childPID} {
				h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
				if err == windows.ERROR_INVALID_PARAMETER {
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				state, waitErr := windows.WaitForSingleObject(h, 5000)
				windows.CloseHandle(h)
				if waitErr != nil || state != windows.WAIT_OBJECT_0 {
					t.Fatalf("managed descendant %d survived: %d %v", pid, state, waitErr)
				}
			}
		})
	}
}
