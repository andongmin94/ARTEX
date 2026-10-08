const { test, expect, _electron } = require("@playwright/test");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const isolatedEnvironment = { ...process.env };
for (const key of Object.keys(isolatedEnvironment)) {
  if (key.startsWith("ARTEX_") || ["OPENAI_API_KEY", "ANTHROPIC_API_KEY"].includes(key)) delete isolatedEnvironment[key];
}

test("Windows 실행 패키지의 실제 Go·SQLite·UI 부팅 및 재시작", async ({}, testInfo) => {
  test.setTimeout(120_000);
  const home = fs.mkdtempSync(path.join(os.tmpdir(), "ARTEX 패키지 한글 #%-"));
  const bundle = path.resolve(__dirname, `../dist/ARTEX-win32-${process.arch}`);
  const executablePath = path.join(bundle, "ARTEX.exe");
  expect(fs.existsSync(executablePath), "먼저 npm run package를 실행하세요").toBe(true);
  for (const license of ["LICENSE", "LICENSES.chromium.html", "resources/artex/licenses/ARTEX-LICENSE.txt", "resources/artex/licenses/neobrutal-ui-MIT.txt", "resources/artex/licenses/OFL-NotoSansKR.txt"]) expect(fs.existsSync(path.join(bundle, license)), license).toBe(true);
  let electron;
  let page;
  async function launch() {
    electron = await _electron.launch({ executablePath, args: [`--artex-home=${home}`], env: isolatedEnvironment, chromiumSandbox: true, timeout: 45_000 });
    page = await electron.firstWindow();
    await expect.poll(async () => (await page.evaluate(() => window.artexDesktop?.status()).catch(() => null))?.state, { timeout: 40_000 }).toBe("ready");
    await page.waitForURL(/^http:\/\/127\.0\.0\.1:\d+\//);
    expect(await electron.evaluate(({ app }) => ({ packaged: app.isPackaged, home: app.getPath("userData"), app: app.getAppPath() }))).toEqual({ packaged: true, home, app: path.join(bundle, "resources/app") });
  }
  try {
    await launch();
    await page.waitForURL(/\/setup\/?$/);
    await page.getByLabel("새 비밀번호", { exact: true }).fill("패키지검증-12345678");
    await page.getByLabel("비밀번호 확인", { exact: true }).fill("패키지검증-12345678");
    await page.getByRole("button", { name: "비밀번호 설정 후 로그인" }).click();
    await page.waitForURL(/\/function\/tasks\/?$/);
    await expect(page.locator('[data-slot="sidebar"]')).toBeVisible();
    await expect(page.locator("main")).toBeVisible();
    expect(fs.existsSync(path.join(home, "data/artex.sqlite"))).toBe(true);
    const customSkill = path.join(home, "skills", "package-inspection.txt");
    fs.writeFileSync(customSkill, "사용자 스킬 파일 보존 검증");
    const ready = await page.evaluate(() => window.artexDesktop.status());
    expect((await fetch(`${ready.url}/api/health`)).status).toBe(403);
    await page.screenshot({ path: testInfo.outputPath("portable-ready.png") });
    await electron.close();
    electron = null;
    await expect.poll(async () => { try { await fetch(`${ready.url}/api/health`, { signal: AbortSignal.timeout(500) }); return false; } catch { return true; } }).toBe(true);
    await launch();
    await page.waitForURL(/\/login\/?$/);
    await expect(page.getByRole("heading", { name: "로그인", exact: true })).toBeVisible();
    expect(fs.readFileSync(customSkill, "utf8")).toBe("사용자 스킬 파일 보존 검증");
    await page.getByLabel("비밀번호", { exact: true }).fill("패키지검증-12345678");
    await page.getByRole("button", { name: "이용 안내", exact: true }).click();
    await page.locator('[data-slot="dialog-content"] .overflow-y-auto').evaluate((node) => { node.scrollTop = node.scrollHeight; node.dispatchEvent(new Event("scroll")); });
    await page.getByRole("button", { name: "모든 조항을 읽었으며 동의합니다", exact: true }).click();
    await page.getByRole("button", { name: "로그인", exact: true }).click();
    await page.waitForURL(/\/function\/tasks\/?$/);
    await page.locator('a[href="/system/settings/"]').click();
    await expect(page.getByText("자동 업데이트 미구성", { exact: true })).toBeVisible();
    await expect(page.getByText("실행 환경 미준비", { exact: true })).toBeVisible();
    await expect(page.getByText("미준비", { exact: true })).toHaveCount(6);
    await page.screenshot({ path: testInfo.outputPath("portable-restarted.png") });
    fs.writeFileSync(testInfo.outputPath("temporary-home.txt"), home);
  } finally {
    if (electron) await electron.close();
  }
});
