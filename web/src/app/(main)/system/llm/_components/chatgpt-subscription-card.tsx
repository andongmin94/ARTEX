"use client";

import * as React from "react";

import Image from "next/image";

import { ExternalLinkIcon, Loader2Icon, RefreshCwIcon } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { api } from "@/lib/api";
import { chatGPTAuthorizationURL, openChatGPTLogin, openChatGPTUsage } from "@/lib/desktop";
import type { ChatGPTModel, ChatGPTStatus, LLMProfile } from "@/lib/types";

const WELCOME_SEEN = "artex_chatgpt_welcome_seen";
type Action = "login" | "cancel" | "logout" | "activate";

const CHATGPT_ERRORS: Record<string, { message: string; reconnect?: boolean }> = {
  chatgpt_not_connected: { message: "ChatGPT 계정을 연결하세요." },
  chatgpt_plan_consent_required: { message: "다시 연결하여 ChatGPT 구독 사용을 승인하세요." },
  chatgpt_reauthentication_required: { message: "ChatGPT 인증이 만료되었습니다. 다시 연결하세요.", reconnect: true },
  chatgpt_remote_revocation_unconfirmed: {
    message: "ChatGPT 측 연결 해제를 확인하지 못했습니다. ChatGPT 설정에서 연결 권한을 확인하세요.",
  },
  chatgpt_credential_storage_failed: {
    message: "연결 정보를 저장할 수 없습니다. 앱 데이터 폴더의 접근 권한을 확인하세요.",
  },
  chatgpt_identity_validation_failed: {
    message: "ChatGPT 계정 확인에 실패했습니다. 다시 연결하세요.",
    reconnect: true,
  },
  chatgpt_invalid_oauth_response: { message: "ChatGPT 로그인 응답을 확인하지 못했습니다. 다시 연결하세요." },
  chatgpt_service_unavailable: { message: "ChatGPT 서비스에 연결할 수 없습니다. 잠시 후 다시 시도하세요." },
  chatgpt_sign_in_expired_or_cancelled: {
    message: "로그인이 만료되었거나 취소되었습니다. 연결하려면 다시 시도하세요.",
  },
  chatgpt_sign_in_declined: { message: "ChatGPT 로그인을 승인하지 않았습니다. 연결하려면 다시 시도하세요." },
  chatgpt_sign_in_cancelled: { message: "ChatGPT 연결이 취소되었습니다. 연결하려면 다시 시도하세요." },
  chatgpt_oauth_request_failed: { message: "ChatGPT 인증 요청에 실패했습니다. 잠시 후 다시 연결하세요." },
  chatgpt_client_closed: { message: "ChatGPT 연결 기능이 종료되었습니다. 앱을 다시 실행하세요." },
};

function chatGPTError(message: string) {
  return Object.hasOwn(CHATGPT_ERRORS, message) ? CHATGPT_ERRORS[message] : { message };
}

export function ChatGPTSubscriptionCard({
  profiles,
  onProfileActivated,
  onStatusChange,
}: {
  profiles: LLMProfile[];
  onProfileActivated: () => Promise<void>;
  onStatusChange: (status: ChatGPTStatus | null) => void;
}) {
  const [status, setStatus] = React.useState<ChatGPTStatus | null>(null);
  const [checking, setChecking] = React.useState(true);
  const [statusError, setStatusError] = React.useState("");
  const [actionError, setActionError] = React.useState("");
  const [busy, setBusy] = React.useState<Action | null>(null);
  const [refreshVersion, setRefreshVersion] = React.useState(0);
  const [authorizationURL, setAuthorizationURL] = React.useState("");
  const [models, setModels] = React.useState<ChatGPTModel[]>([]);
  const [modelsLoading, setModelsLoading] = React.useState(false);
  const [modelsError, setModelsError] = React.useState("");
  const [modelsVersion, setModelsVersion] = React.useState(0);
  const [model, setModel] = React.useState("");
  const [welcomeOpen, setWelcomeOpen] = React.useState(false);

  const ready =
    status?.connected === true &&
    status.sharing === true &&
    !status.pending &&
    !chatGPTError(status.last_error ?? "").reconnect;
  const activeProfile = profiles.find((profile) => profile.is_default && profile.auth_method === "chatgpt");
  const activeModel = activeProfile?.model;
  const disabled = checking || busy !== null || status === null;

  // biome-ignore lint/correctness/useExhaustiveDependencies: refreshVersion restarts this request after 재인증 오류 or explicit 다시 확인.
  React.useEffect(() => {
    if (busy !== null) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    setChecking(true);
    async function refresh() {
      try {
        const next = await api.chatGPTStatus();
        if (cancelled) return;
        setStatus(next);
        onStatusChange(next.pending || chatGPTError(next.last_error ?? "").reconnect ? null : next);
        setStatusError("");
        if (!next.pending) setAuthorizationURL("");
        if (next.pending) timer = setTimeout(() => void refresh(), 1_500);
      } catch (error) {
        if (cancelled) return;
        setStatus(null);
        onStatusChange(null);
        setStatusError(`ChatGPT 연결 상태를 확인할 수 없습니다: ${chatGPTError((error as Error).message).message}`);
      } finally {
        if (!cancelled) setChecking(false);
      }
    }
    void refresh();
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [busy, onStatusChange, refreshVersion]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: modelsVersion requests a fresh account model list on explicit refresh.
  React.useEffect(() => {
    if (!ready) {
      setModels([]);
      setModel("");
      setModelsError("");
      setModelsLoading(false);
      return;
    }
    let cancelled = false;
    setModelsLoading(true);
    setModelsError("");
    api.chatGPTModels().then(
      (next) => {
        if (cancelled) return;
        setModels(next);
        setModelsLoading(false);
      },
      (error: Error) => {
        if (cancelled) return;
        const translated = chatGPTError(error.message);
        setModels([]);
        setModelsError(`사용 가능한 모델을 가져올 수 없습니다: ${translated.message}`);
        setModelsLoading(false);
        if (translated.reconnect) setRefreshVersion((version) => version + 1);
      },
    );
    return () => {
      cancelled = true;
    };
  }, [ready, modelsVersion]);

  React.useEffect(() => {
    setModel((previous) => {
      if (models.some((item) => item.slug === previous)) return previous;
      return models.find((item) => item.slug === activeModel)?.slug ?? models[0]?.slug ?? "";
    });
  }, [models, activeModel]);

  React.useEffect(() => {
    if (!ready) return;
    try {
      if (localStorage.getItem(WELCOME_SEEN) !== "1") setWelcomeOpen(true);
    } catch {
      setWelcomeOpen(true);
    }
  }, [ready]);

  function closeWelcome() {
    setWelcomeOpen(false);
    try {
      // This UI acknowledgement contains no account information or credentials.
      localStorage.setItem(WELCOME_SEEN, "1");
    } catch {
      // An unavailable preference store does not invalidate the backend session.
    }
  }

  async function runAction(action: Action, operation: () => Promise<void>) {
    if (busy !== null || checking) return;
    setBusy(action);
    setActionError("");
    try {
      await operation();
    } catch (error) {
      setActionError(chatGPTError((error as Error).message).message);
    } finally {
      setBusy(null);
    }
  }

  async function login() {
    await runAction("login", async () => {
      try {
        const result = await api.chatGPTLogin();
        const url = chatGPTAuthorizationURL(result.authorization_url);
        if (window.artexDesktop) {
          await openChatGPTLogin(url);
        } else {
          setAuthorizationURL(url);
        }
      } catch (error) {
        // A failed browser launch must not leave an abandoned login pending.
        await api.chatGPTCancel().catch(() => undefined);
        throw error;
      }
    });
  }

  async function cancel() {
    await runAction("cancel", async () => {
      await api.chatGPTCancel();
      setAuthorizationURL("");
    });
  }

  async function logout() {
    await runAction("logout", async () => {
      await api.chatGPTLogout();
      setAuthorizationURL("");
      await onProfileActivated();
      toast.success("ChatGPT 구독 연결을 해제했습니다");
    });
  }

  async function activate() {
    if (!ready || !models.some((item) => item.slug === model)) return;
    await runAction("activate", async () => {
      await api.activateChatGPTModel(model);
      await onProfileActivated();
      toast.success("ChatGPT 구독 모델을 활성화했습니다");
    });
  }

  async function usage() {
    try {
      await openChatGPTUsage();
    } catch (error) {
      setActionError(chatGPTError((error as Error).message).message);
    }
  }

  let connectionLabel = status ? "연결 필요" : "연결 미확인";
  if (ready) connectionLabel = "연결됨";
  if (status?.pending) connectionLabel = "승인 대기 중";
  if (checking) connectionLabel = "연결 확인 중";

  return (
    <>
      <Card id="chatgpt-subscription" tabIndex={-1} className="mb-6 scroll-mt-4 outline-none">
        <CardHeader>
          <div className="flex flex-wrap items-center gap-2">
            <CardTitle>ChatGPT 구독</CardTitle>
            <Badge variant="outline">{connectionLabel}</Badge>
          </div>
          <CardDescription>
            API Key 없이 ChatGPT 계정을 연결합니다. 지원되는 구독의 사용량과 크레딧으로 모델을 사용하며, ARTEX의 별도
            구독을 구매하는 절차가 아닙니다.
          </CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4">
          {statusError && (
            <div role="alert" className="flex flex-wrap items-center gap-2 text-destructive text-sm">
              <p className="flex-1">{statusError}</p>
              <Button variant="outline" size="sm" disabled={checking} onClick={() => setRefreshVersion((v) => v + 1)}>
                <RefreshCwIcon /> 다시 확인
              </Button>
            </div>
          )}
          {(actionError || status?.last_error) && (
            <p role="alert" className="text-destructive text-sm">
              {actionError || chatGPTError(status?.last_error ?? "").message}
            </p>
          )}
          {status?.connected && (
            <div className="flex flex-wrap items-center justify-between gap-3">
              <p className="text-sm">연결 계정: {status.email || status.name || "ChatGPT 계정"}</p>
              <Button variant="outline" size="sm" disabled={disabled || status.pending} onClick={() => void logout()}>
                {busy === "logout" && <Loader2Icon className="animate-spin" />} ChatGPT 로그아웃
              </Button>
            </div>
          )}
          {status?.connected && !status.sharing && (
            <p role="alert" className="text-destructive text-sm">
              이 연결에는 ChatGPT 구독 모델 사용 권한이 없습니다. 다시 연결하여 구독 공유를 승인하세요.
            </p>
          )}
          {(!ready || status?.pending) && (
            <div className="flex flex-wrap items-center gap-3">
              <Button
                disabled={disabled || status?.pending}
                onClick={() => void login()}
                className="bg-black text-white hover:bg-black/90 dark:bg-white dark:text-black dark:hover:bg-white/90"
              >
                {/* Official local assets: developers.openai.com/assets/siwc/sign-in-buttons/chatgpt-logo-{white,black}.svg */}
                {busy === "login" ? (
                  <Loader2Icon className="animate-spin" />
                ) : (
                  <>
                    <Image src="/icons/chatgpt-logo-white.svg" width={20} height={20} alt="" className="dark:hidden" />
                    <Image
                      src="/icons/chatgpt-logo-black.svg"
                      width={20}
                      height={20}
                      alt=""
                      className="hidden dark:block"
                    />
                  </>
                )}
                Continue with ChatGPT
              </Button>
              {status?.pending && (
                <>
                  <p role="status" className="text-muted-foreground text-sm">
                    브라우저에서 ChatGPT 로그인을 완료하고 구독 사용을 승인하세요.
                  </p>
                  <Button variant="outline" size="sm" disabled={disabled} onClick={() => void cancel()}>
                    {busy === "cancel" && <Loader2Icon className="animate-spin" />} 연결 취소
                  </Button>
                </>
              )}
              {authorizationURL && status?.pending && (
                <Button variant="outline" asChild>
                  <a href={authorizationURL} target="_blank" rel="noopener noreferrer">
                    <ExternalLinkIcon /> ChatGPT 승인 페이지 열기
                  </a>
                </Button>
              )}
            </div>
          )}
          {ready && (
            <div className="grid gap-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <p className="text-muted-foreground text-sm">
                  {activeProfile ? "ChatGPT 구독 사용 중" : "모델을 선택하여 ARTEX의 활성 모델로 설정하세요"}
                </p>
                <Button variant="outline" size="sm" onClick={() => void usage()}>
                  <ExternalLinkIcon /> ChatGPT 사용량 관리
                </Button>
              </div>
              <div className="flex flex-wrap items-end gap-3">
                <div className="grid min-w-0 flex-1 gap-2">
                  <Label htmlFor="chatgpt-model">ChatGPT 구독 모델</Label>
                  <Select
                    value={model}
                    onValueChange={setModel}
                    disabled={disabled || modelsLoading || models.length === 0}
                  >
                    <SelectTrigger id="chatgpt-model" className="w-full">
                      <SelectValue placeholder={modelsLoading ? "모델 목록을 가져오는 중…" : "사용 가능한 모델 선택"} />
                    </SelectTrigger>
                    <SelectContent>
                      {models.map((item) => (
                        <SelectItem key={item.slug} value={item.slug}>
                          {item.display_name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <Button
                  disabled={disabled || modelsLoading || !model || activeModel === model}
                  onClick={() => void activate()}
                >
                  {busy === "activate" && <Loader2Icon className="animate-spin" />}
                  {activeModel === model ? "활성화됨" : "선택 모델 활성화"}
                </Button>
                <Button
                  variant="outline"
                  size="icon"
                  aria-label="ChatGPT 모델 목록 새로고침"
                  disabled={disabled || modelsLoading}
                  onClick={() => setModelsVersion((v) => v + 1)}
                >
                  <RefreshCwIcon className={modelsLoading ? "animate-spin" : ""} />
                </Button>
              </div>
              {modelsError && (
                <p role="alert" className="text-destructive text-sm">
                  {modelsError}
                </p>
              )}
              {!modelsLoading && !modelsError && models.length === 0 && (
                <p role="status" className="text-muted-foreground text-sm">
                  이 계정에서 사용할 수 있는 모델이 없습니다. ChatGPT 사용량을 확인하거나 모델 목록을 다시 가져오세요.
                </p>
              )}
              <p className="text-muted-foreground text-xs">
                API Key 방식의 설정과 별도로 연결됩니다. 연결을 해제하면 이 구독 모델을 더 이상 사용할 수 없습니다.
              </p>
            </div>
          )}
        </CardContent>
      </Card>
      <Dialog open={welcomeOpen} onOpenChange={(open) => !open && closeWelcome()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>ChatGPT 구독이 연결되었습니다</DialogTitle>
            <DialogDescription>
              이제 ARTEX에서 지원되는 ChatGPT 구독 모델을 사용할 수 있습니다. 요청은 계정의 사용량과 크레딧을 사용하며,
              ChatGPT 설정에서 사용량과 연결 권한을 관리할 수 있습니다.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => void usage()}>
              ChatGPT 사용량 관리
            </Button>
            <Button onClick={closeWelcome}>확인</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
