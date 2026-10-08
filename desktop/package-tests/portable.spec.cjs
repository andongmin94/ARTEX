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
  for (const license of ["LICENSE", "LICENSES.chromium.html", "resources/artex/licenses/ARTEX-LICENSE.txt", "resources/artex/licenses/neobrutal-ui-MIT.txt", "resources/artex/licenses/OFL-Pretendard.txt"]) expect(fs.existsSync(path.join(bundle, license)), license).toBe(true);
  expect(fs.readdirSync(path.join(bundle, "resources/artex/fonts"))).toEqual(["PretendardVariable.woff2"]);
  expect(fs.existsSync(path.join(bundle, "resources/artex/licenses/OFL-NotoSansKR.txt"))).toBe(false);
  let electron;
  let page;
  async function api(route, method = "GET", body) {
    return page.evaluate(async ({ route, method, body }) => {
      const response = await fetch(`/api${route}`, { method, headers: { Authorization: `Bearer ${localStorage.getItem("artex_token")}`, ...(body ? { "Content-Type": "application/json" } : {}) }, ...(body ? { body: JSON.stringify(body) } : {}) });
      const data = await response.json();
      if (!response.ok) throw new Error(`${method} ${route}: ${response.status} ${JSON.stringify(data)}`);
      return data;
    }, { route, method, body });
  }
  async function launch() {
    electron = await _electron.launch({ executablePath, args: [`--artex-home=${home}`], env: isolatedEnvironment, chromiumSandbox: true, timeout: 45_000 });
    page = await electron.firstWindow();
    await expect.poll(async () => (await page.evaluate(() => window.artexDesktop?.status()).catch(() => null))?.state, { timeout: 40_000 }).toBe("ready");
    await page.waitForURL(/^http:\/\/127\.0\.0\.1:\d+\//);
    expect(await electron.evaluate(({ app }) => ({ packaged: app.isPackaged, home: app.getPath("userData"), app: app.getAppPath() }))).toEqual({ packaged: true, home, app: path.join(bundle, "resources/app") });
  }
  try {
    await launch();
    await page.waitForURL(/\/function\/tasks\/?$/);
    await expect(page.locator('[data-slot="sidebar"]')).toBeVisible();
    await expect(page.locator("main")).toBeVisible();
    await expect(page.locator('input[type="password"]')).toHaveCount(0);
    expect(await page.evaluate(async () => { await document.fonts.ready; return [...document.fonts].map(({ family, status }) => ({ family, status })); })).toEqual([{ family: expect.stringMatching(/pretendard/i), status: "loaded" }]);
    expect(await api("/auth/status")).toEqual({ initialized: false, mode: "desktop" });
    expect(fs.existsSync(path.join(home, "data/artex.sqlite"))).toBe(true);
    const customSkill = path.join(home, "skills", "package-inspection.txt");
    fs.writeFileSync(customSkill, "사용자 스킬 파일 보존 검증");
    const ready = await page.evaluate(() => window.artexDesktop.status());
    expect((await fetch(`${ready.url}/api/health`)).status).toBe(403);
    await page.screenshot({ path: testInfo.outputPath("portable-ready.png") });
    await api("/auth/init", "POST", { password: "패키지검증-12345678" });
    await electron.close();
    electron = null;
    await expect.poll(async () => { try { await fetch(`${ready.url}/api/health`, { signal: AbortSignal.timeout(500) }); return false; } catch { return true; } }).toBe(true);
    await launch();
    await page.waitForURL(/\/function\/tasks\/?$/);
    await expect(page.locator('[data-slot="sidebar"]')).toBeVisible();
    await expect(page.locator('input[type="password"]')).toHaveCount(0);
    expect(fs.readFileSync(customSkill, "utf8")).toBe("사용자 스킬 파일 보존 검증");
    expect(await api("/auth/status")).toEqual({ initialized: true, mode: "desktop" });
    expect((await api("/auth/login", "POST", { username: "ARTEX", password: "패키지검증-12345678" })).token.split(".")).toHaveLength(3);
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
