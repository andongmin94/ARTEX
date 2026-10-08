const { test, expect, chromium } = require("@playwright/test");
const { spawn } = require("node:child_process");
const { createInterface } = require("node:readline");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

test.use({ trace: "on" });

test("일반 Chrome의 실제 Go setup·비밀번호·이용 안내·로그인·종료", async ({}, testInfo) => {
  test.setTimeout(120_000);
  const home = fs.mkdtempSync(path.join(os.tmpdir(), "ARTEX 일반 브라우저 한글 #%-"));
  const executable = path.resolve(__dirname, "../resources", process.platform === "win32" ? "artex.exe" : "artex");
  expect(fs.existsSync(executable), "최신 desktop/resources 백엔드를 먼저 빌드하세요").toBe(true);
  expect(fs.existsSync("C:/Program Files/Google/Chrome/Application/chrome.exe"), "검사에는 설치된 Chrome이 필요합니다").toBe(true);
  const env = { ...process.env };
  for (const key of Object.keys(env)) {
    if (key.startsWith("ARTEX_") || /(?:API_?KEY|TOKEN|SECRET|PASSWORD)/i.test(key)) delete env[key];
  }
  env.ARTEX_HOME = home;
  env.NORMA_DISABLE_RIPGREP = "1";
  const diagnostics = { home, executable, pid: null, ready: null, exit: null, stdout: "", stderr: "", pageErrors: [], navigations: [] };
  fs.writeFileSync(testInfo.outputPath("temporary-home.txt"), home);
  const child = spawn(executable, ["-addr", "127.0.0.1:0", "-proxy", "", "-ready-stdout", "-parent-stdin"], {
    cwd: home, env, stdio: ["pipe", "pipe", "pipe"], windowsHide: true,
  });
  diagnostics.pid = child.pid;
  child.stdout.on("data", (data) => { diagnostics.stdout += data.toString(); });
  child.stderr.on("data", (data) => { diagnostics.stderr += data.toString(); });
  child.stdin.on("error", (error) => { diagnostics.stderr += `\nparent stdin: ${error.message}\n`; });
  const exited = new Promise((resolve) => {
    child.once("exit", (code, signal) => { diagnostics.exit = { code, signal }; resolve(diagnostics.exit); });
    child.once("error", (error) => { diagnostics.exit = { error: error.message }; resolve(diagnostics.exit); });
  });
  const lines = createInterface({ input: child.stdout });
  const ready = new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("일반 백엔드 ready 제한 시간 초과")), 30_000);
    const finish = (error, value) => {
      clearTimeout(timer);
      child.removeListener("error", failed);
      child.removeListener("exit", stopped);
      lines.removeListener("line", received);
      if (error) reject(error); else resolve(value);
    };
    const failed = (error) => finish(error);
    const stopped = (code, signal) => finish(new Error(`ready 이전 백엔드 종료: ${signal ?? code}`));
    const received = (line) => {
      try {
        const event = JSON.parse(line);
        const url = new URL(event.url);
        if (event.event !== "ready" || event.pid !== child.pid || url.protocol !== "http:" || url.hostname !== "127.0.0.1" || !url.port || url.username || url.password || url.pathname !== "/" || url.search || url.hash) {
          throw new Error("일반 백엔드 ready의 PID 또는 loopback URL이 유효하지 않습니다");
        }
        diagnostics.ready = event;
        finish(null, url.origin);
      } catch (error) { finish(error); }
    };
    child.once("error", failed);
    child.once("exit", stopped);
    lines.on("line", received);
  });
  function waitForExit(timeout = 15_000) {
    let timer;
    return Promise.race([
      exited,
      new Promise((_, reject) => { timer = setTimeout(() => reject(new Error("부모 stdin 종료 후 백엔드가 종료되지 않았습니다")), timeout); }),
    ]).finally(() => clearTimeout(timer));
  }
  let browser;
  let page;
  try {
    const origin = await ready;
    expect(await (await fetch(`${origin}/api/auth/status`, { signal: AbortSignal.timeout(5_000) })).json()).toEqual({ mode: "standalone", initialized: false });
    expect((await fetch(`${origin}/api/auth/desktop-session`, { method: "POST", signal: AbortSignal.timeout(5_000) })).status).toBe(403);
    expect((await fetch(`${origin}/api/tasks`, { signal: AbortSignal.timeout(5_000) })).status).toBe(401);
    browser = await chromium.launch({ channel: "chrome", headless: true });
    const context = await browser.newContext({ viewport: { width: 1440, height: 960 } });
    page = await context.newPage();
    page.on("pageerror", (error) => diagnostics.pageErrors.push(error.message));
    page.on("framenavigated", (frame) => { if (frame === page.mainFrame()) diagnostics.navigations.push(frame.url()); });
    const authStatusURL = `${origin}/api/auth/status`;
    const unavailableMessage = "인증 저장소를 일시적으로 사용할 수 없습니다. 잠시 후 다시 시도하세요";
    await test.step("503 UI fixture는 비밀번호 초기 설정 폼을 차단한다", async () => {
      await page.route(authStatusURL, (route) => route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({ error: unavailableMessage }),
      }));
      await page.goto(`${origin}/setup`);
      await page.waitForURL(/\/setup\/?$/);
      const alert = page.getByRole("alert").filter({ hasText: "앱에 연결할 수 없습니다" });
      await expect(alert).toBeVisible();
      await expect(alert.getByText(unavailableMessage, { exact: true })).toBeVisible();
      await expect(alert.getByRole("button", { name: "다시 시도", exact: true })).toBeVisible();
      await expect(page.getByRole("heading", { name: "비밀번호 초기 설정", exact: true })).toHaveCount(0);
      await expect(page.locator("form")).toHaveCount(0);
      await expect(page.locator('input[type="password"]')).toHaveCount(0);
      await page.screenshot({ path: testInfo.outputPath("standalone-setup-unavailable-fixture.png") });
    });
    await test.step("재시도는 실제 Go 인증 상태로 복구한다", async () => {
      await page.unroute(authStatusURL);
      const statusResponse = page.waitForResponse((response) => response.url() === authStatusURL && response.request().method() === "GET");
      await page.getByRole("button", { name: "다시 시도", exact: true }).click();
      const response = await statusResponse;
      expect(response.status()).toBe(200);
      expect(await response.json()).toEqual({ mode: "standalone", initialized: false });
      await expect(page.getByRole("heading", { name: "비밀번호 초기 설정", exact: true })).toBeVisible();
      await expect(page.getByRole("alert").filter({ hasText: "앱에 연결할 수 없습니다" })).toHaveCount(0);
    });
    await page.goto(origin);
    await page.waitForURL(/\/setup\/?$/);
    expect(await page.evaluate(() => typeof window.artexDesktop)).toBe("undefined");
    await expect(page.getByRole("heading", { name: "비밀번호 초기 설정", exact: true })).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath("standalone-setup.png") });
    const password = "일반브라우저-검증-12345678";
    await page.getByLabel("새 비밀번호", { exact: true }).fill(password);
    await page.getByLabel("비밀번호 확인", { exact: true }).fill(password);
    await page.getByRole("button", { name: "비밀번호 설정 후 로그인", exact: true }).click();
    await page.waitForURL(/\/function\/tasks\/?$/);
    await expect(page.locator('[data-slot="sidebar"]')).toBeVisible();
    const userMenu = page.locator('[data-slot="sidebar-footer"]').getByRole("button");
    await userMenu.click();
    await expect(page.getByRole("menuitem", { name: "비밀번호 변경", exact: true })).toBeVisible();
    await expect(page.getByRole("menuitem", { name: "로그아웃", exact: true })).toBeVisible();
    await page.getByRole("menuitem", { name: "비밀번호 변경", exact: true }).click();
    await expect(page.getByRole("dialog", { name: "비밀번호 변경", exact: true })).toBeVisible();
    await page.keyboard.press("Escape");
    await userMenu.click();
    await page.getByRole("menuitem", { name: "로그아웃", exact: true }).click();
    await page.waitForURL(/\/login\/?$/);
    await expect(page.getByRole("heading", { name: "로그인", exact: true })).toBeVisible();
    await page.getByLabel("비밀번호", { exact: true }).fill(password);
    const login = page.getByRole("button", { name: "로그인", exact: true });
    await expect(login).toBeDisabled();
    await page.getByRole("button", { name: "이용 안내", exact: true }).click();
    await expect(page.getByRole("dialog", { name: "ARTEX 이용 안내 및 면책 조항", exact: true })).toBeVisible();
    await page.locator('[data-slot="dialog-content"] .overflow-y-auto').evaluate((node) => {
      node.scrollTop = node.scrollHeight;
      node.dispatchEvent(new Event("scroll"));
    });
    await page.getByRole("button", { name: "모든 조항을 읽었으며 동의합니다", exact: true }).click();
    await expect(login).toBeEnabled();
    await page.screenshot({ path: testInfo.outputPath("standalone-password-login.png") });
    await login.click();
    await page.waitForURL(/\/function\/tasks\/?$/);
    await expect(page.locator('[data-slot="sidebar"]')).toBeVisible();
    const token = await page.evaluate(() => localStorage.getItem("artex_token"));
    expect(token).toBeTruthy();
    expect((await fetch(`${origin}/api/tasks`, { headers: { Authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(5_000) })).status).toBe(200);
    expect((await fetch(`${origin}/api/auth/desktop-session`, { method: "POST", headers: { Authorization: `Bearer ${token}`, "X-Artex-Desktop-Session": "ab".repeat(32) }, signal: AbortSignal.timeout(5_000) })).status).toBe(403);
    expect(await (await fetch(`${origin}/api/auth/status`, { signal: AbortSignal.timeout(5_000) })).json()).toEqual({ mode: "standalone", initialized: true });
    await userMenu.click();
    await expect(page.getByRole("menuitem", { name: "비밀번호 변경", exact: true })).toBeVisible();
    await expect(page.getByRole("menuitem", { name: "로그아웃", exact: true })).toBeVisible();
    await page.keyboard.press("Escape");
    expect(diagnostics.pageErrors).toEqual([]);
    await page.screenshot({ path: testInfo.outputPath("standalone-authenticated.png") });
    await browser.close();
    browser = null;
    child.stdin.end();
    expect(await waitForExit()).toEqual({ code: 0, signal: null });
    await expect.poll(async () => {
      try { await fetch(`${origin}/api/health`, { signal: AbortSignal.timeout(500) }); return false; }
      catch { return true; }
    }, { timeout: 10_000 }).toBe(true);
  } finally {
    if (page && !page.isClosed()) await page.screenshot({ path: testInfo.outputPath("standalone-final-state.png") }).catch(() => {});
    if (browser) await browser.close().catch((error) => diagnostics.pageErrors.push(`browser cleanup: ${error.message}`));
    if (!diagnostics.exit) {
      child.stdin.end();
      try { await waitForExit(); }
      catch { child.kill(); await waitForExit(5_000).catch(() => {}); }
    }
    lines.close();
    const diagnosticsPath = testInfo.outputPath("standalone-process.json");
    fs.writeFileSync(diagnosticsPath, JSON.stringify(diagnostics, null, 2));
    fs.writeFileSync(testInfo.outputPath("standalone-stdout.log"), diagnostics.stdout);
    fs.writeFileSync(testInfo.outputPath("standalone-stderr.log"), diagnostics.stderr);
    await testInfo.attach("standalone-process", { path: diagnosticsPath, contentType: "application/json" });
    // 실패 진단과 임시 SQLite를 보존한다. 사용자 데이터는 사용하지 않는다.
  }
});
