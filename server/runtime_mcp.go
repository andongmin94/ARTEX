package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/internal/toolruntime"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

type managedMCPReply struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type managedMCPClient struct {
	name                           string
	process                        *toolruntime.Process
	ctx                            context.Context
	mu                             sync.Mutex
	writeGate                      chan struct{}
	nextID                         int
	pending                        map[int]chan managedMCPReply
	done                           chan struct{}
	readDone, stderrDone, waitDone chan struct{}
	once                           sync.Once
	readErr                        error
	closed                         bool
}

func connectManagedMCP(ctx context.Context, m *db.MCPServer) (mcpClient, error) {
	if desktopToolsUnavailable() {
		return nil, errors.New(unmanagedDesktopToolsMessage)
	}
	if m.Name == browserMCPName {
		return nil, errors.New("브라우저 MCP는 작업 승인 문맥의 Electron 메인 중계에서만 실행합니다")
	}
	b, err := toolruntime.FromEnvironment()
	if err != nil {
		return nil, err
	}
	key := ""
	for _, component := range b.Manifest.Components {
		exe, err := b.Path(component.Entrypoint)
		if err != nil {
			return nil, err
		}
		if m.Command == component.Key || (filepath.IsAbs(m.Command) && strings.EqualFold(filepath.Clean(m.Command), exe)) {
			key = component.Key
			break
		}
	}
	if key == "" {
		return nil, errors.New("stdio MCP 명령은 앱 매니페스트의 도구 key 또는 검증된 실행 파일 절대 경로여야 합니다")
	}
	if key == "pty" {
		return nil, errors.New("이 MCP 실행 경로의 격리 연결이 아직 준비되지 않았습니다")
	}
	work, err := newManagedToolWorkspace("mcp-" + strconv.FormatInt(m.ID, 10) + "-")
	if err != nil {
		return nil, err
	}
	if err := validateManagedWorkspace(work); err != nil {
		return nil, err
	}
	p, err := b.Start(ctx, toolruntime.Request{Component: key, Args: jsonStrSlice(m.Args), WorkingDir: work, Environment: jsonStrMap(m.Env)})
	if err != nil {
		return nil, err
	}
	c := &managedMCPClient{name: m.Name, process: p, ctx: ctx, writeGate: make(chan struct{}, 1), pending: make(map[int]chan managedMCPReply), done: make(chan struct{}), readDone: make(chan struct{}), stderrDone: make(chan struct{}), waitDone: make(chan struct{})}
	c.writeGate <- struct{}{}
	// Drain stderr independently so an MCP cannot block its JSON-RPC stream.
	go func() { defer close(c.stderrDone); _, _ = io.Copy(io.Discard, p.Stderr) }()
	go c.readLoop()
	go func() { defer close(c.waitDone); _, err := p.Wait(); c.finish(err) }()
	raw, err := c.call(ctx, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "ARTEX", "version": BuildVersion}})
	if err != nil {
		c.Close()
		return nil, err
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if json.Unmarshal(raw, &init) != nil || init.ProtocolVersion != "2025-06-18" {
		c.Close()
		return nil, errors.New("MCP 프로토콜 버전이 지원되지 않습니다")
	}
	if err := c.write(ctx, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (c *managedMCPClient) finish(err error) {
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.readErr = err
		clear(c.pending)
		close(c.done)
		c.mu.Unlock()
	})
}

func (c *managedMCPClient) closedError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readErr != nil {
		return c.readErr
	}
	return errors.New("MCP 연결이 종료되었습니다")
}

func (c *managedMCPClient) readLoop() {
	defer close(c.readDone)
	scanner := bufio.NewScanner(c.process.Stdout)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var envelope struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			managedMCPReply
		}
		if json.Unmarshal(scanner.Bytes(), &envelope) != nil || envelope.JSONRPC != "2.0" {
			c.finish(errors.New("잘못된 MCP JSON-RPC 응답"))
			c.process.Close()
			return
		}
		if envelope.Method != "" {
			if len(envelope.ID) == 0 {
				continue
			}
			answer := map[string]any{"jsonrpc": "2.0", "id": envelope.ID}
			if envelope.Method == "ping" {
				answer["result"] = map[string]any{}
			} else {
				answer["error"] = map[string]any{"code": -32601, "message": "MCP client method is not supported"}
			}
			if err := c.write(c.ctx, answer); err != nil {
				c.finish(err)
				c.process.Close()
				return
			}
			continue
		}
		var id int
		if json.Unmarshal(envelope.ID, &id) != nil || id <= 0 {
			continue
		}
		c.mu.Lock()
		pending := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if pending != nil {
			pending <- envelope.managedMCPReply
		}
	}
	c.finish(scanner.Err())
	c.process.Close()
}

func (c *managedMCPClient) write(ctx context.Context, value any) error {
	// Watching starts before acquiring the serialization gate: a canceled
	// caller must also release an older request blocked in the pipe. Closing the
	// managed process kills its descendants and breaks every pending pipe write.
	watchDone := make(chan struct{})
	stopWatch := context.AfterFunc(ctx, func() { _ = c.Close(); close(watchDone) })
	defer func() {
		// A cancellation can win the select before AfterFunc starts. Close here
		// as well so stopping that callback cannot leave another writer blocked.
		if ctx.Err() != nil {
			_ = c.Close()
		}
		if !stopWatch() {
			<-watchDone
		}
	}()
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(raw) > 8<<20 {
		return errors.New("MCP 요청 크기가 제한을 초과했습니다")
	}
	raw = append(raw, '\n')
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return c.closedError()
	case <-c.writeGate:
	}
	defer func() { c.writeGate <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-c.done:
		return c.closedError()
	default:
	}
	_, err = c.process.Stdin.Write(raw)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (c *managedMCPClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		_ = c.Close()
		return nil, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, c.closedError()
	}
	c.nextID++
	id := c.nextID
	pending := make(chan managedMCPReply, 1)
	c.pending[id] = pending
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	if err := c.write(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	select {
	case reply := <-pending:
		if ctx.Err() != nil {
			_ = c.Close()
			return nil, ctx.Err()
		}
		if reply.Error != nil {
			return nil, fmt.Errorf("MCP 오류 %d: %s", reply.Error.Code, reply.Error.Message)
		}
		return reply.Result, nil
	case <-ctx.Done():
		c.Close()
		return nil, ctx.Err()
	case <-c.done:
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, c.closedError()
	}
}

func (c *managedMCPClient) Tools(ctx context.Context) ([]actool.CoreTool, error) {
	cursor := ""
	seen := make(map[string]bool)
	out := []actool.CoreTool{}
	for page := 0; page < 100; page++ {
		var listing struct {
			Tools []struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				InputSchema map[string]any `json:"inputSchema"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &listing); err != nil {
			return nil, err
		}
		for _, remote := range listing.Tools {
			name := remote.Name
			if name == "" || seen[name] {
				return nil, errors.New("MCP 도구 이름이 비어 있거나 중복되었습니다")
			}
			seen[name] = true
			full := "mcp__" + c.name + "__" + name
			schema := remote.InputSchema
			if schema == nil {
				schema = map[string]any{"type": "object"}
			}
			out = append(out, actool.Build(actool.Spec{Name: full, Description: remote.Description, Schema: schema, Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
				return permission.AskUser("MCP 도구 실행: " + full)
			}, Run: func(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
				var args any
				if len(in) > 0 {
					if json.Unmarshal(in, &args) != nil {
						return actool.Errorf("MCP 입력 JSON이 유효하지 않습니다"), nil
					}
				}
				raw, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				return managedMCPResult(raw, tc), nil
			}}))
		}
		if listing.NextCursor == "" {
			return out, nil
		}
		if listing.NextCursor == cursor {
			return nil, errors.New("MCP 도구 목록 페이지가 반복됩니다")
		}
		cursor = listing.NextCursor
	}
	return nil, errors.New("MCP 도구 목록 페이지 제한을 초과했습니다")
}

func managedMCPResult(raw json.RawMessage, tc *actool.ToolContext) actool.Result {
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return actool.Errorf("MCP 도구 응답이 유효하지 않습니다")
	}
	text := ""
	unsupported := make(map[string]bool)
	for _, part := range result.Content {
		if part.Type == "text" {
			text += part.Text
			continue
		}
		kind := part.Type
		if len(kind) > 128 {
			kind = kind[:128]
		}
		if !unsupported[kind] {
			unsupported[kind] = true
			// norma's current vendor-neutral model only has text blocks. Never turn
			// an omitted image/audio/resource into a silently successful empty result.
			text += "\n[MCP 반환 콘텐츠 " + strconv.Quote(kind) + "는 현재 모델 메시지에서 지원되지 않습니다]"
		}
	}
	out := actool.Text(actool.Capture(tc, text))
	out.IsError = result.IsError || len(unsupported) > 0
	return out
}

func (c *managedMCPClient) Close() error { c.finish(nil); return c.process.Close() }
