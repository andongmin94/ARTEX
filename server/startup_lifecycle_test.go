package server

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

func TestServerStartupPropagatesRequiredStoreFailures(t *testing.T) {
	for _, table := range []string{"side_question_requests", "server_logs", "tasks", "task_archives"} {
		t.Run(table, func(t *testing.T) {
			m, err := NewManager(t.TempDir(), "")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.Close() })
			if _, err := m.pg.Exec(`DROP TABLE ` + table); err != nil {
				t.Fatal(err)
			}
			s, err := New(context.Background(), m, t.TempDir(), t.TempDir(), t.TempDir())
			if err == nil || s != nil {
				if s != nil {
					_ = s.Close(context.Background())
				}
				t.Fatalf("missing required %s accepted: server=%v err=%v", table, s, err)
			}
		})
	}
}

func TestServerStartupReturnsJWTKeyFailure(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	keyDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(keyDir, "jwt.key"), 0700); err != nil {
		t.Fatal(err)
	}
	s, err := New(context.Background(), m, t.TempDir(), t.TempDir(), keyDir)
	if err == nil || s != nil {
		t.Fatalf("invalid key path accepted: %v %v", s, err)
	}
}

func TestManagerPolicyFailureClosesBusinessStore(t *testing.T) {
	dir := t.TempDir()
	store, err := db.Open(filepath.Join(dir, "artex.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SetSetting("intercept_enabled_tools", "invalid-json"); err != nil {
		t.Fatal(err)
	}
	m, err := newManagerFromDB(dir, "", store)
	if err == nil || m != nil {
		t.Fatalf("invalid execution policy accepted: %v %v", m, err)
	}
	if err := store.Ping(); err == nil {
		t.Fatal("failed manager startup left database open")
	}
}

func TestManagerProxyBindFailureClosesBusinessStore(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	dir := t.TempDir()
	store, err := db.Open(filepath.Join(dir, "artex.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m, err := newManagerFromDB(dir, listener.Addr().String(), store)
	if err == nil || m != nil || !strings.Contains(err.Error(), "프록시 시작") {
		t.Fatalf("occupied proxy accepted: %v %v", m, err)
	}
	if err := store.Ping(); err == nil {
		t.Fatal("proxy startup failure left database open")
	}
}

type policyProbeTool struct {
	actool.CoreTool
	called *bool
}

func (t policyProbeTool) Call(context.Context, json.RawMessage, *actool.ToolContext) (actool.Result, error) {
	*t.called = true
	return actool.Result{}, nil
}

func TestToolCatalogFailureBlocksUnderlyingCall(t *testing.T) {
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
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	called := false
	resolved := agent.ToolResolve(context.Background(), "worker", []actool.CoreTool{policyProbeTool{CoreTool: actool.NewBash(), called: &called}})
	if len(resolved) != 1 {
		t.Fatal("failure must retain an explicit blocking tool instead of triggering SDK default tools")
	}
	result, err := resolved[0].Call(context.Background(), json.RawMessage(`{}`), nil)
	if called || err != nil || !result.IsError {
		t.Fatalf("policy failure executed tool: called=%v result=%+v err=%v", called, result, err)
	}
}
