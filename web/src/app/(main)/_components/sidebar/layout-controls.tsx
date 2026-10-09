"use client";

import { Check, Settings } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import type { ContentLayout, NavbarStyle, SidebarCollapsible, SidebarVariant } from "@/lib/preferences/layout";
import {
  applyContentLayout,
  applyNavbarStyle,
  applySidebarCollapsible,
  applySidebarVariant,
} from "@/lib/preferences/layout-utils";
import { PREFERENCE_DEFAULTS } from "@/lib/preferences/preferences-config";
import { persistPreference } from "@/lib/preferences/preferences-storage";
import { THEME_PRESET_OPTIONS, type ThemeMode, type ThemePreset } from "@/lib/preferences/theme";
import { applyThemePreset } from "@/lib/preferences/theme-utils";
import { usePreferencesStore } from "@/stores/preferences/preferences-provider";

export function LayoutControls() {
  const themeMode = usePreferencesStore((s) => s.themeMode);
  const resolvedThemeMode = usePreferencesStore((s) => s.resolvedThemeMode);
  const setThemeMode = usePreferencesStore((s) => s.setThemeMode);
  const themePreset = usePreferencesStore((s) => s.themePreset);
  const setThemePreset = usePreferencesStore((s) => s.setThemePreset);
  const contentLayout = usePreferencesStore((s) => s.contentLayout);
  const setContentLayout = usePreferencesStore((s) => s.setContentLayout);
  const navbarStyle = usePreferencesStore((s) => s.navbarStyle);
  const setNavbarStyle = usePreferencesStore((s) => s.setNavbarStyle);
  const variant = usePreferencesStore((s) => s.sidebarVariant);
  const setSidebarVariant = usePreferencesStore((s) => s.setSidebarVariant);
  const collapsible = usePreferencesStore((s) => s.sidebarCollapsible);
  const setSidebarCollapsible = usePreferencesStore((s) => s.setSidebarCollapsible);

  const onThemePresetChange = (preset: ThemePreset | "") => {
    if (!preset) return;
    applyThemePreset(preset);
    setThemePreset(preset);
    void persistPreference("theme_preset", preset);
  };

  const onThemeModeChange = (mode: ThemeMode | "") => {
    if (!mode) return;
    setThemeMode(mode);
    void persistPreference("theme_mode", mode);
  };

  const onContentLayoutChange = (layout: ContentLayout | "") => {
    if (!layout) return;
    applyContentLayout(layout);
    setContentLayout(layout);
    void persistPreference("content_layout", layout);
  };

  const onNavbarStyleChange = (style: NavbarStyle | "") => {
    if (!style) return;
    applyNavbarStyle(style);
    setNavbarStyle(style);
    void persistPreference("navbar_style", style);
  };

  const onSidebarStyleChange = (value: SidebarVariant | "") => {
    if (!value) return;
    setSidebarVariant(value);
    applySidebarVariant(value);
    void persistPreference("sidebar_variant", value);
  };

  const onSidebarCollapseModeChange = (value: SidebarCollapsible | "") => {
    if (!value) return;
    setSidebarCollapsible(value);
    applySidebarCollapsible(value);
    void persistPreference("sidebar_collapsible", value);
  };

  const handleRestore = () => {
    onThemePresetChange(PREFERENCE_DEFAULTS.theme_preset);
    onThemeModeChange(PREFERENCE_DEFAULTS.theme_mode);
    onContentLayoutChange(PREFERENCE_DEFAULTS.content_layout);
    onNavbarStyleChange(PREFERENCE_DEFAULTS.navbar_style);
    onSidebarStyleChange(PREFERENCE_DEFAULTS.sidebar_variant);
    onSidebarCollapseModeChange(PREFERENCE_DEFAULTS.sidebar_collapsible);
  };

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button size="icon" aria-label="화면 설정">
          <Settings />
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="end"
        aria-label="화면 설정"
        className="max-h-(--radix-popover-content-available-height) w-112 max-w-[calc(100vw-2rem)] overflow-y-auto p-4"
      >
        <div className="flex flex-col gap-5">
          <div className="space-y-1.5">
            <h4 className="font-medium text-sm leading-none">화면 설정</h4>
            <p className="text-muted-foreground text-xs">테마와 화면 배치를 설정합니다.</p>
          </div>
          <Tabs defaultValue="theme" className="flex-col gap-4">
            <TabsList aria-label="화면 설정 항목" className="h-9 w-full">
              <TabsTrigger
                value="theme"
                className="data-[state=active]:border-border data-[state=active]:bg-background data-[state=active]:text-foreground"
              >
                테마
              </TabsTrigger>
              <TabsTrigger
                value="layout"
                className="data-[state=active]:border-border data-[state=active]:bg-background data-[state=active]:text-foreground"
              >
                화면 배치
              </TabsTrigger>
            </TabsList>
            <TabsContent value="theme" className="space-y-4">
              <div className="space-y-1">
                <Label className="font-medium text-xs">화면 모드</Label>
                <ToggleGroup
                  size="sm"
                  spacing={0}
                  variant="outline"
                  type="single"
                  aria-label="화면 모드"
                  className="w-full *:flex-1 *:text-xs"
                  value={themeMode}
                  onValueChange={onThemeModeChange}
                >
                  <ToggleGroupItem value="light" aria-label="라이트 모드">
                    라이트
                  </ToggleGroupItem>
                  <ToggleGroupItem value="dark" aria-label="다크 모드">
                    다크
                  </ToggleGroupItem>
                  <ToggleGroupItem value="system" aria-label="시스템 설정 사용">
                    시스템
                  </ToggleGroupItem>
                </ToggleGroup>
              </div>
              <div className="space-y-2">
                <div className="flex items-center justify-between gap-2">
                  <Label id="theme-preset-label" className="font-medium text-xs">
                    테마 프리셋
                  </Label>
                  <span className="text-muted-foreground text-xs">19가지 색상</span>
                </div>
                <ToggleGroup
                  type="single"
                  variant="outline"
                  value={themePreset}
                  onValueChange={onThemePresetChange}
                  aria-labelledby="theme-preset-label"
                  className="grid w-full grid-cols-3 gap-2 sm:grid-cols-4"
                >
                  {THEME_PRESET_OPTIONS.map((preset) => (
                    <ToggleGroupItem
                      key={preset.value}
                      value={preset.value}
                      aria-label={preset.label}
                      className="h-14 min-w-0 flex-col items-stretch gap-1.5 px-2 py-2 text-xs data-[state=on]:bg-accent data-[state=on]:ring-1 data-[state=on]:ring-border"
                    >
                      <span
                        aria-hidden="true"
                        className="h-3 w-full rounded-xs border border-border"
                        style={{
                          backgroundColor: resolvedThemeMode === "dark" ? preset.primary.dark : preset.primary.light,
                        }}
                      />
                      <span className="flex items-center justify-between gap-1">
                        <span>{preset.label}</span>
                        <Check
                          aria-hidden="true"
                          className={`size-3 ${themePreset === preset.value ? "opacity-100" : "opacity-0"}`}
                        />
                      </span>
                    </ToggleGroupItem>
                  ))}
                </ToggleGroup>
              </div>
            </TabsContent>
            <TabsContent
              value="layout"
              className="space-y-3 **:data-[slot=toggle-group]:w-full **:data-[slot=toggle-group-item]:flex-1 **:data-[slot=toggle-group-item]:text-xs"
            >
              <div className="space-y-1">
                <Label className="font-medium text-xs">페이지 너비</Label>
                <ToggleGroup
                  size="sm"
                  spacing={0}
                  variant="outline"
                  type="single"
                  value={contentLayout}
                  onValueChange={onContentLayoutChange}
                >
                  <ToggleGroupItem value="centered" aria-label="가운데 정렬">
                    가운데 정렬
                  </ToggleGroupItem>
                  <ToggleGroupItem value="full-width" aria-label="전체 너비">
                    전체 너비
                  </ToggleGroupItem>
                </ToggleGroup>
              </div>

              <div className="space-y-1">
                <Label className="font-medium text-xs">상단 메뉴 동작</Label>
                <ToggleGroup
                  size="sm"
                  spacing={0}
                  variant="outline"
                  type="single"
                  value={navbarStyle}
                  onValueChange={onNavbarStyleChange}
                >
                  <ToggleGroupItem value="sticky" aria-label="상단 고정">
                    상단 고정
                  </ToggleGroupItem>
                  <ToggleGroupItem value="scroll" aria-label="함께 스크롤">
                    함께 스크롤
                  </ToggleGroupItem>
                </ToggleGroup>
              </div>

              <div className="space-y-1">
                <Label className="font-medium text-xs">사이드바 형태</Label>
                <ToggleGroup
                  size="sm"
                  spacing={0}
                  variant="outline"
                  type="single"
                  value={variant}
                  onValueChange={onSidebarStyleChange}
                >
                  <ToggleGroupItem value="inset" aria-label="안쪽 배치">
                    안쪽 배치
                  </ToggleGroupItem>
                  <ToggleGroupItem value="sidebar" aria-label="기본 사이드바">
                    기본
                  </ToggleGroupItem>
                  <ToggleGroupItem value="floating" aria-label="떠 있는 사이드바">
                    떠 있는 형태
                  </ToggleGroupItem>
                </ToggleGroup>
              </div>

              <div className="space-y-1">
                <Label className="font-medium text-xs">사이드바 접기</Label>
                <ToggleGroup
                  size="sm"
                  spacing={0}
                  variant="outline"
                  type="single"
                  value={collapsible}
                  onValueChange={onSidebarCollapseModeChange}
                >
                  <ToggleGroupItem value="icon" aria-label="아이콘만 표시">
                    아이콘만
                  </ToggleGroupItem>
                  <ToggleGroupItem value="offcanvas" aria-label="완전히 숨기기">
                    완전히 숨기기
                  </ToggleGroupItem>
                </ToggleGroup>
              </div>
            </TabsContent>
          </Tabs>
          <Button type="button" size="sm" variant="outline" className="w-full text-xs" onClick={handleRestore}>
            기본값 복원
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}
