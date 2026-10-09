package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/artex/internal/toolruntime"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/hinshun/vt10x"
)

// One owner is created per agent run. It is never shared by other agents or
// workspaces; AugmentTools closes it even when a run returns normally.
type managedToolSessions struct {
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	closed      bool
	work, agent string
	seq         int
	entries     map[string]*managedToolSession
}

type managedToolSession struct {
	mu                            sync.Mutex
	inputMu                       sync.Mutex
	id, command, mode, outputPath string
	terminal                      bool
	process                       *toolruntime.Process
	cancel                        context.CancelFunc
	done                          chan struct{}
	changed                       chan struct{}
	status                        string
	exit                          *int
	err                           error
	output                        []byte
	total, cursor                 int64
	lastOut                       time.Time
	rows, cols                    int
	emu                           vt10x.Terminal
	prompt                        *regexp.Regexp
	watch                         []*regexp.Regexp
	matched                       map[int]bool
	notes                         []string
}

func newManagedToolSessions(ctx context.Context) *managedToolSessions {
	ctx, cancel := context.WithCancel(ctx)
	return &managedToolSessions{ctx: ctx, cancel: cancel, entries: make(map[string]*managedToolSession)}
}

func (m *managedToolSessions) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.cancel()
	entries := make([]*managedToolSession, 0, len(m.entries))
	for _, s := range m.entries {
		entries = append(entries, s)
	}
	m.mu.Unlock()
	var result error
	for _, s := range entries {
		s.cancel()
		<-s.done
		result = errors.Join(result, s.process.Close())
	}
	return result
}

func (m *managedToolSessions) workspace(tc *actool.ToolContext) error {
	if tc == nil {
		return errors.New("도구 작업 폴더가 지정되지 않았습니다")
	}
	if err := validateManagedWorkspace(tc.WorkingDir); err != nil {
		return err
	}
	if err := toolruntime.ValidateDirectory(tc.WorkingDir); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.ctx.Err() != nil {
		return errors.New("도구 실행 소유자가 종료되었습니다")
	}
	work := filepath.Clean(tc.WorkingDir)
	if m.work == "" {
		m.work, m.agent = work, tc.AgentID
	}
	if !strings.EqualFold(m.work, work) || m.agent != tc.AgentID {
		return errors.New("다른 에이전트나 작업 폴더의 프로세스에는 접근할 수 없습니다")
	}
	return nil
}

func (m *managedToolSessions) spawn(command string, terminal bool, mode string, rows, cols int, prompt *regexp.Regexp, watch []*regexp.Regexp) (*managedToolSession, error) {
	b, err := toolruntime.FromEnvironment()
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.ctx.Err() != nil {
		return nil, errors.New("도구 실행 소유자가 종료되었습니다")
	}
	active := 0
	for _, s := range m.entries {
		s.mu.Lock()
		if s.exit == nil {
			active++
		}
		s.mu.Unlock()
	}
	if active >= 8 || len(m.entries) >= 128 {
		return nil, errors.New("한 실행에서 허용하는 프로세스 수를 초과했습니다")
	}
	ctx, cancel := context.WithCancel(m.ctx)
	component, args := "shell", managedPowerShellArgs(command, false)
	if terminal {
		component, args = "pty", managedPowerShellArgs(command, true)
	}
	p, err := b.Start(ctx, toolruntime.Request{Component: component, Args: args, WorkingDir: m.work, Terminal: terminal, Rows: rows, Cols: cols})
	if err != nil {
		cancel()
		return nil, err
	}
	m.seq++
	prefix := "task_"
	if terminal {
		prefix = "shell_"
	}
	s := &managedToolSession{id: fmt.Sprintf("%s%d", prefix, m.seq), command: command, terminal: terminal, mode: mode, process: p, cancel: cancel, done: make(chan struct{}), changed: make(chan struct{}, 1), status: "running", lastOut: time.Now(), prompt: prompt, watch: watch, matched: make(map[int]bool), rows: rows, cols: cols}
	if terminal {
		if s.rows == 0 {
			s.rows = 24
		}
		if s.cols == 0 {
			s.cols = 80
		}
		s.emu = vt10x.New(vt10x.WithSize(s.cols, s.rows))
	}
	var outputFile *os.File
	if !terminal {
		dir := filepath.Join(m.work, ".managed-output")
		if err = os.MkdirAll(dir, 0o700); err == nil {
			err = toolruntime.ValidateDirectory(dir)
		}
		if err == nil {
			outputFile, err = os.CreateTemp(dir, "task-*.log")
		}
		if err != nil {
			cancel()
			p.Close()
			return nil, err
		}
		s.outputPath = outputFile.Name()
		p.Stdin.Close()
	}
	m.entries[s.id] = s
	go s.collect(outputFile)
	return s, nil
}

func (s *managedToolSession) collect(file *os.File) {
	var readers sync.WaitGroup
	for _, stream := range []io.Reader{s.process.Stdout, s.process.Stderr} {
		readers.Add(1)
		go func(stream io.Reader) {
			defer readers.Done()
			buf := make([]byte, 8192)
			for {
				n, err := stream.Read(buf)
				if n > 0 {
					s.mu.Lock()
					if !s.terminal && s.total+int64(n) > 50<<20 {
						s.err = errors.New("백그라운드 출력이 50 MiB 제한을 초과했습니다")
						s.cancel()
						s.mu.Unlock()
						return
					}
					if file != nil {
						if _, writeErr := file.Write(buf[:n]); writeErr != nil {
							s.err = writeErr
							s.cancel()
							s.mu.Unlock()
							return
						}
					}
					s.total += int64(n)
					s.output = append(s.output, buf[:n]...)
					if len(s.output) > 256<<10 {
						s.output = append([]byte(nil), s.output[len(s.output)-(256<<10):]...)
					}
					if s.emu != nil {
						_, _ = s.emu.Write(buf[:n])
					}
					s.lastOut = time.Now()
					for i, pattern := range s.watch {
						if !s.matched[i] && pattern.Match(s.output) {
							s.matched[i] = true
							s.notes = append(s.notes, "감시 패턴 일치: "+pattern.String())
						}
					}
					s.mu.Unlock()
					select {
					case s.changed <- struct{}{}:
					default:
					}
				}
				if err != nil {
					if err != io.EOF {
						s.mu.Lock()
						s.err = errors.Join(s.err, err)
						s.mu.Unlock()
					}
					return
				}
			}
		}(stream)
	}
	exit, waitErr := s.process.Wait()
	readers.Wait()
	if file != nil {
		waitErr = errors.Join(waitErr, file.Close())
	}
	s.mu.Lock()
	s.exit = &exit
	s.err = errors.Join(s.err, waitErr)
	s.status = "completed"
	if exit != 0 || s.err != nil {
		s.status = "failed"
	}
	if errors.Is(waitErr, context.Canceled) {
		s.status = "killed"
	}
	if s.mode == "dispatch" || s.mode == "monitor" {
		s.notes = append(s.notes, fmt.Sprintf("프로세스 완료: %s, 종료 코드 %d", s.status, exit))
	}
	s.mu.Unlock()
	s.cancel()
	close(s.done)
	_ = s.process.Close()
}

func (m *managedToolSessions) find(id string, terminal bool) (*managedToolSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.entries[id]
	if !ok || s.terminal != terminal {
		return nil, errors.New("이 실행에서 소유한 프로세스가 아닙니다: " + id)
	}
	return s, nil
}

func (s *managedToolSession) read(from int64, maxBytes, maxLines int, drain bool) (string, int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	base := s.total - int64(len(s.output))
	omitted := from < base
	if from < base {
		from = base
	}
	if from > s.total {
		from = s.total
	}
	out := s.output[from-base:]
	if !drain {
		if maxBytes <= 0 {
			maxBytes = 8192
		}
		if maxBytes > 256<<10 {
			maxBytes = 256 << 10
		}
		if len(out) > maxBytes {
			out = out[:maxBytes]
			omitted = true
		}
		if maxLines > 0 {
			count := 0
			for i, b := range out {
				if b == '\n' {
					count++
					if count >= maxLines && i+1 < len(out) {
						out = out[:i+1]
						omitted = true
						break
					}
				}
			}
		}
	}
	return string(out), from + int64(len(out)), omitted
}

func (s *managedToolSession) waitQuiet(ctx context.Context, quiet, grace, timeout time.Duration, prompt *regexp.Regexp, from int64) string {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	started := time.Now()
	tick := time.NewTicker(40 * time.Millisecond)
	defer tick.Stop()
	for {
		s.mu.Lock()
		base := s.total - int64(len(s.output))
		begin := from - base
		if begin < 0 {
			begin = 0
		}
		if begin > int64(len(s.output)) {
			begin = int64(len(s.output))
		}
		matched := prompt != nil && prompt.Match(s.output[begin:])
		last := s.lastOut
		s.mu.Unlock()
		if matched {
			return "prompt"
		}
		select {
		case <-s.done:
			return "exited"
		case <-ctx.Done():
			return "timeout"
		case <-deadline.C:
			return "timeout"
		case <-tick.C:
			if time.Since(started) >= grace && time.Since(last) >= quiet {
				return "quiet"
			}
		}
	}
}

func managedJSON(v any) (actool.Result, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	return actool.Text(string(b)), nil
}

func (m *managedToolSessions) call(ctx context.Context, name string, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	if err := m.workspace(tc); err != nil {
		return actool.Errorf(err.Error()), nil
	}
	if desktopToolsUnavailable() {
		return actool.Errorf(unmanagedDesktopToolsMessage), nil
	}
	switch name {
	case "Bash", "Monitor":
		return m.bash(ctx, name, in, tc)
	case "shell_open":
		return m.open(ctx, in)
	case "shell_send":
		return m.send(ctx, in)
	case "shell_read":
		return m.readShell(in)
	case "shell_close":
		return m.closeShell(in)
	case "shell_list":
		return m.list(true)
	case "TaskOutput":
		return m.taskOutput(ctx, in, tc)
	case "TaskStop":
		return m.stop(in)
	case "TaskList":
		return m.list(false)
	}
	return actool.Errorf("관리된 실행 도구를 확인할 수 없습니다"), nil
}

func (m *managedToolSessions) bash(ctx context.Context, name string, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	var a struct {
		Command    string `json:"command"`
		Background bool   `json:"run_in_background"`
		Timeout    int    `json:"timeout_ms"`
	}
	if err := json.Unmarshal(in, &a); err != nil {
		return actool.Errorf(err.Error()), nil
	}
	if strings.TrimSpace(a.Command) == "" {
		return actool.Errorf("command가 비어 있습니다"), nil
	}
	if floor := actool.NewBash().CheckPermissions(ctx, in, permission.Context{}); floor.Behavior == permission.Deny {
		return actool.Errorf(floor.Message), nil
	}
	s, err := m.spawn(a.Command, false, "", 0, 0, nil, nil)
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	if a.Background || name == "Monitor" {
		return actool.Text(fmt.Sprintf("백그라운드 작업 %s를 시작했습니다. 출력: %s\nTaskOutput{task_id:%q}로 상태를 확인하고 TaskStop으로 종료합니다.", s.id, s.outputPath, s.id)), nil
	}
	defer func() { m.mu.Lock(); delete(m.entries, s.id); m.mu.Unlock() }()
	timeout := timeoutOr(a.Timeout, 120000)
	if timeout > 10*time.Minute {
		timeout = 10 * time.Minute
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-s.done:
	case <-ctx.Done():
		s.cancel()
		<-s.done
	case <-timer.C:
		// Timeout never expands execution lifetime silently. The caller can choose
		// explicit background execution when it intends the command to continue.
		s.cancel()
		<-s.done
	}
	s.mu.Lock()
	status, runErr := s.status, s.err
	s.mu.Unlock()
	out, _, omitted := s.read(0, 256<<10, 0, true)
	if omitted {
		out = "(이전 출력 생략; 전체 출력 파일: " + s.outputPath + ")\n" + out
	}
	if status != "completed" {
		s.mu.Lock()
		exit := s.exit
		s.mu.Unlock()
		return actool.Errorf(actool.Capture(tc, out) + "\n" + fmt.Sprintf("종료 코드 %v: %v", exit, runErr)), nil
	}
	if strings.TrimSpace(out) == "" {
		out = "(명령 출력 없음)"
	}
	return actool.Text(actool.Capture(tc, out)), nil
}

func parsePattern(value string) (*regexp.Regexp, error) {
	if value == "" {
		return nil, nil
	}
	return regexp.Compile(value)
}

func (m *managedToolSessions) open(ctx context.Context, in json.RawMessage) (actool.Result, error) {
	var a struct {
		Command string   `json:"command"`
		Mode    string   `json:"mode"`
		Prompt  string   `json:"prompt_regex"`
		Watch   []string `json:"watch"`
		Rows    int      `json:"rows"`
		Cols    int      `json:"cols"`
		Quiet   int      `json:"quiet_ms"`
		Grace   int      `json:"startup_grace_ms"`
	}
	if err := json.Unmarshal(in, &a); err != nil {
		return actool.Errorf(err.Error()), nil
	}
	if strings.TrimSpace(a.Command) == "" {
		return actool.Errorf("command가 비어 있습니다"), nil
	}
	if floor := actool.NewBash().CheckPermissions(ctx, in, permission.Context{}); floor.Behavior == permission.Deny {
		return actool.Errorf(floor.Message), nil
	}
	if a.Mode == "" {
		a.Mode = "hands-free"
	}
	if a.Mode != "hands-free" && a.Mode != "interactive" && a.Mode != "dispatch" && a.Mode != "monitor" {
		return actool.Errorf("알 수 없는 PTY 실행 모드입니다"), nil
	}
	prompt, err := parsePattern(a.Prompt)
	if err != nil {
		return actool.Errorf("prompt_regex: " + err.Error()), nil
	}
	var watch []*regexp.Regexp
	if a.Mode == "monitor" && len(a.Watch) == 0 {
		return actool.Errorf("monitor 모드에는 watch 패턴이 필요합니다"), nil
	}
	if len(a.Watch) > 32 {
		return actool.Errorf("watch 패턴은 32개까지 허용됩니다"), nil
	}
	for _, value := range a.Watch {
		pattern, err := regexp.Compile(value)
		if err != nil {
			return actool.Errorf(err.Error()), nil
		}
		watch = append(watch, pattern)
	}
	s, err := m.spawn(a.Command, true, a.Mode, a.Rows, a.Cols, prompt, watch)
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	result := map[string]any{"session_id": s.id, "state": "running", "mode": a.Mode}
	if a.Mode == "interactive" {
		result["done_by"] = s.waitQuiet(ctx, timeoutOr(a.Quiet, 8000), timeoutOr(a.Grace, 15000), 120*time.Second, prompt, 0)
		out, cursor, omitted := s.read(0, 32768, 0, false)
		result["output"], result["cursor"], result["omitted"] = out, cursor, omitted
	}
	return managedJSON(result)
}

var managedKeys = map[string]string{"enter": "\r", "return": "\r", "tab": "\t", "esc": "\x1b", "escape": "\x1b", "backspace": "\x7f", "space": " ", "up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D", "home": "\x1b[H", "end": "\x1b[F", "pageup": "\x1b[5~", "pagedown": "\x1b[6~", "delete": "\x1b[3~", "insert": "\x1b[2~"}

func (m *managedToolSessions) send(ctx context.Context, in json.RawMessage) (actool.Result, error) {
	var a struct {
		ID      string   `json:"session_id"`
		Text    string   `json:"text"`
		Paste   string   `json:"paste"`
		Submit  bool     `json:"submit"`
		Keys    []string `json:"keys"`
		Hex     []string `json:"hex"`
		Wait    *bool    `json:"wait"`
		Prompt  string   `json:"prompt_regex"`
		Quiet   int      `json:"quiet_ms"`
		Timeout int      `json:"timeout_ms"`
	}
	if err := json.Unmarshal(in, &a); err != nil {
		return actool.Errorf(err.Error()), nil
	}
	s, err := m.find(a.ID, true)
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	s.mu.Lock()
	before, prompt, ended := s.total, s.prompt, s.exit != nil
	s.mu.Unlock()
	if ended {
		return actool.Errorf("PTY 세션이 종료되었습니다"), nil
	}
	if a.Prompt != "" {
		prompt, err = parsePattern(a.Prompt)
		if err != nil {
			return actool.Errorf(err.Error()), nil
		}
	}
	payload := []byte(a.Text)
	if a.Submit {
		payload = append(payload, '\r')
	}
	for _, key := range a.Keys {
		key = strings.ToLower(strings.TrimSpace(key))
		value, ok := managedKeys[key]
		if !ok && strings.HasPrefix(key, "ctrl+") && len(key) == 6 {
			c := key[5]
			if c >= 'a' && c <= 'z' {
				value = string([]byte{c & 0x1f})
				ok = true
			} else if c == '[' {
				value = "\x1b"
				ok = true
			} else if c == '\\' {
				value = "\x1c"
				ok = true
			}
		}
		if !ok {
			return actool.Errorf("알 수 없는 키: " + key), nil
		}
		payload = append(payload, value...)
	}
	for _, value := range a.Hex {
		n, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "0x"), 16, 8)
		if err != nil {
			return actool.Errorf("잘못된 hex 바이트입니다"), nil
		}
		payload = append(payload, byte(n))
	}
	if a.Paste != "" {
		payload = append(payload, "\x1b[200~"...)
		payload = append(payload, a.Paste...)
		payload = append(payload, "\x1b[201~"...)
	}
	if len(payload) == 0 || len(payload) > 1<<20 {
		return actool.Errorf("PTY 입력은 1 바이트부터 1 MiB까지 허용됩니다"), nil
	}
	s.inputMu.Lock()
	_, err = s.process.Stdin.Write(payload)
	s.inputMu.Unlock()
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	s.mu.Lock()
	s.lastOut = time.Now()
	s.mu.Unlock()
	if a.Wait != nil && !*a.Wait {
		return managedJSON(map[string]any{"session_id": s.id, "sent": len(payload)})
	}
	timeout := timeoutOr(a.Timeout, 120000)
	if timeout > 10*time.Minute {
		timeout = 10 * time.Minute
	}
	by := s.waitQuiet(ctx, timeoutOr(a.Quiet, 8000), 0, timeout, prompt, before)
	out, cursor, omitted := s.read(before, 8192, 0, false)
	s.mu.Lock()
	running := s.exit == nil
	s.mu.Unlock()
	return managedJSON(map[string]any{"session_id": s.id, "output": out, "cursor": cursor, "omitted": omitted, "running": running, "done_by": by})
}

func (m *managedToolSessions) readShell(in json.RawMessage) (actool.Result, error) {
	var a struct {
		ID       string `json:"session_id"`
		View     string `json:"view"`
		Since    *int64 `json:"since_cursor"`
		MaxBytes int    `json:"max_bytes"`
		MaxLines int    `json:"max_lines"`
		Drain    bool   `json:"drain"`
	}
	if err := json.Unmarshal(in, &a); err != nil {
		return actool.Errorf(err.Error()), nil
	}
	s, err := m.find(a.ID, true)
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	s.mu.Lock()
	from, running, exit, notes := s.cursor, s.exit == nil, s.exit, s.notes
	s.notes = nil
	s.mu.Unlock()
	if a.View == "screen" {
		s.mu.Lock()
		screen := s.emu.String()
		cursor := s.emu.Cursor()
		rows, cols := s.rows, s.cols
		s.mu.Unlock()
		return managedJSON(map[string]any{"view": "screen", "screen": screen, "rows": rows, "cols": cols, "cursor": map[string]int{"row": cursor.Y, "col": cursor.X}, "running": running, "notifications": notes})
	}
	if a.View != "" && a.View != "stream" {
		return actool.Errorf("알 수 없는 PTY 출력 보기입니다"), nil
	}
	if a.Since != nil {
		from = *a.Since
	}
	if a.MaxLines == 0 {
		a.MaxLines = 200
	}
	out, cursor, omitted := s.read(from, a.MaxBytes, a.MaxLines, a.Drain)
	s.mu.Lock()
	s.cursor = cursor
	s.mu.Unlock()
	return managedJSON(map[string]any{"view": "stream", "output": out, "cursor": cursor, "omitted": omitted, "running": running, "exit_code": exit, "notifications": notes})
}

func (m *managedToolSessions) closeShell(in json.RawMessage) (actool.Result, error) {
	var a struct {
		ID string `json:"session_id"`
	}
	if err := json.Unmarshal(in, &a); err != nil {
		return actool.Errorf(err.Error()), nil
	}
	s, err := m.find(a.ID, true)
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	s.cancel()
	<-s.done
	m.mu.Lock()
	delete(m.entries, s.id)
	m.mu.Unlock()
	s.mu.Lock()
	exit := s.exit
	s.mu.Unlock()
	return managedJSON(map[string]any{"closed": s.id, "exit_code": exit})
}

func (m *managedToolSessions) list(terminal bool) (actool.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.entries))
	for id, s := range m.entries {
		if s.terminal == terminal {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	items := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		s := m.entries[id]
		s.mu.Lock()
		items = append(items, map[string]any{"session_id": s.id, "task_id": s.id, "command": s.command, "state": s.status, "mode": s.mode, "exit_code": s.exit, "output_file": s.outputPath, "notifications": s.notes})
		s.notes = nil
		s.mu.Unlock()
	}
	key := "tasks"
	if terminal {
		key = "sessions"
	}
	return managedJSON(map[string]any{key: items})
}

func (m *managedToolSessions) taskOutput(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	var a struct {
		ID       string `json:"task_id"`
		MaxBytes int    `json:"max_bytes"`
		Block    bool   `json:"block"`
		Timeout  int    `json:"timeout_ms"`
	}
	if err := json.Unmarshal(in, &a); err != nil {
		return actool.Errorf(err.Error()), nil
	}
	s, err := m.find(a.ID, false)
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	if a.Block {
		timeout := timeoutOr(a.Timeout, 30000)
		if timeout > 10*time.Minute {
			timeout = 10 * time.Minute
		}
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-s.done:
		case <-ctx.Done():
		case <-timer.C:
		}
	}
	s.mu.Lock()
	total, status, exit := s.total, s.status, s.exit
	s.mu.Unlock()
	if a.MaxBytes <= 0 {
		a.MaxBytes = 30000
	}
	if a.MaxBytes > 256<<10 {
		a.MaxBytes = 256 << 10
	}
	from := total - int64(a.MaxBytes)
	if from < 0 {
		from = 0
	}
	out, _, _ := s.read(from, a.MaxBytes, 0, true)
	header := fmt.Sprintf("[task %s | %s", s.id, status)
	if exit != nil {
		header += fmt.Sprintf(" | exit %d", *exit)
	}
	header += "]\n"
	return actool.Text(actool.Capture(tc, header+out)), nil
}

func (m *managedToolSessions) stop(in json.RawMessage) (actool.Result, error) {
	var a struct {
		ID string `json:"task_id"`
	}
	if err := json.Unmarshal(in, &a); err != nil {
		return actool.Errorf(err.Error()), nil
	}
	if a.ID != "" {
		s, err := m.find(a.ID, false)
		if err != nil {
			return actool.Errorf(err.Error()), nil
		}
		s.cancel()
		<-s.done
		return actool.Text("작업 종료: " + s.id), nil
	}
	m.mu.Lock()
	entries := make([]*managedToolSession, 0)
	for _, s := range m.entries {
		if !s.terminal {
			entries = append(entries, s)
		}
	}
	m.mu.Unlock()
	for _, s := range entries {
		s.cancel()
		<-s.done
	}
	return actool.Text(fmt.Sprintf("백그라운드 작업 %d개를 종료했습니다", len(entries))), nil
}
