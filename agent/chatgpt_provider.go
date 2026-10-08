package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/norma/llm"
)

// ChatGPTTokenSource is owned by the Go authentication service. The provider
// obtains a current token for each request instead of copying one into config.
type ChatGPTTokenSource interface {
	AccessToken(context.Context) (string, error)
}

const chatGPTResponsesURL = "https://api.openai.com/v1/responses"
const chatGPTToolNamespace = "artex"

type chatGPTProvider struct {
	cfg         Config
	client      *http.Client
	endpoint    string
	rateMu      sync.Mutex
	nextRequest time.Time
}

func newChatGPTProvider(cfg Config) (llm.Provider, error) {
	if cfg.Format != llm.FormatOpenAIResponses || strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("ChatGPT 구독 모델을 선택하세요")
	}
	// Subscription credentials can only go to the official public endpoint.
	client, err := quotaAwareHTTPClient("", "")
	if err != nil {
		return nil, err
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &chatGPTProvider{cfg: cfg, client: client, endpoint: chatGPTResponsesURL}, nil
}

func chatGPTRequest(cfg Config, req llm.CompletionRequest) ([]byte, error) {
	input := make([]map[string]any, 0)
	for _, msg := range req.Messages {
		if msg.Role != llm.RoleUser && msg.Role != llm.RoleAssistant {
			return nil, fmt.Errorf("ChatGPT: unsupported message role %q", msg.Role)
		}
		for _, block := range msg.Content {
			switch block.Type {
			case llm.BlockText:
				input = append(input, map[string]any{"role": string(msg.Role), "content": block.Text})
			case llm.BlockToolUse:
				args := string(block.Input)
				if args == "" {
					args = "{}"
				}
				input = append(input, map[string]any{"type": "function_call", "namespace": chatGPTToolNamespace, "call_id": block.ID, "name": block.Name, "arguments": args})
			case llm.BlockToolResult:
				var output strings.Builder
				for _, b := range block.Content {
					if b.Type == llm.BlockText {
						output.WriteString(b.Text)
					}
				}
				input = append(input, map[string]any{"type": "function_call_output", "call_id": block.ToolUseID, "output": output.String()})
			case llm.BlockThinking:
				// Provider-neutral thinking lacks Responses encrypted reasoning data.
				// Full text and tool history are sent; private reasoning is not replayed.
			default:
				return nil, fmt.Errorf("ChatGPT: unsupported content block %q", block.Type)
			}
		}
	}
	// The ChatGPT HTTP contract requires full input, instructions, stream and
	// store:false. Output caps/temperature and server-stored history are omitted.
	body := map[string]any{"model": cfg.Model, "instructions": strings.Join(req.System, "\n\n"), "input": input, "store": false, "stream": true}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, tool := range req.Tools {
			tools = append(tools, map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": tool.InputSchema, "strict": false})
		}
		body["tools"] = []map[string]any{{"type": "namespace", "name": chatGPTToolNamespace, "description": "ARTEX local tools subject to the configured target and approval policy.", "tools": tools}}
	}
	if cfg.ReasoningEffort != "" && req.Thinking != "disabled" {
		body["reasoning"] = map[string]any{"effort": cfg.ReasoningEffort}
	}
	return json.Marshal(body)
}

func (p *chatGPTProvider) waitRate(ctx context.Context) error {
	var interval time.Duration
	if p.cfg.RatePerSecond > 0 {
		interval = time.Duration(float64(time.Second) / p.cfg.RatePerSecond)
	}
	if p.cfg.RatePerMinute > 0 {
		interval = max(interval, time.Duration(float64(time.Minute)/p.cfg.RatePerMinute))
	}
	if interval <= 0 {
		return ctx.Err()
	}
	p.rateMu.Lock()
	due := maxTime(time.Now(), p.nextRequest)
	p.nextRequest = due.Add(interval)
	p.rateMu.Unlock()
	return chatGPTWait(ctx, time.Until(due))
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
func chatGPTWait(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (p *chatGPTProvider) connect(ctx context.Context, body []byte) (*http.Response, error) {
	if err := p.waitRate(ctx); err != nil {
		return nil, err
	}
	retries := p.cfg.Retry.ConnectAttempts
	if retries == 0 {
		retries = 3
	}
	if retries < 0 {
		retries = 0
	}
	for attempt := 0; ; attempt++ {
		token, err := p.cfg.ChatGPT.AccessToken(ctx)
		if err != nil {
			return nil, err
		}
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "text/event-stream")
		resp, err := p.client.Do(r)
		if err == nil && resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		retryable := err != nil
		if resp != nil {
			data, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			if readErr != nil {
				err = readErr
			} else {
				err = fmt.Errorf("ChatGPT HTTP status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
			}
			retryable = resp.StatusCode == 429 || resp.StatusCode >= 500
		}
		if !retryable || attempt >= retries || ctx.Err() != nil {
			return nil, err
		}
		delay := p.cfg.Retry.ConnectInterval
		if delay <= 0 {
			delay = min(500*time.Millisecond*time.Duration(1<<min(attempt, 4)), 8*time.Second)
		}
		if err := chatGPTWait(ctx, delay); err != nil {
			return nil, err
		}
	}
}

type chatGPTFrame struct {
	Type    string `json:"type"`
	Delta   string `json:"delta"`
	ItemID  string `json:"item_id"`
	RawItem struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"item"`
	Error    *chatGPTError `json:"error"`
	Response struct {
		Status string        `json:"status"`
		Error  *chatGPTError `json:"error"`
		Usage  struct {
			Input   int `json:"input_tokens"`
			Output  int `json:"output_tokens"`
			Details struct {
				Cached int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	} `json:"response"`
}
type chatGPTError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (p *chatGPTProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		fail := func(err error) { yield(llm.StreamEvent{}, err) }
		body, err := chatGPTRequest(p.cfg, req)
		if err != nil {
			fail(err)
			return
		}
		resp, err := p.connect(ctx, body)
		if err != nil {
			fail(err)
			return
		}
		defer resp.Body.Close()
		if !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
			fail(errors.New("ChatGPT: expected an SSE response"))
			return
		}
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64<<10), 8<<20)
		var data []string
		completed := false
		type toolCall struct {
			id, name string
			args     strings.Builder
		}
		var calls []*toolCall
		byItem := map[string]*toolCall{}
		parse := func() bool {
			if len(data) == 0 {
				return true
			}
			payload := strings.Join(data, "\n")
			data = nil
			if payload == "[DONE]" {
				if !completed {
					fail(errors.New("ChatGPT: response.completed 없이 스트림이 종료되었습니다"))
				}
				return false
			}
			var frame chatGPTFrame
			if err := json.Unmarshal([]byte(payload), &frame); err != nil {
				fail(fmt.Errorf("ChatGPT: invalid SSE data: %w", err))
				return false
			}
			var event llm.StreamEvent
			switch frame.Type {
			case "response.created":
				event.Type = llm.SEMessageStart
			case "response.output_text.delta":
				event.Type, event.Text = llm.SETextDelta, frame.Delta
			case "response.reasoning_summary_text.delta":
				event.Type, event.Text = llm.SEThinkingDelta, frame.Delta
			case "response.output_item.added":
				if frame.RawItem.Type != "function_call" {
					return true
				}
				if frame.RawItem.Namespace != chatGPTToolNamespace || frame.RawItem.ID == "" || frame.RawItem.CallID == "" || frame.RawItem.Name == "" || byItem[frame.RawItem.ID] != nil {
					fail(errors.New("ChatGPT: invalid tool namespace or call identity"))
					return false
				}
				known := false
				for _, tool := range req.Tools {
					if tool.Name == frame.RawItem.Name {
						known = true
						break
					}
				}
				if !known {
					fail(errors.New("ChatGPT: undeclared tool call"))
					return false
				}
				call := &toolCall{id: frame.RawItem.CallID, name: frame.RawItem.Name}
				calls = append(calls, call)
				byItem[frame.RawItem.ID] = call
				return true
			case "response.function_call_arguments.delta":
				call := byItem[frame.ItemID]
				if call == nil {
					fail(errors.New("ChatGPT: arguments without a declared call"))
					return false
				}
				call.args.WriteString(frame.Delta)
				return true
			case "response.completed":
				if frame.Response.Status != "completed" {
					fail(errors.New("ChatGPT: completion status is not completed"))
					return false
				}
				completed = true
				// Norma's normalized stream has one active tool block. Buffer per
				// item so interleaved Responses calls retain their own arguments, and
				// expose executable calls only after confirmed completion.
				seenIDs := map[string]bool{}
				for _, call := range calls {
					var object map[string]json.RawMessage
					if json.Unmarshal([]byte(call.args.String()), &object) != nil || object == nil || seenIDs[call.id] {
						fail(errors.New("ChatGPT: invalid completed tool arguments"))
						return false
					}
					seenIDs[call.id] = true
				}
				for _, call := range calls {
					if !yield(llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: call.id, ToolName: call.name}, nil) {
						return false
					}
					if !yield(llm.StreamEvent{Type: llm.SEToolInputJSON, Text: call.args.String()}, nil) {
						return false
					}
				}
				event.Type, event.StopReason = llm.SEMessageDelta, "end_turn"
				if len(calls) > 0 {
					event.StopReason = "tool_use"
				}
				event.Usage = llm.Usage{InputTokens: frame.Response.Usage.Input, OutputTokens: frame.Response.Usage.Output, CacheReadTokens: frame.Response.Usage.Details.Cached}
				if !yield(event, nil) {
					return false
				}
				yield(llm.StreamEvent{Type: llm.SEMessageStop}, nil)
				return false
			case "response.failed", "response.incomplete", "error":
				detail := frame.Response.Error
				if detail == nil {
					detail = frame.Error
				}
				if detail != nil {
					fail(fmt.Errorf("ChatGPT %s: %s: %s", frame.Type, detail.Code, detail.Message))
				} else {
					fail(fmt.Errorf("ChatGPT: %s", frame.Type))
				}
				return false
			default:
				return true
			}
			return yield(event, nil)
		}
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if !parse() {
					return
				}
				continue
			}
			if strings.HasPrefix(line, "data:") {
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
		if scanner.Err() != nil {
			fail(fmt.Errorf("ChatGPT stream: %w", scanner.Err()))
			return
		}
		if len(data) > 0 && !parse() {
			return
		}
		if !completed {
			fail(errors.New("ChatGPT: response.completed 없이 스트림이 종료되었습니다"))
		}
	}
}

// Complete assembles the same required SSE protocol for SDK calls such as
// compaction. ChatGPT subscription inference never sends stream:false.
func (p *chatGPTProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	msg := llm.Message{Role: llm.RoleAssistant}
	var stop string
	var usage llm.Usage
	var text, thinking, args strings.Builder
	toolID, toolName := "", ""
	flush := func() {
		if text.Len() > 0 {
			msg.Content = append(msg.Content, llm.TextBlock(text.String()))
			text.Reset()
		}
		if thinking.Len() > 0 {
			msg.Content = append(msg.Content, llm.ContentBlock{Type: llm.BlockThinking, Thinking: thinking.String()})
			thinking.Reset()
		}
		if toolID != "" {
			msg.Content = append(msg.Content, llm.ContentBlock{Type: llm.BlockToolUse, ID: toolID, Name: toolName, Input: json.RawMessage(args.String())})
			args.Reset()
			toolID, toolName = "", ""
		}
	}
	for event, err := range p.Stream(ctx, req) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		switch event.Type {
		case llm.SETextDelta:
			text.WriteString(event.Text)
		case llm.SEThinkingDelta:
			thinking.WriteString(event.Text)
		case llm.SEToolUseStart:
			flush()
			toolID, toolName = event.ToolID, event.ToolName
		case llm.SEToolInputJSON:
			args.WriteString(event.Text)
		case llm.SEMessageDelta:
			stop, usage = event.StopReason, event.Usage
		}
	}
	flush()
	return msg, stop, usage, nil
}
