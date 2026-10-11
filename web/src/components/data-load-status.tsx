"use client";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";

export function DataLoadStatus({
  loading = false,
  error = "",
  label,
  onRetry,
}: {
  loading?: boolean;
  error?: string;
  label: string;
  onRetry: () => void;
}) {
  if (loading) return <p role="status" className="text-sm text-muted-foreground">{label.replace(/불러오기$/, "불러오는")} 중…</p>;
  if (!error) return null;
  return (
    <Alert variant="destructive">
      <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
        <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">{label} 실패: {error}</span>
        <Button variant="outline" size="sm" onClick={onRetry}>다시 시도</Button>
      </AlertDescription>
    </Alert>
  );
}
