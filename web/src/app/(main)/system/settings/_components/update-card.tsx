"use client";

import * as React from "react";

import {
  CheckCircle2Icon,
  DownloadIcon,
  ExternalLinkIcon,
  RefreshCwIcon,
  RotateCcwIcon,
  TriangleAlertIcon,
} from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { api, sseUrl } from "@/lib/api";
import type { UpdateCheck, UpdateProgress } from "@/lib/types";

/** 等待新版本上线的最长时间。一次升级要经过三次进程启动（暂存 → 换装 → 新版），
 *  每次都是秒级，三分钟足够覆盖慢磁盘和 Docker 容器重建。 */
const RESTART_TIMEOUT_MS = 180_000;

function humanSize(n?: number): string {
  if (!n || n <= 0) return "";
  const units = ["B", "KB", "MB", "GB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

export function UpdateCard() {
  const [info, setInfo] = React.useState<UpdateCheck | null>(null);
  const [checking, setChecking] = React.useState(true);
  const [progress, setProgress] = React.useState<UpdateProgress | null>(null);
  // 与 progress 分开：暂存完成后进程就没了，SSE 会断，此时要切到轮询 /api/health。
  const [restarting, setRestarting] = React.useState(false);
  const [busy, setBusy] = React.useState(false);

  // quiet 同时决定要不要绕过后端缓存：进页面时的自动检查用缓存（顶栏刚查过），
  // 用户手动点「检查更新」则强制回源，否则刚发布的版本要等缓存过期才看得到。
  const check = React.useCallback((quiet = false) => {
    setChecking(true);
    api
      .checkUpdate(!quiet)
      .then((r) => {
        setInfo(r);
        if (!quiet) {
          if (r.error) toast.error("업데이트 확인 실패:" + r.error);
          else if (r.has_update) toast.success(`새 버전 발견: ${r.latest}`);
          else if (r.comparable) toast.success("최신 버전입니다");
        }
      })
      .catch((e) => {
        if (!quiet) toast.error("업데이트 확인 실패:" + (e as Error).message);
      })
      .finally(() => setChecking(false));
  }, []);

  React.useEffect(() => {
    check(true);
  }, [check]);

  // 轮询 /api/health 直到版本号变化。
  //
  // 判据必须是"版本变了"而不是"能连上了"：换装过程中旧版本会短暂地重新起来一次
  // （那一次只负责把 artex.new 换上去然后立刻退出），只看连通性会误判成功。
  const waitForNewVersion = React.useCallback(async (fromVersion: string) => {
    setRestarting(true);
    const deadline = Date.now() + RESTART_TIMEOUT_MS;
    while (Date.now() < deadline) {
      await sleep(2000);
      try {
        const r = await fetch("/api/health", { cache: "no-store" });
        if (r.ok) {
          const j = (await r.json()) as { version?: string };
          if (j.version && j.version !== fromVersion) {
            toast.success(`업데이트 완료: ${j.version}, 페이지를 다시 불러옵니다`);
            await sleep(800);
            window.location.reload();
            return;
          }
        }
      } catch {
        // 重启窗口内连不上是预期的，继续轮询。
      }
    }
    setRestarting(false);
    toast.error("서비스 재시작 대기 시간이 초과되었습니다. 백엔드 로그를 확인하거나 ARTEX를 start.sh / start.bat으로 시작했는지 확인하세요.");
  }, []);

  // 订阅更新进度。SSE 不走 Next 的 /api 重写（那层会缓冲，事件推不出来）。
  const openStream = React.useCallback(
    (fromVersion: string) => {
      const es = new EventSource(sseUrl("/api/update/stream"));
      es.onmessage = (ev) => {
        let p: UpdateProgress;
        try {
          p = JSON.parse(ev.data) as UpdateProgress;
        } catch {
          return;
        }
        setProgress(p);
        if (p.phase === "failed") {
          es.close();
          setBusy(false);
          toast.error("업데이트 실패:" + (p.error || p.message));
          return;
        }
        if (p.phase === "staged") {
          es.close();
          void waitForNewVersion(fromVersion);
        }
      };
      es.onerror = () => {
        // 进程退出时 SSE 必然断开。如果已经进入等待重启，这属于正常现象，
        // 交给 /api/health 轮询继续判定即可。
        es.close();
      };
      return es;
    },
    [waitForNewVersion],
  );

  const doUpdate = () => {
    if (!info) return;
    const from = info.current;
    const ok = window.confirm(
      `업데이트할 버전: ${info.latest}？\n\n` +
        "업데이트 시 프로그램이 재시작되며 실행 중인 작업이 중단됩니다.\n" +
        (info.mode === "docker"
          ? "\n주의: 컨테이너 내 업데이트는 프로그램만 교체합니다. 이미지의 playwright / nmap 등의 도구는 업데이트하지 않습니다." +
            "새 버전에서 다른 도구가 필요하면 docker compose pull을 사용하세요."
          : ""),
    );
    if (!ok) return;

    setBusy(true);
    setProgress({ phase: "downloading", percent: 0, message: "준비 중…" });
    const es = openStream(from);
    api.applyUpdate().catch((e) => {
      es.close();
      setBusy(false);
      setProgress(null);
      toast.error("업데이트 시작 실패:" + (e as Error).message);
    });
  };

  const doRollback = () => {
    if (!info) return;
    if (
      !window.confirm(
        "이전 버전으로 되돌릴까요?\n\n프로그램이 재시작되며 실행 중인 작업이 중단됩니다.\n주의: DB 구조는 되돌리지 않으므로 이전 버전에서 새 데이터를 읽지 못할 수 있습니다.",
      )
    )
      return;
    const from = info.current;
    setBusy(true);
    api
      .rollbackUpdate()
      .then(() => {
        toast.success("이전 버전으로 전환했습니다. 재시작 중…");
        void waitForNewVersion(from);
      })
      .catch((e) => {
        setBusy(false);
        toast.error("되돌리기 실패:" + (e as Error).message);
      });
  };

  const phase = progress?.phase;
  const showProgress = busy || restarting;
  // 只有下载阶段拿得到真实百分比（按 Content-Length 算）。校验/解压/等待重启都是
  // 时长不可知的阶段，进度条填满并加个脉冲动画表示"在忙但说不准还要多久"。
  const downloading = !restarting && phase === "downloading";
  const pct = downloading ? Math.max(progress?.percent ?? 0, 0) : 100;

  return (
    // 设置页是多列瀑布流布局，卡片自己负责行间距并禁止跨列断开（见 page.tsx 的注释）。
    <Card className="mb-4 break-inside-avoid md:mb-6">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <DownloadIcon className="size-4" />
          버전 및 업데이트
        </CardTitle>
        <CardDescription>GitHub에서 새 버전을 확인하고 설치합니다. 업데이트 시 프로그램이 재시작되며 실행 중인 작업이 중단됩니다.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span className="text-muted-foreground">현재 버전</span>
          <Badge variant="secondary" className="font-mono">
            {info?.current ?? "…"}
          </Badge>
          {info && (
            <>
              <Badge variant="outline" className="font-mono">
                {info.os}/{info.arch}
              </Badge>
              <Badge variant="outline">{info.mode === "docker" ? "Docker" : "독립 실행 프로그램"}</Badge>
            </>
          )}
          {info?.latest && (
            <>
              <span className="text-muted-foreground">최신 버전</span>
              <Badge variant={info.has_update ? "default" : "secondary"} className="font-mono">
                {info.latest}
              </Badge>
            </>
          )}
          {info?.html_url && (
            <a
              href={info.html_url}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1 text-xs text-muted-foreground underline-offset-4 hover:underline"
            >
              변경 내역 <ExternalLinkIcon className="size-3" />
            </a>
          )}
        </div>

        {info?.boot_notice && (
          <p className="flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 p-2 text-xs text-amber-700 dark:text-amber-400">
            <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
            {info.boot_notice}
          </p>
        )}

        {info?.error && (
          <p className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">
            <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
            GitHub에 연결할 수 없습니다:{info.error}
            {"　"}위에서 전역 프록시를 설정한 뒤 다시 시도할 수 있습니다.
          </p>
        )}

        {info && !info.comparable && info.reason && <p className="text-xs text-muted-foreground">{info.reason}</p>}

        {info?.has_update && info.asset_available === false && (
          <p className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">
            <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
            {info.latest} 에는 {info.os}/{info.arch} 용 배포 패키지가 없습니다(누락: {info.asset}). 자동으로 업데이트할 수 없습니다.
          </p>
        )}

        {info?.has_update && info.asset_available !== false && (
          <p className="text-xs text-muted-foreground">
            다운로드할 파일: <span className="font-mono">{info.asset}</span>
            {info.size ? `（${humanSize(info.size)}）` : ""}. SHA256 검증과 실행 점검을 통과한 뒤 교체하며, 실패하면 현재 버전을 유지합니다.
          </p>
        )}

        {info && !info.has_update && info.comparable && !info.error && (
          <p className="flex items-center gap-2 text-xs text-muted-foreground">
            <CheckCircle2Icon className="size-3.5 text-emerald-600" />
            최신 버전입니다.
          </p>
        )}

        {info?.mode === "docker" && info.has_update && (
          <p className="text-xs text-muted-foreground">
            Docker 내 업데이트는 프로그램만 교체하며 이미지의 playwright / nmap 등 도구는 업데이트하지 않습니다.
            <span className="font-mono"> docker compose up -d </span>
            컨테이너를 다시 만들면 이미지의 원래 버전으로 돌아갑니다. 이미지까지 업데이트하려면 다음 명령을 실행하세요:
            <span className="font-mono"> docker compose pull artex &amp;&amp; docker compose up -d artex</span>。
          </p>
        )}

        {showProgress && (
          <div className="space-y-1.5">
            <Progress value={pct} className={downloading ? undefined : "animate-pulse"} />
            <p className="text-xs text-muted-foreground">
              {restarting ? "재시작하여 새 버전을 적용하는 중입니다(페이지가 자동으로 새로고침됩니다)…" : progress?.message}
            </p>
          </div>
        )}

        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" onClick={() => check(false)} disabled={checking || busy || restarting}>
            <RefreshCwIcon className={checking ? "size-4 animate-spin" : "size-4"} />
            업데이트 확인
          </Button>
          <Button
            size="sm"
            onClick={doUpdate}
            disabled={busy || restarting || !info?.has_update || info?.asset_available === false}
          >
            <DownloadIcon className="size-4" />
            {info?.has_update ? `업데이트: ${info.latest}` : "지금 업데이트"}
          </Button>
          {info?.has_backup && (
            <Button variant="ghost" size="sm" onClick={doRollback} disabled={busy || restarting}>
              <RotateCcwIcon className="size-4" />
              이전 버전으로 되돌리기
            </Button>
          )}
        </div>

        <p className="text-xs text-muted-foreground">
          원클릭 업데이트는 실행 스크립트를 통해 재시작합니다. ARTEX를 <span className="font-mono">start.sh</span>(Windows:
          <span className="font-mono"> start.bat</span>)으로 시작하세요. artex 실행 파일을 직접 실행하면 종료 후 자동으로 다시 시작되지 않습니다.
        </p>
      </CardContent>
    </Card>
  );
}
