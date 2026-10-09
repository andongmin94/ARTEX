package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Autumn-27/artex/internal/toolruntime"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

type managedDesktopTool struct {
	actool.CoreTool
	sessions *managedToolSessions
}

func (t managedDesktopTool) Close() error {
	if t.sessions != nil {
		return t.sessions.Close()
	}
	return nil
}

func (t managedDesktopTool) Description() string {
	return t.CoreTool.Description() + "\n앱 전용 PowerShell 명령을 사용합니다. 작업 폴더 외 파일과 직접 네트워크 접근은 차단되며 완료한 백그라운드 작업은 TaskOutput으로 확인합니다."
}

func (t managedDesktopTool) Prompt() string {
	if t.Name() == "Bash" || t.Name() == "Monitor" {
		return "앱에 포함된 PowerShell로 명령을 실행합니다. Python/Node/Git은 고정된 앱 도구 경로에서 사용합니다. Bash/POSIX 구문 대신 PowerShell 구문을 사용하세요. 백그라운드 실행은 run_in_background로 요청하고 TaskOutput·TaskList·TaskStop으로 관리합니다. foreground 시간 제한이 지나면 해당 프로세스를 종료합니다."
	}
	return t.CoreTool.Prompt() + "\nPTY 완료 상태와 dispatch/monitor 알림은 shell_read 또는 shell_list의 notifications에서 확인합니다."
}

func (t managedDesktopTool) InputSchema() map[string]any {
	if t.Name() != "Bash" {
		return t.CoreTool.InputSchema()
	}
	source := t.CoreTool.InputSchema()
	schema := make(map[string]any, len(source))
	for k, v := range source {
		schema[k] = v
	}
	props := make(map[string]any)
	if existing, ok := source["properties"].(map[string]any); ok {
		for k, v := range existing {
			props[k] = v
		}
	}
	props["run_in_background"] = map[string]any{"type": "boolean", "description": "작업 전용 프로세스를 백그라운드에서 실행하고 TaskOutput으로 결과를 읽습니다."}
	schema["properties"] = props
	return schema
}

func (t managedDesktopTool) Call(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	if t.sessions != nil {
		return t.sessions.call(ctx, t.Name(), in, tc)
	}
	if t.Name() == "Bash" {
		return runManagedBash(ctx, in, tc)
	}
	return actool.Errorf("관리된 PTY·백그라운드 도구 실행이 아직 준비되지 않았습니다"), nil
}

func runManagedBash(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	if desktopToolsUnavailable() {
		return actool.Errorf(unmanagedDesktopToolsMessage), nil
	}
	var input struct {
		Command    string `json:"command"`
		TimeoutMs  int    `json:"timeout_ms"`
		Background bool   `json:"run_in_background"`
	}
	if err := json.Unmarshal(in, &input); err != nil {
		return actool.Errorf("명령 입력 형식이 유효하지 않습니다"), nil
	}
	if strings.TrimSpace(input.Command) == "" {
		return actool.Errorf("command가 비어 있습니다"), nil
	}
	if input.Background {
		return actool.Errorf("백그라운드 실행에는 에이전트 실행 소유자가 필요합니다"), nil
	}
	if floor := actool.NewBash().CheckPermissions(ctx, in, permission.Context{}); floor.Behavior == permission.Deny {
		return actool.Errorf(floor.Message), nil
	}
	if tc == nil {
		return actool.Errorf("도구 작업 폴더가 지정되지 않았습니다"), nil
	}
	if err := validateManagedWorkspace(tc.WorkingDir); err != nil {
		return actool.Errorf(err.Error()), nil
	}
	timeout := timeoutOr(input.TimeoutMs, 120000)
	if timeout > 10*time.Minute {
		timeout = 10 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	bundle, err := toolruntime.FromEnvironment()
	if err != nil {
		return actool.Errorf(unmanagedDesktopToolsMessage + ": " + err.Error()), nil
	}
	out, err := runManagedCapture(runCtx, bundle, toolruntime.Request{Component: "shell", Args: managedPowerShellArgs(input.Command, false), WorkingDir: tc.WorkingDir}, nil)
	if err != nil {
		return actool.Errorf(actool.Capture(tc, out) + "\n" + err.Error()), nil
	}
	if strings.TrimSpace(out) == "" {
		out = "(명령 출력 없음)"
	}
	return actool.Text(actool.Capture(tc, out)), nil
}

func managedPowerShellArgs(command string, terminal bool) []string {
	args := []string{"-NoLogo", "-NoProfile"}
	if !terminal {
		args = append(args, "-NonInteractive")
	}
	// Console applications otherwise retain the host ANSI code page even when
	// their standard handles are redirected. Keep all managed text pipes UTF-8.
	command = "[Console]::InputEncoding=[System.Text.UTF8Encoding]::new($false); [Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false); $OutputEncoding=[Console]::OutputEncoding; " + command
	return append(args, "-Command", command)
}

func validateManagedWorkspace(work string) error {
	home := os.Getenv("ARTEX_HOME")
	if !filepath.IsAbs(home) || !filepath.IsAbs(work) {
		return errors.New("앱 도구 작업 폴더를 확인할 수 없습니다")
	}
	work = filepath.Clean(work)
	for _, kind := range []string{"tasks", "sessions", "tool-workspaces"} {
		root := filepath.Join(workspaceDirectory(filepath.Join(home, "data")), kind)
		rel, err := filepath.Rel(root, work)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil
		}
	}
	return errors.New("명령 실행은 앱의 작업별 전용 폴더에서만 허용됩니다")
}

// Caller-owned test/editor calls receive a new workspace rather than the data
// root; granting that root to a tool would also expose SQLite and credentials.
func (s *Server) managedEditorWorkspace() (string, error) {
	return newManagedToolWorkspace("editor-")
}

func runManagedCapture(ctx context.Context, b *toolruntime.Bundle, r toolruntime.Request, input []byte) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p, err := b.Start(ctx, r)
	if err != nil {
		return "", err
	}
	defer p.Close()
	writeDone := make(chan error, 1)
	go func() {
		_, err := p.Stdin.Write(input)
		closeErr := p.Stdin.Close()
		if err == nil {
			err = closeErr
		}
		writeDone <- err
	}()
	type output struct {
		body []byte
		err  error
	}
	read := func(reader io.Reader, ch chan output) {
		body, err := io.ReadAll(io.LimitReader(reader, (8<<20)+1))
		if len(body) > 8<<20 {
			body = body[:8<<20]
			err = errors.New("도구 출력이 8 MiB 제한을 초과했습니다")
			cancel()
		}
		ch <- output{body, err}
	}
	stdout, stderr := make(chan output, 1), make(chan output, 1)
	go read(p.Stdout, stdout)
	go read(p.Stderr, stderr)
	exit, waitErr := p.Wait()
	out, errOut := <-stdout, <-stderr
	body := string(out.body) + string(errOut.body)
	if out.err != nil || errOut.err != nil {
		return body, errors.Join(out.err, errOut.err)
	}
	if waitErr != nil {
		return body, fmt.Errorf("도구 실행 종료: %w", waitErr)
	}
	if err := <-writeDone; err != nil && len(input) > 0 {
		return body, fmt.Errorf("도구 매개변수 전달: %w", err)
	}
	if exit != 0 {
		return body, fmt.Errorf("도구 종료 코드 %d", exit)
	}
	return body, nil
}

func managedPythonPath() (string, error) {
	b, err := toolruntime.FromEnvironment()
	if err != nil {
		return "", err
	}
	if err = b.Verify("python"); err != nil {
		return "", err
	}
	c, _ := b.Component("python")
	return b.Path(c.Entrypoint)
}

func runManagedPython(ctx context.Context, key, code string, params map[string]any, tc *actool.ToolContext, timeout time.Duration) (actool.Result, error) {
	if tc == nil {
		return actool.Errorf("도구 작업 폴더가 지정되지 않았습니다"), nil
	}
	if err := validateManagedWorkspace(tc.WorkingDir); err != nil {
		return actool.Errorf(err.Error()), nil
	}
	b, err := toolruntime.FromEnvironment()
	if err != nil {
		return actool.Errorf(unmanagedDesktopToolsMessage + ": " + err.Error()), nil
	}
	dir := filepath.Join(tc.WorkingDir, ".tools")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return actool.Errorf(err.Error()), nil
	}
	if err := toolruntime.ValidateDirectory(dir); err != nil {
		return actool.Errorf(err.Error()), nil
	}
	f, err := os.CreateTemp(dir, "script-*.py")
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(code); err != nil {
		f.Close()
		return actool.Errorf(err.Error()), nil
	}
	if err := f.Close(); err != nil {
		return actool.Errorf(err.Error()), nil
	}
	env := make(map[string]string)
	for k, v := range params {
		if scalar, ok := scalarStr(v); ok {
			env["TOOL_"+strings.ToUpper(k)] = scalar
		}
	}
	input, _ := json.Marshal(params)
	if timeout > 10*time.Minute {
		timeout = 10 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := runManagedCapture(runCtx, b, toolruntime.Request{Component: "python", Args: []string{"-I", "-X", "utf8", f.Name()}, WorkingDir: tc.WorkingDir, Environment: env}, input)
	if err != nil {
		return actool.Errorf(actool.Capture(tc, out) + "\n" + err.Error()), nil
	}
	return actool.Text(actool.Capture(tc, out)), nil
}
