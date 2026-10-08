interface ArtexDesktop {
  status: () => Promise<{ state: string; url?: string; pid?: number }>;
  retry: () => Promise<void>;
  quit: () => Promise<void>;
  openChatGPTLogin: (url: string) => Promise<void>;
  openChatGPTUsage: () => Promise<void>;
}

declare global {
  interface Window {
    artexDesktop?: ArtexDesktop;
  }
}

export const CHATGPT_USAGE_URL = "https://chatgpt.com/#settings/Usage";

export function chatGPTAuthorizationURL(value: string) {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    throw new Error("허용되지 않은 ChatGPT 인증 주소입니다");
  }
  const prohibited = [...url.searchParams.keys()].some((key) =>
    ["access_token", "refresh_token", "id_token", "id_token_hint"].includes(key.toLowerCase()),
  );
  if (
    url.origin !== "https://auth.openai.com" ||
    url.pathname !== "/api/accounts/authorize" ||
    url.username ||
    url.password ||
    url.hash ||
    prohibited
  ) {
    throw new Error("허용되지 않은 ChatGPT 인증 주소입니다");
  }
  return url.href;
}

export async function openChatGPTLogin(url: string) {
  if (!window.artexDesktop?.openChatGPTLogin) {
    throw new Error("이 ARTEX 앱에서는 ChatGPT 구독 연결을 열 수 없습니다. 최신 앱으로 실행하세요");
  }
  await window.artexDesktop.openChatGPTLogin(chatGPTAuthorizationURL(url));
}

export async function openChatGPTUsage() {
  if (window.artexDesktop) {
    if (!window.artexDesktop.openChatGPTUsage) {
      throw new Error("이 ARTEX 앱에서는 ChatGPT 사용량 화면을 열 수 없습니다. 최신 앱으로 실행하세요");
    }
    await window.artexDesktop.openChatGPTUsage();
    return;
  }
  window.open(CHATGPT_USAGE_URL, "_blank", "noopener,noreferrer");
}
