const { expect } = require("@playwright/test");
const { assertAccessibleControls } = require("./ui-accessibility.cjs");

async function verifyFileStates(page, origin, testInfo, mode) {
  const workspaceText = "검사 전용 작업 파일 원문";
  const skillTexts = { "A.txt": "검사 전용 스킬 파일 A 원문", "B.txt": "검사 전용 스킬 파일 B 원문" };
  const writes = [];
  let workspaceFails = true;
  let skillReadFails = true;
  let skillWriteFails = true;
  let usageFails = true;
  let delayedA = false;
  let releaseWrite;
  let releaseRead;
  let releaseDelete;
  let releaseMeta;
  let releaseVisibility;
  let releaseVisibilityB;
  let writeGate;
  let readGate;
  let deleteGate;
  let metaGate;
  let visibilityGate;
  let visibilityGateB;
  let deleteStarted = false;
  let workspaceDeleted = false;
  let metaStarted = false;
  let holdInitialVisibility = false;
  let visibilityStarted = false;
  let visibilityStartedB = false;
  let visibilityReads = 0;
  let visibilityAgents = [];
  const workspaceListPaths = [];
  const confirmations = [];
  let discard = false;
  const onDialog = async (dialog) => {
    confirmations.push(dialog.message());
    if (discard) await dialog.accept(); else await dialog.dismiss();
  };
  const escapedOrigin = origin.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const workspacePattern = new RegExp(`^${escapedOrigin}/api/workspace/(?:list|read|write|delete)(?:\\?.*)?$`);
  const skillPattern = new RegExp(`^${escapedOrigin}/api/skills(?:/(?:missing|fixture-state(?:-b)?/(?:files/[AB]\\.txt|usage|meta)))?(?:\\?.*)?$`);
  const visibilityPattern = new RegExp(`^${escapedOrigin}/api/visibility/skill/(?:fixture-state(?:-b)?|toggle)(?:\\?.*)?$`);
  const optionsPattern = new RegExp(`^${escapedOrigin}/api/(?:mcp|agents)(?:\\?.*)?$`);
  const fulfill = (route, status, body) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
  const workspace = async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.pathname.endsWith("/delete")) {
      if (request.method() !== "DELETE") return fulfill(route, 405, { error: "검사 전용 삭제 메서드 오류" });
      writes.push({ kind: "workspace-delete", path: url.searchParams.get("path") });
      deleteStarted = true;
      if (deleteGate) await deleteGate;
      workspaceDeleted = true;
      return fulfill(route, 200, { ok: true });
    }
    if (url.pathname.endsWith("/write")) {
      if (request.method() !== "POST") return fulfill(route, 405, { error: "검사 전용 쓰기 메서드 오류" });
      const input = request.postDataJSON();
      writes.push({ kind: "workspace", ...input });
      if (writeGate) await writeGate;
      if (workspaceFails) return fulfill(route, 500, { error: "검사 전용 작업 파일 저장 실패" });
      return fulfill(route, 200, { ok: true, path: input.path });
    }
    if (request.method() !== "GET") return fulfill(route, 405, { error: "검사 전용 조회 메서드 오류" });
    if (url.pathname.endsWith("/read")) return fulfill(route, 200, { path: "state-file.txt", size: workspaceText.length, binary: false, content: workspaceText });
    const path = url.searchParams.get("path") || "";
    workspaceListPaths.push(path);
    const entries = path === "state-dir" ? [
      { name: "state-child.txt", path: "state-dir/state-child.txt", dir: false, size: 1, mtime: 1_790_000_000_000 },
    ] : [
      ...(!workspaceDeleted ? [{ name: "state-file.txt", path: "state-file.txt", dir: false, size: workspaceText.length, mtime: 1_790_000_000_000 }] : []),
      { name: "state-dir", path: "state-dir", dir: true, size: 0, mtime: 1_790_000_000_000 },
    ];
    return fulfill(route, 200, { path, entries });
  };
  const skill = async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    const file = pathname.match(/\/files\/([AB]\.txt)$/)?.[1];
    if (pathname.endsWith("/meta")) {
      if (request.method() !== "PUT") return fulfill(route, 405, { error: "검사 전용 메타 수정 메서드 오류" });
      writes.push({ kind: "skill-meta", path: pathname, ...request.postDataJSON() });
      metaStarted = true;
      if (metaGate) await metaGate;
      return fulfill(route, 500, { error: "검사 전용 MCP 연결 저장 실패" });
    }
    if (file && request.method() === "PUT") {
      const input = request.postDataJSON();
      writes.push({ kind: "skill", file, ...input });
      if (skillWriteFails) return fulfill(route, 500, { error: "검사 전용 스킬 파일 저장 실패" });
      skillTexts[file] = input.content;
      return fulfill(route, 200, { ok: true });
    }
    if (request.method() !== "GET") return fulfill(route, 405, { error: "검사 전용 스킬 메서드 오류" });
    if (file) {
      if (file === "A.txt" && delayedA) await readGate;
      if (file === "B.txt" && skillReadFails) return fulfill(route, 500, { error: "검사 전용 스킬 파일 읽기 실패" });
      return fulfill(route, 200, { file, content: skillTexts[file] });
    }
    if (pathname.endsWith("/missing")) return fulfill(route, 200, { missing: [] });
    if (pathname.endsWith("/usage")) {
      if (pathname.includes("/fixture-state-b/")) return fulfill(route, 200, { calls: [] });
      if (usageFails) return fulfill(route, 500, { error: "검사 전용 스킬 호출 상세 실패" });
      return fulfill(route, 200, { calls: [{ ts: "2026-10-01T03:00:00Z", agent_key: "fixture-usage", task_id: 0, session_id: "fixture-session", args_len: 1 }] });
    }
    return fulfill(route, 200, { skills: [
      { name: "fixture-state", description: "화면 상태 검사 전용 스킬", files: ["A.txt", "B.txt"], calls: 1, tasks: 0, usage_agents: [], mcps: ["fixture-mcp-a"] },
      { name: "fixture-state-b", description: "검사 전용 스킬 B 고유 메타 정보", files: [], calls: 0, tasks: 0, usage_agents: [], mcps: ["fixture-mcp-b"] },
    ] });
  };
  const visibility = async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    if (pathname.endsWith("/toggle")) {
      if (request.method() !== "POST") return fulfill(route, 405, { error: "검사 전용 권한 변경 메서드 오류" });
      const input = request.postDataJSON();
      writes.push({ kind: "skill-visibility", ...input });
      visibilityAgents = input.visible ? [input.agent_id] : [];
      return fulfill(route, 200, { ok: true });
    }
    if (request.method() !== "GET") return fulfill(route, 405, { error: "검사 전용 권한 조회 메서드 오류" });
    if (pathname.endsWith("/fixture-state-b")) {
      if (!holdInitialVisibility) return fulfill(route, 200, { agents: [] });
      visibilityStartedB = true;
      await visibilityGateB;
      return fulfill(route, 200, { agents: ["fixture-agent"] });
    }
    const agents = [...visibilityAgents];
    if (holdInitialVisibility && ++visibilityReads === 1) {
      visibilityStarted = true;
      await visibilityGate;
    }
    return fulfill(route, 200, { agents });
  };
  const options = (route) => {
    if (route.request().method() !== "GET") return fulfill(route, 405, { error: "검사 전용 연결 목록 메서드 오류" });
    return new URL(route.request().url()).pathname.endsWith("/agents")
      ? fulfill(route, 200, { agents: [{ id: "fixture-agent", key: "fixture-agent", name: "검사 접근 에이전트", role: "worker", builtin: false, enabled: true }] })
      : fulfill(route, 200, { servers: [
        { id: 9001, name: "fixture-mcp-a", transport: "http", url: "http://example.test/a", args: [], env: {}, enabled: true, tools: [] },
        { id: 9002, name: "fixture-mcp-b", transport: "http", url: "http://example.test/b", args: [], env: {}, enabled: true, tools: [] },
      ] });
  };
  try {
    await page.route(workspacePattern, workspace);
    await page.route(skillPattern, skill);
    await page.route(visibilityPattern, visibility);
    await page.route(optionsPattern, options);
    page.on("dialog", onDialog);
    await page.goto(`${origin}/function/workspace/`);
    await page.getByRole("button", { name: "state-file.txt", exact: true }).click();
    const sheet = page.getByRole("dialog");
    const content = sheet.getByRole("textbox", { name: "파일 내용", exact: true });
    await expect(content).toHaveValue(workspaceText);
    await content.fill("검사 전용 작업 파일 수정본");
    writeGate = new Promise((resolve) => { releaseWrite = resolve; });
    await sheet.getByRole("button", { name: "저장", exact: true }).click();
    await expect(content).toHaveJSProperty("readOnly", true);
    await expect(sheet.getByRole("button", { name: "저장 중…", exact: true })).toBeDisabled();
    await page.keyboard.press("Escape");
    await expect(sheet).toBeVisible();
    expect(confirmations).toEqual([]);
    releaseWrite();
    writeGate = null;
    await expect(page.getByText("저장 실패: 검사 전용 작업 파일 저장 실패", { exact: true })).toBeVisible();
    await expect(content).toHaveValue("검사 전용 작업 파일 수정본");
    await expect(sheet.getByText("저장되지 않은 변경", { exact: true })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(sheet).toBeVisible();
    expect(confirmations).toEqual(["저장되지 않은 파일 변경을 버리고 닫을까요?"]);
    workspaceFails = false;
    await sheet.getByRole("button", { name: "저장", exact: true }).click();
    await expect(sheet.getByText("동기화됨", { exact: true })).toBeVisible();
    await assertAccessibleControls(page, testInfo, `workspace-file-recovered-${mode}`);
    await page.mouse.move(0, 0);
    await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 12_000 });
    await sheet.screenshot({ path: testInfo.outputPath(`workspace-file-recovered-${mode}.png`) });
    await content.fill("검사 전용 폐기할 수정본");
    discard = true;
    await page.keyboard.press("Escape");
    await expect(sheet).toHaveCount(0);
    expect(writes.filter((item) => item.kind === "workspace").map((item) => item.content)).toEqual(["검사 전용 작업 파일 수정본", "검사 전용 작업 파일 수정본"]);

    deleteGate = new Promise((resolve) => { releaseDelete = resolve; });
    await page.getByRole("button", { name: "state-file.txt 삭제", exact: true }).click();
    await expect.poll(() => deleteStarted).toBe(true);
    await page.getByRole("button", { name: "state-dir", exact: true }).click();
    const currentFolder = page.getByRole("navigation", { name: "현재 폴더", exact: true }).getByRole("button", { name: "state-dir", exact: true });
    await expect(currentFolder).toHaveAttribute("aria-current", "page");
    await expect(page.getByRole("button", { name: "state-child.txt", exact: true })).toBeVisible();
    const listCountBeforeDelete = workspaceListPaths.length;
    const deleteResponse = page.waitForResponse((response) => new URL(response.url()).pathname === "/api/workspace/delete");
    releaseDelete();
    deleteGate = null;
    await deleteResponse;
    await expect.poll(() => workspaceListPaths.length).toBeGreaterThan(listCountBeforeDelete);
    expect(workspaceListPaths.at(-1)).toBe("state-dir");
    await expect(currentFolder).toHaveAttribute("aria-current", "page");
    await expect(page.getByRole("button", { name: "state-child.txt", exact: true })).toBeVisible();
    expect(writes.filter((item) => item.kind === "workspace-delete")).toEqual([{ kind: "workspace-delete", path: "state-file.txt" }]);

    await page.goto(`${origin}/system/skills/`);
    await page.getByRole("button", { name: "fixture-state 1 회", exact: true }).click();
    await expect(page.getByRole("alert").filter({ hasText: "검사 전용 스킬 호출 상세 실패" })).toBeVisible();
    await expect(page.getByText("호출 기록이 없습니다.", { exact: true })).toHaveCount(0);
    usageFails = false;
    await page.getByRole("button", { name: "다시 시도", exact: true }).click();
    await expect(page.getByText("fixture-usage", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "fixture-state 1", exact: true }).click();
    await page.getByRole("button", { name: "A.txt", exact: true }).click();
    await expect(page.getByRole("textbox", { name: "fixture-state A.txt 내용", exact: true })).toHaveValue(skillTexts["A.txt"]);
    await page.getByRole("button", { name: "B.txt", exact: true }).click();
    await expect(page.getByRole("alert").filter({ hasText: "검사 전용 스킬 파일 읽기 실패" })).toBeVisible();
    await expect(page.getByRole("textbox", { name: /fixture-state [AB]\.txt 내용/ })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "저장", exact: true })).toBeDisabled();
    expect(writes.filter((item) => item.kind === "skill")).toEqual([]);
    skillReadFails = false;
    await page.getByRole("button", { name: "다시 시도", exact: true }).click();
    const fileB = page.getByRole("textbox", { name: "fixture-state B.txt 내용", exact: true });
    await expect(fileB).toHaveValue(skillTexts["B.txt"]);
    await fileB.fill("검사 전용 스킬 파일 B 수정본");
    await page.getByRole("button", { name: "저장", exact: true }).click();
    await expect(page.getByText("저장 실패:검사 전용 스킬 파일 저장 실패", { exact: true })).toBeVisible();
    await expect(fileB).toHaveValue("검사 전용 스킬 파일 B 수정본");
    await expect(page.getByRole("button", { name: "저장", exact: true })).toBeEnabled();
    skillWriteFails = false;
    await page.getByRole("button", { name: "저장", exact: true }).click();
    await expect(page.getByRole("button", { name: "저장", exact: true })).toBeDisabled();
    expect(writes.filter((item) => item.kind === "skill").map((item) => ({ file: item.file, content: item.content }))).toEqual([
      { file: "B.txt", content: "검사 전용 스킬 파일 B 수정본" }, { file: "B.txt", content: "검사 전용 스킬 파일 B 수정본" },
    ]);

    delayedA = true;
    readGate = new Promise((resolve) => { releaseRead = resolve; });
    await page.getByRole("button", { name: "A.txt", exact: true }).click();
    await expect(page.getByRole("status").filter({ hasText: "스킬 파일 불러오는" })).toBeVisible();
    await page.getByRole("button", { name: "B.txt", exact: true }).click();
    await expect(fileB).toHaveValue("검사 전용 스킬 파일 B 수정본");
    const lateResponse = page.waitForResponse((response) => new URL(response.url()).pathname === "/api/skills/fixture-state/files/A.txt");
    releaseRead();
    await lateResponse;
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    await expect(fileB).toHaveValue("검사 전용 스킬 파일 B 수정본");
    await expect(page.getByRole("textbox", { name: "fixture-state A.txt 내용", exact: true })).toHaveCount(0);
    await assertAccessibleControls(page, testInfo, `skill-file-recovered-${mode}`);
    await page.mouse.move(0, 0);
    await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 12_000 });
    await page.screenshot({ path: testInfo.outputPath(`skill-file-recovered-${mode}.png`) });

    await page.getByRole("button", { name: "fixture-state 1", exact: true }).click();
    await expect(page.getByRole("heading", { name: "fixture-state", exact: true })).toBeVisible();
    const mcpA = page.getByRole("checkbox", { name: "fixture-mcp-a", exact: true });
    const mcpB = page.getByRole("checkbox", { name: "fixture-mcp-b", exact: true });
    await expect(mcpA).toBeChecked();
    await expect(mcpB).not.toBeChecked();
    metaGate = new Promise((resolve) => { releaseMeta = resolve; });
    await mcpA.click();
    await expect.poll(() => metaStarted).toBe(true);
    await expect(mcpA).toBeDisabled();
    await expect(mcpB).toBeDisabled();
    await page.getByRole("button", { name: "fixture-state-b", exact: true }).click();
    await expect(page.getByRole("heading", { name: "fixture-state-b", exact: true })).toBeVisible();
    await expect(mcpA).not.toBeChecked();
    await expect(mcpB).toBeChecked();
    await expect(mcpB).toBeDisabled();
    releaseMeta();
    metaGate = null;
    await expect(page.getByText("작업 실패:검사 전용 MCP 연결 저장 실패", { exact: true })).toBeVisible();
    await expect(mcpB).toBeEnabled();
    await expect(mcpB).toBeChecked();
    await expect(mcpA).not.toBeChecked();
    await expect(page.getByText("검사 전용 스킬 B 고유 메타 정보", { exact: true })).toBeVisible();
    expect(writes.filter((item) => item.kind === "skill-meta")).toEqual([{ kind: "skill-meta", path: "/api/skills/fixture-state/meta", mcps: [] }]);

    holdInitialVisibility = true;
    visibilityGate = new Promise((resolve) => { releaseVisibility = resolve; });
    visibilityGateB = new Promise((resolve) => { releaseVisibilityB = resolve; });
    await page.goto(`${origin}/system/skills/`);
    await expect.poll(() => visibilityStarted).toBe(true);
    await expect.poll(() => visibilityStartedB).toBe(true);
    await page.getByRole("button", { name: "fixture-state 1", exact: true }).click();
    const permission = page.getByRole("checkbox", { name: "검사 접근 에이전트", exact: true });
    await expect(permission).toBeEnabled();
    await expect(permission).not.toBeChecked();
    await permission.click();
    await expect(permission).toBeChecked();
    await expect(permission).toBeEnabled();
    expect(writes.filter((item) => item.kind === "skill-visibility")).toEqual([{ kind: "skill-visibility", agent_id: "fixture-agent", skill_name: "fixture-state", visible: true }]);
    const oldVisibilityResponse = page.waitForResponse((response) => new URL(response.url()).pathname === "/api/visibility/skill/fixture-state");
    releaseVisibility();
    await oldVisibilityResponse;
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    await expect(permission).toBeChecked();
    await expect(permission).toBeEnabled();
    const heldVisibilityResponseB = page.waitForResponse((response) => new URL(response.url()).pathname === "/api/visibility/skill/fixture-state-b");
    releaseVisibilityB();
    await heldVisibilityResponseB;
    await page.getByRole("button", { name: "fixture-state-b", exact: true }).click();
    await expect(page.getByRole("heading", { name: "fixture-state-b", exact: true })).toBeVisible();
    await expect(permission).toBeChecked();
    await expect(permission).toBeEnabled();
    await page.mouse.move(0, 0);
    await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 12_000 });
    await testInfo.attach(`file-fixture-writes-${mode}`, { body: JSON.stringify(writes, null, 2), contentType: "application/json" });
  } finally {
    releaseWrite?.();
    releaseRead?.();
    releaseDelete?.();
    releaseMeta?.();
    releaseVisibility?.();
    releaseVisibilityB?.();
    page.off("dialog", onDialog);
    await page.unroute(workspacePattern, workspace);
    await page.unroute(skillPattern, skill);
    await page.unroute(visibilityPattern, visibility);
    await page.unroute(optionsPattern, options);
  }
}

module.exports = { verifyFileStates };
