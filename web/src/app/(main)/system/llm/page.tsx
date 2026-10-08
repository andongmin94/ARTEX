"use client";

import * as React from "react";

import {
  Loader2Icon,
  PlugZapIcon,
  PlusIcon,
  RefreshCwIcon,
  RotateCcwIcon,
  SaveIcon,
  StarIcon,
  Trash2Icon,
  ZapIcon,
} from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api } from "@/lib/api";
import type { LLMPoolMember, LLMPoolStatus, LLMProfile, LLMRetryOverride } from "@/lib/types";
import { cn } from "@/lib/utils";

import { ProfileRetryFields, RetryPolicyPanel, ZERO_OVERRIDE } from "./_components/retry";

// 思考开끔(thinking.type)与思考强度(reasoning_effort)是两个【互相独立】的字段，
// 各自单独设置——有些接口没有 thinking 字段、只靠强度参数就能활성思考，故需解耦。
// 存库空字符串 = 该字段【不发送】；Radix Select 不接受空 value，故 UI 用 "none"
// 哨兵表示不发送，存取时与 "" 互转（NONE / fromStore / toStore）。
const NONE = "none";
const fromStore = (v?: string) => (v ? v : NONE);
const toStore = (v: string) => (v === NONE ? "" : v);
const THINKING_TYPES: { value: string; label: string }[] = [
  { value: NONE, label: "전송 안 함(기본)" },
  { value: "disabled", label: "끄기" },
  { value: "enabled", label: "켜기" },
];
// 输出上限用哪个请求字段名（仅 openai 형식有意义）。NONE ↔ "" 走同一套哨兵转换。
const MAX_TOKENS_FIELDS: { value: string; label: string }[] = [
  { value: NONE, label: "max_tokens(기본)" },
  { value: "max_completion_tokens", label: "max_completion_tokens" },
];
// 另外两种형식各自定死了字段名，选项对它们无意义，说明文案里直接讲清楚。
const MAX_TOKENS_FIELD_HINTS: Record<string, string> = {
  openai:
    "출력 상한을 어떤 키로 보낼지 지정합니다. 기본은 max_tokens이며 대부분의 호환 게이트웨이가 이를 사용합니다. 반대로 OpenAI 공식 추론 모델(o 시리즈 / GPT-5)은 max_completion_tokens만 인식하고 max_tokens를 보내면 unsupported_parameter 오류가 발생할 수 있습니다.",
  anthropic: "openai 형식에서만 선택할 수 있습니다. Anthropic은 필드명이 max_tokens로 고정됩니다.",
  "openai-responses": "openai 형식에서만 선택할 수 있습니다. Responses API는 필드명이 max_output_tokens로 고정됩니다.",
};
const EFFORT_LEVELS: { value: string; label: string }[] = [
  { value: NONE, label: "전송 안 함(기본)" },
  { value: "low", label: "low" },
  { value: "medium", label: "medium" },
  { value: "high", label: "high" },
  { value: "xhigh", label: "xhigh" },
  { value: "max", label: "max" },
];

function cooldownText(secs: number) {
  if (secs <= 0) return "";
  if (secs < 60) return `${secs}s`;
  return `${Math.ceil(secs / 60)}min`;
}

// 一个配置在卡片上显示的「是否정상」。没填 Key 的配置根本发不出请求，比熔断更该先说；
// 其余状态来自轮询的熔断记录（轮询끔着时不会产生新记录，此时「정상」= 没有已知故障）。
type Health = { label: string; cls: string; hint?: string };
function healthOf(p: LLMProfile, m?: LLMPoolMember): Health {
  if (!p.api_key_hint) {
    return {
      label: "Key 미설정",
      cls: "border-muted-foreground/40 text-muted-foreground",
      hint: "API Key가 없어 호출할 수 없습니다",
    };
  }
  if (m?.state === "tripped") {
    return {
      label: m.cooldown_secs > 0 ? `회로 차단됨 · ${cooldownText(m.cooldown_secs)}` : "회로 차단됨",
      cls: "border-destructive/50 text-destructive",
      hint: m.last_error,
    };
  }
  if (m?.state === "degraded") {
    return {
      label: `오류 · 실패 ${m.fails}회`,
      cls: "border-amber-500/50 text-amber-600 dark:text-amber-400",
      hint: m.last_error,
    };
  }
  return { label: "정상", cls: "border-emerald-500/50 text-emerald-600 dark:text-emerald-400" };
}

// ─────────────────────────────────────────────────────────────────────────────
// 폴백 설정抽屉
// ─────────────────────────────────────────────────────────────────────────────

function PoolSheet({
  open,
  onOpenChange,
  pool,
  onReload,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  pool: LLMPoolStatus | null;
  onReload: () => Promise<void>;
}) {
  const [busy, setBusy] = React.useState(false);

  // 冷却倒计时是后端算出的剩余秒数——抽屉开着且有配置不정상时才定时拉，让它走起来。
  React.useEffect(() => {
    if (!open || !pool?.enabled || !pool.chain.some((m) => m.state !== "ok")) return;
    const t = setInterval(() => void onReload(), 10_000);
    return () => clearInterval(t);
  }, [open, pool, onReload]);

  async function toggle(patch: { llm_pool_enabled?: boolean; llm_pool_bind_fallback?: boolean }) {
    if (busy) return;
    setBusy(true);
    try {
      await api.setSettings(patch);
      await onReload();
      if (patch.llm_pool_enabled !== undefined) {
        toast.success(patch.llm_pool_enabled ? "LLM 폴백 체인을 활성화했습니다" : "LLM 폴백 체인을 비활성화했습니다");
      } else {
        toast.success("폴백 설정을 업데이트했습니다");
      }
    } catch (e) {
      toast.error(`설정 실패: ${(e as Error).message}`);
    } finally {
      setBusy(false);
    }
  }

  async function recover(id?: string) {
    try {
      await api.resetLLMPool(id);
      await onReload();
      toast.success(id ? "해당 설정을 복구했습니다" : "모든 설정을 복구했습니다");
    } catch (e) {
      toast.error(`복구 실패: ${(e as Error).message}`);
    }
  }

  const enabled = pool?.enabled ?? false;
  const chain = pool?.chain ?? [];
  // 参与轮询的成员（排除被标记「폴백 제외」的），顺序即后端实际的尝试顺序。
  const inChain = chain.filter((m) => m.active || !m.excluded);
  const tripped = chain.filter((m) => m.state === "tripped");

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="flex flex-col gap-0 p-0 data-[side=right]:sm:max-w-lg">
        <SheetHeader className="px-4">
          <SheetTitle className="flex items-center gap-2">
            <ZapIcon className="size-4" /> LLM 폴백 · 장애 조치
          </SheetTitle>
          <SheetDescription>
            활성화하면<b>모델을 지정하지 않은</b> Agent가 현재 설정을 사용할 수 없을 때(잔액 부족 / Key 무효 / 제한 /
            서비스 오류) 자동으로 다음 설정으로 전환합니다.
          </SheetDescription>
        </SheetHeader>

        <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 pb-6">
          <div className="flex items-center justify-between gap-4 rounded-lg border p-3">
            <div className="grid gap-0.5">
              <Label className="text-sm">폴백 체인 사용</Label>
              <p className="text-muted-foreground text-xs">기본값은 꺼짐입니다. 끄면 활성 설정만 사용하며 실패 시 그대로 실패 처리합니다.</p>
            </div>
            <Switch
              checked={enabled}
              disabled={busy}
              onCheckedChange={(v) => void toggle({ llm_pool_enabled: v })}
              aria-label="LLM 폴백 스위치"
            />
          </div>

          {enabled && (
            <>
              <div className="flex items-center justify-between gap-4 rounded-lg border p-3">
                <div className="grid gap-0.5">
                  <Label className="text-sm">지정 모델 실패 시에도 폴백</Label>
                  <p className="text-muted-foreground text-xs">
                    기본값은 꺼짐입니다. 에이전트나 작업에서 지정한 설정만 사용하며 실패하더라도 다른 모델로 바꾸지 않습니다. 활성화하면 지정 설정이 실패할 때 아래 폴백 체인을 사용합니다.
                  </p>
                </div>
                <Switch
                  checked={pool?.bind_fallback ?? false}
                  disabled={busy}
                  onCheckedChange={(v) => void toggle({ llm_pool_bind_fallback: v })}
                  aria-label="지정 설정 실패 폴백 스위치"
                />
              </div>

              <Separator />

              <div className="grid gap-2">
                <div className="flex items-center justify-between">
                  <Label className="text-sm">폴백 순서</Label>
                  {tripped.length > 0 && (
                    <Button size="sm" variant="ghost" onClick={() => void recover()}>
                      <RotateCcwIcon /> 전체 복구
                    </Button>
                  )}
                </div>
                {inChain.length < 2 && (
                  <p className="text-muted-foreground text-xs">
                    현재 사용 가능한 설정이 {inChain.length}개뿐이라 폴백 체인이 동작하지 않습니다. API Key가 있고 폴백에 참여하는 설정이 최소 2개 필요합니다.
                  </p>
                )}
                {chain.map((m) => {
                  const excluded = m.excluded && !m.active;
                  const order = excluded ? null : inChain.findIndex((x) => x.profile_id === m.profile_id) + 1;
                  return (
                    <div
                      key={m.profile_id}
                      className={cn(
                        "grid gap-1 rounded-lg border p-2.5 text-sm",
                        excluded && "opacity-55",
                        m.state === "tripped" && "border-destructive/40",
                      )}
                    >
                      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                        <span className="w-5 shrink-0 text-center font-sans text-muted-foreground text-xs">
                          {order ?? "—"}
                        </span>
                        <span className="font-medium">{m.name}</span>
                        {m.active && (
                          <Badge variant="outline" className="border-amber-400/50 text-amber-500">
                            활성
                          </Badge>
                        )}
                        {excluded && <Badge variant="outline">폴백 제외</Badge>}
                        <div className="ml-auto flex items-center gap-2">
                          {m.state === "tripped" && m.cooldown_secs > 0 && (
                            <span className="text-muted-foreground text-xs">쿨다운 {cooldownText(m.cooldown_secs)}</span>
                          )}
                          {m.state === "degraded" && (
                            <span className="text-muted-foreground text-xs">연속 실패 {m.fails}회</span>
                          )}
                          {m.state !== "ok" && (
                            <Button
                              size="icon"
                              variant="ghost"
                              className="size-7"
                              aria-label="즉시 복구"
                              title="즉시 복구: 회로 차단을 해제하고 다음 호출에서 해당 설정을 다시 시도합니다"
                              onClick={() => void recover(m.profile_id)}
                            >
                              <RotateCcwIcon className="size-3.5" />
                            </Button>
                          )}
                        </div>
                      </div>
                      <div className="flex flex-wrap items-center gap-x-3 pl-7 text-muted-foreground text-xs">
                        <code className="truncate font-sans">{m.model}</code>
                        {!m.active && <span>우선순위 {m.priority}</span>}
                      </div>
                      {m.last_error && (
                        <p className="truncate pl-7 font-sans text-muted-foreground text-xs" title={m.last_error}>
                          {m.last_error}
                        </p>
                      )}
                    </div>
                  );
                })}
                {chain.length === 0 && (
                  <div className="rounded-lg border border-dashed p-4 text-center text-muted-foreground text-sm">
                    설정 없음
                  </div>
                )}
              </div>

              <div className="rounded-lg border border-dashed p-3 text-muted-foreground text-xs leading-relaxed">
                활성 설정은 항상 1순위이며 나머지는 우선순위가 높은 순서로 사용합니다. 실패한 설정은 60초 → 5분 → 30분의 쿨다운 동안 건너뛰며 복구 후 다시 사용합니다. 현재 요청을 수용하지 못하는 컨텍스트 윈도우의 설정도 건너뜁니다. 특정 모델을 지정한 에이전트와 작업은 기본적으로 폴백에 참여하지 않습니다.
              </div>
            </>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// 모델配置抽屉（생성 / 编辑共用同一套表单）
// ─────────────────────────────────────────────────────────────────────────────

function ProfileSheet({
  profile,
  open,
  onOpenChange,
  onSaved,
}: {
  profile: LLMProfile | null; // null = 생성
  open: boolean;
  onOpenChange: (o: boolean) => void;
  onSaved: (id: string) => void;
}) {
  const isNew = !profile;
  const [name, setName] = React.useState("");
  const [format, setFormat] = React.useState<"anthropic" | "openai" | "openai-responses">("anthropic");
  const [model, setModel] = React.useState("");
  const [baseUrl, setBaseUrl] = React.useState("");
  const [proxy, setProxy] = React.useState("");
  const [apiKey, setApiKey] = React.useState("");
  const [keyHint, setKeyHint] = React.useState("");
  const [rps, setRps] = React.useState("0");
  const [rpm, setRpm] = React.useState("0");
  const [cw, setCw] = React.useState("0"); // 上下文窗口(K tokens);0=默认200K
  const [thinkingType, setThinkingType] = React.useState(NONE);
  const [effort, setEffort] = React.useState(NONE);
  const [priority, setPriority] = React.useState("0"); // 轮询顺位;越大越先
  const [poolExclude, setPoolExclude] = React.useState(false);
  const [streaming, setStreaming] = React.useState(true); // true=流式(默认);false=非流式
  const [maxTokens, setMaxTokens] = React.useState("0"); // 单次回复输出上限;0=不发送
  const [maxTokensField, setMaxTokensField] = React.useState(NONE); // 上限用哪个字段名;NONE=max_tokens
  const [sessionHeaderKey, setSessionHeaderKey] = React.useState(""); // 自定义会话头名;空=不发送
  const [retry, setRetry] = React.useState<LLMRetryOverride>(ZERO_OVERRIDE); // 本配置的重试覆盖;全 0=跟随全局
  const [testing, setTesting] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [models, setModels] = React.useState<string[]>([]);
  const [loadingModels, setLoadingModels] = React.useState(false);
  const [modelsOpen, setModelsOpen] = React.useState(false);

  // 每次打开时从传入的 profile 灌一遍表单（생성则重置为默认值）。抽屉끔掉再打开
  // 就是一次干净的开始，不会留下上一个配置的残影。
  React.useEffect(() => {
    if (!open) return;
    setName(profile?.name ?? "");
    setFormat(profile?.format === "openai" || profile?.format === "openai-responses" ? profile.format : "anthropic");
    setModel(profile?.model ?? "");
    setBaseUrl(profile?.base_url ?? "");
    setProxy(profile?.proxy ?? "");
    setRps(String(profile?.rate_per_second ?? 0));
    setRpm(String(profile?.rate_per_minute ?? 0));
    setCw(String(profile?.context_window_k ?? 0));
    setThinkingType(fromStore(profile?.thinking_type));
    setEffort(fromStore(profile?.reasoning_effort));
    setPriority(String(profile?.priority ?? 0));
    setPoolExclude(profile?.pool_exclude ?? false);
    setStreaming(profile?.streaming ?? true);
    setMaxTokens(String(profile?.max_tokens ?? 0));
    setMaxTokensField(fromStore(profile?.max_tokens_field));
    setSessionHeaderKey(profile?.session_header_key ?? "");
    setRetry(profile?.retry ?? ZERO_OVERRIDE);
    setApiKey("");
    setKeyHint(profile?.api_key_hint ?? "");
    setModels([]);
    setModelsOpen(false);
  }, [open, profile]);

  const profileId = profile ? Number(profile.id) : undefined;

  async function loadModels() {
    if (loadingModels) return;
    setLoadingModels(true);
    setModels([]);
    try {
      const r = await api.fetchLLMModels(format, baseUrl, apiKey, proxy, profileId);
      if (r.ok && r.models && r.models.length > 0) {
        setModels(r.models);
        setModelsOpen(true);
        toast.success(`모델 ${r.models.length}개를 불러왔습니다`);
      } else {
        toast.error(`모델 불러오기 실패: ${r.error ?? "모델을 가져오지 못함"}`);
      }
    } catch (e) {
      toast.error(`모델 불러오기 오류: ${(e as Error).message}`);
    } finally {
      setLoadingModels(false);
    }
  }

  async function testConnection() {
    if (testing) return;
    setTesting(true);
    try {
      // 用配置实际会跑的思考参数来测，这样不支持该字段的모델在这里就失败，
      // 而不是等到跑任务时才炸。传 profile id：Key 输入框留空时用已存的 Key。
      const r = await api.testLLM(
        format,
        model,
        baseUrl,
        apiKey,
        proxy,
        toStore(thinkingType),
        toStore(effort),
        profileId,
        streaming,
        sessionHeaderKey.trim(),
      );
      // 回复内容一并展示：看得见모델确实说了话，才算和会话里跑通是一回事。
      if (r.ok)
        toast.success(`연결 성공 · ${r.latency_ms ?? "?"}ms · ${r.model ?? model}`, {
          description: r.reply ? `응답: ${r.reply}` : undefined,
        });
      else toast.error(`연결 실패: ${r.error ?? "알 수 없음"}`);
    } catch (e) {
      toast.error(`테스트 오류: ${(e as Error).message}`);
    } finally {
      setTesting(false);
    }
  }

  async function save() {
    if (!name.trim() || !model.trim()) {
      toast.error("이름과 모델을 입력하세요");
      return;
    }
    if (saving) return;
    setSaving(true);
    try {
      const { id } = await api.saveLLMProfile({
        ...(profile ? { id: Number(profile.id) } : {}),
        name: name.trim(),
        format,
        model: model.trim(),
        base_url: baseUrl.trim(),
        proxy: proxy.trim(),
        api_key: apiKey,
        rate_per_second: Number(rps) || 0,
        rate_per_minute: Number(rpm) || 0,
        context_window_k: Number(cw) || 0,
        thinking_type: toStore(thinkingType),
        reasoning_effort: toStore(effort),
        priority: Number(priority) || 0,
        pool_exclude: poolExclude,
        streaming,
        max_tokens: Math.max(0, Number(maxTokens) || 0),
        // 字段名开끔只对 openai(Chat Completions) 有意义，其它형식一律回落到默认；
        // 后端也会再做一次同样的归一化，这里只是别让 UI 送出自相矛盾的值。
        max_tokens_field: format === "openai" ? toStore(maxTokensField) : "",
        session_header_key: sessionHeaderKey.trim(),
        retry,
      });
      if (isNew) toast.success(`생성됨:${name.trim()}(카드에서 「활성으로 설정」을 눌러 사용하세요)`);
      else toast.success(profile?.is_default ? "저장되었습니다. 활성 설정은 재시작 없이 즉시 적용됩니다" : "저장되었습니다");
      onSaved(String(id));
      onOpenChange(false);
    } catch (e) {
      toast.error(`저장 실패: ${(e as Error).message}`);
    } finally {
      setSaving(false);
    }
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="flex flex-col gap-0 p-0 data-[side=right]:min-w-[420px] data-[side=right]:sm:max-w-xl"
      >
        <SheetHeader className="px-4">
          <SheetTitle className="flex items-center gap-2">
            {isNew ? "모델 설정 생성" : `편집: ${profile?.name}`}
            {profile?.is_default && (
              <Badge variant="outline" className="border-amber-400/50 text-amber-500">
                활성 상태
              </Badge>
            )}
          </SheetTitle>
          <SheetDescription>
            {isNew
              ? "새 설정은 자동으로 활성화되지 않습니다. 카드에서 「활성으로 설정」을 눌러 사용하세요."
              : "변경 후 저장을 누르세요. 활성 설정은 저장 즉시 모든 에이전트에 적용됩니다."}
          </SheetDescription>
        </SheetHeader>

        <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 pb-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="grid gap-2">
              <Label htmlFor="p-name">이름</Label>
              <Input
                id="p-name"
                placeholder="예: OpenAI 운영"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </div>
            <div className="grid gap-2">
              <Label>형식</Label>
              <Select value={format} onValueChange={(v) => setFormat(v as "anthropic" | "openai" | "openai-responses")}>
                <SelectTrigger>
                  <SelectValue placeholder="형식 선택" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="anthropic">Anthropic</SelectItem>
                  <SelectItem value="openai">OpenAI (Chat Completions)</SelectItem>
                  <SelectItem value="openai-responses">OpenAI (Responses API)</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="grid gap-2">
            <Label htmlFor="p-model">모델</Label>
            <div className="flex gap-2">
              <Input
                id="p-model"
                className="font-sans"
                placeholder="claude-opus-4-8"
                value={model}
                onChange={(e) => setModel(e.target.value)}
              />
              {/* modal: 这个 Popover 的内容被 portal 到 <body>，在 Sheet 的滚动锁之外，
                  不加 modal 时列表能渲染却滚不动。modal 让它自己持有最上层滚动锁。 */}
              <Popover open={modelsOpen} onOpenChange={setModelsOpen} modal>
                <PopoverTrigger asChild>
                  <Button
                    type="button"
                    variant="outline"
                    size="icon"
                    className="shrink-0"
                    disabled={loadingModels}
                    onClick={loadModels}
                    title="API에서 사용 가능한 모델 불러오기"
                  >
                    {loadingModels ? <Loader2Icon className="animate-spin" /> : <RefreshCwIcon />}
                  </Button>
                </PopoverTrigger>
                {models.length > 0 && (
                  <PopoverContent className="max-h-72 w-72 gap-0 overflow-y-auto overscroll-contain p-1" align="end">
                    {models.map((m) => (
                      <button
                        key={m}
                        type="button"
                        className="w-full shrink-0 rounded-md px-2 py-1.5 text-left font-sans text-xs hover:bg-accent hover:text-accent-foreground"
                        onClick={() => {
                          setModel(m);
                          setModelsOpen(false);
                        }}
                      >
                        {m}
                      </button>
                    ))}
                  </PopoverContent>
                )}
              </Popover>
            </div>
          </div>

          <div className="grid gap-2">
            <Label htmlFor="p-base-url">Base URL(선택)</Label>
            <Input
              id="p-base-url"
              className="font-sans"
              placeholder="https://api.openai.com/v1"
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
            />
          </div>

          <div className="grid gap-2">
            <Label htmlFor="p-proxy">프록시(선택)</Label>
            <Input
              id="p-proxy"
              className="font-sans"
              placeholder="socks5://user:pass@127.0.0.1:1080 · http://127.0.0.1:8080"
              value={proxy}
              onChange={(e) => setProxy(e.target.value)}
            />
            <p className="text-muted-foreground text-xs">
              LLM 외부 요청만 이 프록시를 사용합니다. http/https/socks5를 지원하며 계정과 비밀번호를 포함할 수 있습니다(예:
              socks5://user:pass@host:port, 비밀번호에 특수문자가 있으면 URL 인코딩 필요). 비워두면 프록시 없이 직접 연결합니다.
            </p>
          </div>

          <div className="grid gap-2">
            <Label htmlFor="p-session-header">사용자 정의 세션 헤더(선택)</Label>
            <Input
              id="p-session-header"
              className="font-sans"
              placeholder="예: x-session-id(비워두면 전송 안 함)"
              value={sessionHeaderKey}
              onChange={(e) => setSessionHeaderKey(e.target.value)}
            />
            <p className="text-muted-foreground text-xs">
              헤더 이름을 입력하면 모든 요청에 이 HTTP 헤더가 추가되며 값은 자동으로 <b>현재 세션의 session id</b>(chat 세션 예:
              conv-12, worker 예:exp3-worker-i87). session-id 헤더 기반 프롬프트 캐시 /
              스티키 라우팅 게이트웨이에 사용합니다. 같은 세션은 여러 라운드 동안 동일하고 서로 다른 세션은 구분됩니다. 비워두면 전송하지 않습니다.
            </p>
          </div>

          <div className="grid gap-2">
            <Label htmlFor="p-api-key">API Key</Label>
            <Input
              id="p-api-key"
              type="password"
              placeholder={keyHint ? `설정됨(${keyHint}), 비워두면 기존 값 유지` : "sk-…"}
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
            />
          </div>

          <div className="grid gap-4 sm:grid-cols-3">
            <div className="grid gap-2">
              <Label htmlFor="p-rps">초당 제한</Label>
              <Input id="p-rps" type="number" min={0} value={rps} onChange={(e) => setRps(e.target.value)} />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="p-rpm">분당 제한</Label>
              <Input id="p-rpm" type="number" min={0} value={rpm} onChange={(e) => setRpm(e.target.value)} />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="p-cw">컨텍스트 윈도우(K)</Label>
              <Input
                id="p-cw"
                type="number"
                min={0}
                max={1000}
                value={cw}
                onChange={(e) => setCw(e.target.value)}
                placeholder="200"
              />
            </div>
          </div>
          <p className="-mt-2 text-muted-foreground text-xs">
            제한 0 = 무제한이며 모든 Agent가 공유합니다. 컨텍스트 윈도우 단위는 K(천 token), 0 = 기본 200K, 최대 1000(즉
            1M)입니다. 너무 크게 설정하면 압축이 동작하지 않을 수 있습니다.
          </p>

          <div className="grid gap-3 rounded-lg border p-3">
            <div className="flex items-center justify-between gap-4">
              <div className="grid gap-0.5">
                <Label htmlFor="p-priority" className="text-sm">
                  폴백 우선순위
                </Label>
                <p className="text-muted-foreground text-xs">
                  숫자가 클수록 먼저 선택합니다. 활성 설정은 이 값과 관계없이 항상 1순위입니다. 우선순위가 같은 설정은 번갈아 사용하여 할당량을 분산합니다.
                </p>
              </div>
              <Input
                id="p-priority"
                type="number"
                className="w-24 shrink-0"
                value={priority}
                onChange={(e) => setPriority(e.target.value)}
              />
            </div>
            <div className="flex items-center justify-between gap-4 border-t pt-3">
              <div className="grid gap-0.5">
                <Label className="text-sm">폴백 제외</Label>
                <p className="text-muted-foreground text-xs">
                  활성화하면 장애 조치 대상으로 사용하지 않습니다. 에이전트나 작업에서 명시적으로 지정할 수는 있습니다. 특정 에이전트 전용으로 사용할 고비용 설정에 적합합니다.
                </p>
              </div>
              <Switch checked={poolExclude} onCheckedChange={setPoolExclude} aria-label="폴백 제외" />
            </div>
            <div className="flex items-center justify-between gap-4 border-t pt-3">
              <div className="grid gap-0.5">
                <Label className="text-sm">스트리밍 출력 · streaming</Label>
                <p className="text-muted-foreground text-xs">
                  활성화(기본)하면 SSE 스트리밍으로 실시간 진행과 토큰 수를 표시합니다. 끄면 stream:false로 전체 응답을 한 번에 받습니다. 일부 게이트웨이의 빈 프레임이나 추론 필드 손실을 피할 수 있지만 실행 중 실시간 진행은 볼 수 없습니다.
                </p>
              </div>
              <Switch checked={streaming} onCheckedChange={setStreaming} aria-label="스트리밍 출력" />
            </div>
          </div>

          <div className="grid gap-3 rounded-lg border p-3">
            <div className="flex items-center justify-between gap-4">
              <div className="grid gap-0.5">
                <Label htmlFor="p-max-tokens" className="text-sm">
                  출력 상한 · max tokens
                </Label>
                <p className="text-muted-foreground text-xs">
                  한 번의 응답에서 생성할 최대 토큰 수입니다. 0(기본)이면 필드를 보내지 않고 서버 기본값을 사용합니다. 「컨텍스트 윈도우」는 모델 전체 용량과 로컬 압축 임계값 계산용으로, 이 값과 다릅니다. 출력 상한이 너무 작으면 추론 단계에서 잘려 답변이 생성되지 않을 수 있습니다.
                </p>
              </div>
              <Input
                id="p-max-tokens"
                type="number"
                min={0}
                className="w-28 shrink-0"
                value={maxTokens}
                onChange={(e) => setMaxTokens(e.target.value)}
                placeholder="0"
              />
            </div>
            <div className="flex items-center justify-between gap-4 border-t pt-3">
              <div className="grid gap-0.5">
                <Label className="text-sm">상한 필드명</Label>
                <p className="text-muted-foreground text-xs">{MAX_TOKENS_FIELD_HINTS[format]}</p>
              </div>
              <Select
                value={format === "openai" ? maxTokensField : NONE}
                onValueChange={setMaxTokensField}
                disabled={format !== "openai"}
              >
                <SelectTrigger className="w-56 shrink-0">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {MAX_TOKENS_FIELDS.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {o.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="grid gap-3 rounded-lg border p-3">
            <div className="flex items-center justify-between gap-4">
              <div className="grid gap-0.5">
                <Label className="text-sm">reasoning 스위치 · thinking.type</Label>
                <p className="text-muted-foreground text-xs">
                  thinking 필드 전송 여부를 제어합니다. 전송 안 함은 해당 필드를 생략하고, 끄기는 disabled, 켜기는 enabled를 전송합니다. 아래 강도 설정과 독립적입니다.
                </p>
              </div>
              <Select value={thinkingType} onValueChange={setThinkingType}>
                <SelectTrigger className="w-32 shrink-0">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {THINKING_TYPES.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {o.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex items-center justify-between gap-4 border-t pt-3">
              <div className="grid gap-0.5">
                <Label className="text-sm">reasoning 강도 · reasoning_effort</Label>
                <p className="text-muted-foreground text-xs">
                  독립적인 추론 강도 설정입니다(OpenAI reasoning_effort / Anthropic output_config.effort). 일부 API는 thinking 필드 없이 강도만으로 추론을 활성화하므로 스위치를 보내지 않고 별도로 설정할 수 있습니다.
                </p>
              </div>
              <Select value={effort} onValueChange={setEffort}>
                <SelectTrigger className="w-32 shrink-0">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {EFFORT_LEVELS.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {o.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          <ProfileRetryFields value={retry} onChange={setRetry} />
        </div>

        <div className="flex gap-2 border-t px-4 py-3">
          <Button variant="outline" onClick={testConnection} disabled={testing}>
            {testing ? <Loader2Icon className="animate-spin" /> : <PlugZapIcon />}
            {testing ? "테스트 중…" : "연결 테스트"}
          </Button>
          <Button onClick={save} disabled={saving} className="flex-1">
            {saving && <Loader2Icon className="animate-spin" />}
            {!saving && (isNew ? <PlusIcon /> : <SaveIcon />)}
            {isNew ? "생성" : "저장"}
          </Button>
        </div>
      </SheetContent>
    </Sheet>
  );
}

// ─────────────────────────────────────────────────────────────────────────────

export default function LLMPage() {
  const [profiles, setProfiles] = React.useState<LLMProfile[]>([]);
  const [pool, setPool] = React.useState<LLMPoolStatus | null>(null);
  const [poolOpen, setPoolOpen] = React.useState(false);
  // 抽屉的开끔和内容分开存：끄기时 editing 保持不变，否则끄기动画期间标题会从
  // 「编辑 X」闪成「생성」。editing = null 表示생성。
  const [editOpen, setEditOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<LLMProfile | null>(null);
  const openEditor = React.useCallback((p: LLMProfile | null) => {
    setEditing(p);
    setEditOpen(true);
  }, []);

  const loadPool = React.useCallback(async () => {
    try {
      setPool(await api.llmPool());
    } catch {
      /* ignore */
    }
  }, []);

  const load = React.useCallback(async () => {
    try {
      setProfiles(await api.llmProfiles());
    } catch {
      /* ignore */
    }
    await loadPool();
  }, [loadPool]);

  React.useEffect(() => {
    void load();
  }, [load]);

  // 卡片上的健康徽章按 profile id 取轮询状态。
  const health = React.useMemo(() => {
    const m = new Map<string, LLMPoolMember>();
    for (const c of pool?.chain ?? []) m.set(c.profile_id, c);
    return m;
  }, [pool]);

  async function activate(id: string, name: string) {
    try {
      await api.activateLLMProfile(id);
      toast.success(`활성화됨:${name}`);
      await load();
    } catch (e) {
      toast.error(`활성화 실패:${(e as Error).message}`);
    }
  }

  async function remove(p: LLMProfile) {
    if (p.is_default) {
      toast.error("현재 활성 설정은 삭제할 수 없습니다");
      return;
    }
    try {
      await api.deleteLLMProfile(p.id);
      toast.success(`삭제됨: ${p.name}`);
      await load();
    } catch (e) {
      toast.error(`삭제 실패: ${(e as Error).message}`);
    }
  }

  const poolOn = pool?.enabled ?? false;

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="font-semibold text-xl tracking-tight">LLM</h1>
          <p className="text-muted-foreground text-sm">
            모든 에이전트가 공유하는 형식·모델·속도 제한 설정입니다. 카드를 클릭하여 편집하세요. 별표는 현재 활성 설정입니다.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" onClick={() => setPoolOpen(true)}>
            <ZapIcon /> 폴백 설정
            {poolOn && (
              <Badge variant="outline" className="ml-1 border-emerald-500/50 text-emerald-600 dark:text-emerald-400">
                활성화됨
              </Badge>
            )}
          </Button>
          <Button size="sm" variant="outline" onClick={() => openEditor(null)}>
            <PlusIcon /> 생성
          </Button>
        </div>
      </div>

      <Tabs defaultValue="profiles" className="flex-1">
        <TabsList>
          <TabsTrigger value="profiles">모델 설정</TabsTrigger>
          <TabsTrigger value="retry">재시도 및 백오프</TabsTrigger>
        </TabsList>

        <TabsContent value="profiles" className="mt-4">
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
            {profiles.map((p) => {
              const h = healthOf(p, health.get(p.id));
              return (
                // biome-ignore lint/a11y/useSemanticElements: 卡片内含自己的操作按钮，用原生 <button> 会造成按钮嵌套（非法 HTML）
                <Card
                  key={p.id}
                  role="button"
                  tabIndex={0}
                  onClick={() => openEditor(p)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      openEditor(p);
                    }
                  }}
                  className={cn(
                    "cursor-pointer gap-0 py-4 outline-none transition-colors hover:border-foreground/30",
                    p.is_default && "border-amber-400/50 bg-amber-400/5",
                  )}
                >
                  <CardContent className="grid gap-2 px-4">
                    <div className="flex items-start gap-2">
                      <StarIcon
                        className={cn(
                          "mt-0.5 size-4 shrink-0",
                          p.is_default ? "fill-amber-400 text-amber-400" : "text-muted-foreground",
                        )}
                      />
                      <div className="min-w-0 flex-1">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="truncate font-medium text-sm">{p.name}</span>
                          <Badge variant="outline" className="uppercase">
                            {p.format}
                          </Badge>
                          <Badge variant="outline" className={cn("ml-auto", h.cls)} title={h.hint}>
                            {h.label}
                          </Badge>
                        </div>
                        <code className="mt-1 block truncate font-sans text-muted-foreground text-xs">{p.model}</code>
                      </div>
                    </div>

                    <div className="flex flex-wrap gap-x-3 gap-y-0.5 pl-6 text-muted-foreground text-xs">
                      {p.api_key_hint && <span>{p.api_key_hint}</span>}
                      <span>
                        {p.rate_per_second}/s · {p.rate_per_minute}/min
                      </span>
                      {p.proxy && <span className="truncate">프록시 {p.proxy}</span>}
                      {p.reasoning_effort && (
                        <span>reasoning {p.reasoning_effort === "off" ? "끔" : p.reasoning_effort}</span>
                      )}
                      {/* 轮询相끔的两个字段只在轮询开着时才有意义，끔着时不占版面 */}
                      {poolOn &&
                        !p.is_default &&
                        (p.pool_exclude ? <span>폴백 제외</span> : <span>우선순위 {p.priority ?? 0}</span>)}
                    </div>

                    <div className="mt-1 flex gap-2">
                      <Button
                        size="sm"
                        variant="outline"
                        className="flex-1"
                        disabled={p.is_default}
                        onClick={(e) => {
                          e.stopPropagation();
                          void activate(p.id, p.name);
                        }}
                      >
                        {p.is_default ? "활성화됨" : "활성으로 설정"}
                      </Button>
                      <Button
                        size="icon"
                        variant="outline"
                        aria-label="설정 삭제"
                        onClick={(e) => {
                          e.stopPropagation();
                          void remove(p);
                        }}
                      >
                        <Trash2Icon className="text-destructive" />
                      </Button>
                    </div>
                  </CardContent>
                </Card>
              );
            })}
            {profiles.length === 0 && (
              <div className="col-span-full rounded-lg border border-dashed p-10 text-center text-muted-foreground text-sm">
                모델 설정이 없습니다. 오른쪽 위 「생성」을 눌러 첫 설정을 만드세요.
              </div>
            )}
          </div>
        </TabsContent>

        <TabsContent value="retry" className="mt-4">
          <RetryPolicyPanel />
        </TabsContent>
      </Tabs>

      <ProfileSheet profile={editing} open={editOpen} onOpenChange={setEditOpen} onSaved={() => void load()} />
      <PoolSheet open={poolOpen} onOpenChange={setPoolOpen} pool={pool} onReload={loadPool} />
    </div>
  );
}
