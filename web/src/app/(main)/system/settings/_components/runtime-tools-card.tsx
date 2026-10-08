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
            <Badge variant="outline">{status.ready ? "실행 환경 준비됨" : "실행 환경 미준비"}</Badge>
            <p className="text-muted-foreground">{status.message}</p>
            <dl className="divide-y-2 rounded-md border-2 px-3">
              {status.components.map((component) => (
                <div key={component.key} className="flex justify-between gap-3 py-2">
                  <dt>{labels[component.key]}</dt>
                  <dd className="text-muted-foreground">
                    {component.state === "not_prepared" ? "미준비" : component.state}
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
