const { test: base, expect, _electron } = require("@playwright/test");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const env = { ...process.env };
for (const key of Object.keys(env)) {
  if (key.startsWith("ARTEX_") || ["OPENAI_API_KEY", "ANTHROPIC_API_KEY"].includes(key)) delete env[key];
}

const test = base.extend({
  desktop: async ({}, use, testInfo) => {
    const home = fs.mkdtempSync(path.join(os.tmpdir(), "ARTEX ChatGPT 한글 #%-"));
    const diagnostics = { home, stdout: "", stderr: "", exit: null, pageErrors: [], events: [], status: null };
    let electron;
    let page;
    try {
      electron = await _electron.launch({ args: [path.resolve(__dirname, ".."), `--artex-home=${home}`], env, chromiumSandbox: true, timeout: 45_000 });
      const child = electron.process();
      child.stdout.on("data", (chunk) => { diagnostics.stdout += chunk.toString(); });
      child.stderr.on("data", (chunk) => { diagnostics.stderr += chunk.toString(); });
      child.on("exit", (code, signal) => { diagnostics.exit = { code, signal }; });
      page = await electron.firstWindow();
      page.on("pageerror", (error) => diagnostics.pageErrors.push(error.message));
      page.on("close", () => diagnostics.events.push("page-closed"));
      await expect.poll(async () => {
        if (page.isClosed()) throw new Error(`Electron 창이 준비 전에 닫혔습니다: ${JSON.stringify(diagnostics)}`);
        try { diagnostics.status = await page.evaluate(() => window.artexDesktop?.status()); }
        catch (error) {
          if (page.isClosed() || !error.message.includes("Execution context was destroyed")) throw error;
          return false;
        }
        return !!diagnostics.status && !["starting", "navigating"].includes(diagnostics.status.state);
      }, { timeout: 40_000 }).toBe(true);
      expect(diagnostics.status).toMatchObject({ state: "ready" });
      await page.waitForURL(/\/function\/tasks\/?$/);
      await expect(page.locator('[data-slot="sidebar"]')).toBeVisible();
      expect(await page.evaluate(() => Object.keys(window.artexDesktop).sort())).toEqual(["backupStatus","checkUpdate","createBackup","downloadUpdate","installUpdate","openChatGPTLogin","openChatGPTUsage","openRestoredHome","quit","restoreBackup","retry","setAutomaticBackup","status","updateStatus"]);
      expect(await electron.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].webContents.getLastWebPreferences())).toMatchObject({ sandbox: true, contextIsolation: true, nodeIntegration: false, webSecurity: true });

      // The only stub in the real API test is this OS-browser boundary. It never
      // opens a browser or sends an OAuth request. Public authorization URLs stay
      // in test-owned main-process memory and only sanitized metadata is attached.
      await electron.evaluate(({ shell }) => {
        globalThis.__chatGPTBrowserURLs = [];
        shell.openExternal = async (url) => { globalThis.__chatGPTBrowserURLs.push(url); };
      });
      async function api(route, method = "GET", body, authenticated = true) {
        return page.evaluate(async ({ route, method, body, authenticated }) => {
          const response = await fetch(`/api${route}`, {
            method,
            credentials: "omit",
            headers: { ...(authenticated ? { Authorization: `Bearer ${localStorage.getItem("artex_token")}` } : {}), ...(body !== undefined ? { "Content-Type": "application/json" } : {}) },
            ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
          });
          return { status: response.status, body: await response.json() };
        }, { route, method, body, authenticated });
      }
      async function browserURLs() {
        return electron.evaluate(() => globalThis.__chatGPTBrowserURLs.map((value) => {
          const url = new URL(value);
          const q = url.searchParams;
          const redirect = q.get("redirect_uri");
          const callback = redirect ? new URL(redirect) : null;
          return {
            origin: url.origin, path: url.pathname, hash: url.hash, credentials: !!(url.username || url.password),
            clientID: q.get("client_id"), agentName: q.get("agent_name_hint"), hostIDPresent: !!q.get("ext_agent_host_id"),
            responseType: q.get("response_type"), resource: q.get("resource"), scopes: (q.get("scope") ?? "").split(" "),
            challengeMethod: q.get("code_challenge_method"), challengeValid: /^[A-Za-z0-9_-]{43}$/.test(q.get("code_challenge") ?? ""),
            stateValid: /^[A-Za-z0-9_-]{43}$/.test(q.get("state") ?? ""), nonceValid: /^[A-Za-z0-9_-]{43}$/.test(q.get("nonce") ?? ""),
            independentNonce: q.get("state") !== q.get("nonce"), verifierAbsent: !q.has("code_verifier"),
            prohibitedFields: [...q.keys()].filter((key) => ["access_token", "refresh_token", "id_token", "id_token_hint"].includes(key.toLowerCase())),
            callback: callback ? { origin: callback.origin, protocol: callback.protocol, hostname: callback.hostname, port: callback.port, path: callback.pathname, search: callback.search, hash: callback.hash } : null,
          };
        }));
      }
      await use({ electron, page, api, browserURLs, home, diagnostics });
      expect(diagnostics.pageErrors).toEqual([]);
    } finally {
      if (electron) await electron.close().catch((error) => diagnostics.events.push(`cleanup: ${error.message}`));
      const logPath = testInfo.outputPath("chatgpt-process.json");
      fs.writeFileSync(logPath, JSON.stringify(diagnostics, null, 2));
      fs.writeFileSync(testInfo.outputPath("temporary-home.txt"), home);
      await testInfo.attach("chatgpt-process", { path: logPath, contentType: "application/json" });
      // Preserve test evidence and its isolated SQLite. Never use user data.
    }
  },
});

test("실제 Electron·Go ChatGPT 연결 시작/취소와 인증·IPC 경계 (OAuth 완료 없음)", async ({ desktop }, testInfo) => {
  const { electron, page, api, browserURLs, diagnostics } = desktop;
  const origin = diagnostics.status.url;
  expect(await api("/chatgpt/status")).toEqual({ status: 200, body: { connected: false, pending: false, sharing: false } });
  for (const [route, method, body] of [["/chatgpt/status", "GET"], ["/chatgpt/login", "POST", {}], ["/chatgpt/cancel", "POST", {}], ["/chatgpt/models", "GET"], ["/chatgpt/profile", "POST", { model: "local-fixture" }]]) {
    expect((await api(route, method, body, false)).status, `${method} ${route} requires an ARTEX JWT`).toBe(401);
  }
  expect((await api("/chatgpt/models")).status).toBe(409);
  expect((await api("/chatgpt/profile", "POST", { model: "local-fixture" })).status).toBe(409);
  const jwt = await page.evaluate(() => localStorage.getItem("artex_token"));
  expect((await fetch(`${origin}/api/chatgpt/status`, { headers: { Authorization: `Bearer ${jwt}` }, signal: AbortSignal.timeout(5_000) })).status).toBe(403);

  await page.getByRole("link", { name: "LLM", exact: true }).click();
  const card = page.locator("#chatgpt-subscription");
  await expect(card.getByRole("button", { name: "Continue with ChatGPT", exact: true })).toBeEnabled();
  await expect(card.getByLabel("ChatGPT 구독 모델", { exact: true })).toHaveCount(0);
  await expect(card.locator('input[type="password"]')).toHaveCount(0);
  await expect(card.getByLabel("API Key", { exact: true })).toHaveCount(0);
  expect(await browserURLs()).toEqual([]);
  await card.getByRole("button", { name: "Continue with ChatGPT", exact: true }).click();
  await expect(card.getByText("승인 대기 중", { exact: true })).toBeVisible();
  await expect(card.getByRole("button", { name: "Continue with ChatGPT", exact: true })).toBeDisabled();
  await expect(card.getByRole("button", { name: "연결 취소", exact: true })).toBeEnabled();
  expect((await api("/chatgpt/status")).body).toEqual({ connected: false, pending: true, sharing: false });
  const captured = await browserURLs();
  expect(captured).toHaveLength(1);
  expect(captured[0]).toMatchObject({
    origin: "https://auth.openai.com", path: "/api/accounts/authorize", hash: "", credentials: false,
    clientID: "dynamic_agent_client", agentName: "ARTEX", hostIDPresent: true, responseType: "code", resource: "https://api.openai.com/v1",
    scopes: expect.arrayContaining(["openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"]),
    challengeMethod: "S256", challengeValid: true, stateValid: true, nonceValid: true, independentNonce: true,
    verifierAbsent: true, prohibitedFields: [], callback: { protocol: "http:", hostname: "127.0.0.1", path: "/auth/callback", search: "", hash: "" },
  });
  expect(captured[0].callback.port).not.toBe(new URL(origin).port);
  const callback = `${captured[0].callback.origin}/auth/callback`;
  expect((await fetch(callback, { signal: AbortSignal.timeout(5_000) })).status).toBe(400);
  await page.screenshot({ path: testInfo.outputPath("chatgpt-real-begin-pending.png") });
  await card.getByRole("button", { name: "연결 취소", exact: true }).click();
  await expect(card.getByText("연결 필요", { exact: true })).toBeVisible();
  expect((await api("/chatgpt/status")).body).toEqual({ connected: false, pending: false, sharing: false });
  await expect.poll(async () => {
    try { await fetch(callback, { signal: AbortSignal.timeout(500) }); return false; }
    catch { return true; }
  }).toBe(true);

  const denied = [
    "http://auth.openai.com/api/accounts/authorize", "https://auth.openai.com.evil.test/api/accounts/authorize",
    "https://auth.openai.com/api/accounts/authorize/extra", "https://auth.openai.com:444/api/accounts/authorize",
    "https://user@auth.openai.com/api/accounts/authorize", "https://auth.openai.com/api/accounts/authorize#fixture",
    "https://auth.openai.com/api/accounts/authorize?access_token=fixture", "https://auth.openai.com/api/accounts/authorize?refresh_token=fixture",
    "https://auth.openai.com/api/accounts/authorize?id_token=fixture", "https://auth.openai.com/api/accounts/authorize?ID_TOKEN_HINT=fixture",
    "file:///C:/fixture.txt", "not-a-url", null,
  ];
  for (const url of denied) {
    await expect(page.evaluate((value) => window.artexDesktop.openChatGPTLogin(value), url)).rejects.toThrow(/ChatGPT 인증 브라우저를 열 수 없습니다/);
  }
  expect(await browserURLs()).toHaveLength(1);

  // A sandboxed second window deliberately loads the trusted backend origin.
  // Its actual IPC sender frame still must not impersonate the app's main frame.
  await electron.evaluate(async ({ BrowserWindow }, { origin, preload }) => {
    const foreign = new BrowserWindow({ show: false, webPreferences: { preload, sandbox: true, contextIsolation: true, nodeIntegration: false, webSecurity: true } });
    globalThis.__chatGPTForeignWindow = foreign;
    await foreign.loadURL(`${origin}/function/tasks/`);
  }, { origin, preload: path.resolve(__dirname, "../src/preload.cjs") });
  try {
    await expect.poll(() => electron.windows().length).toBe(2);
    const foreign = electron.windows().find((window) => window !== page);
    expect(foreign).toBeTruthy();
    expect(await foreign.evaluate(() => typeof window.artexDesktop.openChatGPTLogin)).toBe("function");
    await expect(foreign.evaluate(() => window.artexDesktop.openChatGPTLogin("https://auth.openai.com/api/accounts/authorize?client_id=fixture"))).rejects.toThrow(/허용되지 않은 IPC 호출자/);
    await expect(foreign.evaluate(() => window.artexDesktop.openChatGPTUsage())).rejects.toThrow(/허용되지 않은 IPC 호출자/);
    for (const method of ["updateStatus", "checkUpdate", "downloadUpdate", "installUpdate"]) {
      await expect(foreign.evaluate((method) => window.artexDesktop[method](), method)).rejects.toThrow(/허용되지 않은 IPC 호출자/);
    }
  } finally {
    await electron.evaluate(() => { globalThis.__chatGPTForeignWindow.destroy(); delete globalThis.__chatGPTForeignWindow; });
  }
  expect(await browserURLs()).toHaveLength(1);
  expect(await page.evaluate(() => Object.keys(localStorage).filter((key) => /access_token|refresh_token|id_token|oauth/i.test(key)))).toEqual([]);
  expect((await api("/llm/profiles")).body.profiles).toEqual([]);
  expect(await page.evaluate(() => window.artexDesktop.status())).toMatchObject({ state: "ready" });
  await testInfo.attach("real-api-and-ipc-scope", { body: JSON.stringify({ source: "actual Electron and Go; shell.openExternal stub only", oauthCompleted: false, browserOpened: false, authorization: captured[0], maliciousURLsRejected: denied.length, foreignSenderFrameRejected: true }, null, 2), contentType: "application/json" });
});

test("명시적 page.route fixture: 동의·첫 안내·모델 순서·활성화·해제 화면 (OAuth 완료 아님)", async ({ desktop }, testInfo) => {
  const { page, electron, api, browserURLs, diagnostics } = desktop;
  const origin = diagnostics.status.url;
  const state = {
    status: { connected: true, pending: false, sharing: false, email: "local-ui-fixture@example.test" },
    models: [{ slug: "fixture-model-z", display_name: "Fixture 모델 Z" }, { slug: "fixture-model-a", display_name: "Fixture 모델 A" }],
    profiles: [{ id: "1", name: "기존 API 방식 설정", format: "openai", auth_method: "api-key", model: "fixture-api", api_key_hint: "****fixture", is_default: true, rate_per_second: 0, rate_per_minute: 0 }],
    calls: [], modelsFail: false, logoutCount: 0,
  };
  const fixture = "**/api/chatgpt/**";
  await page.route(fixture, async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    state.calls.push({ method: request.method(), path: pathname });
    if (pathname === "/api/chatgpt/status") return route.fulfill({ json: state.status });
    if (pathname === "/api/chatgpt/models") return route.fulfill(state.modelsFail ? { status: 429, json: { error: "fixture: ChatGPT 구독 사용량 제한" } } : { json: { models: state.models } });
    if (pathname === "/api/chatgpt/profile" && request.method() === "POST") {
      const body = request.postDataJSON();
      expect(body).toEqual({ model: "fixture-model-a" });
      state.profiles = [...state.profiles.map((profile) => ({ ...profile, is_default: false })), { id: "2", name: "ChatGPT 구독 · Fixture 모델 A", format: "openai-responses", auth_method: "chatgpt", model: body.model, is_default: true, rate_per_second: 0, rate_per_minute: 0 }];
      return route.fulfill({ json: { id: 2, ok: true } });
    }
    if (pathname === "/api/chatgpt/logout" && request.method() === "POST") {
      state.logoutCount++;
      state.status = { connected: false, pending: false, sharing: false };
      return route.fulfill({ json: { ok: true } });
    }
    return route.fulfill({ status: 500, json: { error: "fixture에서 승인하지 않은 ChatGPT 요청입니다" } });
  });
  await page.route("**/api/llm/profiles", (route) => route.request().method() === "GET" ? route.fulfill({ json: { profiles: state.profiles } }) : route.fulfill({ status: 500, json: { error: "fixture: 일반 API 프로필 편집은 허용하지 않습니다" } }));
  await page.goto(`${origin}/system/llm/`);
  const card = page.locator("#chatgpt-subscription");
  await expect(card.getByRole("alert")).toContainText("구독 모델 사용 권한이 없습니다");
  await expect(card.getByRole("button", { name: "Continue with ChatGPT", exact: true })).toBeEnabled();
  await expect(card.getByLabel("ChatGPT 구독 모델", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("dialog", { name: "ChatGPT 구독이 연결되었습니다", exact: true })).toHaveCount(0);
  expect(state.calls.filter((call) => call.path === "/api/chatgpt/models")).toEqual([]);

  state.status = { ...state.status, sharing: true };
  await page.reload();
  const welcome = page.getByRole("dialog", { name: "ChatGPT 구독이 연결되었습니다", exact: true });
  await expect(welcome).toBeVisible();
  await expect(welcome).toContainText("사용량과 크레딧");
  await page.screenshot({ path: testInfo.outputPath("chatgpt-fixture-first-welcome.png") });
  await welcome.getByRole("button", { name: "확인", exact: true }).click();
  await expect(welcome).toHaveCount(0);
  const model = card.getByLabel("ChatGPT 구독 모델", { exact: true });
  await expect(model).toBeEnabled();
  await model.click();
  await expect(page.getByRole("option")).toHaveText(["Fixture 모델 Z", "Fixture 모델 A"]);
  await page.getByRole("option", { name: "Fixture 모델 A", exact: true }).click();
  await card.getByRole("button", { name: "선택 모델 활성화", exact: true }).click();
  await expect(card.getByRole("button", { name: "활성화됨", exact: true })).toBeDisabled();
  await expect(card.getByText("ChatGPT 구독 사용 중", { exact: true })).toBeVisible();
  const subscriptionProfile = page.locator('[data-slot="card"][role="button"]').filter({ hasText: "ChatGPT 구독 · Fixture 모델 A" });
  await expect(subscriptionProfile.getByText("구독 연결됨", { exact: true })).toBeVisible();
  await subscriptionProfile.getByText("ChatGPT 구독 · Fixture 모델 A", { exact: true }).click();
  await expect(card).toBeFocused();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(subscriptionProfile.getByText("Key 미설정", { exact: true })).toHaveCount(0);
  await expect(subscriptionProfile.getByText("0/s · 0/min", { exact: true })).toHaveCount(0);
  await expect(card.getByLabel("API Key", { exact: true })).toHaveCount(0);
  await expect(card.getByLabel("Base URL(선택)", { exact: true })).toHaveCount(0);
  expect(state.calls.filter((call) => call.path === "/api/chatgpt/profile")).toEqual([{ method: "POST", path: "/api/chatgpt/profile" }]);

  state.modelsFail = true;
  await card.getByRole("button", { name: "ChatGPT 모델 목록 새로고침", exact: true }).click();
  await expect(card.getByRole("alert")).toContainText("fixture: ChatGPT 구독 사용량 제한");
  await expect(model).toBeDisabled();
  await expect(card.getByRole("button", { name: "선택 모델 활성화", exact: true })).toBeDisabled();
  await card.getByRole("button", { name: "ChatGPT 사용량 관리", exact: true }).click();
  expect((await browserURLs()).map(({ origin, path, hash }) => ({ origin, path, hash }))).toEqual([{ origin: "https://chatgpt.com", path: "/", hash: "#settings/Usage" }]);
  state.modelsFail = false;
  await card.getByRole("button", { name: "ChatGPT 모델 목록 새로고침", exact: true }).click();
  await expect(model).toBeEnabled();
  await expect(card.getByRole("alert")).toHaveCount(0);

  await page.reload();
  await expect(model).toBeEnabled();
  await expect(welcome).toHaveCount(0);
  expect(await page.evaluate(() => localStorage.getItem("artex_chatgpt_welcome_seen"))).toBe("1");
  await electron.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].setContentSize(1280, 900));
  await page.screenshot({ path: testInfo.outputPath("chatgpt-fixture-ready-light-1280.png") });
  await page.evaluate(() => { document.cookie = "theme_mode=dark; path=/"; });
  await page.reload();
  await expect(model).toBeEnabled();
  await electron.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].setContentSize(1440, 960));
  await page.screenshot({ path: testInfo.outputPath("chatgpt-fixture-ready-dark-1440.png") });
  await card.getByRole("button", { name: "ChatGPT 로그아웃", exact: true }).click();
  await expect(card.getByRole("button", { name: "Continue with ChatGPT", exact: true })).toBeEnabled();
  await expect(model).toHaveCount(0);
  await expect(subscriptionProfile.getByText("구독 연결 필요", { exact: true })).toBeVisible();
  expect(state.logoutCount).toBe(1);
  expect(await page.evaluate(() => localStorage.getItem("artex_token"))).toBeTruthy();
  expect(page.url()).toContain("/system/llm/");

  await page.unroute(fixture);
  await page.unroute("**/api/llm/profiles");
  expect((await api("/chatgpt/status")).body).toEqual({ connected: false, pending: false, sharing: false });
  expect((await api("/llm/profiles")).body.profiles).toEqual([]);
  await testInfo.attach("ui-fixture-scope", { body: JSON.stringify({ source: "explicit Playwright page.route fixtures only", oauthCompleted: false, backendCredentialsCreated: false, backendProfilesCreated: false, browserOpened: false, modelOrder: state.models.map(({ slug }) => slug), requests: state.calls }, null, 2), contentType: "application/json" });
});
