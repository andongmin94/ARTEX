"use client";

import { useEffect, useState } from "react";

import { WrenchIcon } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/api";
import type { RuntimeToolsStatus } from "@/lib/types";

const labels = {
  shell: "명령 셸",
  pty: "터미널",
  python: "Python",
  node: "Node.js",
  browser: "브라우저",
  cli: "명령 도구",
};

function preparationLabel(status: RuntimeToolsStatus) {
  if (status.ready) return "실행 환경 준비됨";
  if (status.components.some((component) => component.execution === "available")) return "일부 도구 사용 가능";
  return "실행 환경 미준비";
}

function executionLabel(component: RuntimeToolsStatus["components"][number]) {
  if (component.execution === "available") return "사용 가능";
  if (component.state === "verified") return "실행 차단";
  if (component.state === "not_prepared") return "미준비";
  return "검증 실패";
}

export function RuntimeToolsCard() {
  const [status, setStatus] = useState<RuntimeToolsStatus | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    api
      .runtimeTools()
      .then(setStatus)
      .catch((cause) => setError((cause as Error).message));
  }, []);

  return (
    <Card className="mb-4 break-inside-avoid md:mb-6">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <WrenchIcon className="size-4" />
          외부 실행 도구
        </CardTitle>
        <CardDescription>앱 전용 실행 환경의 준비 상태입니다.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        {error && (
          <p role="alert" className="text-destructive">
            준비 상태 조회 실패: {error}
          </p>
        )}
        {!error && !status && <p className="text-muted-foreground">준비 상태를 확인하는 중…</p>}
        {!error && status && (
          <>
            <Badge variant="outline">{preparationLabel(status)}</Badge>
            <p className="text-muted-foreground">{status.message}</p>
            <dl className="divide-y-2 rounded-md border-2 px-3">
              {status.components.map((component) => (
                <div key={component.key} className="space-y-1 py-2">
                  <div className="flex justify-between gap-3">
                    <dt>{labels[component.key]}</dt>
                    <dd className="shrink-0 text-muted-foreground">{executionLabel(component)}</dd>
                  </div>
                  <dd className="text-muted-foreground text-xs">
                    {component.version && <span>버전 {component.version}</span>}
                    {component.message && <p className="mt-1">{component.message}</p>}
                  </dd>
                </div>
              ))}
            </dl>
          </>
        )}
      </CardContent>
    </Card>
  );
}
