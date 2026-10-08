"use client";

import * as React from "react";

import { CpuIcon, FlaskConicalIcon, KeyboardIcon, RadioTowerIcon, SearchIcon, ShieldAlertIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { api } from "@/lib/api";
import { CHAT_SEND_MODE_OPTIONS, type ChatSendMode, setChatSendMode, useChatSendMode } from "@/lib/chat-send-mode";
import type { Settings } from "@/lib/types";

import { UpdateCard } from "./_components/update-card";
import { RuntimeToolsCard } from "./_components/runtime-tools-card";

export default function SystemSettingsPage() {
  const [trafficCapture, setTrafficCapture] = React.useState(false);
  const [agentTrafficBinding, setAgentTrafficBinding] = React.useState(false);
  const [webSearch, setWebSearch] = React.useState(false);
  const [backend, setBackend] = React.useState("ddgs");
  const [braveKeySet, setBraveKeySet] = React.useState(false);
  const [braveKeyInput, setBraveKeyInput] = React.useState("");
  const [tavilyKeySet, setTavilyKeySet] = React.useState(false);
  const [tavilyKeyInput, setTavilyKeyInput] = React.useState("");
  const [savingTavilyKey, setSavingTavilyKey] = React.useState(false);
  const [proxyInput, setProxyInput] = React.useState("");
  const [savingProxy, setSavingProxy] = React.useState(false);
  const [globalProxyInput, setGlobalProxyInput] = React.useState("");
  const [savingGlobalProxy, setSavingGlobalProxy] = React.useState(false);
  const [testing, setTesting] = React.useState(false);
  const [loaded, setLoaded] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [savingKey, setSavingKey] = React.useState(false);
  const [pyInterp, setPyInterp] = React.useState("");
  const [workers, setWorkers] = React.useState("3");
  const [savingWorkers, setSavingWorkers] = React.useState(false);
  // 操作约束注入范围(默认都开)。
  const [injectPlanner, setInjectPlanner] = React.useState(true);
  const [injectWorker, setInjectWorker] = React.useState(true);
  // 实验功能:noa 上下文压缩(默认关)。
  const [noaCompaction, setNoaCompaction] = React.useState(false);
  // 纯前端偏好：不走 /api/settings，直接读写 localStorage。
  const sendMode = useChatSendMode();

  const apply = React.useCallback((s: Settings) => {
    setTrafficCapture(!!s.traffic_capture);
    setAgentTrafficBinding(!!s.agent_traffic_binding);
    setWebSearch(!!s.web_search_enabled);
    setBackend(s.web_search_backend || "ddgs");
    setBraveKeySet(!!s.brave_key_set);
    setTavilyKeySet(!!s.tavily_key_set);
    setProxyInput(s.web_search_proxy ?? "");
    setGlobalProxyInput(s.global_proxy ?? "");
    setPyInterp(s.python_interpreter ?? "");
    setWorkers(String(s.workers ?? 3));
    setInjectPlanner(s.constraints_inject_planner !== false);
    setInjectWorker(s.constraints_inject_worker !== false);
    setNoaCompaction(!!s.noa_compaction);
  }, []);

  const saveWorkers = () => {
    const n = Number(workers);
    if (!Number.isInteger(n) || n <= 0) {
      toast.error("동시 실행 수는 0보다 큰 정수여야 합니다");
      return;
    }
    setSavingWorkers(true);
    api
      .setSettings({ workers: n })
      .then((s) => {
        apply(s);
        toast.success("동시 실행 에이전트 수를 저장했습니다(이후 시작하는 작업부터 적용)");
      })
      .catch((e) => toast.error("저장 실패:" + (e as Error).message))
      .finally(() => setSavingWorkers(false));
  };

  const savePython = () => {
    setSaving(true);
    api
      .setSettings({ python_interpreter: pyInterp.trim() })
      .then((s) => {
        apply(s);
        toast.success("Python 인터프리터 설정을 저장했습니다");
      })
      .catch((e) => toast.error("저장 실패:" + (e as Error).message))
      .finally(() => setSaving(false));
  };
  const detectPython = () => {
    setSaving(true);
    api
      .detectPython()
      .then((r) => setPyInterp(r.python_interpreter))
      .catch(() => undefined)
      .finally(() => setSaving(false));
  };

  React.useEffect(() => {
    api
      .settings()
      .then(apply)
      .catch(() => undefined)
      .finally(() => setLoaded(true));
  }, [apply]);

  const toggleTraffic = (v: boolean) => {
    setTrafficCapture(v); // optimistic
    setSaving(true);
    api
      .setSettings({ traffic_capture: v })
      .then(apply)
      .catch(() => setTrafficCapture(!v)) // revert on failure
      .finally(() => setSaving(false));
  };

  const toggleInjectPlanner = (v: boolean) => {
    setInjectPlanner(v); // optimistic
    api
      .setSettings({ constraints_inject_planner: v })
      .then(apply)
      .catch(() => setInjectPlanner(!v)); // revert on failure
  };

  const toggleAgentTrafficBinding = (v: boolean) => {
    setAgentTrafficBinding(v);
    setSaving(true);
    api
      .setSettings({ agent_traffic_binding: v })
      .then((s) => {
        apply(s);
        toast.success(v ? "에이전트의 트래픽 자동 연결을 켰습니다" : "에이전트의 트래픽 자동 연결을 껐습니다");
      })
      .catch((e) => {
        setAgentTrafficBinding(!v);
        toast.error(`저장 실패:${(e as Error).message}`);
      })
      .finally(() => setSaving(false));
  };

  const toggleInjectWorker = (v: boolean) => {
    setInjectWorker(v); // optimistic
    api
      .setSettings({ constraints_inject_worker: v })
      .then(apply)
      .catch(() => setInjectWorker(!v)); // revert on failure
  };

  const toggleNoaCompaction = (v: boolean) => {
    setNoaCompaction(v); // optimistic
    api
      .setSettings({ noa_compaction: v })
      .then((s) => {
        apply(s);
        toast.success(v ? "noa 컨텍스트 압축을 켰습니다(이후 시작하는 실행부터 적용)" : "noa 컨텍스트 압축을 끄고 기본 압축으로 복원했습니다");
      })
      .catch((e) => {
        setNoaCompaction(!v); // revert on failure
        toast.error(`저장 실패:${(e as Error).message}`);
      });
  };

  // Persist a web-search patch (enable and/or backend). Optimistic with refetch.
  const saveWebSearch = (patch: Partial<Settings>) => {
    setSaving(true);
    api
      .setSettings(patch)
      .then((s) => {
        apply(s);
        toast.success("웹 검색 설정을 저장했습니다");
      })
      .catch((e) => {
        toast.error("저장 실패:" + (e as Error).message);
        api
          .settings()
          .then(apply)
          .catch(() => undefined);
      })
      .finally(() => setSaving(false));
  };

  const saveBraveKey = () => {
    setSavingKey(true);
    api
      .setSettings({ brave_search_api_key: braveKeyInput })
      .then((s) => {
        apply(s);
        setBraveKeyInput("");
        toast.success("Brave API Key를 저장했습니다");
      })
      .catch((e) => toast.error("저장 실패:" + (e as Error).message))
      .finally(() => setSavingKey(false));
  };

  const saveTavilyKey = () => {
    setSavingTavilyKey(true);
    api
      .setSettings({ tavily_search_api_key: tavilyKeyInput })
      .then((s) => {
        apply(s);
        setTavilyKeyInput("");
        toast.success("Tavily API Key를 저장했습니다");
      })
      .catch((e) => toast.error("저장 실패:" + (e as Error).message))
      .finally(() => setSavingTavilyKey(false));
  };

  const saveProxy = () => {
    setSavingProxy(true);
    api
      .setSettings({ web_search_proxy: proxyInput.trim() })
      .then((s) => {
        apply(s);
        toast.success(proxyInput.trim() ? "외부 연결 프록시를 저장했습니다" : "외부 연결 프록시를 제거하고 직접 연결로 변경했습니다");
      })
      .catch((e) => toast.error("저장 실패:" + (e as Error).message))
      .finally(() => setSavingProxy(false));
  };

  const saveGlobalProxy = () => {
    setSavingGlobalProxy(true);
    api
      .setSettings({ global_proxy: globalProxyInput.trim() })
      .then((s) => {
        apply(s);
        toast.success(globalProxyInput.trim() ? "전역 프록시를 저장했습니다" : "전역 프록시를 제거하고 직접 연결로 변경했습니다");
      })
      .catch((e) => toast.error("저장 실패:" + (e as Error).message))
      .finally(() => setSavingGlobalProxy(false));
  };

  // Run a real "test" search ("test") against the CURRENT form values (backend +
  // proxy + entered key), falling back to saved values server-side. Toasts result.
  const runTest = () => {
    setTesting(true);
    api
      .testWebSearch({
        web_search_backend: backend,
        web_search_proxy: proxyInput.trim(),
        brave_search_api_key: braveKeyInput,
        tavily_search_api_key: tavilyKeyInput,
      })
      .then((r) => {
        if (r.ok) toast.success(`검색 테스트 성공 · ${r.backend} 돌아가기 ${r.count} 건의 결과`);
        else toast.error("검색 테스트 실패:" + (r.error || "알 수 없는 오류"));
      })
      .catch((e) => toast.error("검색 테스트 실패:" + (e as Error).message))
      .finally(() => setTesting(false));
  };

  // brave-free selected but no key stored and none being entered → tool stays off.
  const braveNeedsKey = webSearch && backend === "brave-free" && !braveKeySet;

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">시스템 설정</h1>
        <p className="text-muted-foreground text-sm">전역 실행 설정</p>
      </div>

      {/* 多列而非 grid：网络搜索卡片比其余高数倍，且高度随所选后端变化（brave/tavily
          的 key 输入是条件渲染）。grid 会按最高的一张撑满整行、在旁边留下大片空白，
          多列则自动按内容高度平衡填充。卡片间距靠 mb 而非 gap——多列布局下
          column-gap 只管列间距，行间距要由子元素自己给。 */}
      <div className="columns-1 gap-4 md:gap-6 lg:columns-2">
        <UpdateCard />
        <RuntimeToolsCard />

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RadioTowerIcon className="size-4" />
              트래픽 캡처
            </CardTitle>
            <CardDescription>
              활성화하면 모든 에이전트의 HTTP 트래픽을 기록 프록시를 통해 저장하고 traffic_search / traffic_get 도구와 프록시 설정을 제공합니다. 프롬프트에도 프록시 안내를 포함합니다.
              <br />
              끄면(기본값) 트래픽을 기록하지 않습니다. 에이전트에는
              <b>제공하지 않는 항목:</b>프록시 설정 및 트래픽 도구. 프롬프트에서도<b>제외되는 항목:</b>프록시 관련 안내. 전환하면 에이전트를 다시 구성하여 즉시 적용합니다.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Label htmlFor="traffic-capture" className="text-sm font-normal text-muted-foreground">
              {trafficCapture ? "켜짐 · 트래픽 기록 및 프록시 설정 제공" : "꺼짐 · 기록 및 프록시 설정 제공 안 함"}
            </Label>
            <Switch
              id="traffic-capture"
              checked={trafficCapture}
              disabled={!loaded || saving}
              onCheckedChange={toggleTraffic}
            />
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RadioTowerIcon className="size-4" />
              에이전트의 트래픽 자동 연결
            </CardTitle>
            <CardDescription id="agent-traffic-binding-description">
              기본값은 꺼짐입니다. 활성화하면 취약점 등록 시 보고서 에이전트가 기존 HTTP 요청/응답을 검토하고 해당 트래픽을 연결한 뒤 보고서를 작성합니다.
              <b>패킷 검토와 추가 도구 호출은 토큰 사용량을 늘립니다.</b>
              <br />
              TCP 취약점, 미캡처 항목, 일치하는 트래픽이 없는 항목도 등록할 수 있습니다. 이 설정은 트래픽 캡처, 수동 연결, 기존 증거 조회에는 영향을 주지 않습니다. 다음 에이전트 라운드부터 적용하며, 끄면 새로운 자동 연결은 즉시 차단합니다.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Label htmlFor="agent-traffic-binding" className="text-sm font-normal text-muted-foreground">
              {agentTrafficBinding ? "켜짐 · 토큰 사용량 증가" : "꺼짐 · 수동 연결 가능"}
            </Label>
            <Switch
              id="agent-traffic-binding"
              aria-describedby="agent-traffic-binding-description"
              checked={agentTrafficBinding}
              disabled={!loaded || saving}
              onCheckedChange={toggleAgentTrafficBinding}
            />
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RadioTowerIcon className="size-4" />
              전역 프록시
            </CardTitle>
            <CardDescription>
              모든 에이전트의<b>대상 트래픽</b>은 이 프록시를 통해 외부로 연결됩니다(출발지 IP 변경 / 중계 서버 경유). 지원 형식: <b>http / https / socks5</b>, 인증 방식:{" "}
              <code>user:pass</code> . 비워두면 직접 연결합니다.
              <br />
              활성화:<b>트래픽 캡처</b>가 켜져 있으면 기록 프록시의<b>상위 프록시</b>로 동작합니다. 트래픽을 먼저 저장한 뒤 이 프록시로 전송합니다. 캡처를 끄면 에이전트의 Bash / WebFetch에 직접 설정합니다. 웹 검색 및 LLM 프록시와는 독립적입니다.
              <br />
              <b>힌트</b>: SOCKS5 사용 시<b>캡처가 꺼져 있으면</b>각 명령줄 도구가 <code>ALL_PROXY</code> 를 지원해야 합니다(curl은 지원하지만 일부 도구는 무시할 수 있음). SOCKS5를 주로 사용한다면 트래픽 캡처를 켜는 편이 좋습니다. 이 경우 MITM 프록시가 직접 연결하므로 개별 도구의 지원 여부와 관계없이 적용됩니다.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-2">
            <Label htmlFor="global-proxy" className="text-sm font-normal text-muted-foreground">
              프록시 주소
            </Label>
            <div className="flex items-center gap-2">
              <Input
                id="global-proxy"
                autoComplete="off"
                placeholder="socks5://user:pass@host:1080 또는 http://host:port(비워두면 직접 연결)"
                value={globalProxyInput}
                disabled={!loaded || savingGlobalProxy}
                onChange={(e) => setGlobalProxyInput(e.target.value)}
              />
              <Button type="button" onClick={saveGlobalProxy} disabled={!loaded || savingGlobalProxy}>
                저장
              </Button>
            </div>
            <p className="text-muted-foreground text-xs">
              {globalProxyInput.trim() ? "설정됨 · 모든 대상 트래픽이 이 프록시를 경유합니다" : "미설정 · 대상에 직접 연결합니다"}
            </p>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <ShieldAlertIcon className="size-4" />
              실행 제약 전달
            </CardTitle>
            <CardDescription>
              활성화하면 각 작업의<b>실행 제약</b>(작업 개요에서 관리하는 allow/deny 항목)을 해당 에이전트의 시스템 프롬프트에 포함합니다. 「현재 포트만 테스트」, 「무차별 대입 금지」 같은 탐색 경계를 전달합니다.
              <br />
              전달 대상을 각각 설정할 수 있습니다: <b>계획 에이전트(planner)</b>및 <b>실행 에이전트(worker)</b>
              . 기본적으로 둘 다 켜져 있습니다. 다음 라운드부터 적용되며 에이전트를 재구성할 필요가 없습니다. 끄면 해당 에이전트에 제약을 전달하지 않습니다.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex items-center justify-between gap-4">
              <Label htmlFor="inject-planner" className="text-sm font-normal text-muted-foreground">
                계획 에이전트(planner)에 전달{injectPlanner ? " · 켜짐" : " · 꺼짐"}
              </Label>
              <Switch
                id="inject-planner"
                checked={injectPlanner}
                disabled={!loaded}
                onCheckedChange={toggleInjectPlanner}
              />
            </div>
            <div className="flex items-center justify-between gap-4">
              <Label htmlFor="inject-worker" className="text-sm font-normal text-muted-foreground">
                실행 에이전트(worker)에 전달{injectWorker ? " · 켜짐" : " · 꺼짐"}
              </Label>
              <Switch
                id="inject-worker"
                checked={injectWorker}
                disabled={!loaded}
                onCheckedChange={toggleInjectWorker}
              />
            </div>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <FlaskConicalIcon className="size-4" />
              실험 기능
            </CardTitle>
            <CardDescription>
              검증 중인 기능으로 기본값은 꺼짐입니다. 에이전트 동작이나 안정성에 영향을 줄 수 있으므로 동작을 이해한 뒤 활성화하세요.
              <br />
              <b>noa 컨텍스트 압축</b>: 모델이 긴 대화 기록을 직접 압축합니다(norma v0.4.0). 활성화하면 네 종류의 에이전트(
              <b>계획 / 실행 / 주 에이전트 / 대화</b>)가 기본 압축 대신 noa를 사용합니다. 압축 전 원문은 작업 디렉터리에 보관하여 추적할 수 있습니다. 이후 시작하는 실행부터 적용하며 에이전트를 재구성할 필요가 없습니다. 끄면 기본 압축으로 복원합니다.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Label htmlFor="noa-compaction" className="text-sm font-normal text-muted-foreground">
              noa 컨텍스트 압축{noaCompaction ? " · 켜짐" : " · 꺼짐"}
            </Label>
            <Switch
              id="noa-compaction"
              checked={noaCompaction}
              disabled={!loaded}
              onCheckedChange={toggleNoaCompaction}
            />
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <SearchIcon className="size-4" />
              웹 검색
            </CardTitle>
            <CardDescription>
              웹 검색의<b>전체 스위치 및 검색 서비스 설정</b>입니다. 켜야<b>각 에이전트 설정</b>에서 개별적으로 사용 여부를 지정할 수 있는 도구:
              <b>web_search</b>. 제목·링크·요약만 반환하며 본문은 WebFetch로 가져옵니다. 웹 검색은<b>독립 연결을 사용하며</b>
              트래픽 기록 프록시와 캡처의 영향을 받지 않습니다.
              <br />
              검색 서비스: <b>DuckDuckGo（ddgs）</b>(Key 불필요),<b>Brave(무료 플랜)</b>(Brave API Key 필요),{" "}
              <b>Tavily</b>(Tavily API Key 필요) 또는 <b>DeepSeek</b>(현재 LLM 설정 재사용). 전체 스위치를 끄면 개별 에이전트의 웹 검색도 사용할 수 없습니다.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex items-center justify-between gap-4">
              <Label htmlFor="web-search" className="text-sm font-normal text-muted-foreground">
                {webSearch ? "켜짐 · 각 에이전트 설정에서 개별 활성화 가능" : "꺼짐 · 에이전트의 웹 검색 사용 불가"}
              </Label>
              <Switch
                id="web-search"
                checked={webSearch}
                disabled={!loaded || saving}
                onCheckedChange={(v) => {
                  setWebSearch(v); // optimistic
                  saveWebSearch({ web_search_enabled: v });
                }}
              />
            </div>

            {webSearch && (
              <div className="flex items-center justify-between gap-4">
                <Label className="text-sm font-normal text-muted-foreground">검색 서비스</Label>
                <Select
                  value={backend}
                  disabled={!loaded || saving}
                  onValueChange={(v) => {
                    setBackend(v); // optimistic
                    saveWebSearch({ web_search_backend: v });
                  }}
                >
                  <SelectTrigger className="w-48 shrink-0">
                    <SelectValue placeholder="검색 서비스 선택" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="ddgs">DuckDuckGo(ddgs · 무료, Key 불필요)</SelectItem>
                    <SelectItem value="brave-free">Brave(무료 플랜 · Key 필요)</SelectItem>
                    <SelectItem value="tavily">Tavily(Key 필요)</SelectItem>
                    <SelectItem value="deepseek">DeepSeek(공식)</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            )}

            {webSearch && backend === "deepseek" && (
              <div className="border-border/60 bg-muted/30 flex flex-col gap-2 rounded-md border p-3">
                <p className="text-sm font-medium">DeepSeek 공식 웹 검색</p>
                <p className="text-muted-foreground text-xs leading-relaxed">
                  이 검색 서비스는<b>현재 활성 LLM 설정</b>을 그대로 사용합니다. 따라서
                  <b>DeepSeek 공식 모델만 지원</b>하며, 해당 설정은<b>anthropic 프로토콜을 사용해야 합니다</b>
                  . DeepSeek의 OpenAI 프로토콜 엔드포인트는 서버 측 검색을 지원하지 않습니다. LLM 설정을 바꾸면 이 검색 서비스가 동작하지 않을 수 있습니다.
                </p>
                <p className="text-muted-foreground text-xs leading-relaxed">
                  다른 서비스와 달리 검색은 <b>DeepSeek 서버에서 실행됩니다</b>. 검색마다 모델을 한 번 더 호출하여 토큰 비용이 발생합니다. 검색 요청은<b>위의 외부 연결 프록시를 경유하지 않고</b>,<b>트래픽 기록에도 포함되지 않습니다</b>. 반환 결과에는<b>제목과 링크만 포함됩니다</b>
                  (요약 없음). 본문이 필요하면 WebFetch를 사용합니다.
                </p>
                <p className="text-muted-foreground text-xs leading-relaxed">
                  조건 충족 여부는 직접 확인해야 합니다. 시스템은 이를 차단하지 않습니다. 아래 「검색 테스트」로 실제 동작을 확인할 수 있습니다.
                </p>
              </div>
            )}

            {webSearch && backend === "brave-free" && (
              <div className="flex flex-col gap-2">
                <Label htmlFor="brave-key" className="text-sm font-normal text-muted-foreground">
                  Brave Search API Key
                  {braveKeySet && <span className="ml-2 text-xs text-emerald-500">설정됨</span>}
                </Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="brave-key"
                    type="password"
                    autoComplete="off"
                    placeholder={braveKeySet ? "설정됨(비워두면 유지)" : "Brave API Key 입력"}
                    value={braveKeyInput}
                    disabled={!loaded || savingKey}
                    onChange={(e) => setBraveKeyInput(e.target.value)}
                  />
                  <Button
                    type="button"
                    onClick={saveBraveKey}
                    disabled={!loaded || savingKey || braveKeyInput.trim() === ""}
                  >
                    저장
                  </Button>
                </div>
                {braveNeedsKey && (
                  <p className="text-xs text-amber-500">
                    Brave가 선택되었지만 Key가 없습니다. Key를 저장해야 검색 도구를 사용할 수 있습니다.
                  </p>
                )}
                <p className="text-muted-foreground text-xs">
                  Brave Search API 페이지(https://brave.com/search/api/)에서 Key와 플랜별 사용량을 확인하세요.
                </p>
              </div>
            )}

            {webSearch && backend === "tavily" && (
              <div className="flex flex-col gap-2">
                <Label htmlFor="tavily-key" className="text-sm font-normal text-muted-foreground">
                  Tavily Search API Key
                  {tavilyKeySet && <span className="ml-2 text-xs text-emerald-500">설정됨</span>}
                </Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="tavily-key"
                    type="password"
                    autoComplete="off"
                    placeholder={tavilyKeySet ? "설정됨(비워두면 유지)" : "Tavily API Key 입력(tvly-…)"}
                    value={tavilyKeyInput}
                    disabled={!loaded || savingTavilyKey}
                    onChange={(e) => setTavilyKeyInput(e.target.value)}
                  />
                  <Button
                    type="button"
                    onClick={saveTavilyKey}
                    disabled={!loaded || savingTavilyKey || tavilyKeyInput.trim() === ""}
                  >
                    저장
                  </Button>
                </div>
                {webSearch && backend === "tavily" && !tavilyKeySet && (
                  <p className="text-xs text-amber-500">
                    Tavily가 선택되었지만 Key가 없습니다. Key를 저장해야 검색 도구를 사용할 수 있습니다.
                  </p>
                )}
                <p className="text-muted-foreground text-xs">https://tavily.com 에서 가입하고 API Key를 발급받으세요.</p>
              </div>
            )}

            {webSearch && (
              <div className="flex flex-col gap-2">
                <Label htmlFor="ws-proxy" className="text-sm font-normal text-muted-foreground">
                  외부 연결 프록시(선택)
                </Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="ws-proxy"
                    autoComplete="off"
                    placeholder="http://host:port 또는 socks5://host:port(비워두면 직접 연결)"
                    value={proxyInput}
                    disabled={!loaded || savingProxy}
                    onChange={(e) => setProxyInput(e.target.value)}
                  />
                  <Button type="button" onClick={saveProxy} disabled={!loaded || savingProxy}>
                    저장
                  </Button>
                </div>
                <p className="text-muted-foreground text-xs">
                  검색 엔드포인트에만 사용하는 독립 프록시입니다(VPN/SOCKS 등). 트래픽 기록용 MITM 프록시와 별개이며 직접 연결이 어려울 때 사용합니다.
                </p>
              </div>
            )}

            {webSearch && (
              <div className="flex items-center justify-between gap-4 border-t pt-4">
                <p className="text-muted-foreground text-xs">
                  현재 검색 서비스·프록시·Key로 「test」를 검색하여 동작을 확인합니다.
                </p>
                <Button
                  type="button"
                  variant="outline"
                  onClick={runTest}
                  disabled={!loaded || testing}
                  className="shrink-0"
                >
                  {testing ? "테스트 중…" : "검색 테스트"}
                </Button>
              </div>
            )}
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RadioTowerIcon className="size-4" />
              사용자 정의 스크립트 · Python 인터프리터
            </CardTitle>
            <CardDescription>
              사용자 정의 <b>script</b> 유형 도구가 Python을 실행할 때 사용합니다. 시작 시 python3를 우선 탐색합니다. 가상 환경이나 특정 버전의 절대 경로를 지정할 수 있으며, 비워두면 실행 시 자동 탐색합니다.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              <Input
                className="font-mono text-sm"
                placeholder="/usr/bin/python3(비워두면 자동 탐색)"
                value={pyInterp}
                disabled={!loaded || saving}
                onChange={(e) => setPyInterp(e.target.value)}
              />
              <Button variant="outline" onClick={detectPython} disabled={!loaded || saving}>
                다시 탐색
              </Button>
              <Button onClick={savePython} disabled={!loaded || saving}>
                저장
              </Button>
            </div>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <CpuIcon className="size-4" />
              동시 실행 · 작업 에이전트 수
            </CardTitle>
            <CardDescription>
              작업마다 동시에 실행할 에이전트 수입니다(기본 3). 숫자가 클수록 동시 테스트와 사용 비용이 늘어납니다. 변경 사항은
              <b>이후 시작하는 작업에 적용되며</b>현재 실행 중인 작업에는 영향을 주지 않습니다.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              <Input
                type="number"
                min={1}
                className="w-32 font-mono text-sm"
                placeholder="3"
                value={workers}
                disabled={!loaded || savingWorkers}
                onChange={(e) => setWorkers(e.target.value)}
              />
              <Button onClick={saveWorkers} disabled={!loaded || savingWorkers}>
                저장
              </Button>
            </div>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <KeyboardIcon className="size-4" />
              대화 입력창 전송 키
            </CardTitle>
            <CardDescription>
              대화 페이지와 작업 상세의 주 에이전트 입력창이 공유하는 설정입니다. 저장 없이 즉시 적용됩니다.
              <br />
              이 설정은<b>현재 브라우저에만 저장됩니다</b>. 계정과 동기화되지 않으므로 브라우저를 바꾸거나 사이트 데이터를 지우면 다시 설정해야 합니다.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Label htmlFor="chat-send-mode" className="text-sm font-normal text-muted-foreground">
              전송 방식
            </Label>
            <Select value={sendMode} onValueChange={(v) => setChatSendMode(v as ChatSendMode)}>
              <SelectTrigger id="chat-send-mode" className="w-72">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {CHAT_SEND_MODE_OPTIONS.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
