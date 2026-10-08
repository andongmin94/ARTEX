export const THEME_MODE_OPTIONS = [
  { label: "라이트", value: "light" },
  { label: "다크", value: "dark" },
  { label: "시스템", value: "system" },
] as const;
export const THEME_MODE_VALUES = THEME_MODE_OPTIONS.map((option) => option.value);
export type ThemeMode = (typeof THEME_MODE_VALUES)[number];
export type ResolvedThemeMode = "light" | "dark";

export const THEME_PRESET_OPTIONS = [{ label: "Mono", value: "mono", primary: { light: "#27282b", dark: "#e4e7ec" } }] as const;
export const THEME_PRESET_VALUES = THEME_PRESET_OPTIONS.map((preset) => preset.value);
export type ThemePreset = (typeof THEME_PRESET_OPTIONS)[number]["value"];
