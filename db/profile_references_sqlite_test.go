package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// Use the actual business opener, complete schema and seeds, not a reduced DDL.
func newProfileReferenceDB(t *testing.T) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "모델 참조 #%.sqlite")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d, path
}
func referenceProfile(t *testing.T, d *DB, name string) int64 {
	t.Helper()
	id, err := d.SaveProfile(&LLMProfile{Name: name, Format: "openai", Model: "local-fixture", APIKey: "test-only-key", Streaming: true})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func referenceTask(t *testing.T, d *DB, profiles []int64, active *int64) int64 {
	t.Helper()
	var exp, id int64
	if err := d.QueryRow(`INSERT INTO explorations(goal) VALUES ('local fixture') RETURNING id`).Scan(&exp); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`INSERT INTO tasks(description,goal,exploration_id,llm_profile_id,active_llm_profile_id)
VALUES ('fixture','fixture',$1,$2,$2) RETURNING id`, exp, active).Scan(&id); err != nil {
		t.Fatal(err)
	}
	for i, p := range profiles {
		if _, err := d.Exec(`INSERT INTO task_llm_profiles(task_id,profile_id,position) VALUES ($1,$2,$3)`, id, p, i); err != nil {
			t.Fatal(err)
		}
	}
	return id
}
func referenceCursor(t *testing.T, d *DB, task int64) (sql.NullInt64, int64) {
	t.Helper()
	var active sql.NullInt64
	var rev int64
	if err := d.QueryRow(`SELECT active_llm_profile_id,llm_chain_revision FROM tasks WHERE id=$1`, task).Scan(&active, &rev); err != nil {
		t.Fatal(err)
	}
	return active, rev
}

func TestSQLiteProfileReferenceSuccessorAndHistory(t *testing.T) {
	d, _ := newProfileReferenceDB(t)
	ids := []int64{referenceProfile(t, d, "earlier"), referenceProfile(t, d, "deleted"), referenceProfile(t, d, "exhausted"), referenceProfile(t, d, "later")}
	task := referenceTask(t, d, ids, &ids[1])
	if _, err := d.Exec(`UPDATE task_llm_profiles SET status='quota_exhausted' WHERE profile_id=$1`, ids[2]); err != nil {
		t.Fatal(err)
	}
	if err := d.SetAgentLLMProfile("worker", &ids[1]); err != nil {
		t.Fatal(err)
	}
	conv, err := d.CreateConversation("worker", "보존할 대화", &ids[1])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO conversation_activities(conversation_id,summary) VALUES ($1,'keep')`, conv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO llm_profile_health(profile_id) VALUES ($1)`, ids[1]); err != nil {
		t.Fatal(err)
	}
	// All nested work must stay on its transaction, even with only one connection.
	d.SetMaxOpenConns(1)
	if err := d.DeleteProfile(ids[1]); err != nil {
		t.Fatal(err)
	}
	active, rev := referenceCursor(t, d, task)
	if !active.Valid || active.Int64 != ids[3] || rev != 1 {
		t.Fatalf("cursor=%v revision=%d", active, rev)
	}
	a, err := d.GetAgentByKey("worker")
	if err != nil || a.LLMProfileID != nil {
		t.Fatalf("agent=%+v err=%v", a, err)
	}
	c, err := d.GetConversation(conv.ID)
	if err != nil || c == nil || c.LLMProfileID != nil {
		t.Fatalf("conversation=%+v err=%v", c, err)
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM conversation_activities WHERE conversation_id=$1`, conv.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("history=%d err=%v", count, err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM llm_profile_health WHERE profile_id=$1`, ids[1]).Scan(&count); err != nil || count != 0 {
		t.Fatalf("health=%d err=%v", count, err)
	}
}

func TestSQLiteProfileReferenceNoRewindAndDirectRevision(t *testing.T) {
	d, _ := newProfileReferenceDB(t)
	a, b := referenceProfile(t, d, "a"), referenceProfile(t, d, "b")
	last := referenceTask(t, d, []int64{a, b}, &b)
	direct := referenceTask(t, d, nil, &b)
	inactive := referenceTask(t, d, []int64{a, b}, &a)
	unrelated := referenceTask(t, d, []int64{a}, &a)
	if err := d.DeleteProfile(b); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{last, direct} {
		active, rev := referenceCursor(t, d, id)
		if active.Valid || rev != 1 {
			t.Fatalf("task=%d cursor=%v rev=%d", id, active, rev)
		}
		var count int
		if err := d.QueryRow(`SELECT count(*) FROM task_llm_profiles WHERE task_id=$1`, id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("chain=%d err=%v", count, err)
		}
	}
	for _, tc := range []struct{ id, rev int64 }{{inactive, 1}, {unrelated, 0}} {
		active, rev := referenceCursor(t, d, tc.id)
		if !active.Valid || active.Int64 != a || rev != tc.rev {
			t.Fatalf("task=%d cursor=%v rev=%d", tc.id, active, rev)
		}
	}
}

func TestSQLiteProfileReferenceProtectionAndRollback(t *testing.T) {
	d, _ := newProfileReferenceDB(t)
	a, b := referenceProfile(t, d, "a"), referenceProfile(t, d, "b")
	if err := d.SetActiveProfile(a); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteProfile(a); !errors.Is(err, ErrActiveLLMProfileDelete) {
		t.Fatalf("active: %v", err)
	}
	if err := d.DeleteProfile(1 << 50); !errors.Is(err, ErrLLMProfileNotFound) {
		t.Fatalf("missing: %v", err)
	}
	task := referenceTask(t, d, []int64{a, b}, &b)
	if err := d.SetAgentLLMProfile("worker", &b); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`CREATE TRIGGER fail_reference_revision BEFORE UPDATE OF llm_chain_revision ON tasks BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteProfile(b); err == nil {
		t.Fatal("expected rollback")
	}
	p, err := d.ProfileByID(b)
	if err != nil || p == nil {
		t.Fatalf("profile lost: %v", err)
	}
	active, rev := referenceCursor(t, d, task)
	if !active.Valid || active.Int64 != b || rev != 0 {
		t.Fatalf("cursor=%v rev=%d", active, rev)
	}
	ag, err := d.GetAgentByKey("worker")
	if err != nil || ag.LLMProfileID == nil || *ag.LLMProfileID != b {
		t.Fatalf("binding lost: %v", err)
	}
}

func TestSQLiteProfileReferenceBindingValidation(t *testing.T) {
	d, _ := newProfileReferenceDB(t)
	p := referenceProfile(t, d, "valid")
	missing := int64(1 << 50)
	if c, err := d.CreateConversation("worker", "bad", &missing); c != nil || !errors.Is(err, ErrLLMProfileNotFound) {
		t.Fatalf("bad create: %+v %v", c, err)
	}
	all, err := d.ListConversations()
	if err != nil || len(all) != 0 {
		t.Fatalf("orphan conversations: %d %v", len(all), err)
	}
	c, err := d.CreateConversation("worker", "good", &p)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.UpdateConversationProfile(c.ID, &missing); !errors.Is(err, ErrLLMProfileNotFound) {
		t.Fatal(err)
	}
	if err := d.SetAgentLLMProfile("worker", &missing); !errors.Is(err, ErrLLMProfileNotFound) {
		t.Fatal(err)
	}
	if err := d.UpdateConversationProfile(c.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetConversation(c.ID)
	if err != nil || got.LLMProfileID != nil {
		t.Fatalf("clear: %+v %v", got, err)
	}
	if err := d.SetAgentLLMProfile("worker", &p); err != nil {
		t.Fatal(err)
	}
	if err := d.SetAgentLLMProfile("worker", nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.DeleteProfileContext(ctx, p); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if got, err := d.ProfileByID(p); err != nil || got == nil {
		t.Fatalf("canceled deletion changed profile: %v", err)
	}
}

func TestSQLiteProfileReferenceConcurrentDeleteAndBind(t *testing.T) {
	d, _ := newProfileReferenceDB(t)
	for round := 0; round < 8; round++ {
		p := referenceProfile(t, d, fmt.Sprint("race-", round))
		c, err := d.CreateConversation("worker", "race", nil)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		result := make(chan error, 3)
		var wg sync.WaitGroup
		for _, fn := range []func() error{func() error { return d.DeleteProfile(p) }, func() error { return d.SetAgentLLMProfile("worker", &p) }, func() error { return d.UpdateConversationProfile(c.ID, &p) }} {
			wg.Add(1)
			go func(fn func() error) { defer wg.Done(); <-start; result <- fn() }(fn)
		}
		close(start)
		wg.Wait()
		close(result)
		for err := range result {
			if err != nil && !errors.Is(err, ErrLLMProfileNotFound) {
				t.Fatal(err)
			}
		}
		ag, err := d.GetAgentByKey("worker")
		if err != nil || ag.LLMProfileID != nil {
			t.Fatalf("dangling agent: %v", err)
		}
		got, err := d.GetConversation(c.ID)
		if err != nil || got.LLMProfileID != nil {
			t.Fatalf("dangling conversation: %v", err)
		}
	}
}

func TestSQLiteConversationReferencesMetadataAndBatch(t *testing.T) {
	d, _ := newProfileReferenceDB(t)
	p := referenceProfile(t, d, "model")
	c, err := d.CreateConversation("worker", "처음", &p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE conversations SET updated_at='2000-01-01 00:00:00.000' WHERE id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	title, pin := "변경", true
	updated, err := d.UpdateConversation(c.ID, ConversationPatch{Title: &title, Pinned: &pin})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := d.GetConversation(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != title || !updated.Pinned || updated.UpdatedAt.Year() == 2000 || !updated.UpdatedAt.Equal(reloaded.UpdatedAt) {
		t.Fatalf("stale mutation: %+v stored=%+v", updated, reloaded)
	}
	again, err := d.UpdateConversation(c.ID, ConversationPatch{Pinned: &pin})
	if err != nil || !again.PinnedAt.Equal(*updated.PinnedAt) {
		t.Fatalf("pin order changed: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO conversation_activities(conversation_id,kind,input_tokens,output_tokens) VALUES ($1,'result',12,3)`, c.ID); err != nil {
		t.Fatal(err)
	}
	summaries, err := d.ConversationTokenSummaries()
	if err != nil || len(summaries) != 1 || summaries[0].InputTokens != 12 {
		t.Fatalf("summaries=%+v err=%v", summaries, err)
	}
	deleted, err := d.DeleteConversations([]int64{c.ID, c.ID, 1 << 50})
	if err != nil || len(deleted) != 1 || deleted[0] != c.ID {
		t.Fatalf("deleted=%v err=%v", deleted, err)
	}
	if got, err := d.GetConversation(c.ID); err != nil || got != nil {
		t.Fatalf("still exists: %+v %v", got, err)
	}
}

func TestSQLiteProfileReferenceReopen(t *testing.T) {
	d, path := newProfileReferenceDB(t)
	a, b := referenceProfile(t, d, "a"), referenceProfile(t, d, "b")
	task := referenceTask(t, d, []int64{a, b}, &a)
	c, err := d.CreateConversation("worker", "재시작", &a)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteProfile(a); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	active, rev := referenceCursor(t, d, task)
	if !active.Valid || active.Int64 != b || rev != 1 {
		t.Fatalf("cursor=%v rev=%d", active, rev)
	}
	got, err := d.GetConversation(c.ID)
	if err != nil || got == nil || got.LLMProfileID != nil || got.Title != "재시작" {
		t.Fatalf("conversation=%+v err=%v", got, err)
	}
	var integrity string
	if err := d.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%s err=%v", integrity, err)
	}
	rows, err := d.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
