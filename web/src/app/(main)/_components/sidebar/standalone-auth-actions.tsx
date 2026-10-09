"use client";

import { useState } from "react";

import { KeyRound, LogOut } from "lucide-react";

import { SidebarFooter, SidebarMenu, SidebarMenuButton, SidebarMenuItem } from "@/components/ui/sidebar";
import { auth } from "@/lib/auth";
import { useAuthStore } from "@/stores/auth-store";

import { ChangePasswordDialog } from "./change-password-dialog";

export function StandaloneAuthActions() {
  const mode = useAuthStore((state) => state.mode);
  const [passwordOpen, setPasswordOpen] = useState(false);

  if (mode !== "standalone") return null;

  function logout() {
    auth.clearToken();
    window.location.href = "/login";
  }

  return (
    <SidebarFooter>
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton type="button" tooltip="비밀번호 변경" onClick={() => setPasswordOpen(true)}>
            <KeyRound aria-hidden="true" />
            <span>비밀번호 변경</span>
          </SidebarMenuButton>
        </SidebarMenuItem>
        <SidebarMenuItem>
          <SidebarMenuButton type="button" tooltip="로그아웃" onClick={logout}>
            <LogOut aria-hidden="true" />
            <span>로그아웃</span>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
      <ChangePasswordDialog open={passwordOpen} onOpenChange={setPasswordOpen} />
    </SidebarFooter>
  );
}
