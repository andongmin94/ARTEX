import localFont from "next/font/local";

// 한글 글리프를 앱에 포함해 OS 글꼴이나 원격 Google Fonts에 의존하지 않는다.
const notoSansKR = localFont({ src: "./files/NotoSansKR-Variable.ttf", variable: "--font-noto-sans-kr", weight: "100 900", display: "swap" });
const geistMono = localFont({ src: "../../../node_modules/geist/dist/fonts/geist-mono/GeistMono-Variable.woff2", variable: "--font-geist-mono", weight: "100 900", display: "swap" });

export const fontRegistry = {
  notoSansKR: { label: "Noto Sans KR", font: notoSansKR },
  geistMono: { label: "Geist Mono + Noto Sans KR", font: geistMono },
} as const;

export type FontKey = keyof typeof fontRegistry;
export const fontVars = Object.values(fontRegistry).map((entry) => entry.font.variable).join(" ");
export const fontOptions = Object.entries(fontRegistry).map(([key, entry]) => ({ key: key as FontKey, label: entry.label, variable: entry.font.variable }));
