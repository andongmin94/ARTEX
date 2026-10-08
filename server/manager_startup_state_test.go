package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestManagerRuntimeSettingsDefaults(t *testing.T) {
	got, err := parseManagerRuntimeSettings(nil)
	if err != nil || got != (managerRuntimeSettings{webSearchBackend: defaultWebSearchBackend}) {
		t.Fatalf("unexpected default state: %+v, %v", got, err)
	}
}

func TestManagerRuntimeSettingsRestoresOneSnapshot(t *testing.T) {
	values := map[string]string{
		settingTrafficCapture: "true", settingLLMRecord: "1", settingWebSearchOn: "false",
		settingWebSearchBackend: "tavily", settingBraveKey: "첫 키", settingTavilyKey: "두 번째 키",
		settingWebSearchProxy: "http://127.0.0.1:8001", settingGlobalProxy: "  socks5://127.0.0.1:8002  ",
	}
	got, err := parseManagerRuntimeSettings(values)
	if err != nil {
		t.Fatal(err)
	}
	want := managerRuntimeSettings{
		trafficOn: true, llmRecordOn: true, webSearchBackend: "tavily",
		braveKey: "첫 키", tavilyKey: "두 번째 키", webSearchProxy: "http://127.0.0.1:8001",
		globalProxy: "socks5://127.0.0.1:8002",
	}
	if got != want {
		t.Fatal("restored settings do not match the stored snapshot")
	}
	values[settingBraveKey] = "changed"
	if got.braveKey != want.braveKey {
		t.Fatal("runtime state aliases the source map")
	}
}

func TestManagerRuntimeSettingsRejectsCorruptFlags(t *testing.T) {
	for _, key := range []string{settingTrafficCapture, settingLLMRecord, settingWebSearchOn} {
		for _, value := range []string{"", "TRUE", "yes", "do-not-leak-this-secret"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				got, err := parseManagerRuntimeSettings(map[string]string{key: value, settingBraveKey: "private"})
				if err == nil || got != (managerRuntimeSettings{}) {
					t.Fatal("corrupt setting returned a usable runtime snapshot")
				}
				if !strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "do-not-leak-this-secret") || strings.Contains(err.Error(), "private") {
					t.Fatal("error did not identify the field safely")
				}
			})
		}
	}
	for _, value := range []string{"false", "0", "true", "1"} {
		got, err := parseManagerRuntimeSettings(map[string]string{settingTrafficCapture: value})
		if err != nil || got.trafficOn != (value == "true" || value == "1") {
			t.Fatalf("valid boolean %q was rejected", value)
		}
	}
}

func TestManagerDirectoryIsAbsoluteAndPreservesFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "한글 공백 # 100%", "data")
	got, err := prepareManagerDirectory(dir)
	if err != nil || !filepath.IsAbs(got) || got != dir {
		t.Fatalf("directory=%q err=%v", got, err)
	}
	marker := filepath.Join(dir, "evidence.txt")
	if err := os.WriteFile(marker, []byte("보존"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareManagerDirectory(dir); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(marker)
	if err != nil || string(body) != "보존" {
		t.Fatal("directory initialization changed existing data")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(dir)
		if err != nil || info.Mode().Perm()&0o077 != 0 {
			t.Fatal("new data directory is not private")
		}
	}
}

func TestManagerDirectoryRejectsFileAndInvalidPath(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, filepath.Join(file, "child"), "bad\x00path"} {
		if result, err := prepareManagerDirectory(path); err == nil || result != "" {
			t.Fatalf("accepted invalid directory %q", path)
		}
	}
	body, _ := os.ReadFile(file)
	if string(body) != "unchanged" {
		t.Fatal("invalid directory initialization replaced a file")
	}
}

func TestBrowserProxySettingsPreservesUserOptions(t *testing.T) {
	args := json.RawMessage(`["@playwright/mcp","--headless","--proxy-server=old","--proxy-bypass","example.invalid","--user-data-dir","한글 경로"]`)
	env := json.RawMessage(`{"TOKEN":"keep-private","NODE_EXTRA_CA_CERTS":"old","OPTION":""}`)
	beforeArgs, beforeEnv := string(args), string(env)
	gotArgs, gotEnv, err := browserProxySettings(args, env, "http://127.0.0.1:8181", "/한글 #/ca.pem")
	if err != nil {
		t.Fatal(err)
	}
	var decodedArgs []string
	var decodedEnv map[string]string
	if err := json.Unmarshal(gotArgs, &decodedArgs); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(gotEnv, &decodedEnv); err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"@playwright/mcp", "--headless", "--user-data-dir", "한글 경로", "--proxy-server", "http://127.0.0.1:8181"}
	if !reflect.DeepEqual(decodedArgs, wantArgs) || decodedEnv["TOKEN"] != "keep-private" || decodedEnv["NODE_EXTRA_CA_CERTS"] != "/한글 #/ca.pem" {
		t.Fatal("unrelated MCP settings were lost")
	}
	if string(args) != beforeArgs || string(env) != beforeEnv {
		t.Fatal("input JSON was modified")
	}
	againArgs, againEnv, err := browserProxySettings(gotArgs, gotEnv, "http://127.0.0.1:8181", "/한글 #/ca.pem")
	if err != nil || string(againArgs) != string(gotArgs) || string(againEnv) != string(gotEnv) {
		t.Fatal("reconciliation is not idempotent")
	}
	offArgs, offEnv, err := browserProxySettings(gotArgs, gotEnv, "", "")
	if err != nil || strings.Contains(string(offArgs), "proxy-server") || strings.Contains(string(offEnv), "NODE_EXTRA_CA_CERTS") || !strings.Contains(string(offEnv), "keep-private") {
		t.Fatal("capture-off reconciliation did not preserve unrelated settings")
	}
}

func TestBrowserProxySettingsRejectsInvalidJSONWithoutReplacement(t *testing.T) {
	cases := [][2]string{
		{"", "{}"}, {"null", "{}"}, {"{}", "{}"}, {`[1]`, "{}"}, {`[null]`, "{}"},
		{"[]", ""}, {"[]", "null"}, {"[]", "[]"}, {"[]", `{"KEY":false}`}, {"[]", `{"KEY":null}`},
		{`["do-not-leak-this-secret"`, "{}"}, {"[]", `{"do-not-leak-this-secret":123}`},
	}
	for i, pair := range cases {
		a, e, err := browserProxySettings(json.RawMessage(pair[0]), json.RawMessage(pair[1]), "", "")
		if err == nil || a != nil || e != nil {
			t.Fatalf("case %d returned replacement settings for invalid JSON", i)
		}
		if strings.Contains(err.Error(), "do-not-leak-this-secret") {
			t.Fatal("malformed settings leaked through the error")
		}
	}
	a, e, err := browserProxySettings(json.RawMessage("[]"), json.RawMessage("{}"), "", "")
	if err != nil || string(a) != "[]" || string(e) != "{}" {
		t.Fatalf("empty valid settings rejected: %s %s %v", a, e, err)
	}
}
