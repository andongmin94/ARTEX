"use client";

import { useEffect, useState } from "react";

import { DownloadIcon } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import type { ArtexDesktop, DesktopUpdateStatus } from "@/lib/desktop";

export function UpdateCard() {
  const [status, setStatus] = useState<DesktopUpdateStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    window.artexDesktop
      ?.updateStatus()
      .then(setStatus)
      .catch((cause: Error) => setError(cause.message));
  }, []);
  async function execute(action: (bridge: ArtexDesktop) => Promise<DesktopUpdateStatus | undefined>) {
    setBusy(true);
    setError("");
    try {
      const bridge = window.artexDesktop;
      if (!bridge) throw new Error("ARTEX 데스크톱 앱에서 다시 시도하세요");
      const result = await action(bridge);
      if (result) setStatus(result);
    } catch (cause) {
      setError((cause as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Card className="mb-4 break-inside-avoid md:mb-6">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <DownloadIcon className="size-4" />
          데스크톱 업데이트
        </CardTitle>
        <CardDescription>앱 전체를 서명과 무결성 검증 후 함께 업데이트합니다.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        <Badge variant="outline">
          {!status || status.state === "unavailable" ? "자동 업데이트 미구성" : `현재 버전 ${status.version}`}
        </Badge>
        <p className="text-muted-foreground">
          {status?.message ?? "ARTEX 데스크톱 앱에서 업데이트 상태를 확인할 수 있습니다."}
        </p>
        {error && (
          <p role="alert" className="text-destructive">
            {error}
          </p>
        )}
        {status && status.state !== "unavailable" && (
          <div className="flex flex-wrap gap-2">
            <Button
              size="sm"
              variant="outline"
              disabled={busy || ["downloading", "installing", "downloaded"].includes(status.state)}
              onClick={() => void execute((bridge) => bridge.checkUpdate())}
            >
              업데이트 확인
            </Button>
            {status.state === "available" && (
              <Button size="sm" disabled={busy} onClick={() => void execute((bridge) => bridge.downloadUpdate())}>
                업데이트 다운로드
              </Button>
            )}
            {status.state === "downloaded" && (
              <Button
                size="sm"
                disabled={busy}
                onClick={() => void execute((bridge) => bridge.installUpdate().then(() => undefined))}
              >
                백업 후 업데이트·재시작
              </Button>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  );
}
