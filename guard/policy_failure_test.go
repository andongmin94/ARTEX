package guard

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
)

func TestPolicyFailureBlocksExecution(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*testing.T, *db.DB)
	}{
		{"closed store", func(t *testing.T, store *db.DB) {
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
		}},
		{"invalid tool policy", func(t *testing.T, store *db.DB) {
			if err := store.SetSetting("intercept_enabled_tools", "invalid-json"); err != nil {
				t.Fatal(err)
			}
		}},
		{"invalid judge flag", func(t *testing.T, store *db.DB) {
			if err := store.SetSetting("llm_judge_enabled", "broken"); err != nil {
				t.Fatal(err)
			}
		}},
		{"judge unavailable", func(t *testing.T, store *db.DB) {
			if err := store.SetSetting("llm_judge_enabled", "true"); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, err := db.Open(filepath.Join(t.TempDir(), "artex.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if _, err := store.Exec(`DELETE FROM intercept_rules`); err != nil {
				t.Fatal(err)
			}
			test.prepare(t, store)
			guard := NewWithInterceptor(intercept.New(store))
			blocked, message, _ := guard.Hooks().PreToolUse(context.Background(), "Bash", json.RawMessage(`{"command":"echo fixture"}`))
			if !blocked || message == "" {
				t.Fatalf("unavailable policy allowed execution: blocked=%v message=%q", blocked, message)
			}
		})
	}
}

func TestDisabledJudgeKeepsConfiguredRuleBehavior(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "artex.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.Exec(`DELETE FROM intercept_rules`); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting("llm_judge_enabled", "false"); err != nil {
		t.Fatal(err)
	}
	guard := NewWithInterceptor(intercept.New(store))
	blocked, _, _ := guard.Hooks().PreToolUse(context.Background(), "Bash", json.RawMessage(`{"command":"echo fixture"}`))
	if blocked {
		t.Fatal("valid unmatched rules with a disabled judge should retain configured behavior")
	}
}
