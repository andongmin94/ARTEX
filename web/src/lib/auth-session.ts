"use client";

import { useEffect, useState } from "react";

import { useAuthStore } from "@/stores/auth-store";

import { api } from "./api";
import { auth } from "./auth";
import type { AuthStatus } from "./types";

interface AuthSession extends AuthStatus {
  authenticated: boolean;
}

let pendingSession: Promise<AuthSession> | null = null;

async function establishSession(): Promise<AuthSession> {
  const status = await api.authStatus();
  if (status.mode !== "desktop" && status.mode !== "standalone") throw new Error("실행 모드를 확인할 수 없습니다");
  useAuthStore.setState({ mode: status.mode });
  if (status.mode === "desktop") {
    const { token } = await api.desktopSession();
    if (!token) throw new Error("앱 인증 토큰을 발급받지 못했습니다");
    auth.setToken(token);
  }
  return { ...status, authenticated: Boolean(auth.getToken()) };
}

function bootstrapSession(): Promise<AuthSession> {
  pendingSession ??= establishSession().finally(() => {
    pendingSession = null;
  });
  return pendingSession;
}

export function useAuthSession() {
  const [session, setSession] = useState<AuthSession | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let active = true;
    if (attempt > 0) {
      setSession(null);
      setError(null);
    }
    void bootstrapSession()
      .then((value) => {
        if (active) setSession(value);
      })
      .catch((reason: unknown) => {
        if (active) setError(reason instanceof Error ? reason.message : "백엔드 서비스에 연결할 수 없습니다");
      });
    return () => {
      active = false;
    };
  }, [attempt]);

  return { session, error, retry: () => setAttempt((value) => value + 1) };
}
