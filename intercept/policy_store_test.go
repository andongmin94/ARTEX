package intercept

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestJudgePolicyWriteFailureRollsBackCompleteConfiguration(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "artex.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	interceptor := New(store)
	if err := interceptor.SetJudgeConfig(JudgeConfig{Enabled: false, FailAction: "deny", AskTimeoutAction: "deny", TimeoutSeconds: 15, AskTimeoutSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	before, err := store.SettingsSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`CREATE TRIGGER reject_judge_policy BEFORE INSERT ON settings WHEN NEW.key='llm_judge_timeout_seconds' BEGIN SELECT RAISE(ABORT,'policy write rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := interceptor.SetJudgeConfig(JudgeConfig{Enabled: true, FailAction: "allow", AskTimeoutAction: "allow", TimeoutSeconds: 30, AskTimeoutSeconds: 60}); err == nil {
		t.Fatal("injected policy write failure was ignored")
	}
	after, err := store.SettingsSnapshot(t.Context())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed policy write partially enabled execution: %v", err)
	}
}
