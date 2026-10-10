const { test, expect, _electron } = require("@playwright/test");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { assertAccessibleControls } = require("./ui-accessibility.cjs");

test("실제 UI 로딩·오류 복구·폼·키보드·선택 상태", async ({}, testInfo) => {
  test.setTimeout(120_000);
  const home = fs.mkdtempSync(path.join(os.tmpdir(), "ARTEX UI 상태 한글-"));
  const env = { ...process.env };
  for (const key of Object.keys(env)) {
    if (key.startsWith("ARTEX_") || ["OPENAI_API_KEY", "ANTHROPIC_API_KEY"].includes(key)) delete env[key];
  }
  const app = await _electron.launch({ args: [path.resolve(__dirname, ".."), `--artex-home=${home}`], env, chromiumSandbox: true, timeout: 45_000 });
  const page = await app.firstWindow();
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  let release;
  try {
    await page.waitForURL(/\/function\/tasks\/?$/);
    const origin = new URL(page.url()).origin;
    const pending = new Promise((resolve) => { release = resolve; });
    const delayed = async (route) => { await pending; await route.continue(); };
    await page.route(`${origin}/api/mcp`, delayed);
    await page.goto(`${origin}/system/mcp/`);
    await expect(page.getByRole("status").filter({ hasText: "MCP 서버를 불러오는 중" })).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath("mcp-loading.png") });
    release();
    await expect(page.getByRole("button", { name: "MCP 추가", exact: true })).toBeVisible();
    await page.unroute(`${origin}/api/mcp`, delayed);

    const failed = (route) => route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "검사 전용 MCP 목록 실패" }) });
    await page.route(`${origin}/api/mcp`, failed);
    await page.goto(`${origin}/system/mcp/`);
    await expect(page.getByRole("alert").filter({ hasText: "검사 전용 MCP 목록 실패" })).toBeVisible();
    await expect(page.getByRole("button", { name: "MCP 추가", exact: true })).toHaveCount(0);
    await page.screenshot({ path: testInfo.outputPath("mcp-error.png") });
    await page.unroute(`${origin}/api/mcp`, failed);
    await page.getByRole("button", { name: "다시 시도", exact: true }).focus();
    await page.keyboard.press("Enter");
    await expect(page.getByRole("button", { name: "MCP 추가", exact: true })).toBeVisible();

    for (const theme of ["light", "dark"]) {
      await page.evaluate((mode) => { document.cookie = `theme_mode=${mode}; path=/`; }, theme);
      await page.goto(`${origin}/system/mcp/`);
      const add = page.getByRole("button", { name: "MCP 추가", exact: true });
      await add.focus();
      await page.keyboard.press("Enter");
      const dialog = page.getByRole("dialog");
      await expect(dialog).toBeVisible();
      await dialog.getByRole("button", { name: "추가", exact: true }).click();
      await expect(page.getByText("이름을 입력하세요", { exact: true })).toBeVisible();
      await dialog.getByRole("button", { name: "http(원격)", exact: true }).click();
      await expect(dialog.getByRole("button", { name: "http(원격)", exact: true })).toHaveAttribute("aria-pressed", "true");
      await expect(dialog.getByRole("button", { name: "stdio(로컬)", exact: true })).toHaveAttribute("aria-pressed", "false");
      const check = dialog.getByRole("checkbox", { name: "TLS 인증서 검증 건너뛰기(자체 서명 인증서)", exact: true });
      await check.focus();
      await page.keyboard.press("Space");
      await expect(check).toBeChecked();
      await check.evaluate(async (node) => { await Promise.all(node.getAnimations().map((animation) => animation.finished)); });
      const checkedColor = await check.evaluate((node) => getComputedStyle(node).backgroundColor);
      await page.keyboard.press("Space");
      await expect(check).not.toBeChecked();
      await check.evaluate(async (node) => { await Promise.all(node.getAnimations().map((animation) => animation.finished)); });
      const uncheckedColor = await check.evaluate((node) => getComputedStyle(node).backgroundColor);
      expect(checkedColor).not.toBe(uncheckedColor);
      await assertAccessibleControls(page, testInfo, `mcp-form-${theme}`);
      await page.screenshot({ path: testInfo.outputPath(`mcp-form-${theme}.png`) });
      await page.keyboard.press("Escape");
      await expect(dialog).toHaveCount(0);
      await expect(add).toBeFocused();

      const edit = page.locator('[data-slot="card-title"] button').first();
      await edit.focus();
      await page.keyboard.press("Enter");
      await expect(page.getByRole("dialog")).toBeVisible();
      await assertAccessibleControls(page, testInfo, `mcp-edit-${theme}`);
      await page.keyboard.press("Escape");
      await expect(edit).toBeFocused();

      await page.goto(`${origin}/system/llm/`);
      const create = page.getByRole("button", { name: "생성", exact: true });
      await create.focus();
      await page.keyboard.press("Enter");
      await expect(page.getByRole("dialog")).toBeVisible();
      await assertAccessibleControls(page, testInfo, `model-form-${theme}`);
      const streaming = page.getByRole("dialog").getByRole("switch", { name: "스트리밍 출력", exact: true });
      await expect(streaming).toBeChecked();
      const streamingOn = await streaming.evaluate((node) => ({ color: getComputedStyle(node).backgroundColor, thumb: node.firstElementChild.getBoundingClientRect().left }));
      await streaming.focus();
      await page.keyboard.press("Space");
      await expect(streaming).not.toBeChecked();
      await expect.poll(() => streaming.evaluate((node) => getComputedStyle(node).backgroundColor)).not.toBe(streamingOn.color);
      await expect.poll(() => streaming.evaluate((node) => node.firstElementChild.getBoundingClientRect().left)).toBeLessThan(streamingOn.thumb);
      await page.keyboard.press("Space");
      await expect(streaming).toBeChecked();
      // Tab stays inside the open Sheet; Escape returns focus to its trigger.
      for (let i = 0; i < 30; i++) {
        await page.keyboard.press("Tab");
        expect(await page.evaluate(() => !!document.activeElement?.closest('[role="dialog"]'))).toBe(true);
      }
      await page.keyboard.press("Escape");
      await expect(create).toBeFocused();

      await page.goto(`${origin}/function/findings/`);
      await page.getByRole("button", { name: "내보내기", exact: true }).click();
      const exportDialog = page.getByRole("dialog");
      await expect(exportDialog).toBeVisible();
      await assertAccessibleControls(page, testInfo, `finding-export-${theme}`);
      const csv = exportDialog.getByRole("radio", { name: "CSV 표(.csv)", exact: true });
      await csv.focus();
      await page.keyboard.press("Space");
      await expect(csv).toBeChecked();
      await page.screenshot({ path: testInfo.outputPath(`finding-export-${theme}.png`) });
      await page.keyboard.press("Escape");
    }
    expect(errors).toEqual([]);
  } finally {
    release?.();
    if (!page.isClosed()) await page.screenshot({ path: testInfo.outputPath("ui-state-final.png") });
    await app.close();
    fs.writeFileSync(testInfo.outputPath("temporary-home.txt"), home);
  }
});
