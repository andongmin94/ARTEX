package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Settings keys the UI toggles at runtime.
const (
	settingTrafficCapture      = "traffic_capture"
	settingAgentTrafficBinding = "agent_traffic_binding"
	settingWebSearchOn         = "web_search_enabled"
	settingWebSearchBackend    = "web_search_backend"
	settingBraveKey            = "brave_search_api_key"
	settingTavilyKey           = "tavily_search_api_key"
	settingWebSearchProxy      = "web_search_proxy"
	// settingGlobalProxy is the global egress proxy for all target traffic
	// (http/https/socks5). Empty = direct. Distinct from web_search_proxy (which
	// only routes the search backend) and the per-profile LLM proxy.
	settingGlobalProxy = "global_proxy"
	settingWorkers     = "workers"
	settingLLMRecord   = "llm_record"
	// LLM 轮询(故障转移)。默认关闭——开启后走「全局激活配置」的 agent 在当前配置
	// 不可用(余额不足/key 失效/限流/服务异常)时自动切到下一个配置。
	// settingLLMPoolBindFallback 仅在轮询开启时有意义:默认关闭,即 agent/任务显式
	// 绑定了某个配置就只用它、失败即失败;开启后绑定的配置失败也会回落到轮询链。
	settingLLMPoolOn           = "llm_pool_enabled"
	settingLLMPoolBindFallback = "llm_pool_bind_fallback"
	// 任务并发上限:开关 + 上限数。默认关闭;开启后默认上限 5(见 defaultConcurrencyLimit)。
	settingConcurrencyOn    = "task_concurrency_enabled"
	settingConcurrencyLimit = "task_concurrency_limit"
	// 实验功能:noa 模型驱动上下文压缩(norma v0.4.0)。默认关闭——开启后平台接入的四类
	// agent(planner/worker/主 agent/对话)由 noa 接管上下文压缩,取代内置 compaction。
	// 每 run 读一次,切换只影响之后启动的 run。
	settingNoaCompaction = "noa_compaction"
	// defaultWebSearchBackend is used when web search is on but no backend was picked.
	defaultWebSearchBackend = "ddgs"
	// deepSeekWebSearchBackend borrows the active LLM profile instead of its own
	// key, so it only works on an anthropic-format profile pointed at DeepSeek.
	deepSeekWebSearchBackend = "deepseek"
	// defaultWorkers is the concurrent work-agent count when the setting is unset.
	defaultWorkers = 3
	// defaultConcurrencyLimit is the simultaneous-running-task cap when the feature
	// is enabled but no explicit limit was saved.
	defaultConcurrencyLimit = 5
)

type managerRuntimeSettings struct {
	trafficOn, llmRecordOn, webSearchOn                                bool
	webSearchBackend, braveKey, tavilyKey, webSearchProxy, globalProxy string
}

func parseManagerRuntimeSettings(values map[string]string) (managerRuntimeSettings, error) {
	var state managerRuntimeSettings
	for _, field := range []struct {
		key   string
		value *bool
	}{
		{settingTrafficCapture, &state.trafficOn},
		{settingLLMRecord, &state.llmRecordOn},
		{settingWebSearchOn, &state.webSearchOn},
	} {
		value, exists := values[field.key]
		if !exists {
			continue
		}
		switch value {
		case "true", "1":
			*field.value = true
		case "false", "0":
		default:
			// Do not include the value: a malformed row may contain a secret.
			return managerRuntimeSettings{}, fmt.Errorf("저장된 %s 설정이 올바른 불리언 값이 아닙니다", field.key)
		}
	}
	state.webSearchBackend = values[settingWebSearchBackend]
	if state.webSearchBackend == "" {
		state.webSearchBackend = defaultWebSearchBackend
	}
	state.braveKey = values[settingBraveKey]
	state.tavilyKey = values[settingTavilyKey]
	state.webSearchProxy = values[settingWebSearchProxy]
	state.globalProxy = strings.TrimSpace(values[settingGlobalProxy])
	return state, nil
}

func prepareManagerDirectory(dir string) (string, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("데이터 경로 확인: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return "", fmt.Errorf("데이터 디렉터리 준비: %w", err)
	}
	return absolute, nil
}

// browserProxySettings preserves unrelated MCP options. Invalid JSON is an
// initialization error, not an invitation to replace the user's configuration.
func browserProxySettings(argsJSON, envJSON json.RawMessage, proxy, cert string) (json.RawMessage, json.RawMessage, error) {
	var decodedArgs []*string
	if err := json.Unmarshal(argsJSON, &decodedArgs); err != nil || decodedArgs == nil {
		return nil, nil, fmt.Errorf("browser MCP args는 문자열 배열이어야 합니다")
	}
	args := make([]string, 0, len(decodedArgs))
	for _, value := range decodedArgs {
		if value == nil {
			return nil, nil, fmt.Errorf("browser MCP args에는 null을 사용할 수 없습니다")
		}
		args = append(args, *value)
	}
	var decodedEnv map[string]*string
	if err := json.Unmarshal(envJSON, &decodedEnv); err != nil || decodedEnv == nil {
		return nil, nil, fmt.Errorf("browser MCP env는 문자열 값의 객체여야 합니다")
	}
	env := make(map[string]string, len(decodedEnv))
	for key, value := range decodedEnv {
		if value == nil {
			return nil, nil, fmt.Errorf("browser MCP env에는 null을 사용할 수 없습니다")
		}
		env[key] = *value
	}
	args = stripProxyArgs(args)
	delete(env, "NODE_EXTRA_CA_CERTS")
	if proxy != "" {
		args = append(args, "--proxy-server", proxy)
		if cert != "" {
			env["NODE_EXTRA_CA_CERTS"] = cert
		}
	}
	// Both values have concrete JSON-safe types after successful decoding.
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, nil, err
	}
	envJSON, err = json.Marshal(env)
	if err != nil {
		return nil, nil, err
	}
	return argsJSON, envJSON, nil
}

// stripProxyArgs removes both separated and --flag=value proxy arguments.
func stripProxyArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--proxy-server" || a == "--proxy-bypass" {
			i++
			continue
		}
		if strings.HasPrefix(a, "--proxy-server=") || strings.HasPrefix(a, "--proxy-bypass=") {
			continue
		}
		out = append(out, a)
	}
	return out
}
