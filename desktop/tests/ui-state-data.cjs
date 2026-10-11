const { expect } = require("@playwright/test");
const { assertAccessibleControls } = require("./ui-accessibility.cjs");

async function verifyDataStates(page, origin, testInfo, mode) {
  await verifyTraffic(page, origin, testInfo, mode);
  await verifyFindings(page, origin, testInfo, mode);
  await verifySync(page, origin, testInfo, mode);
  // Activation and edit/delete actions stay inside the rule table at 1280px.
  await page.goto(`${origin}/system/intercept/`);
  await expect(page.getByRole("button", { name: "규칙 편집", exact: true }).first()).toBeVisible();
  const controlBounds = await page.locator('[data-slot="table"] tbody').first().locator('button[aria-label="규칙 편집"],button[aria-label="삭제"],button[role="switch"]').evaluateAll((nodes) => nodes.filter((node) => node.getBoundingClientRect().width > 0).map((node) => {
    const bounds = node.getBoundingClientRect();
    const container = node.closest('[data-slot="table-container"]').getBoundingClientRect();
    return { name: node.getAttribute("aria-label"), left: bounds.left, right: bounds.right, containerLeft: container.left, containerRight: container.right };
  }));
  expect(controlBounds.length).toBeGreaterThan(0);
  for (const control of controlBounds) {
    expect(control.left, control.name).toBeGreaterThanOrEqual(control.containerLeft);
    expect(control.right, control.name).toBeLessThanOrEqual(control.containerRight);
  }
  await testInfo.attach(`command-rule-controls-${mode}`, { body: JSON.stringify(controlBounds), contentType: "application/json" });
  const edit = page.getByRole("button", { name: "규칙 편집", exact: true }).first();
  await expect(edit).toBeVisible();
  await edit.focus();
  const bounds = await edit.boundingBox();
  expect(bounds.x + bounds.width).toBeLessThanOrEqual(1281);
  await page.keyboard.press("Enter");
  const ruleDialog = page.getByRole("dialog");
  await expect(ruleDialog).toBeVisible();
  await expect.poll(() => ruleDialog.evaluate((node) => getComputedStyle(node).opacity === "1" && node.getAnimations().every((animation) => animation.playState === "finished"))).toBe(true);
  await assertAccessibleControls(page, testInfo, `command-rule-edit-${mode}`);
  await page.screenshot({ path: testInfo.outputPath(`command-rule-edit-${mode}.png`) });
  await page.keyboard.press("Escape");
  await expect(edit).toBeFocused();
}

async function verifyTraffic(page, origin, testInfo, mode) {
  const exchange = { id: "fixture-traffic", ts: "2026-10-11T00:00:00Z", host: "example.test", method: "GET", url: "http://example.test/fixture", status: 200, content_type: "text/plain", resp_len: 64 };
  let fails = true;
  const target = (url) => url.origin === origin && ["/api/traffic", "/api/traffic/hosts", "/api/traffic/exchange"].includes(url.pathname);
  const handler = (route) => {
    const endpoint = new URL(route.request().url()).pathname;
    if (endpoint.endsWith("/hosts")) return route.fulfill({ json: { hosts: [{ host: exchange.host, count: 1 }] } });
    if (endpoint.endsWith("/exchange")) return fails
      ? route.fulfill({ status: 500, json: { error: "검사 전용 패킷 상세 실패" } })
      : route.fulfill({ json: { req: "GET /fixture HTTP/1.1\r\nHost: example.test\r\n\r\n", resp: `HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\n${"검사 전용 HTTP 응답\n".repeat(120)}` } });
    return route.fulfill({ json: { enabled: true, count: 1, total: 1, exchanges: [exchange], page: 0, size: 50 } });
  };
  await page.route(target, handler);
  try {
    await page.goto(`${origin}/function/traffic/`);
    await page.getByRole("row").filter({ hasText: exchange.url }).click();
    const dialog = page.getByRole("dialog");
    const error = dialog.getByRole("alert").filter({ hasText: "검사 전용 패킷 상세 실패" });
    await expect(error).toBeVisible();
    fails = false;
    await error.getByRole("button", { name: "다시 시도", exact: true }).click();
    await expect(error).toHaveCount(0);
    await dialog.getByRole("tab", { name: "응답 Response", exact: true }).click();
    await expect(dialog.getByText(/검사 전용 HTTP 응답/).first()).toBeVisible();
    await assertAccessibleControls(page, testInfo, `traffic-detail-${mode}`);
    await page.screenshot({ path: testInfo.outputPath(`traffic-detail-${mode}.png`) });
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
  } finally { await page.unroute(target, handler); }
}

async function verifyFindings(page, origin, testInfo, mode) {
  const finding = { id: "fixture-finding", finding_id: "fixture-finding", name: "검사 전용 취약점", vulnclass: "검사 분류", severity: "high", status: "pending", summary: "한글 요약", evidence: "검사 증거", report: "# 검사 보고서\n\n검사 전용 보고서 내용", task_id: "99001", task_description: "검사 작업", ts: "2026-10-11T00:00:00Z" };
  let detailState = "error";
  let groupFails = true;
  let mutationFails = true;
  let mutationGate;
  let releaseMutation;
  const mutations = [];
  const target = (url) => url.origin === origin && ["/api/exploration/findings", "/api/exploration/findings/fixture-finding", "/api/exploration/findings/groups"].includes(url.pathname);
  const handler = async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("/fixture-finding") && route.request().method() === "PATCH") {
      const input = route.request().postDataJSON();
      mutations.push(input);
      if (mutationGate) await mutationGate;
      if (mutationFails) return route.fulfill({ status: 500, json: { error: "검사 전용 취약점 저장 실패" } });
      Object.assign(finding, input);
      return route.fulfill({ json: finding });
    }
    if (url.pathname.endsWith("/fixture-finding")) return detailState === "error"
      ? route.fulfill({ status: 500, json: { error: "검사 전용 취약점 상세 실패" } })
      : detailState === "missing" ? route.fulfill({ status: 404, json: { error: "검사 전용 미존재" } }) : route.fulfill({ json: finding });
    if (url.pathname.endsWith("/groups")) return route.fulfill({ json: { items: [{ task_id: "99001", task_name: "검사 그룹", task_description: "검사 작업", task_status: "done", count: 1, critical: 0, high: 1, medium: 0, low: 0, last_found_at: finding.ts }], total: 1, finding_total: 1, page: 1, page_size: 10 } });
    if (groupFails && url.searchParams.get("task_id") === "99001") return route.fulfill({ status: 500, json: { error: "검사 전용 그룹 상세 실패" } });
    return route.fulfill({ json: url.searchParams.has("page") ? { items: [finding], total: 1, page: 1, page_size: 20 } : { findings: [finding] } });
  };
  await page.route(target, handler);
  try {
    await page.goto(`${origin}/function/findings/detail/?id=fixture-finding`);
    const error = page.getByRole("alert").filter({ hasText: "검사 전용 취약점 상세 실패" });
    await expect(error).toBeVisible();
    await expect(page.getByText(/찾을 수 없습니다/)).toHaveCount(0);
    detailState = "success";
    await error.getByRole("button", { name: "다시 시도", exact: true }).click();
    await expect(page.getByText(finding.name, { exact: true })).toBeVisible();
    const severity = page.getByRole("combobox", { name: "취약점 심각도", exact: true });
    const status = page.getByRole("combobox", { name: "취약점 상태", exact: true });
    mutationGate = new Promise((resolve) => { releaseMutation = resolve; });
    await severity.click();
    await page.getByRole("option", { name: "중간", exact: true }).click();
    await expect(severity).toBeDisabled();
    await expect(status).toBeDisabled();
    expect(mutations).toEqual([{ severity: "medium" }]);
    releaseMutation();
    mutationGate = null;
    await expect(page.getByText("업데이트 실패: 검사 전용 취약점 저장 실패", { exact: true })).toBeVisible();
    await expect(severity).toBeEnabled();
    await expect(status).toBeEnabled();
    await expect(severity).toHaveText("높음");
    mutationFails = false;
    mutationGate = new Promise((resolve) => { releaseMutation = resolve; });
    await status.click();
    await page.getByRole("option", { name: "확인됨", exact: true }).click();
    await expect(severity).toBeDisabled();
    await expect(status).toBeDisabled();
    releaseMutation();
    mutationGate = null;
    await expect(status).toBeEnabled();
    await expect(status).toHaveText("확인됨");
    await expect(severity).toHaveText("높음");
    expect(mutations).toEqual([{ severity: "medium" }, { status: "confirmed" }]);
    await assertAccessibleControls(page, testInfo, `finding-detail-${mode}`);
    await page.mouse.move(0, 0);
    await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 12_000 });
    await page.screenshot({ path: testInfo.outputPath(`finding-detail-${mode}.png`) });
    detailState = "missing";
    await page.reload();
    await expect(page.getByText("취약점 fixture-finding을(를) 찾을 수 없습니다", { exact: true })).toBeVisible();
    await expect(page.getByRole("alert").filter({ hasText: /취약점 상세 불러오기 실패|검사 전용 미존재/ })).toHaveCount(0);
    await page.goto(`${origin}/function/findings/`);
    await page.getByRole("tab", { name: "작업별 그룹", exact: true }).click();
    await page.getByRole("button", { name: /검사 그룹/ }).click();
    const groupError = page.getByRole("alert").filter({ hasText: "검사 전용 그룹 상세 실패" });
    await expect(groupError).toBeVisible();
    groupFails = false;
    await groupError.getByRole("button", { name: "다시 시도", exact: true }).click();
    await expect(groupError).toHaveCount(0);
    await expect(page.getByText(finding.name, { exact: true })).toBeVisible();
    await assertAccessibleControls(page, testInfo, `finding-group-${mode}`);
    await page.screenshot({ path: testInfo.outputPath(`finding-group-${mode}.png`) });
  } finally { releaseMutation?.(); await page.unroute(target, handler); }
}

async function verifySync(page, origin, testInfo, mode) {
  let projectFails = true;
  let syncFails = true;
  const calls = [];
  const target = (url) => url.origin === origin && url.pathname.startsWith("/api/sync/scopesentry/");
  const handler = (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("/status")) return route.fulfill({ json: { exists: true, configured: true, enabled: true, reachable: true, url: "http://example.test/mcp", tools: [] } });
    if (url.pathname.endsWith("/projects")) return projectFails
      ? route.fulfill({ status: 500, json: { error: "검사 전용 동기화 대상 실패" } })
      : route.fulfill({ json: { projects: [{ id: "fixture-project", name: "검사 프로젝트", AssetCount: 5, tag: "검사" }], tag: {} } });
    if (url.pathname.endsWith("/sync")) {
      calls.push(route.request().postDataJSON());
      return syncFails ? route.fulfill({ status: 500, json: { error: "검사 전용 동기화 처리 실패" } }) : route.fulfill({ json: { synced: { subdomain: 2, service: 3 }, companies: ["검사 기업"], warnings: ["검사 전용 일부 데이터 경고"], errors: [] } });
    }
    return route.continue();
  };
  await page.route(target, handler);
  try {
    await page.goto(`${origin}/function/sync/`);
    const error = page.getByRole("alert").filter({ hasText: "검사 전용 동기화 대상 실패" });
    await expect(error).toBeVisible();
    await expect(page.getByText("데이터 없음", { exact: true })).toHaveCount(0);
    projectFails = false;
    await error.getByRole("button", { name: "다시 시도", exact: true }).click();
    const selected = page.getByRole("checkbox", { name: "검사 프로젝트 프로젝트 선택", exact: true });
    await selected.focus();
    await page.keyboard.press("Space");
    await expect(selected).toBeChecked();
    const sync = page.getByRole("button", { name: "선택 항목 동기화", exact: true });
    await sync.click();
    await expect(page.getByText("동기화 실패: 검사 전용 동기화 처리 실패", { exact: true })).toBeVisible();
    await expect(selected).toBeChecked();
    syncFails = false;
    await sync.click();
    await expect(page.getByText("기업 생성/업데이트: 검사 기업", { exact: true })).toBeVisible();
    expect(calls).toHaveLength(2);
    expect(calls[1].targets).toEqual(["fixture-project"]);
    await assertAccessibleControls(page, testInfo, `sync-configured-result-${mode}`);
    await page.mouse.move(0, 0);
    await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 12_000 });
    await page.screenshot({ path: testInfo.outputPath(`sync-configured-result-${mode}.png`) });
  } finally { await page.unroute(target, handler); }
}

module.exports = { verifyDataStates };
