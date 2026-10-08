import { Button } from "@/components/ui/button";

export function AuthSessionState({ error, onRetry }: { error: string | null; onRetry: () => void }) {
  return (
    <div className="flex min-h-dvh items-center justify-center p-6">
      {error ? (
        <div role="alert" className="max-w-md space-y-4 text-center">
          <p className="font-semibold">앱에 연결할 수 없습니다</p>
          <p className="text-destructive text-sm">{error}</p>
          <Button type="button" onClick={onRetry}>
            다시 시도
          </Button>
        </div>
      ) : (
        <p role="status" className="text-muted-foreground">
          앱을 준비하는 중…
        </p>
      )}
    </div>
  );
}
