"use client";

import { useEffect, useState } from "react";

import { ArchiveRestoreIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import type { ArtexDesktop, DesktopBackupStatus } from "@/lib/desktop";

export function BackupCard() {
  const [status, setStatus] = useState<DesktopBackupStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [desktop, setDesktop] = useState(false);
  useEffect(() => {
    setDesktop(!!window.artexDesktop);
    window.artexDesktop
      ?.backupStatus()
      .then(setStatus)
      .catch((cause: Error) => setError(cause.message));
  }, []);
  async function execute(
    action: (bridge: ArtexDesktop) => Promise<DesktopBackupStatus | { cancelled: true } | undefined>,
  ) {
    setBusy(true);
    setError("");
    try {
      const bridge = window.artexDesktop;
      if (!bridge) throw new Error("ARTEX 데스크톱 앱에서 다시 시도하세요");
      const result = await action(bridge);
      if (result && !("cancelled" in result)) setStatus(result);
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
          <ArchiveRestoreIcon className="size-4" />
          백업과 복원
        </CardTitle>
        <CardDescription>설정·DB·증거·사용자 스킬을 함께 보존합니다.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        {!desktop && <p className="text-muted-foreground">ARTEX 데스크톱 앱에서 백업과 복원을 사용할 수 있습니다.</p>}
        {desktop && (
          <>
            <p className="text-muted-foreground">
              백업 중에는 실행 중인 작업을 종료하고, 완료 후 앱을 다시 연결합니다. 복원은 새 폴더에 만들며 기존 데이터와
              이전 백업을 보존합니다.
            </p>
            <div className="flex items-center justify-between gap-3">
              <Label htmlFor="automatic-backup">하루 첫 정상 종료 시 자동 백업</Label>
              <Switch
                id="automatic-backup"
                checked={status?.automatic ?? true}
                disabled={!status || busy}
                onCheckedChange={(value) => void execute((bridge) => bridge.setAutomaticBackup(value))}
              />
            </div>
            {status?.lastBackupAt && <p>마지막 백업: {new Date(status.lastBackupAt).toLocaleString("ko-KR")}</p>}
            {status?.lastBackup && <p className="break-all text-muted-foreground">{status.lastBackup}</p>}
            {(error || status?.error) && (
              <p role="alert" className="break-words text-destructive">
                {error || status?.error}
              </p>
            )}
            <div className="flex flex-wrap gap-2">
              <Button
                size="sm"
                disabled={!status || busy}
                onClick={() => void execute((bridge) => bridge.createBackup())}
              >
                백업 만들기
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={!status || busy}
                onClick={() => void execute((bridge) => bridge.restoreBackup())}
              >
                새 폴더로 복원
              </Button>
            </div>
            {status?.restoredHome && (
              <div className="space-y-2">
                <p className="break-all">복원 위치: {status.restoredHome}</p>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={busy}
                  onClick={() => void execute((bridge) => bridge.openRestoredHome().then(() => undefined))}
                >
                  복원한 데이터로 재시작
                </Button>
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}
