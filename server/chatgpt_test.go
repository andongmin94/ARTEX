package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/internal/chatgpt"
)

func TestSubscriptionLoginOwnedByServerAndNoCredentialDTO(t *testing.T) {
	s, _ := newRetestServer(t)
	client, err := chatgpt.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.chatGPT = client
	t.Cleanup(func() { client.Close() })
	requestCtx, cancel := context.WithCancel(t.Context())
	w := httptest.NewRecorder()
	s.chatGPTLogin(w, httptest.NewRequest("POST", "/api/chatgpt/login", nil).WithContext(requestCtx))
	cancel()
	if w.Code != 200 || !client.Status().Pending {
		t.Fatalf("request context ended login: %d pending=%v", w.Code, client.Status().Pending)
	}
	var body struct {
		URL string `json:"authorization_url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	authURL, err := url.Parse(body.URL)
	if err != nil || authURL.Scheme != "https" || authURL.Host != "auth.openai.com" || authURL.Path != "/api/accounts/authorize" {
		t.Fatal("incorrect authorization endpoint")
	}
	for _, key := range []string{"id_token_hint", "access_token", "refresh_token", "client_secret"} {
		if authURL.Query().Has(key) {
			t.Errorf("credential query %s", key)
		}
	}
	w = httptest.NewRecorder()
	s.chatGPTStatus(w, httptest.NewRequest("GET", "/api/chatgpt/status", nil))
	var status map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"access_token", "refresh_token", "id_token", "client_id", "subject", "host_id"} {
		if _, ok := status[field]; ok {
			t.Errorf("credential field %s", field)
		}
	}
	w = httptest.NewRecorder()
	s.chatGPTCancel(w, httptest.NewRequest("POST", "/api/chatgpt/cancel", nil))
	if w.Code != 200 || client.Status().Pending {
		t.Fatal("login listener not cancelled")
	}
	for _, handler := range []http.HandlerFunc{s.chatGPTModels, s.chatGPTProfile} {
		w = httptest.NewRecorder()
		handler(w, httptest.NewRequest("POST", "/api/chatgpt/profile", strings.NewReader(`{"model":"ungranted-model"}`)))
		if w.Code != 409 {
			t.Fatalf("unconnected model accepted: %d", w.Code)
		}
	}
}

func TestSubscriptionBindingAndPinNeverUseAPIKeyFallback(t *testing.T) {
	s, _ := newRetestServer(t)
	id, err := s.m.pg.SaveProfile(&db.LLMProfile{Name: "subscription", AuthMethod: "chatgpt", Format: "openai-responses", Model: "fixture", Streaming: true})
	if err != nil {
		t.Fatal(err)
	}
	pt, err := s.m.pg.CreateTask("isolated fixture", "check unavailable subscription", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	task := &Task{ID: i64s(pt.ID)}
	s.m.tasks[task.ID] = task
	fallback := &scriptedLLMProvider{}
	s.llmOn, s.llmProv = true, fallback
	s.chatAgent = agent.NewChatAgent(fallback, "paid-fixture", s.m.dir, nil, 10000)
	if _, err := s.m.pg.Exec(`UPDATE agents SET llm_profile_id=?1 WHERE key='worker'`, id); err != nil {
		t.Fatal(err)
	}
	runtime := &taskLLMRuntime{s: s, taskID: task.ID, agentKey: "worker"}
	if selection, err := runtime.current(); err == nil || selection.provider != nil {
		t.Fatalf("subscription binding used global API fallback: %+v %v", selection, err)
	}
	if s.taskRuntimeAvailable(task, "worker") {
		t.Fatal("unavailable subscription reported runnable")
	}
	resolved, err := s.resolveTaskRoleLLM(task, "worker")
	if err != nil || resolved.Available || resolved.Source != "agent_binding" {
		t.Fatalf("resolution fell back: %+v %v", resolved, err)
	}
	if got := s.resolveChatAgent(&db.Conversation{LLMProfileID: &id}); got != nil {
		t.Fatal("subscription pin used global API chat")
	}
	if got := s.resolveChatAgent(&db.Conversation{AgentKey: "worker"}); got != nil {
		t.Fatal("subscription binding used global API chat")
	}
	if fallback.calls != 0 {
		t.Fatal("paid fallback was invoked")
	}
}

func TestSubscriptionProfilesRequireAuthAndDedicatedEditor(t *testing.T) {
	s, _ := newRetestServer(t)
	p := &db.LLMProfile{Name: "subscription", AuthMethod: "chatgpt", Format: "openai-responses", Model: "fixture", Streaming: true}
	id, err := s.m.pg.SaveProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	p.ID = id
	resolved := s.resolutionFromProfile(p, "task_chain")
	if resolved.Available || !strings.Contains(resolved.Reason, "ChatGPT") {
		t.Fatalf("subscription accepted without login: %+v", resolved)
	}
	if _, ok := s.loadProfileConfig(id); ok {
		t.Fatal("subscription configured without authorization")
	}
	for _, body := range []string{`{"name":"new","auth_method":"chatgpt","format":"openai-responses","model":"fixture"}`, `{"id":` + i64s(id) + `,"name":"overwrite","format":"openai","model":"other","api_key":"new-key"}`} {
		w := httptest.NewRecorder()
		s.pgSaveProfile(w, httptest.NewRequest("POST", "/api/llm/profiles", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("generic editor changed subscription: %d", w.Code)
		}
	}
}
