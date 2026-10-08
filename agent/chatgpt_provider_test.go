package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/llmrec"
	"github.com/Autumn-27/norma/llm"
)

type subscriptionFixtureToken struct {
	err   error
	calls int
}

func (s *subscriptionFixtureToken) AccessToken(context.Context) (string, error) {
	s.calls++
	return "fixture-oauth-token", s.err
}

func subscriptionFixtureProvider(t *testing.T, source *subscriptionFixtureToken, handler http.HandlerFunc) *chatGPTProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	cfg := Config{Format: llm.FormatOpenAIResponses, Model: "fixture-model", ChatGPT: source, Retry: RetryConfig{ConnectAttempts: -1}}
	provider, err := newChatGPTProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p := provider.(*chatGPTProvider)
	// Test-only endpoint injection; production configuration cannot override it.
	p.endpoint, p.client = srv.URL, srv.Client()
	return p
}

func TestChatGPTWireHistoryAndCompletion(t *testing.T) {
	token := &subscriptionFixtureToken{}
	p := subscriptionFixtureProvider(t, token, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-oauth-token" {
			t.Error("missing OAuth authorization")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["store"] != false || body["stream"] != true || body["instructions"] != "policy\n\ncontext" {
			t.Errorf("incorrect contract: %v", body)
		}
		for _, key := range []string{"max_output_tokens", "temperature", "previous_response_id", "conversation", "metadata", "user"} {
			if _, ok := body[key]; ok {
				t.Errorf("unsupported field %s", key)
			}
		}
		input := body["input"].([]any)
		if len(input) != 4 || input[1].(map[string]any)["namespace"] != "artex" || input[2].(map[string]any)["type"] != "function_call_output" {
			t.Errorf("history not retained: %v", input)
		}
		tools := body["tools"].([]any)
		if tools[0].(map[string]any)["type"] != "namespace" {
			t.Error("tools not namespaced")
		}
		w.Header().Set("Content-Type", "Text/Event-Stream; charset=utf-8")
		fmt.Fprint(w, "data: {\"type\":\"response.created\"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"확인\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":13,\"output_tokens\":2,\"input_tokens_details\":{\"cached_tokens\":4}}}}\n\n")
	})
	temp := 0.5
	request := llm.CompletionRequest{System: []string{"policy", "context"}, MaxTokens: 99, Temperature: &temp,
		Messages: []llm.Message{llm.UserText("read"), {Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: "call-old", Name: "Read", Input: json.RawMessage(`{"path":"copy.java"}`)}}}, {Role: llm.RoleUser, Content: []llm.ContentBlock{llm.ToolResultText("call-old", "source", false)}}, llm.UserText("explain")},
		Tools:    []llm.ToolSchema{{Name: "Read", InputSchema: map[string]any{"type": "object"}}}}
	msg, stop, usage, err := p.Complete(t.Context(), request)
	if err != nil || msg.Text() != "확인" || stop != "end_turn" || usage.InputTokens != 13 || usage.CacheReadTokens != 4 || token.calls != 1 {
		t.Fatalf("completion=%+v %s %+v %v calls=%d", msg, stop, usage, err, token.calls)
	}
}

func TestChatGPTStreamRequiresCompleted(t *testing.T) {
	for _, tc := range []struct{ name, payload, want string }{
		{"eof", `{"type":"response.output_text.delta","delta":"partial"}`, "response.completed"},
		{"failed", `{"type":"response.failed","response":{"error":{"code":"usage_limit_reached","message":"shared allowance exhausted"}}}`, "usage_limit_reached"},
		{"incomplete", `{"type":"response.incomplete","response":{"status":"incomplete"}}`, "response.incomplete"},
		{"malformed", `{`, "invalid SSE"},
		{"done", `[DONE]`, "response.completed"},
		{"wrong-status", `{"type":"response.completed","response":{"status":"incomplete"}}`, "not completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := subscriptionFixtureProvider(t, &subscriptionFixtureToken{}, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: %s\n\n", tc.payload)
			})
			msg, _, _, err := p.Complete(t.Context(), llm.CompletionRequest{})
			if err == nil || !strings.Contains(err.Error(), tc.want) || len(msg.Content) != 0 {
				t.Fatalf("unexpected success/partial result: %+v %v", msg, err)
			}
		})
	}
}

func TestChatGPTNonSSECapturesBoundedDiagnosticAndRejects(t *testing.T) {
	for _, tc := range []struct{ name, contentType, body, wantType string }{
		{"json", "application/json; diagnostic=fixture-oauth-token", `{"status":"completed","output":"private-response-body"}`, "application/json"},
		{"html", "text/html; charset=utf-8", "<html>private-response-body</html>", "text/html"},
		{"malformed", `text/event-stream; charset="unfinished`, "private-response-body", "(invalid)"},
		{"wrong-sse-prefix", "text/event-streaming", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", "text/event-streaming"},
		{"bounded", "application/json", "private-response-body" + strings.Repeat("x", 70<<10), "application/json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := &subscriptionFixtureToken{}
			p := subscriptionFixtureProvider(t, token, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-oauth-token" {
					t.Error("missing OAuth authorization")
				}
				// A nil value prevents net/http's automatic content sniffing.
				w.Header()["Content-Type"] = nil
				if tc.contentType != "" {
					w.Header().Set("Content-Type", tc.contentType)
				}
				fmt.Fprint(w, tc.body)
			})
			// Exercise the production transport and tee, not a synthetic capture.
			p.client.Transport = quotaAwareTransport{base: p.client.Transport}
			ctx, capture := llmrec.NewCapture(t.Context())
			msg, _, _, err := p.Complete(ctx, llm.CompletionRequest{})
			if err == nil || !strings.Contains(err.Error(), "media type: "+tc.wantType) || len(msg.Content) != 0 {
				t.Fatalf("unexpected non-SSE result: %+v %v", msg, err)
			}
			if strings.Contains(err.Error(), "private-response-body") || strings.Contains(err.Error(), "fixture-oauth-token") {
				t.Fatal("private body or header parameter escaped into public error")
			}
			wantBody := tc.body[:min(len(tc.body), 64<<10)]
			attempts := capture.Attempts()
			if len(attempts) != 1 || attempts[0].Status != http.StatusOK || attempts[0].Body != wantBody || token.calls != 1 {
				t.Fatalf("diagnostic missing, unbounded, or retried: attempts=%d body=%d calls=%d", len(attempts), len(capture.RawResponse()), token.calls)
			}
			if strings.Contains(capture.RawRequest(), "fixture-oauth-token") || strings.Contains(capture.RawResponse(), "fixture-oauth-token") {
				t.Fatal("OAuth authorization was recorded")
			}
		})
	}
}

func TestChatGPTMissingContentTypeRequiresSSECompletion(t *testing.T) {
	validSSE := "event: response.created\ndata: {\"type\":\"response.created\"}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"확인\"}\n\n" +
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item-1\",\"type\":\"function_call\",\"namespace\":\"artex\",\"call_id\":\"call-1\",\"name\":\"Read\"}}\n\n" +
		"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"item-1\",\"delta\":\"{\\\"path\\\":\\\"fixture.txt\\\"}\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":13,\"output_tokens\":2,\"input_tokens_details\":{\"cached_tokens\":4}}}}\n\n"
	for _, tc := range []struct{ name, body, wantError string }{
		{"valid-sse", validSSE, ""},
		{"json", `{"type":"response.completed","response":{"status":"completed"},"output":"private-response-body"}`, "response.completed"},
		{"html", "<html>private-response-body</html>", "response.completed"},
		{"empty", "", "response.completed"},
		{"truncated", "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"private-response-body\"}\n\n", "response.completed"},
		{"malformed-data", "data: {\"private-response-body\":\n\n", "invalid SSE data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := &subscriptionFixtureToken{}
			p := subscriptionFixtureProvider(t, token, func(w http.ResponseWriter, r *http.Request) {
				// Match the official endpoint's observed absence of Content-Type.
				w.Header()["Content-Type"] = nil
				fmt.Fprint(w, tc.body)
			})
			p.client.Transport = quotaAwareTransport{base: p.client.Transport}
			ctx, capture := llmrec.NewCapture(t.Context())
			msg, stop, usage, err := p.Complete(ctx, llm.CompletionRequest{Tools: []llm.ToolSchema{{Name: "Read"}}})
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || len(msg.Content) != 0 {
					t.Fatalf("non-SSE or incomplete body accepted: %+v %v", msg, err)
				}
				if strings.Contains(err.Error(), "private-response-body") || strings.Contains(err.Error(), "fixture-oauth-token") {
					t.Fatal("private response escaped into public error")
				}
			} else {
				uses := msg.ToolUses()
				if err != nil || msg.Text() != "확인" || stop != "tool_use" || usage.InputTokens != 13 || usage.OutputTokens != 2 || usage.CacheReadTokens != 4 || len(uses) != 1 {
					t.Fatalf("headerless SSE completion=%+v %s %+v %v", msg, stop, usage, err)
				}
				if uses[0].ID != "call-1" || uses[0].Name != "Read" || string(uses[0].Input) != `{"path":"fixture.txt"}` {
					t.Fatalf("headerless tool identity or arguments lost: %+v", uses)
				}
			}
			attempts := capture.Attempts()
			if len(attempts) != 1 || attempts[0].Status != http.StatusOK || attempts[0].Body != tc.body || token.calls != 1 {
				t.Fatalf("headerless capture lost or retried: attempts=%d body=%d calls=%d", len(attempts), len(capture.RawResponse()), token.calls)
			}
			if strings.Contains(capture.RawRequest(), "fixture-oauth-token") || strings.Contains(capture.RawResponse(), "fixture-oauth-token") {
				t.Fatal("OAuth authorization was recorded")
			}
		})
	}
}

func TestChatGPTInterleavedToolArguments(t *testing.T) {
	p := subscriptionFixtureProvider(t, &subscriptionFixtureToken{}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range []string{
			`{"type":"response.output_item.added","item":{"id":"i1","type":"function_call","namespace":"artex","call_id":"c1","name":"Read"}}`,
			`{"type":"response.output_item.added","item":{"id":"i2","type":"function_call","namespace":"artex","call_id":"c2","name":"LS"}}`,
			`{"type":"response.function_call_arguments.delta","item_id":"i1","delta":"{\"path\":"}`,
			`{"type":"response.function_call_arguments.delta","item_id":"i2","delta":"{\"path\":\"folder\"}"}`,
			`{"type":"response.function_call_arguments.delta","item_id":"i1","delta":"\"file\"}"}`,
			`{"type":"response.completed","response":{"status":"completed"}}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", frame)
		}
	})
	msg, stop, _, err := p.Complete(t.Context(), llm.CompletionRequest{Tools: []llm.ToolSchema{{Name: "Read"}, {Name: "LS"}}})
	if err != nil || stop != "tool_use" || len(msg.ToolUses()) != 2 {
		t.Fatalf("tool completion=%+v %s %v", msg, stop, err)
	}
	uses := msg.ToolUses()
	if uses[0].ID != "c1" || string(uses[0].Input) != `{"path":"file"}` || uses[1].ID != "c2" || string(uses[1].Input) != `{"path":"folder"}` {
		t.Fatalf("mixed arguments: %+v", uses)
	}
}

func TestChatGPTMissingAuthorizationNeverSendsRequest(t *testing.T) {
	source := &subscriptionFixtureToken{err: errors.New("login required")}
	p := subscriptionFixtureProvider(t, source, func(w http.ResponseWriter, r *http.Request) { t.Error("unauthorized model request") })
	_, _, _, err := p.Complete(t.Context(), llm.CompletionRequest{})
	if err == nil || err.Error() != "login required" || source.calls != 1 {
		t.Fatalf("authorization=%v calls=%d", err, source.calls)
	}
}

func TestChatGPTInvalidParallelCallEmitsNoExecutableTools(t *testing.T) {
	for _, invalid := range []string{`{`, `null`, `[]`} {
		p := subscriptionFixtureProvider(t, &subscriptionFixtureToken{}, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			for _, frame := range []any{
				map[string]any{"type": "response.output_item.added", "item": map[string]any{"id": "i1", "type": "function_call", "namespace": "artex", "call_id": "c1", "name": "Read"}},
				map[string]any{"type": "response.output_item.added", "item": map[string]any{"id": "i2", "type": "function_call", "namespace": "artex", "call_id": "c2", "name": "Read"}},
				map[string]any{"type": "response.function_call_arguments.delta", "item_id": "i1", "delta": "{}"},
				map[string]any{"type": "response.function_call_arguments.delta", "item_id": "i2", "delta": invalid},
				map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed"}},
			} {
				payload, _ := json.Marshal(frame)
				fmt.Fprintf(w, "data: %s\n\n", payload)
			}
		})
		failed := false
		for event, err := range p.Stream(t.Context(), llm.CompletionRequest{Tools: []llm.ToolSchema{{Name: "Read"}}}) {
			if event.Type == llm.SEToolUseStart || event.Type == llm.SEToolInputJSON {
				t.Fatal("partial executable tool escaped invalid completed response")
			}
			if err != nil {
				failed = true
			}
		}
		if !failed {
			t.Fatalf("invalid call accepted: %s", invalid)
		}
	}
}
