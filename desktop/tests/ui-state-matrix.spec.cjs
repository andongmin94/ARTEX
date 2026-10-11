const { test, expect, _electron } = require("@playwright/test");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { assertAccessibleControls } = require("./ui-accessibility.cjs");
const { verifyApprovalStates } = require("./ui-state-approvals.cjs");
const { verifyFileStates } = require("./ui-state-files.cjs");
const { verifyDataStates } = require("./ui-state-data.cjs");
const { verifyTaskStates } = require("./ui-state-task.cjs");

// The primary-screen matrix uses real Electron/Go, delays or fails its selected
// API, then retries against real Go. The populated-state helpers below instead
// supply explicitly synthetic records, approval decisions and file responses.
const screens = [
  { route: "dashboard", api: "/stats", status: "대시보드 데이터", empty: null },
  { route: "chat", api: "/conversations", status: "대화 목록", empty: "대화 없음" },
  { route: "function/tasks", api: "/tasks", status: "작업 목록", empty: "작업이 없습니다" },
  { route: "function/findings", api: "/exploration/findings", status: "취약점 목록", empty: "일치하는 취약점이 없습니다" },
  { route: "function/traffic", api: "/traffic", status: "트래픽", empty: "일치하는 트래픽이 없습니다" },
  { route: "function/commands", api: "/commands", status: "도구 실행 기록", empty: "기록 없음" },
  { route: "function/llm-records", api: "/llm/records", status: "LLM 기록", empty: "기록 없음" },
  { route: "function/assets", api: "/companies", status: "기업 목록", empty: "등록된 기업이 없습니다" },
  { route: "function/sync", api: "/sync/scopesentry/status", status: "연결 상태를 확인" },
  { route: "function/workspace", api: "/workspace/list", status: "작업 파일", empty: "폴더가 비어 있습니다" },
  { route: "system/llm", api: "/llm/profiles", status: "모델 설정", empty: "모델 설정 없음" },
  { route: "system/agents", api: "/agents", status: "에이전트 목록", empty: "에이전트 없음" },
  { route: "system/mcp", api: "/mcp", status: "MCP 서버를 불러오는", empty: "MCP 서버 없음" },
  { route: "system/skills", api: "/skills", status: "스킬 라이브러리", empty: null },
  { route: "system/tools", api: "/tools", status: "도구 목록", empty: null },
  { route: "system/notify", api: "/notify/meta", status: "알림 설정 및 채널", empty: null },
  { route: "system/intercept", api: "/intercept/rules", status: "명령 차단 규칙", empty: "명령 차단 규칙 없음" },
  { route: "system/intercept/assets", api: "/asset-intercept/rules", status: "자산 차단 규칙", empty: "자산 차단 규칙 없음" },
  { route: "system/intercept/approvals", api: "/intercept/history", status: "승인 기록을 불러오는", retry: "새로고침", empty: "승인 기록 없음" },
  { route: "system/logs", api: "/logs/stream", status: "실시간 로그", error: "로그 스트림에 연결하지 못했습니다", empty: null },
  { route: "system/settings", api: "/settings", status: "시스템 설정", empty: null },
];

async function withApp(testInfo, run, nativeViewport = false) {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), "ARTEX UI 상태행렬 한글-"));
  const env = { ...process.env };
  for (const key of Object.keys(env)) {
    if (key.startsWith("ARTEX_") || ["OPENAI_API_KEY", "ANTHROPIC_API_KEY"].includes(key)) delete env[key];
  }
  const app = await _electron.launch({ args: [path.resolve(__dirname, ".."), `--artex-home=${home}`], env, chromiumSandbox: true, timeout: 45_000 });
  const page = await app.firstWindow();
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  try {
    await page.waitForURL(/\/function\/tasks\/?$/);
    if (!nativeViewport) await page.setViewportSize({ width: 1280, height: 900 });
    await run(page, new URL(page.url()).origin, app);
    expect(errors).toEqual([]);
  } finally {
    if (!page.isClosed()) await page.screenshot({ path: testInfo.outputPath("final.png") });
    await app.close();
    fs.writeFileSync(testInfo.outputPath("temporary-home.txt"), home);
  }
}

async function shot(page, testInfo, name) {
  await page.screenshot({ path: testInfo.outputPath(`${name}.png`) });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), `${name}: 화면 전체의 가로 넘침`).toBe(true);
}

test("21개 화면 × Mono 밝게·어둡게: 로딩·실패·키보드 재시도", async ({}, testInfo) => {
  test.setTimeout(420_000);
  await withApp(testInfo, async (page, origin) => {
    const completed = [];
    for (const mode of ["light", "dark"]) {
      await page.evaluate((value) => { document.cookie = `theme_mode=${value}; path=/`; }, mode);
      for (const screen of screens) {
        await test.step(`${mode} ${screen.route}`, async () => {
          let phase = "loading";
          let release;
          const pending = new Promise((resolve) => { release = resolve; });
          const target = (url) => url.origin === origin && url.pathname === `/api${screen.api}`;
          const marker = `검사 전용 ${screen.route} 읽기 실패${screen.route === "system/tools" ? ` ${"0123456789abcdef".repeat(180)}` : ""}`;
          const injected = async (route) => {
            if (route.request().method() !== "GET") return route.continue();
            if (phase === "loading") await pending;
            if (phase === "recover") return route.continue();
            return route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: marker }) });
          };
          await page.route(target, injected);
          const id = `${mode}-${screen.route.replaceAll("/", "-")}`;
          try {
            await page.goto(`${origin}/${screen.route}/`);
            await expect(page.getByRole("status").filter({ hasText: screen.status }).first()).toBeVisible();
            await shot(page, testInfo, `${id}-loading`);
            phase = "error";
            release();
            const alert = page.getByRole("alert").filter({ hasText: screen.error || marker }).first();
            await expect(alert).toBeVisible();
            if (screen.empty) await expect(page.getByText(screen.empty, { exact: false })).toHaveCount(0);
            if (screen.route === "system/settings") await expect(page.locator("#traffic-capture")).toBeDisabled();
            await assertAccessibleControls(page, testInfo, `${id}-error`);
            await shot(page, testInfo, `${id}-error`);
            phase = "recover";
            const retry = screen.retry ? page.getByRole("button", { name: screen.retry, exact: true }).first() : alert.getByRole("button", { name: "다시 시도", exact: true });
            await retry.focus();
            await page.keyboard.press("Enter");
            await expect(alert).toHaveCount(0);
            await expect(page.getByRole("status").filter({ hasText: screen.status })).toHaveCount(0);
            await assertAccessibleControls(page, testInfo, `${id}-recovered`);
            await shot(page, testInfo, `${id}-recovered`);
            completed.push({ route: screen.route, mode, loading: true, error: true, retry: true });
          } finally {
            release();
            await page.unroute(target, injected);
          }
        });
      }
    }
    expect(completed).toHaveLength(42);
    await testInfo.attach("ui-state-matrix", { body: JSON.stringify({ source: "Electron UI + real Go; API error fixtures only", completed }, null, 2), contentType: "application/json" });
  });
});

test("Mono 두 모드의 채워진 기록·상세 복구·승인·입력 폼", async ({}, testInfo) => {
  test.setTimeout(300_000);
  await withApp(testInfo, async (page, origin) => {
    for (const mode of ["light", "dark"]) {
      await page.evaluate((value) => { document.cookie = `theme_mode=${value}; path=/`; }, mode);
      await verifyApprovalStates(page, origin, testInfo, mode);
      await verifyRecords(page, origin, testInfo, mode);
      await verifyFileStates(page, origin, testInfo, mode);
      await verifyDataStates(page, origin, testInfo, mode);
      await verifyForms(page, origin, testInfo, mode);
    }
  });
});

test("Mono 두 모드의 작업 생성·재검증·보고서 상태", async ({}, testInfo) => {
  test.setTimeout(240_000);
  await withApp(testInfo, async (page, origin) => {
    for (const mode of ["light", "dark"]) {
      await page.evaluate((value) => { document.cookie = `theme_mode=${value}; path=/`; }, mode);
      await verifyTaskStates(page, origin, testInfo, mode);
    }
  });
});

async function verifyRecords(page, origin, testInfo, mode) {
  const longText = "검사 전용 긴 입력·결과 — 한글 example.test ".repeat(150);
  const command = { id: 99001, exploration_id: 99001, worker: "검사 Worker", tool: "Read", command: JSON.stringify({ file: "example.test", note: longText }), output: longText, is_error: false, created_at: "2026-10-11T00:00:00Z" };
  const commandRoute = (url) => url.origin === origin && url.pathname === "/api/commands";
  await page.route(commandRoute, (route) => route.fulfill({ json: { commands: [command, { ...command, id: 99002, tool: "Bash", is_error: true }], total: 2 } }));
  try {
    await page.goto(`${origin}/function/commands/`);
    await page.getByRole("row").filter({ hasText: "Read" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await dialog.evaluate(async (node) => { await Promise.all(node.getAnimations().map((animation) => animation.finished)); });
    await assertAccessibleControls(page, testInfo, `command-detail-${mode}`);
    const bounds = await dialog.boundingBox();
    expect(bounds.x + bounds.width).toBeLessThanOrEqual(1281);
    expect(bounds.y + bounds.height).toBeLessThanOrEqual(901);
    await shot(page, testInfo, `command-detail-${mode}`);
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
  } finally { await page.unroute(commandRoute); }

  const record = { id: 99001, ts: "2026-10-11T00:00:00Z", model: "검사 모델", profile_name: "검사 프로필", session_id: "fixture-session", task_id: "fixture-task", worker: "검사 Worker", latency_ms: 1800, input_tokens: 150, output_tokens: 250, cache_read: 0, cache_write: 0, status: "ok" };
  let detailFails = true;
  const llmRoute = (url) => url.origin === origin && ["/api/llm/records", "/api/llm/records/99001"].includes(url.pathname);
  await page.route(llmRoute, (route) => {
    if (new URL(route.request().url()).pathname.endsWith("/99001")) return detailFails
      ? route.fulfill({ status: 500, json: { error: "검사 전용 상세 읽기 실패" } })
      : route.fulfill({ json: { ...record, request_body: JSON.stringify({ prompt: longText }), response_body: JSON.stringify({ result: longText }), raw_request: JSON.stringify({ input: longText }), raw_response: `data: ${JSON.stringify({ result: longText })}\n\n` } });
    return route.fulfill({ json: { records: [record], total: 1 } });
  });
  try {
    await page.goto(`${origin}/function/llm-records/`);
    await page.getByRole("row").filter({ hasText: "검사 프로필" }).click();
    const error = page.getByRole("alert").filter({ hasText: "검사 전용 상세 읽기 실패" });
    await expect(error).toBeVisible();
    await expect(page.getByText("(비어 있음)", { exact: true })).toHaveCount(0);
    detailFails = false;
    await error.getByRole("button", { name: "다시 시도", exact: true }).click();
    await expect(error).toHaveCount(0);
    await expect(page.getByText(/검사 전용 긴 입력/).first()).toBeVisible();
    await page.getByRole("button", { name: "원문", exact: true }).click();
    await expect(page.getByText("Response · 원문（SSE）", { exact: true })).toBeVisible();
    await assertAccessibleControls(page, testInfo, `llm-record-detail-${mode}`);
    await shot(page, testInfo, `llm-record-detail-${mode}`);
    await page.getByRole("button", { name: "LLM 기록 상세 닫기", exact: true }).click();
  } finally { await page.unroute(llmRoute); }
}

async function closeForm(page, testInfo, name) {
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await dialog.evaluate(async (node) => { await Promise.all(node.getAnimations().map((animation) => animation.finished)); });
  await assertAccessibleControls(page, testInfo, name);
  const bounds = await dialog.boundingBox();
  const viewport = page.viewportSize();
  expect(bounds.x).toBeGreaterThanOrEqual(-1);
  expect(bounds.x + bounds.width).toBeLessThanOrEqual(viewport.width + 1);
  expect(bounds.y + bounds.height).toBeLessThanOrEqual(viewport.height + 1);
  for (let index = 0; index < 12; index++) {
    await page.keyboard.press("Tab");
    expect(await page.evaluate(() => !!document.activeElement?.closest('[role="dialog"]'))).toBe(true);
  }
  await shot(page, testInfo, name);
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
}

async function verifyForms(page, origin, testInfo, mode) {
  await page.goto(`${origin}/function/tasks/`);
  await page.getByRole("button", { name: "새 작업", exact: true }).click();
  await page.getByRole("dialog").getByRole("button", { name: "생성", exact: true }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await closeForm(page, testInfo, `task-form-${mode}`);

  await page.goto(`${origin}/function/assets/`);
  await page.getByRole("button", { name: "기업 추가", exact: true }).click();
  await closeForm(page, testInfo, `company-form-${mode}`);

  await page.goto(`${origin}/system/agents/`);
  await page.getByRole("button", { name: "새 Agent", exact: true }).click();
  const agentDialog = page.getByRole("dialog");
  await expect(agentDialog.getByRole("button", { name: "생성", exact: true })).toBeDisabled();
  await agentDialog.getByRole("textbox", { name: "Key", exact: true }).fill("INVALID");
  await expect(page.getByText("소문자로 시작하며 소문자/숫자/밑줄만 사용", { exact: true })).toBeVisible();
  await closeForm(page, testInfo, `agent-form-${mode}`);

  await page.goto(`${origin}/system/tools/`);
  await page.getByRole("tab", { name: "사용자 정의 도구", exact: true }).click();
  await page.getByRole("button", { name: "사용자 정의 도구 생성", exact: true }).click();
  const toolDialog = page.getByRole("dialog");
  for (const type of ["command(Shell 명령 템플릿)", "script(Python 스크립트)", "http(API 요청)"]) {
    await toolDialog.getByRole("combobox", { name: "도구 실행 방식", exact: true }).click();
    await page.getByRole("option", { name: type, exact: true }).click();
    await assertAccessibleControls(page, testInfo, `custom-tool-${type.split("(")[0]}-${mode}`);
  }
  await closeForm(page, testInfo, `custom-tool-form-${mode}`);

  await page.goto(`${origin}/system/intercept/assets/`);
  await page.getByRole("button", { name: "새 규칙", exact: true }).click();
  await closeForm(page, testInfo, `asset-rule-form-${mode}`);

  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`${origin}/system/settings/`);
  await assertAccessibleControls(page, testInfo, `settings-populated-${mode}`);
  await shot(page, testInfo, `settings-populated-${mode}`);
  await page.setViewportSize({ width: 1280, height: 900 });
}

const physicalScale = Number(process.env.ARTEX_UI_PHYSICAL_SCALE || 1.5);
if (![1.25, 1.5].includes(physicalScale)) throw new Error("실기 검사 배율은 1.25 또는 1.5여야 합니다");
const physicalPercent = Math.round(physicalScale * 100);

test(`실제 Windows ${physicalPercent}% 모니터: 두 Mono 모드의 모델·MCP·작업 폼`, async ({}, testInfo) => {
  test.setTimeout(180_000);
  await withApp(testInfo, async (page, origin, app) => {
    const position = await app.evaluate(({ BrowserWindow, screen }, scale) => {
      const win = BrowserWindow.getAllWindows()[0];
      const original = win.getBounds();
      const display = screen.getAllDisplays().find((item) => item.scaleFactor === scale);
      if (!display) return { original, displays: screen.getAllDisplays(), found: false };
      const width = Math.min(1280, display.workArea.width - 80);
      const height = Math.min(1000, display.workArea.height - 80);
      win.setBounds({ x: display.workArea.x + Math.floor((display.workArea.width - width) / 2), y: display.workArea.y + Math.floor((display.workArea.height - height) / 2), width, height });
      win.webContents.setZoomFactor(1);
      return { original, display, found: true };
    }, physicalScale);
    await testInfo.attach(`physical-display-${physicalPercent}`, { body: JSON.stringify(position, null, 2), contentType: "application/json" });
    expect(position.found, `실제 ${physicalPercent}% 모니터가 있어야 이 검사를 검증으로 기록할 수 있습니다`).toBe(true);
    try {
      await expect.poll(() => page.evaluate((scale) => Math.abs(window.devicePixelRatio - scale), physicalScale)).toBeLessThan(0.001);
      expect(await app.evaluate(({ BrowserWindow, screen }) => screen.getDisplayMatching(BrowserWindow.getAllWindows()[0].getBounds()).scaleFactor)).toBe(physicalScale);
      const forms = [
        { route: "system/llm", trigger: "생성" },
        { route: "system/mcp", trigger: "MCP 추가" },
        { route: "function/tasks", trigger: "새 작업" },
      ];
      for (const mode of ["light", "dark"]) {
        await page.evaluate((value) => { document.cookie = `theme_mode=${value}; path=/`; }, mode);
        for (const form of forms) {
          await page.goto(`${origin}/${form.route}/`);
          const trigger = page.getByRole("button", { name: form.trigger, exact: true });
          await trigger.focus();
          await page.keyboard.press("Enter");
          const dialog = page.getByRole("dialog");
          await expect(dialog).toBeVisible();
          await dialog.evaluate(async (node) => { await Promise.all(node.getAnimations().map((animation) => animation.finished)); });
          const native = await app.evaluate(({ BrowserWindow, screen }) => {
            const win = BrowserWindow.getAllWindows()[0];
            return { bounds: win.getBounds(), display: screen.getDisplayMatching(win.getBounds()), zoom: win.webContents.getZoomFactor() };
          });
          expect(native.zoom).toBe(1);
          expect(native.display.scaleFactor).toBe(physicalScale);
          const geometry = await dialog.evaluate((element) => {
            const rect = element.getBoundingClientRect();
            return { x: rect.x, y: rect.y, right: rect.right, bottom: rect.bottom, width: innerWidth, height: innerHeight, dpr: devicePixelRatio, scrollRegions: [...element.querySelectorAll("*")].filter((node) => node.scrollHeight > node.clientHeight + 1 && ["auto", "scroll"].includes(getComputedStyle(node).overflowY)).length };
          });
          expect(Math.abs(geometry.dpr - physicalScale)).toBeLessThan(0.001);
          expect(geometry.x).toBeGreaterThanOrEqual(-1);
          expect(geometry.y).toBeGreaterThanOrEqual(-1);
          expect(geometry.right).toBeLessThanOrEqual(geometry.width + 1);
          expect(geometry.bottom).toBeLessThanOrEqual(geometry.height + 1);
          if (form.route === "function/tasks") expect(geometry.scrollRegions).toBeGreaterThan(0);
          const name = `physical-${physicalPercent}-${mode}-${form.route.replaceAll("/", "-")}`;
          await assertAccessibleControls(page, testInfo, name);
          const close = dialog.getByRole("button", { name: "닫기", exact: true }).first();
          await expect(close).toBeVisible();
          const closeGeometry = await close.evaluate((element) => {
            const rect = element.getBoundingClientRect();
            const center = document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2);
            return { ...rect.toJSON(), width: rect.width, height: rect.height, hit: !!center && element.contains(center), color: getComputedStyle(element).color };
          });
          expect(closeGeometry.width).toBeGreaterThan(0);
          expect(closeGeometry.height).toBeGreaterThan(0);
          expect(closeGeometry.x).toBeGreaterThanOrEqual(geometry.x);
          expect(closeGeometry.right).toBeLessThanOrEqual(geometry.right);
          expect(closeGeometry.y).toBeGreaterThanOrEqual(geometry.y);
          expect(closeGeometry.bottom).toBeLessThanOrEqual(geometry.bottom);
          expect(closeGeometry.hit).toBe(true);
          // Playwright's screenshot clip can crop this Windows native-DPI surface.
          // Capture the actual webContents surface without changing its scale.
          const png = await app.evaluate(async ({ BrowserWindow }) => (await BrowserWindow.getAllWindows()[0].webContents.capturePage()).toPNG().toString("base64"));
          fs.writeFileSync(testInfo.outputPath(`${name}.png`), Buffer.from(png, "base64"));
          fs.writeFileSync(testInfo.outputPath(`${name}.json`), JSON.stringify({ geometry, closeGeometry, native }, null, 2));
          await testInfo.attach(`${name}-geometry`, { body: JSON.stringify(geometry), contentType: "application/json" });
          await testInfo.attach(`${name}-native`, { body: JSON.stringify(native), contentType: "application/json" });
          await close.click();
          await expect(dialog).toHaveCount(0);
          await expect(trigger).toBeFocused();
          await page.keyboard.press("Enter");
          await expect(dialog).toBeVisible();
          await dialog.evaluate(async (node) => { await Promise.all(node.getAnimations().map((animation) => animation.finished)); });
          await page.keyboard.press("Escape");
          await expect(dialog).toHaveCount(0);
          await expect(trigger).toBeFocused();
        }
      }
    } finally {
      await app.evaluate(({ BrowserWindow }, bounds) => BrowserWindow.getAllWindows()[0].setBounds(bounds), position.original);
    }
  }, true);
});
