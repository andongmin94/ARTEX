"use client";

import { useEffect, useRef, useState } from "react";

import { useRouter } from "next/navigation";

import { AlertTriangle, ShieldCheck } from "lucide-react";

import { AuthSessionState } from "@/components/auth-session-state";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogClose, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { auth } from "@/lib/auth";
import { useAuthSession } from "@/lib/auth-session";

export default function LoginPage() {
  const router = useRouter();
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const { session, error: sessionError, retry } = useAuthSession();
  const [agreed, setAgreed] = useState(false);
  const [termsOpen, setTermsOpen] = useState(false);
  const [readToEnd, setReadToEnd] = useState(false);
  const termsBodyRef = useRef<HTMLDivElement>(null);

  // 滚动到条款底部（含无需滚动即可完整展示的情况）方可点击「同意」。
  function handleTermsScroll() {
    const el = termsBodyRef.current;
    if (!el) return;
    if (el.scrollTop + el.clientHeight >= el.scrollHeight - 8) setReadToEnd(true);
  }

  useEffect(() => {
    if (!termsOpen) return;
    // 打开时重置，并处理内容本就不足一屏、无法触发滚动的场景。
    setReadToEnd(false);
    const el = termsBodyRef.current;
    if (el && el.scrollHeight <= el.clientHeight + 8) setReadToEnd(true);
  }, [termsOpen]);

  useEffect(() => {
    if (session?.authenticated) {
      const token = auth.getToken();
      if (token) auth.setToken(token);
      window.location.replace("/function/tasks");
    } else if (session && !session.initialized) {
      router.replace("/setup");
    }
  }, [router, session]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!agreed) {
      setError("먼저 이용 안내를 읽고 동의해 주세요");
      return;
    }
    setLoading(true);
    setError("");
    try {
      const { token } = await api.login("ARTEX", password);
      auth.setToken(token);
      window.location.replace("/function/tasks");
    } catch {
      setError("사용자 이름 또는 비밀번호가 올바르지 않습니다");
    } finally {
      setLoading(false);
    }
  }

  if (!session || session.authenticated || !session.initialized)
    return <AuthSessionState error={sessionError} onRetry={retry} />;

  return (
    <div className="flex h-dvh">
      {/* Left panel */}
      <div className="hidden flex-col items-center justify-center bg-primary p-12 text-center lg:flex lg:w-1/3">
        <div className="relative flex items-center justify-center">
          <div className="absolute size-80 rounded-full border border-primary-foreground/10" />
          <div className="absolute size-60 rounded-full border border-primary-foreground/15" />
          <div className="absolute size-40 rounded-full border border-primary-foreground/20" />
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src="/logo.png" alt="ARTEX" width={160} height={160} className="relative brightness-0 invert" />
        </div>
      </div>

      {/* Right panel */}
      <div className="flex w-full items-center justify-center bg-background p-8 lg:w-2/3">
        <div className="w-full max-w-md space-y-10 py-24 lg:py-32">
          <div className="space-y-4 text-center">
            <h2 className="text-2xl font-medium tracking-tight">로그인</h2>
            <p className="mx-auto max-w-xl text-muted-foreground">다시 오신 것을 환영합니다. ARTEX를 계속 사용하려면 비밀번호를 입력하세요.</p>
          </div>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <div className="space-y-1.5">
              <Label htmlFor="username">사용자 이름</Label>
              <Input id="username" value="ARTEX" readOnly className="bg-muted text-muted-foreground" />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="password">비밀번호</Label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="비밀번호를 입력하세요"
                autoFocus
                autoComplete="current-password"
              />
            </div>
            <div className="flex items-start gap-2">
              <Checkbox
                id="agree-terms"
                checked={agreed}
                onCheckedChange={(v) => setAgreed(v === true)}
                className="mt-0.5"
              />
              <Label htmlFor="agree-terms" className="text-sm font-normal leading-relaxed text-muted-foreground">
                읽었으며 동의합니다
                <button
                  type="button"
                  onClick={() => setTermsOpen(true)}
                  className="mx-0.5 font-medium text-primary underline-offset-4 hover:underline"
                >
                  이용 안내
                </button>
              </Label>
            </div>
            {error && <p className="text-sm text-destructive">{error}</p>}
            <Button type="submit" className="w-full" disabled={loading || !password || !agreed}>
              {loading ? "로그인 중..." : "로그인"}
            </Button>
          </form>
        </div>
      </div>

      <Dialog open={termsOpen} onOpenChange={setTermsOpen}>
        <DialogContent className="gap-0 p-0 sm:max-w-2xl">
          <DialogHeader className="flex-row items-center gap-3 border-b px-6 py-4">
            <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
              <ShieldCheck className="size-5" />
            </div>
            <div className="space-y-0.5">
              <DialogTitle className="text-base">ARTEX 이용 안내 및 면책 조항</DialogTitle>
              <p className="text-xs text-muted-foreground">
                버전 v1.0 · 시행일 2026-09-18 · 로그인하기 전에 아래 조항을 모두 읽어 주세요
              </p>
            </div>
          </DialogHeader>

          <div
            ref={termsBodyRef}
            onScroll={handleTermsScroll}
            className="max-h-[60vh] space-y-5 overflow-y-auto px-6 py-5 text-sm leading-relaxed text-muted-foreground"
          >
            <p className="rounded-lg border bg-muted/40 p-3 text-foreground/80">
              본 「이용 안내 및 면책 조항」(이하 "본 안내")은 사용자와 ARTEX
              프로젝트 작성자 및 기여자 사이의 소프트웨어 이용 조건을 규정합니다. 사용 전에 각 조항, 특히 굵은 글씨나 강조 영역으로 표시된 면책·책임 제한·금지 조항을 충분히 읽고 이해해 주세요.
              <span className="font-medium text-foreground">
                {" "}
                본 소프트웨어를 다운로드, 설치, 접속하거나 어떠한 방식으로든 사용하는 경우 본 안내를 읽고 이해했으며 모든 조건에 동의한 것으로 간주됩니다.
              </span>
            </p>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  1
                </span>
                제1조 · 정의 및 오픈소스 라이선스
              </h4>
              <p className="pl-7">
                본 소프트웨어(ARTEX)는 GNU Affero General Public License
                v3.0(AGPL-3.0)으로 배포되는 오픈소스 프로그램입니다. 해당 라이선스에 따라 자유롭게 사용, 복제, 수정 및 배포할 수 있습니다. 단, 파생 저작물(네트워크를 통해 제3자에게 제공하는 온라인 서비스를 포함) 역시
                AGPL-3.0으로 공개하고 이용자에게 해당 전체 소스 코드를 제공해야 합니다. AGPL-3.0의 전체 조건은 동봉된 LICENSE 파일을 따릅니다.
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  2
                </span>
                제2조 · 허용된 사용 범위
              </h4>
              <p className="pl-7">
                본 소프트웨어는 개인 학습, 코드 연구, 보안 기술 원리 연구 및 사용자가 직접 구축한 로컬 격리 환경에서의 기술 검증을 위해서만 사용할 수 있으며, 학습·학술 연구·코드 검토 등 비공격적이고 비파괴적인 용도에 적용됩니다. 본 조에서 명시적으로 허용한 경우를 제외하고 다른 목적으로 사용할 수 없습니다.
              </p>
            </section>

            <section className="space-y-2">
              <h4 className="flex items-center gap-2 font-medium text-destructive">
                <span className="flex size-5 items-center justify-center rounded-md bg-destructive/10 text-xs font-semibold text-destructive">
                  3
                </span>
                <AlertTriangle className="size-4" />
                제3조 · 금지 행위
              </h4>
              <ul className="ml-7 list-decimal space-y-1.5 rounded-lg border border-destructive/20 bg-destructive/5 p-3 pl-8 text-foreground/80 marker:text-destructive/70">
                <li>
                  어떠한 웹사이트, 온라인 서비스, 타인 또는 제3자가 소유한 네트워크 시스템에 대해서도 스캔, 탐지, 취약점 이용 또는 공격을 수행해서는 안 됩니다(승인 여부나 본인 소유 자산 여부와 관계없음).
                </li>
                <li>본 소프트웨어를 실제 침투 테스트, 공격·방어 훈련, 레드팀·블루팀 훈련 또는 운영 환경에 사용해서는 안 됩니다.</li>
                <li>불법 침입, 데이터 탈취, 랜섬·갈취, 서비스 거부(DoS/DDoS) 또는 기타 파괴적·범죄적 활동에 사용해서는 안 됩니다.</li>
                <li>본 소프트웨어 및 출력물의 저작권, 라이선스 또는 보안 안내를 제거, 변조하거나 우회해서는 안 됩니다.</li>
                <li>사용자가 속한 국가 또는 지역의 법률, 규정 및 감독 규정을 위반하는 행위를 해서는 안 됩니다.</li>
              </ul>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  4
                </span>
                제4조 · 지식재산권
              </h4>
              <p className="pl-7">
                본 소프트웨어의 저작권 및 관련 지식재산권은 프로젝트 작성자와 기여자에게 있으며, AGPL-3.0
                라이선스가 정한 범위에서 사용자에게 권리가 부여됩니다. 해당 라이선스가 명시적으로 부여한 권리를 제외하고 본 안내는 어떠한 추가 권리도 명시적 또는 묵시적으로 부여하지 않습니다.
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  5
                </span>
                제5조 · 데이터 및 개인정보
              </h4>
              <p className="pl-7">
                본 소프트웨어는 자체 배포가 가능한 오픈소스 프로그램이며, 작성자는 중앙 집중형 서비스를 운영하거나 사용 데이터를 수집·업로드하지 않습니다. 사용 과정에서 생성, 처리 또는 접촉하는 모든 데이터는 사용자가 직접 관리하며 적법성과 보안에 대한 책임도 사용자에게 있습니다. 부적절한 데이터 처리로 인한 결과 역시 사용자가 부담합니다.
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  6
                </span>
                제6조 · 규정 준수 및 법적 책임
              </h4>
              <p className="pl-7">
                사용자는 소재 국가 또는 지역의 사이버 보안, 데이터 보안, 개인정보 보호 및 컴퓨터 범죄와 관련된 모든 법률과 규정을 준수해야 합니다.
                <span className="font-medium text-foreground">
                  {" "}
                  위 법률·규정 또는 본 안내를 위반하여 발생하는 모든 법적 책임과 결과는 사용자 본인이 부담하며, 본 소프트웨어의 작성자 및 기여자와는 무관합니다.
                </span>
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  7
                </span>
                제7조 · 면책 및 책임 제한
              </h4>
              <p className="pl-7">
                본 소프트웨어는 "있는 그대로(AS IS)" 및 "제공 가능한 상태(AS
                AVAILABLE)"로 제공되며, 상품성, 특정 목적 적합성, 정확성 및 비침해성을 포함한 어떠한 명시적·묵시적 보증도 제공하지 않습니다. 관련 법률이 허용하는 최대 범위에서 작성자 및 기여자는 본 소프트웨어의 사용 또는 사용 불가로 인한 직접적·간접적·우발적·특수·결과적 손해에 대해 책임지지 않습니다. 여기에는 데이터 손실, 시스템 손상, 업무 중단, 이익 손실 또는 법적 분쟁 등이 포함됩니다.
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  8
                </span>
                제8조 · 조건 변경 및 해석
              </h4>
              <p className="pl-7">
                작성자는 법률·규정 또는 프로젝트의 발전에 따라 본 안내를 수시로 변경할 수 있으며, 변경된 버전은 프로젝트와 함께 공개되고 공개일로부터 적용됩니다. 계속 사용하는 경우 변경된 조건에 동의한 것으로 간주됩니다. 관련 법률이 허용하는 범위에서 본 안내의 최종 해석은 프로젝트 작성자에게 있으며, 일부 조항이 무효로 판단되더라도 나머지 조항의 효력에는 영향을 주지 않습니다.
              </p>
            </section>
          </div>

          <DialogFooter className="mx-0 mb-0 flex-col items-stretch gap-2 rounded-b-xl px-6 sm:flex-row sm:items-center sm:justify-between">
            <p className="text-xs text-muted-foreground">
              {readToEnd ? "모든 조항을 확인했습니다" : "조항을 끝까지 스크롤한 뒤 확인해 주세요"}
            </p>
            <DialogClose asChild>
              <Button
                type="button"
                disabled={!readToEnd}
                onClick={() => {
                  setAgreed(true);
                  setError("");
                }}
              >
                모든 조항을 읽었으며 동의합니다
              </Button>
            </DialogClose>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
