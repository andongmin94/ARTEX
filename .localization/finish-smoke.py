from pathlib import Path
import re

p = Path('web/src/lib/mock/handler.ts')
text = p.read_text(encoding='utf-8')
marker = '  // Notification demo follows the real metadata shape; it never sends messages.\n'
if marker not in text:
    text = text.replace('  IntentAsset,\n', '  IntentAsset,\n  NotificationMeta,\n', 1)
    anchor = '  const task = q.get("task") ?? undefined;\n'
    assert anchor in text
    addition = '''
  // Notification demo follows the real metadata shape; it never sends messages.
  if (path === "/notify/meta" && m === "GET") {
    return {
      kinds: [
        { kind: "dingtalk", default_rate_per_min: 20, secret_keys: ["webhook", "secret"] },
        { kind: "email", default_rate_per_min: 60, secret_keys: ["password"] },
        { kind: "feishu", default_rate_per_min: 100, secret_keys: ["webhook", "secret"] },
        { kind: "telegram", default_rate_per_min: 20, secret_keys: ["bot_token"] },
        { kind: "webhook", default_rate_per_min: 0, secret_keys: ["url", "headers"] },
        { kind: "wecom", default_rate_per_min: 20, secret_keys: ["webhook"] },
      ],
      enabled: false,
      public_base_url: "",
      digest_interval_min: "30",
      defaults: { digest_interval_min: 30 },
      stats: { channels: 0, channels_on: 0, pending: 0, failed: 0, sent_today: 0, backlog_age_ms: 0 },
    } satisfies NotificationMeta;
  }
  if (path === "/notify/channels" && m === "GET") return { channels: [] };
  if (path === "/notify/deliveries" && m === "GET") {
    return { deliveries: [], total: 0, page: Number(q.get("page")) || 1, page_size: Number(q.get("page_size")) || 50 };
  }
  if (path.startsWith("/notify/")) {
    throw new ApiError(501, "데모 모드에서는 알림을 저장하거나 실제 메시지를 전송하지 않습니다.");
  }
'''
    text = text.replace(anchor, anchor + addition, 1)
    p.write_text(text, encoding='utf-8')

# These template controls are reachable from every Korean page. Translate exact
# display strings only; preference keys, enum values and font names stay stable.
def display(name, words):
    p = Path(name)
    text = p.read_text(encoding='utf-8')
    for old, new in sorted(words.items(), key=lambda pair: -len(pair[0])):
        text = re.sub(r'(?<=>)(\s*)' + re.escape(old) + r'(\s*)(?=<)', lambda m: m[1] + new + m[2], text)
        text = re.sub(r'(?m)^([ \t]*)' + re.escape(old) + r'([ \t]*)$', lambda m: m[1] + new + m[2], text)
        # All keys passed here are prose, not machine configuration values.
        text = text.replace('"' + old + '"', '"' + new + '"')
    p.write_text(text, encoding='utf-8')

display('web/src/app/(main)/_components/sidebar/search-dialog.tsx', {
    'Search': '검색', 'Other': '기타',
    'Search dashboards, users, and more…': '메뉴 이름으로 검색…',
    'No results found.': '검색 결과가 없습니다.',
})
display('web/src/app/(main)/_components/sidebar/layout-controls.tsx', {
    'Preferences': '화면 설정', 'Customize your dashboard layout preferences.': '테마와 화면 배치를 설정합니다.',
    'Theme Preset': '테마 프리셋', 'Preset': '프리셋', 'Fonts': '글꼴', 'Select font': '글꼴 선택',
    'Theme Mode': '화면 모드', 'Light': '라이트', 'Dark': '다크', 'System': '시스템',
    'Page Layout': '페이지 너비', 'Centered': '가운데 정렬', 'Full Width': '전체 너비',
    'Navbar Behavior': '상단 메뉴 동작', 'Sticky': '상단 고정', 'Scroll': '함께 스크롤',
    'Sidebar Style': '사이드바 형태', 'Inset': '안쪽 배치', 'Sidebar': '기본', 'Floating': '떠 있는 형태',
    'Sidebar Collapse Mode': '사이드바 접기', 'Icon': '아이콘만', 'OffCanvas': '완전히 숨기기',
    'Restore Defaults': '기본값 복원',
    'Toggle light': '라이트 모드', 'Toggle dark': '다크 모드', 'Toggle system': '시스템 설정 사용',
    'Toggle centered': '가운데 정렬', 'Toggle full-width': '전체 너비',
    'Toggle sticky': '상단 고정', 'Toggle scroll': '함께 스크롤',
    'Toggle inset': '안쪽 배치', 'Toggle sidebar': '기본 사이드바', 'Toggle floating': '떠 있는 사이드바',
    'Toggle icon': '아이콘만 표시', 'Toggle offcanvas': '완전히 숨기기',
})
p = Path('web/src/app/(main)/_components/sidebar/layout-controls.tsx')
text = p.read_text(encoding='utf-8').replace('<Button size="icon">', '<Button size="icon" aria-label="화면 설정">', 1)
p.write_text(text, encoding='utf-8')
display('web/src/components/ui/command.tsx', {
    'Command Palette': '메뉴 검색', 'Search for a command to run...': '이동할 메뉴를 검색하세요.',
})
p = Path('web/src/app/(main)/_components/sidebar/theme-switcher.tsx')
text = p.read_text(encoding='utf-8').replace(
    '`Current theme: ${themeMode}. Click to cycle themes`',
    '`현재 테마: ${{ light: "라이트", dark: "다크", system: "시스템" }[themeMode]}. 클릭하여 테마 전환`')
p.write_text(text, encoding='utf-8')
print('Notification mock contract and shared Korean controls updated')
