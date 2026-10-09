package toolruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestManagedHelperProcess(t *testing.T) {
	mode := os.Getenv("ARTEX_TEST_RUNTIME_HELPER")
	if mode == "" {
		return
	}
	if mode == "guard" {
		if err := InstallProcessTreeGuard(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		child := exec.Command(os.Args[0], "-test.run=^TestManagedHelperProcess$")
		child.Env = append(os.Environ(), "ARTEX_TEST_RUNTIME_HELPER=sleep")
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(4)
		}
		fmt.Println(child.Process.Pid)
		for {
			time.Sleep(time.Hour)
		}
	}
	if mode == "sleep" {
		for {
			time.Sleep(time.Hour)
		}
	}
	if mode == "spawn" {
		child := exec.Command(os.Args[0], "-test.run=^TestManagedHelperProcess$")
		child.Env = append(os.Environ(), "ARTEX_TEST_RUNTIME_HELPER=sleep")
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(5)
		}
		fmt.Println(child.Process.Pid)
		for {
			time.Sleep(time.Hour)
		}
	}
	if mode == "inspect" {
		outside := os.Getenv("ARTEX_TEST_OUTSIDE")
		_, readErr := os.ReadFile(outside)
		writeErr := os.WriteFile(outside, []byte("changed"), 0o600)
		workErr := os.WriteFile("workspace-result.txt", []byte("한글 workspace"), 0o600)
		connection, netErr := net.DialTimeout("tcp", os.Getenv("ARTEX_TEST_NETWORK"), time.Second)
		if connection != nil {
			connection.Close()
		}
		json.NewEncoder(os.Stdout).Encode(map[string]any{"read_denied": readErr != nil, "write_denied": writeErr != nil, "workspace_write": workErr == nil, "network_denied": netErr != nil, "session": os.Getenv("ARTEX_DESKTOP_SESSION"), "path": os.Getenv("PATH")})
		os.Exit(0)
	}
	if mode == "terminal" {
		line := make([]byte, 1024)
		n, err := os.Stdin.Read(line)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(7)
		}
		fmt.Println("terminal-result:" + strings.TrimSpace(string(line[:n])))
		os.Exit(0)
	}
	os.Exit(6)
}

func TestWindowsManagedConPTY(t *testing.T) {
	b, work := executableFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	p, err := b.Start(ctx, Request{Component: "node", Args: []string{"-test.run=^TestManagedHelperProcess$"}, WorkingDir: work, Terminal: true, Environment: map[string]string{"ARTEX_TEST_RUNTIME_HELPER": "terminal"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	go func() { _, _ = p.Stdin.Write([]byte("fixture-input\r\n")) }()
	out, stderr, exit := collectProcess(t, p)
	if exit != 0 || !strings.Contains(out, "terminal-result:fixture-input") {
		t.Fatalf("ConPTY exit=%d stdout=%q stderr=%q", exit, out, stderr)
	}
}

func TestWindowsBundledConPTY(t *testing.T) {
	root := os.Getenv("ARTEX_TEST_TOOL_ROOT")
	if root == "" {
		t.Skip("공식 앱 도구 번들 검사 경로가 지정되지 않았습니다")
	}
	b, err := Load(root, os.Getenv("ARTEX_TEST_TOOL_MANIFEST_SHA256"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p, err := b.Start(ctx, Request{Component: "pty", Args: []string{"-NoLogo", "-NoProfile", "-Command", "$x=Read-Host; Write-Output ('terminal-result:'+$x)"}, WorkingDir: t.TempDir(), Terminal: true})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	go func() { _, _ = p.Stdin.Write([]byte("fixture-input\r\n")) }()
	out, stderr, exit := collectProcess(t, p)
	if exit != 0 || !strings.Contains(out, "terminal-result:fixture-input") {
		t.Fatalf("ConPTY exit=%d stdout=%q stderr=%q", exit, out, stderr)
	}
}

func executableFixture(t *testing.T) (*Bundle, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	digest := writeManifestFixture(t, root, map[string][]byte{"bin/helper.exe": body, "LICENSE": []byte("Go fixture runtime")})
	b, err := Load(root, digest)
	if err != nil {
		t.Fatal(err)
	}
	return b, t.TempDir()
}

func collectProcess(t *testing.T, p *Process) (string, string, int) {
	t.Helper()
	out := make(chan string, 1)
	stderr := make(chan string, 1)
	go func() { body, _ := io.ReadAll(p.Stdout); out <- string(body) }()
	go func() { body, _ := io.ReadAll(p.Stderr); stderr <- string(body) }()
	exit, err := p.Wait()
	if err != nil {
		t.Fatal(err)
	}
	return <-out, <-stderr, exit
}

func TestWindowsAppContainerEnforcesWorkspaceNetworkAndEnvironment(t *testing.T) {
	b, work := executableFixture(t)
	outside := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(outside, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("ARTEX_DESKTOP_SESSION", "parent-secret")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p, err := b.Start(ctx, Request{Component: "node", Args: []string{"-test.run=^TestManagedHelperProcess$"}, WorkingDir: work, Environment: map[string]string{"ARTEX_TEST_RUNTIME_HELPER": "inspect", "ARTEX_TEST_OUTSIDE": outside, "ARTEX_TEST_NETWORK": listener.Addr().String()}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	out, stderr, exit := collectProcess(t, p)
	if exit != 0 {
		t.Fatalf("fixture exit %d stderr=%s stdout=%s", exit, stderr, out)
	}
	var result struct {
		ReadDenied     bool   `json:"read_denied"`
		WriteDenied    bool   `json:"write_denied"`
		WorkspaceWrite bool   `json:"workspace_write"`
		NetworkDenied  bool   `json:"network_denied"`
		Session        string `json:"session"`
		Path           string `json:"path"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err, out, stderr)
	}
	if !result.ReadDenied || !result.WriteDenied || !result.WorkspaceWrite || !result.NetworkDenied || result.Session != "" || strings.Contains(result.Path, os.Getenv("SystemRoot")) {
		t.Fatalf("isolation boundary failed: %+v %s", result, stderr)
	}
	body, err := os.ReadFile(outside)
	if err != nil || string(body) != "original" {
		t.Fatal("outside fixture modified", string(body), err)
	}
	if body, err := os.ReadFile(filepath.Join(work, "workspace-result.txt")); err != nil || string(body) != "한글 workspace" {
		t.Fatal("workspace write missing", string(body), err)
	}
}

func assertProcessGone(t *testing.T, pid int) {
	t.Helper()
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err == windows.ERROR_INVALID_PARAMETER {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	state, err := windows.WaitForSingleObject(h, 5000)
	if err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("descendant %d survived: %d %v", pid, state, err)
	}
}

func TestWindowsManagedCancellationKillsDetachedGrandchild(t *testing.T) {
	b, work := executableFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p, err := b.Start(ctx, Request{Component: "node", Args: []string{"-test.run=^TestManagedHelperProcess$"}, WorkingDir: work, Environment: map[string]string{"ARTEX_TEST_RUNTIME_HELPER": "spawn"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	line := make([]byte, 64)
	n, err := p.Stdout.Read(line)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(line[:n])))
	if err != nil {
		t.Fatal(err, string(line[:n]))
	}
	cancel()
	if _, err := p.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation was not propagated", err)
	}
	assertProcessGone(t, pid)
}

func TestWindowsBundledRuntimeExecution(t *testing.T) {
	root := os.Getenv("ARTEX_TEST_TOOL_ROOT")
	if root == "" {
		t.Skip("공식 앱 도구 번들 검사 경로가 지정되지 않았습니다")
	}
	digest := os.Getenv("ARTEX_TEST_TOOL_MANIFEST_SHA256")
	b, err := Load(root, digest)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		key  string
		args []string
		want string
	}{
		{"node", []string{"-e", "process.stdout.write('node-fixture-'+(process.env.ARTEX_DESKTOP_SESSION || 'clean'))"}, "node-fixture-clean"},
		{"python", []string{"-I", "-X", "utf8", "-c", "import os; print('python-한글-' + os.environ.get('ARTEX_DESKTOP_SESSION','clean'))"}, "python-한글-clean"},
		{"shell", []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "Write-Output 'shell-fixture'"}, "shell-fixture"},
		{"cli", []string{"--version"}, "git version"},
	} {
		t.Run(item.key, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			work := t.TempDir()
			args := item.args
			p, err := b.Start(ctx, Request{Component: item.key, Args: args, WorkingDir: work})
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			p.Stdin.Close()
			out, stderr, exit := collectProcess(t, p)
			if exit != 0 || !strings.Contains(out, item.want) {
				t.Fatalf("%s managed runtime exit=%d output=%q stderr=%q", item.key, exit, out, stderr)
			}
		})
	}
}

func TestWindowsBackendForcedKillClosesInheritedJob(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	root := exec.CommandContext(ctx, exe, "-test.run=^TestManagedHelperProcess$")
	root.Env = append(os.Environ(), "ARTEX_TEST_RUNTIME_HELPER=guard")
	stdout, err := root.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { root.Process.Kill(); root.Wait() }()
	line := make([]byte, 64)
	n, err := stdout.Read(line)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(line[:n])))
	if err != nil {
		t.Fatal(err, string(line[:n]))
	}
	if err := root.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	root.Wait()
	assertProcessGone(t, pid)
}
