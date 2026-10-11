"use client";

import * as React from "react";
import { DataLoadStatus } from "@/components/data-load-status";

import { AlertCircleIcon, CheckCircle2Icon, DownloadIcon, PlugZapIcon, RefreshCwIcon, SearchIcon } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api } from "@/lib/api";
import type { SSProject, SSTask } from "@/lib/types";

type SSStatus = {
  exists: boolean;
  configured: boolean;
  enabled: boolean;
  reachable: boolean;
  url?: string;
  tools: string[];
};

type Dimension = "project" | "task";

const ASSET_TYPES: { key: string; label: string }[] = [
  { key: "subdomain", label: "서브도메인" },
  { key: "service", label: "서비스" },
  { key: "app", label: "App" },
];

export default function AssetSyncPage() {
  return (
    <div className="space-y-6">
      <div className="space-y-1">
        <h1 className="font-semibold text-xl">자산 동기화</h1>
        <p className="text-muted-foreground text-sm">ScopeSentry에서 프로젝트·작업의 자산을 가져옵니다.</p>
      </div>
      <ScopeSentryPanel />
    </div>
  );
}

function ScopeSentryPanel() {
  const [status, setStatus] = React.useState<SSStatus | null>(null);
  const [loadingStatus, setLoadingStatus] = React.useState(true);
  const [statusError, setStatusError] = React.useState("");

  const loadStatus = React.useCallback(() => {
    setLoadingStatus(true);
    setStatusError("");
    api
      .ssStatus()
      .then(setStatus)
      .catch((e: Error) => { setStatus(null); setStatusError(e.message); })
      .finally(() => setLoadingStatus(false));
  }, []);

  React.useEffect(() => {
    loadStatus();
  }, [loadStatus]);

  const ready = !!status && status.exists && status.configured && status.enabled;

  return (
    <div className="space-y-6">
      <DataLoadStatus error={statusError} label="데이터 소스 상태 불러오기" onRetry={loadStatus} />
      <DataSourceCard status={status} loading={loadingStatus} onChanged={loadStatus} />
      {ready ? (
        <SyncWorkbench />
      ) : (
        !loadingStatus &&
        status && (
          <p className="text-muted-foreground text-sm">
            ScopeSentry를 연결하고 활성화하면 동기화 대상을 선택할 수 있습니다.
          </p>
        )
      )}
    </div>
  );
}

// ScopeSentry 연결 설정

function DataSourceCard({
  status,
  loading,
  onChanged,
}: {
  status: SSStatus | null;
  loading: boolean;
  onChanged: () => void;
}) {
  const [url, setUrl] = React.useState("");
  const [apiKey, setApiKey] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    if (status?.url) setUrl(status.url);
  }, [status?.url]);

  const create = async () => {
    setBusy(true);
    try {
      await api.ssDatasource({});
      toast.success("ScopeSentry 데이터 소스를 생성했습니다. 주소와 키를 입력하세요");
      onChanged();
    } catch (e) {
      toast.error(`생성 실패: ${(e as Error).message}`);
    } finally {
      setBusy(false);
    }
  };

  const save = async () => {
    if (!url.trim()) return toast.error("MCP 주소를 입력하세요");
    setBusy(true);
    try {
      const r = await api.ssDatasource({ url: url.trim(), api_key: apiKey.trim() });
      toast.success(
        r.enabled ? "데이터 소스를 저장하고 활성화했습니다" : "저장했습니다(아직 활성화 조건을 충족하지 않음)",
      );
      setApiKey("");
      onChanged();
    } catch (e) {
      toast.error(`저장 실패: ${(e as Error).message}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
        <CardTitle className="flex flex-wrap items-center gap-2 text-base">
          <PlugZapIcon className="size-4" aria-hidden="true" /> ScopeSentry
          <StatusBadge status={status} loading={loading} />
        </CardTitle>
        <Button variant="ghost" size="sm" onClick={onChanged} disabled={loading}>
          <RefreshCwIcon className={loading ? "size-4 animate-spin" : "size-4"} /> 새로고침
        </Button>
      </CardHeader>
      <CardContent className="space-y-3">
        {loading && (
          <p role="status" className="text-muted-foreground text-sm">
            연결 상태를 확인하고 있습니다.
          </p>
        )}
        {!loading &&
          status &&
          (!status.exists ? (
            <div className="flex flex-wrap items-center justify-between gap-4">
              <p className="max-w-prose text-muted-foreground text-sm">
                ScopeSentry를 연결하려면 먼저 데이터 소스를 생성한 뒤 MCP 주소와 API 키를 입력하세요.
              </p>
              <Button onClick={create} disabled={busy}>
                데이터 소스 생성
              </Button>
            </div>
          ) : (
            <>
              {!status.configured && (
                <p className="text-muted-foreground text-sm">
                  MCP 주소와 API 키를 입력한 뒤 저장하면 연결이 활성화됩니다.
                </p>
              )}
              {status.configured && !status.enabled && (
                <p className="text-muted-foreground text-sm">
                  데이터 소스가 설정되었지만 비활성화 상태입니다. 저장하면 자동 활성화됩니다.
                </p>
              )}
              <div className="grid gap-3 md:grid-cols-2">
                <div className="space-y-1.5">
                  <Label htmlFor="scopesentry-url">MCP 주소</Label>
                  <Input
                    id="scopesentry-url"
                    placeholder="http://<호스트>:8082/mcp"
                    value={url}
                    onChange={(e) => setUrl(e.target.value)}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="scopesentry-api-key">API 키</Label>
                  <Input
                    id="scopesentry-api-key"
                    type="password"
                    placeholder="ssk_..."
                    value={apiKey}
                    onChange={(e) => setApiKey(e.target.value)}
                    aria-describedby="scopesentry-api-key-help"
                  />
                  <p id="scopesentry-api-key-help" className="text-muted-foreground text-xs">
                    비워 두면 저장된 키를 유지합니다.
                  </p>
                </div>
              </div>
              <div className="flex flex-wrap items-center gap-3">
                <Button onClick={save} disabled={busy}>
                  저장 및 활성화
                </Button>
                {status.enabled && status.tools.length > 0 && (
                  <span className="text-muted-foreground text-xs">도구 {status.tools.length}개 발견</span>
                )}
              </div>
            </>
          ))}
      </CardContent>
    </Card>
  );
}

function StatusBadge({ status, loading }: { status: SSStatus | null; loading: boolean }) {
  if (loading) return <Badge variant="secondary">확인 중…</Badge>;
  if (!status) return <Badge variant="destructive">확인 실패</Badge>;
  if (!status.exists) return <Badge variant="destructive">미생성</Badge>;
  if (!status.configured) return <Badge variant="outline">미설정</Badge>;
  if (!status.enabled) return <Badge variant="outline">비활성화</Badge>;
  if (status.reachable)
    return (
      <Badge className="bg-emerald-600 hover:bg-emerald-600">
        <CheckCircle2Icon className="mr-1 size-3" /> 연결됨
      </Badge>
    );
  return (
    <Badge variant="destructive">
      <AlertCircleIcon className="mr-1 size-3" /> 접속 불가
    </Badge>
  );
}

// 프로젝트·작업 기준의 동기화 대상 선택

function SyncWorkbench() {
  const [dimension, setDimension] = React.useState<Dimension>("project");
  const [assetTypes, setAssetTypes] = React.useState<Record<string, boolean>>({
    subdomain: true,
    service: true,
    app: true,
  });
  const [createCompany, setCreateCompany] = React.useState(true);

  const [projects, setProjects] = React.useState<SSProject[]>([]);
  const [tasks, setTasks] = React.useState<SSTask[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [loadError, setLoadError] = React.useState("");
  const listRequest = React.useRef(0);
  const [search, setSearch] = React.useState("");
  const [page, setPage] = React.useState(1);
  const [selected, setSelected] = React.useState<Set<string>>(new Set());

  const [syncing, setSyncing] = React.useState(false);
  const [result, setResult] = React.useState<Awaited<ReturnType<typeof api.ssSync>> | null>(null);

  const load = React.useCallback(() => {
    const request = ++listRequest.current;
    setLoading(true);
    setLoadError("");
    setSelected(new Set());
    const fn = dimension === "project"
      ? api.ssProjects(page, 50, search).then((r) => { if (request === listRequest.current) setProjects(r.projects); })
      : api.ssTasks(page, 50, search).then((r) => { if (request === listRequest.current) setTasks(r); });
    fn.catch((error: Error) => { if (request === listRequest.current) setLoadError(error.message); })
      .finally(() => { if (request === listRequest.current) setLoading(false); });
  }, [dimension, page, search]);

  React.useEffect(() => {
    load();
    return () => { listRequest.current++; };
  }, [load]);

  const rows = dimension === "project" ? projects : tasks;
  const idOf = (row: SSProject | SSTask) => (dimension === "project" ? (row as SSProject).id : (row as SSTask).name);

  const toggle = (id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };
  const toggleAll = () => {
    setSelected((prev) => (prev.size === rows.length ? new Set() : new Set(rows.map(idOf))));
  };

  const chosenTypes = ASSET_TYPES.filter((t) => assetTypes[t.key]).map((t) => t.key);

  const runSync = async () => {
    if (loading || loadError || syncing) return;
    if (selected.size === 0) return toast.error(`최소 하나의 ${dimension === "project" ? "프로젝트" : "작업"}`);
    if (chosenTypes.length === 0) return toast.error("자산 유형을 하나 이상 선택하세요");
    setSyncing(true);
    setResult(null);
    try {
      const r = await api.ssSync({
        dimension,
        targets: [...selected],
        asset_types: chosenTypes,
        create_company: dimension === "project" ? createCompany : false,
      });
      setResult(r);
      const total = Object.values(r.synced ?? {}).reduce((a, b) => a + b, 0);
      toast.success(`동기화 완료. 자산 ${total}건 저장됨`);
    } catch (e) {
      toast.error(`동기화 실패: ${(e as Error).message}`);
    } finally {
      setSyncing(false);
    }
  };

  const renderRows = () => {
    if (loading) {
      return (
        <TableRow>
          <TableCell colSpan={4} className="py-8 text-center text-muted-foreground text-sm">
            불러오는 중…
          </TableCell>
        </TableRow>
      );
    }
    if (loadError) return null;
    if (rows.length === 0) {
      return (
        <TableRow>
          <TableCell colSpan={4} className="py-8 text-center text-muted-foreground text-sm">
            데이터 없음
          </TableCell>
        </TableRow>
      );
    }
    if (dimension === "project") {
      return projects.map((p) => (
        <TableRow key={p.id} className="cursor-pointer" onClick={() => toggle(p.id)}>
          <TableCell onClick={(e) => e.stopPropagation()}>
            <Checkbox aria-label={`${p.name} 프로젝트 선택`} checked={selected.has(p.id)} onCheckedChange={() => toggle(p.id)} />
          </TableCell>
          <TableCell className="font-medium">{p.name}</TableCell>
          <TableCell>{p.tag ? <Badge variant="secondary">{p.tag}</Badge> : "—"}</TableCell>
          <TableCell className="text-right">{p.AssetCount ?? 0}</TableCell>
        </TableRow>
      ));
    }
    return tasks.map((t) => (
      <TableRow key={t.id} className="cursor-pointer" onClick={() => toggle(t.name)}>
        <TableCell onClick={(e) => e.stopPropagation()}>
          <Checkbox aria-label={`${t.name} 작업 선택`} checked={selected.has(t.name)} onCheckedChange={() => toggle(t.name)} />
        </TableCell>
        <TableCell className="font-medium">{t.name}</TableCell>
        <TableCell>
          <Badge variant={t.progress === 100 ? "secondary" : "outline"}>
            {t.progress != null ? `${t.progress}%` : "—"}
          </Badge>
        </TableCell>
        <TableCell className="text-muted-foreground text-xs">{t.endTime || t.creatTime || "—"}</TableCell>
      </TableRow>
    ));
  };

  return (
    <section aria-labelledby="sync-target-title" className="space-y-4">
      <h2 id="sync-target-title" className="font-semibold text-base">
        동기화 대상
      </h2>
      <div className="space-y-4">
        {/* 동기화 기준 */}
        <Tabs
          value={dimension}
          onValueChange={(v) => {
            setDimension(v as Dimension);
            setPage(1);
          }}
        >
          <TabsList>
            <TabsTrigger value="project">프로젝트 기준</TabsTrigger>
            <TabsTrigger value="task">작업 기준</TabsTrigger>
          </TabsList>
        </Tabs>

        {/* 자산 유형과 저장 옵션 */}
        <div className="flex flex-wrap items-center gap-4">
          <span className="font-medium text-sm">동기화 자산:</span>
          {ASSET_TYPES.map((t) => (
            <label key={t.key} htmlFor={`at-${t.key}`} className="flex items-center gap-1.5 text-sm">
              <Checkbox
                id={`at-${t.key}`}
                checked={!!assetTypes[t.key]}
                onCheckedChange={(c) => setAssetTypes((prev) => ({ ...prev, [t.key]: !!c }))}
              />
              {t.label}
            </label>
          ))}
          {dimension === "project" && (
            <label htmlFor="create-company" className="flex items-center gap-1.5 text-sm">
              <Checkbox id="create-company" checked={createCompany} onCheckedChange={(c) => setCreateCompany(!!c)} />
              프로젝트별로 기업을 생성하고 자산 범위 기록
            </label>
          )}
        </div>

        {/* 검색과 동기화 */}
        <div className="flex flex-wrap items-center gap-2">
          <div className="relative max-w-xs flex-1">
            <SearchIcon className="absolute top-1/2 left-2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              className="pl-8"
              placeholder={dimension === "project" ? "프로젝트 이름 검색" : "작업 이름 검색"}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  setPage(1);
                  load();
                }
              }}
            />
          </div>
          <Button variant="outline" size="sm" onClick={load} disabled={loading}>
            <span className="sr-only">동기화 대상 새로고침</span>
            <RefreshCwIcon className={loading ? "size-4 animate-spin" : "size-4"} />
          </Button>
          <div className="flex-1" />
          <span className="text-muted-foreground text-xs">{selected.size}개 선택됨</span>
          <Button onClick={runSync} disabled={loading || !!loadError || syncing || selected.size === 0}>
            <DownloadIcon className={syncing ? "size-4 animate-pulse" : "size-4"} /> 선택 항목 동기화
          </Button>
        </div>

        {/* 대상 목록 */}
        <DataLoadStatus loading={loading} error={loadError} label="동기화 대상 불러오기" onRetry={load} />
        <Table className="bg-card">
          <TableHeader>
            <TableRow>
              <TableHead className="w-10">
                <Checkbox aria-label="현재 동기화 대상 전체 선택" disabled={loading || !!loadError} checked={rows.length > 0 && selected.size === rows.length} onCheckedChange={toggleAll} />
              </TableHead>
              <TableHead>{dimension === "project" ? "프로젝트 이름" : "작업 이름"}</TableHead>
              {dimension === "project" ? (
                <>
                  <TableHead>태그</TableHead>
                  <TableHead className="text-right">자산 수</TableHead>
                </>
              ) : (
                <>
                  <TableHead>상태</TableHead>
                  <TableHead>시간</TableHead>
                </>
              )}
            </TableRow>
          </TableHeader>
          <TableBody>{renderRows()}</TableBody>
        </Table>

        {/* 페이지 이동 */}
        <div className="flex items-center justify-end gap-2">
          <Button variant="outline" size="sm" disabled={page <= 1 || loading} onClick={() => setPage((p) => p - 1)}>
            이전 페이지
          </Button>
          <span className="text-muted-foreground text-xs">{page}페이지</span>
          <Button
            variant="outline"
            size="sm"
            disabled={rows.length < 50 || loading}
            onClick={() => setPage((p) => p + 1)}
          >
            다음 페이지
          </Button>
        </div>

        {/* 동기화 결과 */}
        {result && <SyncResult result={result} />}
      </div>
    </section>
  );
}

function SyncResult({ result }: { result: Awaited<ReturnType<typeof api.ssSync>> }) {
  const synced = result.synced ?? {};
  const labels: Record<string, string> = { subdomain: "서브도메인", service: "서비스", app: "App", ip: "IP" };
  return (
    <div className="space-y-2 rounded-md border bg-muted/40 p-3 text-sm">
      <div className="flex flex-wrap gap-3">
        {Object.entries(synced).map(([k, v]) => (
          <Badge key={k} variant="secondary">
            {labels[k] ?? k}: {v}
          </Badge>
        ))}
      </div>
      {result.companies && result.companies.length > 0 && (
        <p className="text-muted-foreground">기업 생성/업데이트: {result.companies.join("、")}</p>
      )}
      {result.warnings && result.warnings.length > 0 && (
        <ul className="list-inside list-disc text-amber-600 dark:text-amber-500">
          {result.warnings.map((wm) => (
            <li key={wm}>{wm}</li>
          ))}
        </ul>
      )}
      {result.errors && result.errors.length > 0 && (
        <ul className="list-inside list-disc text-destructive">
          {result.errors.slice(0, 20).map((em) => (
            <li key={em}>{em}</li>
          ))}
          {result.errors.length > 20 && <li>…총 오류 {result.errors.length}건</li>}
        </ul>
      )}
    </div>
  );
}
