const { test, expect, _electron } = require("@playwright/test");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const http = require("node:http");
const { spawn } = require("node:child_process");
const { assertAccessibleControls } = require("./ui-accessibility.cjs");
const isolatedEnvironment = { ...process.env };
for (const key of Object.keys(isolatedEnvironment)) {
  if (key.startsWith("ARTEX_") || ["OPENAI_API_KEY", "ANTHROPIC_API_KEY"].includes(key)) delete isolatedEnvironment[key];
}

async function localFixture() {
  const state = { calls: [], errors: [], reported: false, assetId: null };
  const evidence = "ARTEX 로컬 HTTP 증거 시작\n" + "local-response-evidence\n".repeat(15_000) + "ARTEX 로컬 HTTP 증거 끝";
  const server = http.createServer(async (request, response) => {
    if (request.url === "/fixture-evidence") {
      response.writeHead(200, { "Content-Type": "text/plain; charset=utf-8" });
      response.end(evidence);
      return;
    }
    try {
      const chunks = [];
      for await (const chunk of request) chunks.push(chunk);
      const input = JSON.parse(Buffer.concat(chunks).toString("utf8"));
      const names = (input.tools ?? []).map((tool) => tool.function?.name);
      state.calls.push({ path: request.url, tools: names, streaming: input.stream });
      let message = { role: "assistant", content: "로컬 검증을 완료했습니다." };
      if (JSON.stringify(input.messages).includes("desktop-record-local-fixture") && !state.reported) {
        if (!names.includes("report_finding")) state.errors.push("실제 주 에이전트에 report_finding 도구가 없습니다");
        state.reported = true;
        message = { role: "assistant", content: null, tool_calls: [{ id: "local-finding-call", type: "function", function: { name: "report_finding", arguments: JSON.stringify({ vulnclass: "로컬 fixture 응답", name: "로컬 HTTP 증거 검증", severity: "low", summary: "검사 전용 로컬 HTTP 응답을 실제 요청하고 저장했습니다.", evidence: "GET /fixture-evidence → 200. 실제 응답: ARTEX 로컬 HTTP 증거 시작. 공개 대상은 호출하지 않았습니다.", asset_ids: [state.assetId] }) } }] };
      }
      response.writeHead(200, { "Content-Type": "application/json" });
      response.end(JSON.stringify({ id: "local-completion", object: "chat.completion", created: 1, model: "local-fixture", choices: [{ index: 0, message, finish_reason: message.tool_calls ? "tool_calls" : "stop" }], usage: { prompt_tokens: 10, completion_tokens: 10, total_tokens: 20 } }));
    } catch (error) { state.errors.push(error.message); response.writeHead(500); response.end("로컬 fixture 오류"); }
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  return { state, evidence, origin: `http://127.0.0.1:${server.address().port}`, close: () => new Promise((resolve) => server.close(resolve)) };
}

async function captureLocalRequest(url) {
  return new Promise((resolve, reject) => {
    const request = http.get({ hostname: "127.0.0.1", port: 8788, path: url, headers: { Host: new URL(url).host } }, (response) => {
      const chunks = [];
      response.on("data", (chunk) => chunks.push(chunk));
      response.on("end", () => resolve({ status: response.statusCode, body: Buffer.concat(chunks).toString("utf8") }));
    });
    request.on("error", reject);
    request.setTimeout(10_000, () => request.destroy(new Error("로컬 프록시 요청 제한 시간 초과")));
  });
}

test("실제 Electron 자동 진입·모델 저장·재시작·화면·격리", async ({}, testInfo) => {
  test.setTimeout(300_000);
  const home = fs.mkdtempSync(path.join(os.tmpdir(), "ARTEX 한글 #%-"));
  const desktop = path.resolve(__dirname, "..");
  const password = "로컬검증-12345678";
  const fixture = await localFixture();
  let electron;
  let page;
  const pageErrors = [];
  const launches = [];
  fs.writeFileSync(testInfo.outputPath("temporary-home.txt"), home);
  async function api(route, method = "GET", body) {
    return page.evaluate(async ({ route, method, body }) => {
      const response = await fetch(`/api${route}`, { method, headers: { Authorization: `Bearer ${localStorage.getItem("artex_token")}`, ...(body ? { "Content-Type": "application/json" } : {}) }, ...(body ? { body: JSON.stringify(body) } : {}) });
      const data = await response.json();
      if (!response.ok) throw new Error(`${method} ${route}: ${response.status} ${JSON.stringify(data)}`);
      return data;
    }, { route, method, body });
  }
  async function launch() {
    electron = await _electron.launch({ args: [desktop, `--artex-home=${home}`], env: isolatedEnvironment, chromiumSandbox: true, timeout: 45_000 });
    const diagnostics = { phase: launches.length ? "restart" : "initial", stdout: "", stderr: "", exit: null, events: [], status: null };
    launches.push(diagnostics);
    const child = electron.process();
    child.stdout.on("data", (chunk) => { diagnostics.stdout += chunk.toString(); });
    child.stderr.on("data", (chunk) => { diagnostics.stderr += chunk.toString(); });
    child.on("exit", (code, signal) => { diagnostics.exit = { code, signal }; });
    page = await electron.firstWindow();
    page.on("pageerror", (error) => pageErrors.push(error.message));
    page.on("close", () => diagnostics.events.push({ event: "page-closed", at: Date.now() }));
    page.on("crash", () => diagnostics.events.push({ event: "page-crashed", at: Date.now() }));
    page.on("framenavigated", (frame) => { if (frame === page.mainFrame()) diagnostics.events.push({ event: "navigation", url: frame.url(), at: Date.now() }); });
    let startup;
    await expect.poll(async () => {
      if (page.isClosed()) throw new Error(`${diagnostics.phase} Electron 창이 준비 전에 닫혔습니다: ${JSON.stringify(diagnostics)}`);
      try { startup = await page.evaluate(() => window.artexDesktop?.status()); }
      catch (error) {
        if (page.isClosed() || !error.message.includes("Execution context was destroyed")) throw error;
        return false;
      }
      diagnostics.status = startup;
      return startup && !["starting", "navigating"].includes(startup.state);
    }, { timeout: 40_000 }).toBe(true);
    expect(startup).toMatchObject({ state: "ready" });
    await page.waitForURL(/^http:\/\/127\.0\.0\.1:\d+\//);
  }
  async function enteredApp() {
    await page.waitForURL(/\/function\/tasks\/?$/);
    await expect(page.locator('[data-slot="sidebar"]')).toBeVisible();
    await expect(page.getByRole("heading", { name: "로그인", exact: true })).toHaveCount(0);
    await expect(page.getByRole("heading", { name: "비밀번호 초기 설정", exact: true })).toHaveCount(0);
    await expect(page.locator('input[type="password"]')).toHaveCount(0);
    expect((await page.evaluate(() => localStorage.getItem("artex_token"))).split(".")).toHaveLength(3);
    expect(await api("/auth/status")).toMatchObject({ mode: "desktop" });
    expect(await api("/runtime/tools")).toMatchObject({ ready: true, mode: "desktop", components: expect.arrayContaining([expect.objectContaining({ key: "shell", state: "verified", execution: "available" }), expect.objectContaining({ key: "pty", state: "verified", execution: "available" }), expect.objectContaining({ key: "browser", state: "verified", execution: "available" })]), isolation: { state: "available", process_tree: "job_object", workspace: "appcontainer_acl", network: "denied" } });
  }
  try {
    await launch();
    await enteredApp();
    const fontState = await page.evaluate(async () => {
      await document.fonts.ready;
      return { faces: [...document.fonts].map(({ family, status }) => ({ family, status })), families: [...new Set([...document.querySelectorAll("body, button, input, textarea, code, pre, kbd, h1, h2, p, span")].map((node) => getComputedStyle(node).fontFamily))] };
    });
    expect(fontState.faces).toHaveLength(1);
    expect(fontState.faces[0]).toMatchObject({ family: expect.stringMatching(/pretendard/i), status: "loaded" });
    expect(fontState.families).toHaveLength(1);
    expect(fontState.families[0]).toMatch(/pretendard/i);
    const cdp = await page.context().newCDPSession(page);
    await cdp.send("DOM.enable");
    await cdp.send("CSS.enable");
    const { root } = await cdp.send("DOM.getDocument");
    const { nodeId } = await cdp.send("DOM.querySelector", { nodeId: root.nodeId, selector: '[data-slot="sidebar-content"] [data-slot="sidebar-menu-button"] span' });
    const { fonts } = await cdp.send("CSS.getPlatformFontsForNode", { nodeId });
    expect(fonts).toHaveLength(1);
    expect(fonts[0]).toMatchObject({ familyName: expect.stringMatching(/Pretendard/), isCustomFont: true });
    expect(fonts[0].glyphCount).toBeGreaterThan(0);
    await cdp.detach();
    await testInfo.attach("rendered-font", { body: JSON.stringify({ ...fontState, platformFonts: fonts }, null, 2), contentType: "application/json" });
    await page.getByRole("button", { name: "화면 설정", exact: true }).click();
    await expect(page.getByText("글꼴", { exact: true })).toHaveCount(0);
    await page.keyboard.press("Escape");
    expect(await api("/auth/status")).toEqual({ initialized: false, mode: "desktop" });
    await expect(page.locator('[data-slot="sidebar-footer"]')).toHaveCount(0);
    await expect(page.locator('header [data-slot="avatar"]')).toHaveCount(0);
    await expect(page.getByText("로컬 데스크톱", { exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "비밀번호 변경", exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "로그아웃", exact: true })).toHaveCount(0);
    const initialOrigin = new URL(page.url()).origin;
    await page.evaluate(() => { localStorage.setItem("artex_token", "invalid-old-token"); document.cookie = "artex_token=invalid-old-token; path=/"; });
    await page.goto(`${initialOrigin}/login/`);
    await enteredApp();
    await page.evaluate(() => { localStorage.removeItem("artex_token"); document.cookie = "artex_token=; path=/; max-age=0"; });
    await page.goto(`${initialOrigin}/setup/`);
    await enteredApp();
    await page.route("**/api/auth/desktop-session", (route) => route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "검사 전용 앱 인증 실패" }) }));
    await page.goto(`${initialOrigin}/function/tasks/`);
    await expect(page.getByRole("alert").filter({ hasText: "검사 전용 앱 인증 실패" })).toBeVisible();
    await expect(page.locator('input[type="password"]')).toHaveCount(0);
    await page.unroute("**/api/auth/desktop-session");
    await page.getByRole("button", { name: "다시 시도", exact: true }).click();
    await enteredApp();
    const unauthenticated = await page.evaluate(async () => (await fetch("/api/stats", { credentials: "omit" })).status);
    expect(unauthenticated).toBe(401);
    await enteredApp();
    await page.getByRole("link", { name: "LLM", exact: true }).click();
    await page.getByRole("button", { name: "생성", exact: true }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("이름", { exact: true }).fill("로컬 저장 검증");
    await dialog.getByLabel("모델", { exact: true }).fill("local-fixture");
    await dialog.getByLabel("Base URL(선택)", { exact: true }).fill("http://127.0.0.1:1");
    await dialog.getByLabel("API Key", { exact: true }).fill("temporary-local-fixture");
    await dialog.getByRole("button", { name: "생성", exact: true }).click();
    await expect(page.getByText("로컬 저장 검증", { exact: true })).toBeVisible();
    await expect(dialog).toHaveCount(0);
    await page.screenshot({ path: testInfo.outputPath("model-saved.png") });
    const profiles = await api("/llm/profiles");
    const profile = profiles.profiles.find((item) => item.name === "로컬 저장 검증");
    await api("/llm/profiles", "POST", { id: Number(profile.id), name: profile.name, format: "openai", model: "local-fixture", base_url: fixture.origin, api_key: "temporary-local-fixture", streaming: false });
    await api("/settings", "PUT", { traffic_capture: true, llm_record: true });
    const task = await api("/tasks", "POST", { name: "한글 로컬 작업 검증", description: "검사 전용 로컬 HTTP 증거", goal: `${fixture.origin}/fixture-evidence 응답을 확인합니다.`, llm_profile_ids: [Number(profile.id)], seed_first_intent: false, coverage_enabled: true });
    expect(await api(`/tasks/${task.id}/control`, "POST", { action: "pause" })).toMatchObject({ paused: true });
    const inserted = await api("/assets", "POST", { assets: [{ type: "service", url: fixture.origin, page_title: "로컬 검증 서비스", technologies: ["fixture"] }, { type: "app", app_name: "삭제 검증용 자산", bundle_id: "local.fixture.removable" }] });
    expect(inserted.errors).toBeNull();
    const assetId = inserted.results[0].id;
    const removableId = inserted.results[1].id;
    fixture.state.assetId = assetId;
    await api(`/tasks/${task.id}/assets`, "POST", { asset_ids: [assetId], source_summary: "로컬 HTTP fixture 연결" });
    expect((await api(`/assets?task_id=${task.id}&limit=50&offset=0`)).assets.some((item) => Number(item.id) === assetId)).toBe(true);
    await api(`/tasks/${task.id}/assets/${assetId}`, "DELETE");
    expect((await api(`/assets?task_id=${task.id}&limit=50&offset=0`)).assets.some((item) => Number(item.id) === assetId)).toBe(false);
    await api(`/tasks/${task.id}/assets`, "POST", { asset_ids: [assetId], source_summary: "보관 검증에 사용할 자산" });
    const updated = await api("/assets", "POST", { assets: [{ type: "service", url: fixture.origin, page_title: "한글 수정 검증 서비스" }] });
    expect(updated.results[0].id).toBe(assetId);
    expect(await api("/assets", "DELETE", { ids: [removableId] })).toMatchObject({ deleted: 1 });
    const captured = await captureLocalRequest(`${fixture.origin}/fixture-evidence`);
    expect(captured).toEqual({ status: 200, body: fixture.evidence });
    let traffic;
    await expect.poll(async () => { traffic = await api(`/traffic?page=0&size=20&host=127.0.0.1&q=fixture-evidence`); return traffic.exchanges?.length; }).toBe(1);
    const trafficId = traffic.exchanges[0].id;
    expect(await api(`/traffic/exchange?id=${trafficId}`)).toMatchObject({ resp: expect.stringContaining("ARTEX 로컬 HTTP 증거 시작") });
    expect(await api(`/chat?task=${task.id}`, "POST", { message: "desktop-record-local-fixture: 방금 검증한 로컬 HTTP 응답만 검사 기록으로 저장하세요." })).toMatchObject({ mode: "llm" });
    let finding;
    await expect.poll(async () => { const findings = await api(`/exploration/findings?task=${task.id}`); finding = findings.find((item) => item.name === "로컬 HTTP 증거 검증"); return Boolean(finding); }, { timeout: 30_000 }).toBe(true);
    await expect.poll(async () => (await api(`/tasks/${task.id}/chat/status`)).running).toBe(false);
    expect(fixture.state.errors).toEqual([]);
    const findingId = finding.finding_id;
    await api(`/exploration/findings/${findingId}`, "PATCH", { status: "confirmed", name: "한글 증거 보관 검증" });
    const binding = await api(`/exploration/findings/${findingId}/traffic`, "POST", { traffic_refs: [{ traffic_id: trafficId, role: "proof", note: "실제 로컬 HTTP 응답" }] });
    expect(binding.bindings).toHaveLength(1);
    const bindingId = binding.bindings[0].id;
    expect((await api(`/exploration/findings/${findingId}/traffic/${bindingId}/body?side=response&offset=0`)).content).toContain("ARTEX 로컬 HTTP 증거 시작");
    const archive = await api(`/tasks/${task.id}/archive`, "POST");
    await expect.poll(async () => { const result = await api(`/task-archives/${archive.id}`); if (result.state.endsWith("failed")) throw new Error(JSON.stringify(result)); return result.state; }, { timeout: 40_000 }).toBe("ready");
    expect((await api("/tasks")).tasks.some((item) => item.id === task.id)).toBe(false);
    await api(`/task-archives/${archive.id}/restore`, "POST");
    await expect.poll(async () => {
      if ((await api("/tasks")).tasks.some((item) => item.id === task.id)) return true;
      const result = await api(`/task-archives/${archive.id}`);
      if (result.state.endsWith("failed")) throw new Error(JSON.stringify(result));
      return false;
    }, { timeout: 40_000 }).toBe(true);
    await expect.poll(async () => (await api(`/exploration/findings/${findingId}/traffic`)).bindings.length).toBe(1);
    expect((await api(`/exploration/findings/${findingId}/traffic/${bindingId}/body?side=response&offset=0`)).content).toContain("ARTEX 로컬 HTTP 증거 시작");
    await testInfo.attach("local-model-calls", { body: JSON.stringify(fixture.state.calls, null, 2), contentType: "application/json" });

    await page.getByRole("link", { name: "대화", exact: true }).click();
    const composer = page.getByRole("textbox", { name: "메시지 입력, @로 기록 참조", exact: true });
    await expect(composer).toBeEnabled();
    await composer.fill("한글 입력 조합 중");
    const prematurePosts = [];
    const recordPost = (request) => { if (request.method() === "POST") prematurePosts.push(new URL(request.url()).pathname); };
    page.on("request", recordPost);
    await composer.dispatchEvent("keydown", { key: "Enter", code: "Enter", keyCode: 229, isComposing: true });
    await expect(composer).toHaveValue("한글 입력 조합 중");
    page.off("request", recordPost);
    expect(prematurePosts).toEqual([]);

    expect(await page.evaluate(() => ({ require: typeof window.require, process: typeof window.process }))).toEqual({ require: "undefined", process: "undefined" });
    expect(await page.evaluate(() => Object.keys(window.artexDesktop).sort())).toEqual(["backupStatus","checkUpdate","createBackup","downloadUpdate","installUpdate","openChatGPTLogin","openChatGPTUsage","openRestoredHome","quit","restoreBackup","retry","setAutomaticBackup","status","updateStatus"]);
    const preferences = await electron.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].webContents.getLastWebPreferences());
    expect(preferences).toMatchObject({ sandbox: true, contextIsolation: true, nodeIntegration: false, webSecurity: true, webviewTag: false });
    expect(await page.evaluate(() => window.open("https://example.com") === null)).toBe(true);
    expect(electron.windows()).toHaveLength(1);
    const savedUrl = page.url();
    await page.evaluate(() => { window.location.href = "https://example.com"; });
    await expect.poll(() => page.url()).toBe(savedUrl);
    const scriptsBlocked = await page.evaluate(async () => {
      window.inlineCspProbe = false;
      const script = document.createElement("script");
      script.textContent = "window.inlineCspProbe = true";
      document.head.appendChild(script);
      await new Promise((resolve) => requestAnimationFrame(resolve));
      return !window.inlineCspProbe;
    });
    expect(scriptsBlocked).toBe(true);

    const status = await page.evaluate(() => window.artexDesktop.status());
    expect((await fetch(`${status.url}/api/health`)).status).toBe(403);
    const token = await page.evaluate(() => localStorage.getItem("artex_token"));
    expect((await fetch(`${status.url}/api/auth/desktop-session`, { method: "POST", headers: { Authorization: `Bearer ${token}` } })).status).toBe(403);
    await api("/auth/init", "POST", { password });
    expect(await api("/auth/status")).toEqual({ initialized: true, mode: "desktop" });
    const secondExit = await new Promise((resolve, reject) => {
      const second = spawn(require("electron"), [desktop, `--artex-home=${home}`], { env: isolatedEnvironment, stdio: "ignore", windowsHide: true });
      const timer = setTimeout(() => { second.kill(); reject(new Error("두 번째 앱 인스턴스가 종료되지 않았습니다")); }, 10_000);
      second.once("error", (error) => { clearTimeout(timer); reject(error); });
      second.once("exit", (code) => { clearTimeout(timer); resolve(code); });
    });
    expect(secondExit).toBe(0);
    expect((await page.evaluate(() => window.artexDesktop.status())).pid).toBe(status.pid);
    expect(electron.windows()).toHaveLength(1);
    await electron.close();
    electron = null;
    await expect.poll(async () => { try { await fetch(`${status.url}/api/health`, { signal: AbortSignal.timeout(500) }); return false; } catch { return true; } }).toBe(true);
    await launch();
    await enteredApp();
    expect(await api("/auth/status")).toEqual({ initialized: true, mode: "desktop" });
    expect((await api("/auth/login", "POST", { username: "ARTEX", password })).token.split(".")).toHaveLength(3);
    await page.getByRole("link", { name: "LLM", exact: true }).click();
    await expect(page.getByText("로컬 저장 검증", { exact: true })).toBeVisible();
    expect((await api("/tasks")).tasks.find((item) => item.id === task.id)).toMatchObject({ paused: true });
    expect((await api(`/assets?task_id=${task.id}&limit=50&offset=0`)).assets.some((item) => Number(item.id) === assetId)).toBe(true);
    expect((await api(`/exploration/findings/${findingId}/traffic`)).bindings[0].id).toBe(bindingId);
    expect((await api(`/exploration/findings/${findingId}/traffic/${bindingId}/body?side=response&offset=0`)).content).toContain("ARTEX 로컬 HTTP 증거 시작");
    expect(await api(`/traffic/exchange?id=${trafficId}`)).toMatchObject({ resp: expect.stringContaining("ARTEX 로컬 HTTP 증거 시작") });

    const routes = ["dashboard", "chat", "function/tasks", "function/findings", "function/traffic", "function/commands", "function/llm-records", "function/assets", "function/sync", "function/workspace", "system/llm", "system/agents", "system/mcp", "system/skills", "system/tools", "system/notify", "system/intercept", "system/intercept/assets", "system/intercept/approvals", "system/logs", "system/settings"];
    const origin = new URL(page.url()).origin;
    await page.goto(`${origin}/function/tasks/detail/?id=${task.id}`);
    await expect(page.getByText("한글 로컬 작업 검증", { exact: true }).first()).toBeVisible();
    for (const tab of ["세션", "개요", "탐색 경로", "활동 보드", "취약점", "재검증", "테스트 자산", "자산 커버리지 그래프", "차단 승인", "보고서"]) {
      const selectedTab = page.getByRole("tab", { name: tab, exact: true });
      await selectedTab.click();
      await expect(selectedTab).toHaveAttribute("data-state", "active");
      const panel = page.getByRole("tabpanel", { name: tab, exact: true });
      await expect(panel).toBeVisible();
      await expect(panel.locator('[data-slot="skeleton"]')).toHaveCount(0);
      await expect(panel.getByText("불러오는 중…", { exact: true })).toHaveCount(0);
      if (tab === "재검증") await expect(panel.getByText("재검증 기록 없음", { exact: true })).toBeVisible();
      if (tab === "보고서") await expect(panel.getByText("침투 테스트 보고서 — 검사 전용 로컬 HTTP 증거", { exact: true })).toBeVisible();
      if (tab === "자산 커버리지 그래프") {
        await expect(panel.locator("canvas").first()).toBeVisible();
        let previousSignature = "", stableSamples = 0;
        await expect.poll(async () => {
          const signature = await panel.locator("canvas").evaluateAll((canvases) => canvases.map((canvas) => {
            if (!canvas.width || !canvas.height) return "";
            const context = canvas.getContext("2d");
            if (!context) return "";
            const { data } = context.getImageData(0, 0, canvas.width, canvas.height);
            const colors = new Set();
            let hash = 2166136261;
            for (let y = 0; y < canvas.height; y += 16) for (let x = 0; x < canvas.width; x += 16) {
              const index = (y * canvas.width + x) * 4;
              if (data[index + 3]) colors.add(`${data[index]},${data[index + 1]},${data[index + 2]}`);
              for (let channel = 0; channel < 4; channel++) hash = Math.imul(hash ^ data[index + channel], 16777619);
            }
            return colors.size > 1 ? String(hash) : "";
          }).filter(Boolean).join(","));
          stableSamples = signature && signature === previousSignature ? stableSamples + 1 : 0;
          previousSignature = signature;
          return stableSamples >= 2;
        }, { intervals: [100, 100, 100], timeout: 10_000 }).toBe(true);
      }
      await expect.poll(() => selectedTab.evaluate((node) => node.getAnimations().every((animation) => animation.playState === "finished"))).toBe(true);
      await page.screenshot({ path: testInfo.outputPath(`task-detail-${tab}.png`) });
    }
    await page.goto(`${origin}/function/findings/detail/?id=${findingId}`);
    await expect(page.getByText("한글 증거 보관 검증", { exact: true }).first()).toBeVisible();
    await expect(page.getByText("실제 로컬 HTTP 응답", { exact: true })).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath("finding-detail-evidence.png") });
    for (const theme of ["light", "dark"]) {
      for (const width of [1280, 1440]) {
        await electron.evaluate(({ BrowserWindow }, viewport) => { BrowserWindow.getAllWindows()[0].setContentSize(viewport.width, 900); }, { width });
        await page.evaluate((mode) => { document.cookie = `theme_mode=${mode}; path=/`; }, theme);
        for (const route of routes) {
          const errors = [];
          const response = (result) => { if (result.url().includes("/api/") && result.status() >= 400) errors.push(`${result.status()} ${new URL(result.url()).pathname}`); };
          page.on("response", response);
          const runtimeResponse = route === "system/settings"
            ? page.waitForResponse((result) => new URL(result.url()).pathname === "/api/runtime/tools", { timeout: 30_000 })
            : null;
          await page.goto(`${origin}/${route}/`);
          await expect(page.locator('[data-slot="sidebar"]')).toBeVisible();
          await expect(page.locator('[data-slot="sidebar"] a[aria-current="page"]')).toHaveCount(1);
          await expect(page.locator("main").first()).toBeVisible();
          if (route === "system/settings") {
            const runtime = await (await runtimeResponse).json();
            expect(runtime.mode).toBe("desktop");
            expect(runtime.isolation.state).toBe("available");
            expect(runtime.components.map((component) => component.key).sort()).toEqual(["browser", "cli", "node", "pty", "python", "shell"]);
            expect(runtime.components.filter((component) => component.key !== "browser").every((component) => component.execution === "available")).toBe(true);
            const available = runtime.components.filter((component) => component.execution === "available").length;
            const blocked = runtime.components.filter((component) => component.execution !== "available" && component.state === "verified").length;
            expect(runtime.ready).toBe(available === runtime.components.length);
            await expect(page.getByText(runtime.ready ? "실행 환경 준비됨" : "일부 도구 사용 가능", { exact: true })).toBeVisible();
            await expect(page.getByText("사용 가능", { exact: true })).toHaveCount(available);
            await expect(page.getByText("실행 차단", { exact: true })).toHaveCount(blocked);
            await expect(page.getByRole("button", { name: "다시 탐색", exact: true })).toHaveCount(0);
            await expect(page.getByText(runtime.message, { exact: true })).toBeVisible();
            await testInfo.attach(`runtime-tools-${theme}-${width}`, { body: JSON.stringify(runtime, null, 2), contentType: "application/json" });
            await expect(page.getByText("자동 업데이트 미구성", { exact: true })).toBeVisible();
          }
          if (route === "system/mcp") {
            const headers = page.locator('[data-slot="card-header"]');
            await expect(headers.first()).toBeVisible();
            await expect.poll(() => headers.evaluateAll((nodes) => nodes.flatMap((header) => {
              const card = header.closest('[data-slot="card"]').getBoundingClientRect();
              return [...header.querySelectorAll('[role="switch"], button')].filter((control) => {
                const rect = control.getBoundingClientRect();
                return rect.width <= 0 || rect.left < card.left || rect.right > card.right
                  || rect.top < card.top || rect.bottom > card.bottom;
              }).map((control) => control.getAttribute("aria-label"));
            }))).toEqual([]);
          }
          await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
          await assertAccessibleControls(page, testInfo, `${route.replaceAll("/", "-")}-${theme}-${width}`);
          await page.screenshot({ path: testInfo.outputPath(`${route.replaceAll("/", "-")}-${theme}-${width}.png`) });
          page.off("response", response);
          expect(errors, `${route} API 실패`).toEqual([]);
        }
      }
    }
    for (const zoom of [1.25, 1.5]) {
      await electron.evaluate(({ BrowserWindow }, factor) => { const window = BrowserWindow.getAllWindows()[0]; window.setContentSize(1440, 960); window.webContents.setZoomFactor(factor); }, zoom);
      await page.goto(`${origin}/system/llm/`);
      await expect(page.getByText("로컬 저장 검증", { exact: true })).toBeVisible();
      await page.getByRole("button", { name: "생성", exact: true }).click();
      const modelDialog = page.getByRole("dialog");
      await expect(modelDialog).toBeVisible();
      await expect.poll(() => modelDialog.evaluate((node) => {
        const rect = node.getBoundingClientRect();
        return getComputedStyle(node).opacity === "1"
          && node.getAnimations().every((animation) => animation.playState === "finished")
          && rect.left >= -1 && rect.right <= innerWidth + 1
          && rect.top >= -1 && rect.bottom <= innerHeight + 1;
      })).toBe(true);
      expect(await modelDialog.evaluate((node) => node.scrollWidth <= node.clientWidth)).toBe(true);
      await expect(modelDialog.getByLabel("이름", { exact: true })).toBeInViewport({ ratio: 1 });
      await expect(modelDialog.getByRole("combobox").first()).toBeInViewport({ ratio: 1 });
      await expect(modelDialog.getByRole("button", { name: "닫기", exact: true })).toBeInViewport({ ratio: 1 });
      await assertAccessibleControls(page, testInfo, `model-dialog-${zoom}`);
      await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
      // Playwright's viewport screenshot crops Electron zoomed content on this
      // Windows display. Capture the complete actual webContents instead.
      const capture = await electron.evaluate(async ({ BrowserWindow }) => {
        const window = BrowserWindow.getAllWindows()[0];
        const image = await window.webContents.capturePage();
        return { png: image.toPNG().toString("base64"), size: image.getSize(), zoom: window.webContents.getZoomFactor() };
      });
      fs.writeFileSync(testInfo.outputPath(`model-dialog-windows-${zoom}.png`), Buffer.from(capture.png, "base64"));
      fs.writeFileSync(testInfo.outputPath(`model-dialog-windows-${zoom}.json`), JSON.stringify({ size: capture.size, zoom: capture.zoom, capture: "Electron webContents.capturePage" }, null, 2));
      await page.keyboard.press("Escape");
      await expect(page.getByRole("dialog")).toHaveCount(0);
    }
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.goto(`${origin}/system/llm/`);
    const motion = await page.getByRole("button", { name: "생성", exact: true }).evaluate((node) => getComputedStyle(node).transitionDuration);
    expect(motion.split(",").every((value) => Number.parseFloat(value) <= 0.001)).toBe(true);
    expect(pageErrors).toEqual([]);
    fs.writeFileSync(testInfo.outputPath("temporary-home.txt"), home);
  } finally {
    if (page && !page.isClosed()) {
      await page.screenshot({ path: testInfo.outputPath("final-state.png") }).catch(() => {});
      const finalStatus = await page.evaluate(() => window.artexDesktop?.status()).catch(() => null);
      await testInfo.attach("desktop-status", { body: JSON.stringify(finalStatus, null, 2), contentType: "application/json" });
    }
    if (electron) await electron.close();
    const diagnosticsPath = testInfo.outputPath("electron-launch-diagnostics.json");
    fs.writeFileSync(diagnosticsPath, JSON.stringify(launches, null, 2));
    await testInfo.attach("electron-launch-diagnostics", { path: diagnosticsPath, contentType: "application/json" });
    await fixture.close();
    // 실패 증거와 임시 SQLite를 남겨 검사할 수 있게 한다. 사용자 데이터는 사용하지 않는다.
  }
});

test("프록시 포트 충돌은 시작 실패로 표시하고 실제 백엔드로 재시도", async ({}, testInfo) => {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), "ARTEX 시작 오류 한글 #%-"));
  const blocker = http.createServer((_request, response) => response.end("local-test-only"));
  await new Promise((resolve) => blocker.listen(8788, "127.0.0.1", resolve));
  let electron;
  let blocked = true;
  try {
    electron = await _electron.launch({ args: [path.resolve(__dirname, ".."), `--artex-home=${home}`], env: isolatedEnvironment, chromiumSandbox: true, timeout: 45_000 });
    const page = await electron.firstWindow();
    await expect.poll(async () => (await page.evaluate(() => window.artexDesktop?.status()).catch(() => null))?.state, { timeout: 40_000 }).toBe("failed");
    await expect(page.getByText("백엔드를 시작할 수 없습니다. 진단 정보를 확인한 뒤 다시 시도하세요.", { exact: true })).toBeVisible();
    const startupFont = await page.evaluate(async () => {
      await document.fonts.ready;
      return { faces: [...document.fonts].map(({ family, status }) => ({ family, status })), body: getComputedStyle(document.body).fontFamily, diagnostics: getComputedStyle(document.querySelector("pre")).fontFamily };
    });
    expect(startupFont.faces).toEqual([{ family: "Pretendard", status: "loaded" }]);
    expect(startupFont.body).toBe(startupFont.diagnostics);
    expect(startupFont.body).toContain("Pretendard");
    await expect(page.getByRole("button", { name: "다시 시도", exact: true })).toBeEnabled();
    await page.getByText("진단 정보", { exact: true }).click();
    await page.screenshot({ path: testInfo.outputPath("proxy-conflict.png") });
    await new Promise((resolve) => blocker.close(resolve));
    blocked = false;
    await page.getByRole("button", { name: "다시 시도", exact: true }).click();
    await page.waitForURL(/\/function\/tasks\/?$/);
    expect(await page.evaluate(() => window.artexDesktop.status())).toMatchObject({ state: "ready" });
    await expect(page.locator('[data-slot="sidebar"]')).toBeVisible();
    await expect(page.locator('input[type="password"]')).toHaveCount(0);
    await page.screenshot({ path: testInfo.outputPath("retry-ready.png") });
    fs.writeFileSync(testInfo.outputPath("temporary-home.txt"), home);
  } finally {
    if (electron) await electron.close();
    if (blocked) await new Promise((resolve) => blocker.close(resolve));
  }
});
