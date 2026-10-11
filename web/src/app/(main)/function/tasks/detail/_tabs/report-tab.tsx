"use client";

import * as React from "react";

import { CheckIcon, CopyIcon, FileTextIcon } from "lucide-react";
import { toast } from "sonner";

import { Markdown } from "@/components/markdown";
import { DataLoadStatus } from "@/components/data-load-status";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/api";
import { copyText } from "@/lib/utils";

export function ReportTab({ taskId }: { taskId: string }) {
  const [report, setReport] = React.useState<string>("");
  const [loading, setLoading] = React.useState(true);
  const [copied, setCopied] = React.useState(false);
  const [error, setError] = React.useState("");
  const [revision, setRevision] = React.useState(0);
  const [loadedTask, setLoadedTask] = React.useState("");

  // biome-ignore lint/correctness/useExhaustiveDependencies: revision explicitly retries this task report.
  React.useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    setReport("");
    setCopied(false);
    api
      .report(taskId)
      .then((text) => {
        if (active) { setReport(text); setLoadedTask(taskId); }
      })
      .catch((error: Error) => {
        if (active) setError(error.message);
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [taskId, revision]);

  async function copy() {
    if (!report || loading || error || loadedTask !== taskId) return;
    const ok = await copyText(report);
    if (ok) {
      setCopied(true);
      toast.success("Markdown을 복사했습니다");
      setTimeout(() => setCopied(false), 1500);
    } else {
      toast.error("복사에 실패했습니다. 텍스트를 직접 선택해 복사하세요");
    }
  }

  let content: React.ReactNode;
  if (error) {
    content = <DataLoadStatus error={error} label="보고서 불러오기" onRetry={() => setRevision((value) => value + 1)} />;
  } else if (loading || loadedTask !== taskId) {
    content = (
      <div className="flex flex-col items-center justify-center gap-2 rounded-md border border-dashed py-16 text-muted-foreground text-sm">
        <FileTextIcon className="size-8 opacity-40" />
        불러오는 중…
      </div>
    );
  } else if (report) {
    content = (
      <div className="max-h-[60vh] overflow-auto rounded-md border bg-muted/20 p-4">
        <Markdown text={report} />
      </div>
    );
  } else {
    content = (
      <div className="flex flex-col items-center justify-center gap-2 rounded-md border border-dashed py-16 text-muted-foreground text-sm">
        <FileTextIcon className="size-8 opacity-40" />
        보고서 없음
      </div>
    );
  }

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between">
        <CardTitle className="flex items-center gap-2 text-sm">
          <FileTextIcon className="size-4" /> 침투 테스트 보고서(Markdown)
        </CardTitle>
        <div className="flex gap-2">
          {report && !loading && !error && loadedTask === taskId && (
            <Button size="sm" variant="outline" onClick={copy}>
              {copied ? <CheckIcon /> : <CopyIcon />} 복사
            </Button>
          )}
        </div>
      </CardHeader>
      <CardContent>{content}</CardContent>
    </Card>
  );
}
