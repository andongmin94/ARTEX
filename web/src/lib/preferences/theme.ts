export const THEME_MODE_OPTIONS = [
  { label: "라이트", value: "light" },
  { label: "다크", value: "dark" },
  { label: "시스템", value: "system" },
] as const;
export const THEME_MODE_VALUES = THEME_MODE_OPTIONS.map((option) => option.value);
export type ThemeMode = (typeof THEME_MODE_VALUES)[number];
export type ResolvedThemeMode = "light" | "dark";

// neobrutal-ui registry/src/data/colors.ts, b4da2463fe710a77bf464c65125a1a7f40424722 (MIT).
export const THEME_PRESET_OPTIONS = [
  { label: "Mono", value: "mono", primary: { light: "#27282b", dark: "#e4e7ec" } },
  { label: "Mono Warm", value: "mono-warm", primary: { light: "#292b29", dark: "#e5e2d9" } },
  { label: "Red", value: "red", primary: { light: "oklch(67.28% 0.2147 24.22)", dark: "oklch(70.49% 0.1869 22.23)" } },
  {
    label: "Orange",
    value: "orange",
    primary: { light: "oklch(72.27% 0.1894 50.19)", dark: "oklch(67.56% 0.1796 49.61)" },
  },
  {
    label: "Amber",
    value: "amber",
    primary: { light: "oklch(84.08% 0.1725 84.2)", dark: "oklch(77.7% 0.1593880864006951 84.38427202675717)" },
  },
  {
    label: "Yellow",
    value: "yellow",
    primary: { light: "oklch(86.03% 0.176 92.36)", dark: "oklch(79.36% 0.1624 92.49)" },
  },
  {
    label: "Lime",
    value: "lime",
    primary: { light: "oklch(83.29% 0.2331 132.51)", dark: "oklch(76.26% 0.21309 132.4002)" },
  },
  {
    label: "Green",
    value: "green",
    primary: { light: "oklch(79.76% 0.2044 153.08)", dark: "oklch(73.03% 0.1865 153.23)" },
  },
  {
    label: "Emerald",
    value: "emerald",
    primary: { light: "oklch(77.54% 0.1681 162.78)", dark: "oklch(70.54% 0.1525 162.97)" },
  },
  {
    label: "Teal",
    value: "teal",
    primary: { light: "oklch(78.57% 0.1422 180.36)", dark: "oklch(71.47% 0.129261 180.4742)" },
  },
  {
    label: "Cyan",
    value: "cyan",
    primary: { light: "oklch(76.89% 0.139164 219.13)", dark: "oklch(64.37% 0.1162 218.75)" },
  },
  {
    label: "Sky",
    value: "sky",
    primary: { light: "oklch(66.9% 0.18368 248.8066)", dark: "oklch(61.9% 0.16907 248.5982)" },
  },
  {
    label: "Blue",
    value: "blue",
    primary: { light: "oklch(67.47% 0.1726 259.49)", dark: "oklch(67.47% 0.1726 259.49)" },
  },
  {
    label: "Indigo",
    value: "indigo",
    primary: { light: "oklch(66.34% 0.1806 277.2)", dark: "oklch(66.34% 0.1806 277.2)" },
  },
  {
    label: "Violet",
    value: "violet",
    primary: { light: "oklch(70.28% 0.1753 295.36)", dark: "oklch(70.28% 0.1753 295.36)" },
  },
  {
    label: "Purple",
    value: "purple",
    primary: { light: "oklch(71.9% 0.198 310.03)", dark: "oklch(67.34% 0.2314 309.13)" },
  },
  {
    label: "Fuchsia",
    value: "fuchsia",
    primary: { light: "oklch(73.43% 0.2332 321.41)", dark: "oklch(60.62% 0.291458 319.6391)" },
  },
  {
    label: "Pink",
    value: "pink",
    primary: { light: "oklch(71.5% 0.197 354.23)", dark: "oklch(65.98% 0.2407 358.64)" },
  },
  {
    label: "Rose",
    value: "rose",
    primary: { light: "oklch(70.79% 0.1862 16.25)", dark: "oklch(67.58% 0.2135 18.63)" },
  },
] as const;
export const THEME_PRESET_VALUES = THEME_PRESET_OPTIONS.map((preset) => preset.value);
export type ThemePreset = (typeof THEME_PRESET_OPTIONS)[number]["value"];
