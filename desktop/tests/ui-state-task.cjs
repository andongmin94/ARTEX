const { expect } = require("@playwright/test");
const fs = require("node:fs");
const { assertAccessibleControls } = require("./ui-accessibility.cjs");

// Every task/model/scope/retest write below is intercepted locally. These are
// synthetic UI records, not an Engine run, target request or policy decision.
async function verifyTaskStates(page, origin, testInfo, mode) {
  await verifyTaskCreation(page, origin, testInfo, mode);
  await verifyTaskReportAndRetests(page, origin, testInfo, mode);
}

async function capture(page, testInfo, name) {
  await assertAccessibleControls(page, testInfo, name);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), `${name}: 화면 가로 넘침`).toBe(true);
  await page.screenshot({ path: testInfo.outputPath(`${name}.png`), animations: "disabled" });
}

async function saveEvidence(testInfo, name, evidence) {
  const body = JSON.stringify(evidence, null, 2);
  const path = testInfo.outputPath(`${name}.json`);
  fs.writeFileSync(path, body);
  await testInfo.attach(name, { path, contentType: "application/json" });
}

function task(id, name) {
  return { id, name, description: "검사 전용 로컬 UI 상태", goal: "합성 화면 상태만 확인합니다", status: "done", paused: true, created_at: "2026-10-11T00:00:00Z", llm_profile_ids: [], company_ids: [], coverage_enabled: true };
}

async function verifyTaskCreation(page, origin, testInfo, mode) {
  const profiles = [
    { id: "99121", name: "검사 모델 A", model: "fixture-a", format: "openai", is_default: false, rate_per_second: 1, rate_per_minute: 60 },
    { id: "99122", name: "검사 모델 B", model: "fixture-b", format: "openai", is_default: false, rate_per_second: 1, rate_per_minute: 60 },
  ];
  const companies = [
    { id: 99131, name: "검사 기업 A", asset_count: 3, scope: [{ id: 99133, company_id: 99131, kind: "domain", raw: "scope-a.example.test", domain: "scope-a.example.test" }] },
    { id: 99132, name: "검사 기업 B", asset_count: 2, scope: [{ id: 99134, company_id: 99132, kind: "ip", raw: "192.0.2.10", net: "192.0.2.10/32" }, { id: 99135, company_id: 99132, kind: "cidr", raw: "192.0.2.0/24", net: "192.0.2.0/24" }] },
  ];
  const sourceTask = { ...task("99151", "검사 원본 작업 이름"), description: "검사 원본 작업 설명", goal: "검사 원본 작업 목표" };
  const category = { id: 99141, name: "검사 작업 분류", task_count: 0 };
  const created = task("99101", "검사 폼 작업");
  const submissions = [];
  let failureLayout;
  let pendingCreation;
  let releaseCreation;
  const creationGate = new Promise((resolve) => { releaseCreation = resolve; });
  let creationFails = true;
  const paths = new Set(["/api/tasks", "/api/companies", "/api/llm/profiles", "/api/task-categories", "/api/task-templates"]);
  const target = (url) => url.origin === origin && paths.has(url.pathname);
  const inFlight = new Set();
  const respond = async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    if (pathname === "/api/tasks" && request.method() === "POST") {
      submissions.push(request.postDataJSON());
      if (submissions.length === 1) await creationGate;
      return creationFails
        ? route.fulfill({ status: 500, json: { error: "검사 전용 작업 생성 실패" } })
        : route.fulfill({ json: created });
    }
    if (request.method() !== "GET") return route.fulfill({ status: 405, json: { error: "검사 전용 메서드 차단" } });
    if (pathname === "/api/tasks") return route.fulfill({ json: { tasks: submissions.length && !creationFails ? [created, sourceTask] : [sourceTask], active: "" } });
    if (pathname === "/api/companies") return route.fulfill({ json: companies });
    if (pathname === "/api/llm/profiles") return route.fulfill({ json: { profiles } });
    if (pathname === "/api/task-categories") return route.fulfill({ json: { categories: [category] } });
    return route.fulfill({ json: { templates: [] } });
  };
  const handler = async (route) => {
    const work = respond(route);
    inFlight.add(work);
    try {
      await work;
    } finally {
      inFlight.delete(work);
    }
  };
  await page.route(target, handler);
  try {
    await page.goto(`${origin}/function/tasks/`);
    const trigger = page.getByRole("button", { name: "새 작업", exact: true });
    await trigger.focus();
    await page.keyboard.press("Enter");
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await dialog.getByRole("textbox", { name: "이름(선택)", exact: true }).fill(created.name);
    const categoryInput = dialog.getByRole("combobox", { name: "작업 분류", exact: true });
    await categoryInput.fill(category.name);
    await page.getByRole("option").filter({ hasText: category.name }).click();
    await expect(dialog.locator('[data-slot="combobox-chip"]').filter({ hasText: category.name })).toBeVisible();
    await dialog.getByRole("heading", { name: "새 작업", exact: true }).click();
    await dialog.getByRole("textbox", { name: "설명", exact: true }).fill(created.description);
    await dialog.getByRole("textbox", { name: "목표", exact: true }).fill(created.goal);
    const sourceInput = dialog.getByRole("combobox", { name: "연결 작업", exact: true });
    await sourceInput.fill(sourceTask.description);
    await page.getByRole("option").filter({ hasText: sourceTask.description }).click();
    await expect(dialog.locator('[data-slot="combobox-chip"]').filter({ hasText: `#${sourceTask.id}` })).toBeVisible();
    await sourceInput.fill("");
    await dialog.getByRole("heading", { name: "새 작업", exact: true }).click();

    const modelInput = dialog.getByRole("combobox", { name: "LLM 설정 체인", exact: true });
    for (const profile of profiles) {
      await modelInput.fill(profile.name);
      await page.getByRole("option").filter({ hasText: profile.name }).click();
      // Base UI retains the previous query through the popup's exit animation.
      // Start the next search after that selection has fully dismissed it.
      await expect(page.locator('[data-slot="combobox-content"]')).toHaveCount(0);
    }
    const chain = dialog.locator("div.flex.flex-col.gap-3").filter({ has: page.locator("#llm-profiles") }).first();
    const chainOrder = () => chain.getByRole("button", { name: "설정 제거", exact: true }).evaluateAll((buttons) => buttons.map((button) => button.parentElement.parentElement.querySelector("p").textContent));
    expect(await chainOrder()).toEqual(profiles.map((profile) => profile.name));
    await chain.getByRole("button", { name: "설정 위로 이동", exact: true }).nth(1).click();
    expect(await chainOrder()).toEqual([profiles[1].name, profiles[0].name]);
    await expect(chain.getByRole("button", { name: "설정 위로 이동", exact: true }).first()).toBeDisabled();
    await expect(chain.getByRole("button", { name: "설정 아래로 이동", exact: true }).last()).toBeDisabled();
    await chain.getByRole("button", { name: "설정 제거", exact: true }).last().click();
    expect(await chainOrder()).toEqual([profiles[1].name]);
    await modelInput.fill(profiles[0].name);
    await page.getByRole("option").filter({ hasText: profiles[0].name }).click();
    expect(await chainOrder()).toEqual([profiles[1].name, profiles[0].name]);
    await modelInput.fill("");
    await dialog.getByRole("heading", { name: "새 작업", exact: true }).click();

    const companyInput = dialog.getByRole("combobox", { name: "기업 자산 범위 연결", exact: true });
    for (const [company, query] of [[companies[0], "scope-a.example.test"], [companies[1], "192.0.2.10"]]) {
      await companyInput.fill(query);
      const option = page.getByRole("option").filter({ hasText: company.name });
      await expect(option).toContainText(query);
      await option.click();
      await expect(page.locator('[data-slot="combobox-content"]')).toHaveCount(0);
    }
    const companyAChip = dialog.locator('[data-slot="combobox-chip"]').filter({ hasText: companies[0].name });
    await companyAChip.getByRole("button", { name: "선택 항목 제거", exact: true }).click();
    await expect(companyAChip).toHaveCount(0);
    await companyInput.fill("scope-a.example.test");
    await page.getByRole("option").filter({ hasText: companies[0].name }).click();
    await companyInput.fill("");
    await dialog.getByRole("heading", { name: "새 작업", exact: true }).click();
    await expect(dialog.locator('[data-slot="combobox-chip"]').filter({ hasText: companies[0].name })).toBeVisible();
    await expect(dialog.locator('[data-slot="combobox-chip"]').filter({ hasText: companies[1].name })).toBeVisible();
    await modelInput.scrollIntoViewIfNeeded();
    await capture(page, testInfo, `task-chain-and-scope-${mode}`);

    await dialog.getByRole("button", { name: "생성", exact: true }).click();
    await expect.poll(() => submissions.length).toBe(1);
    await expect(dialog.getByRole("button", { name: /생성 중$/ })).toBeDisabled();
    await expect(dialog.getByRole("button", { name: "취소", exact: true })).toBeDisabled();
    await expect(dialog.getByRole("button", { name: "닫기", exact: true })).toHaveCount(0);
    await page.keyboard.press("Escape");
    await expect(dialog).toBeVisible();
    await page.keyboard.press("Enter");
    expect(submissions).toHaveLength(1);
    pendingCreation = { submissions: submissions.length, submitDisabled: true, cancelDisabled: true, closeAbsent: true, escapeBlocked: true };
    await capture(page, testInfo, `task-create-pending-${mode}`);
    releaseCreation();
    await expect(page.getByText("생성 실패: 검사 전용 작업 생성 실패", { exact: true })).toBeVisible();
    const createError = dialog.getByRole("alert").filter({ hasText: "생성 실패: 검사 전용 작업 생성 실패" });
    await expect(createError).toBeVisible();
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole("textbox", { name: "이름(선택)", exact: true })).toHaveValue(created.name);
    await expect(dialog.getByRole("textbox", { name: "설명", exact: true })).toHaveValue(created.description);
    await expect(dialog.getByRole("textbox", { name: "목표", exact: true })).toHaveValue(created.goal);
    expect(submissions).toHaveLength(1);
    expect(submissions[0]).toMatchObject({ name: created.name, description: created.description, goal: created.goal, category_id: category.id, source_task_ids: [sourceTask.id], llm_profile_ids: [99122, 99121], company_ids: [99132, 99131], seed_first_intent: false });
    expect(await chainOrder()).toEqual([profiles[1].name, profiles[0].name]);
    const alertBox = await createError.boundingBox();
    expect(alertBox).not.toBeNull();
    const buttonChecks = [];
    for (const name of ["취소", "생성"]) {
      const button = dialog.getByRole("button", { name, exact: true });
      const bounds = await button.boundingBox();
      expect(bounds).not.toBeNull();
      expect(alertBox.y + alertBox.height <= bounds.y + 1, `${mode}: 생성 오류가 ${name} 버튼을 덮음`).toBe(true);
      const hit = await button.evaluate((element) => {
        const rect = element.getBoundingClientRect();
        const target = document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2);
        return target === element || element.contains(target);
      });
      expect(hit, `${mode}: 생성 오류 후 ${name} 버튼 중심에 실제 도달 가능`).toBe(true);
      buttonChecks.push({ name, bounds, hit });
    }
    failureLayout = { alert: alertBox, buttons: buttonChecks };
    await capture(page, testInfo, `task-create-error-${mode}`);
    creationFails = false;
    await dialog.getByRole("button", { name: "생성", exact: true }).click();
    await expect(dialog).toHaveCount(0);
    expect(submissions).toHaveLength(2);
    expect(submissions[1]).toEqual(submissions[0]);
    await expect(page.getByText(created.name, { exact: true }).first()).toBeVisible();
    await saveEvidence(testInfo, `task-create-ui-fixture-${mode}`, { source: "synthetic page.route responses; no task/model/target execution", checked: ["category name search", "source task description search", "model name search", "next model/company search waits for previous popup DOM exit", "model reorder", "remove and re-add model", "domain/IP scope search", "remove and re-add company", "pending create blocks cancel/close/Escape and second submit", "500 preserves form", "inline error leaves cancel/create buttons unobstructed and hit-testable", "retry submits same ordered payload"], pendingCreation, failureLayout, submissions });
  } finally {
    releaseCreation();
    await Promise.allSettled([...inFlight]);
    await page.unroute(target, handler);
  }
}

async function verifyTaskReportAndRetests(page, origin, testInfo, mode) {
  const taskA = task("99201", "검사 보고서 작업 A");
  const taskB = task("99202", "검사 보고서 작업 B");
  const finding = { id: "99211", finding_id: "99211", name: "검사 전용 재검증 취약점", vulnclass: "검사 분류", severity: "high", status: "pending", summary: "합성 재검증 기록과 긴 결과를 확인합니다", evidence: "검사 증거", task_id: taskA.id, ts: "2026-10-11T00:00:00Z" };
  const findingB = { ...finding, id: "99212", finding_id: "99212", name: "검사 작업 B의 재검증 취약점", task_id: taskB.id };
  const timestamp = "2026-10-11T00:00:00Z";
  const completed = { id: 99221, finding_id: Number(finding.id), conversation_id: null, status: "completed", verdict: "fixed", notes: "검사 전용 조건", summary: "검사 전용 수정 확인 결과", evidence: `# 검사 전용 재검증 증거\n\n${"한글 결과와 example.test 증거 설명\n".repeat(80)}\n\n\`\`\`text\n${"0123456789abcdef".repeat(120)}\n\`\`\``, error: "", created_at: timestamp, started_at: timestamp, finished_at: timestamp };
  let records = [completed, { ...completed, id: 99222, status: "failed", verdict: "", summary: "", evidence: "", error: `검사 전용 재검증 실행 실패 ${"abcdef0123456789".repeat(90)}` }, { ...completed, id: 99223, status: "stopped", verdict: "", summary: "", evidence: "" }];
  let reportPhase = "loading";
  let retestPhase = "loading";
  let startFails = true;
  let releaseReport;
  let releaseRetests;
  let releaseStart;
  let startGate;
  const reportGate = new Promise((resolve) => { releaseReport = resolve; });
  const retestGate = new Promise((resolve) => { releaseRetests = resolve; });
  let delayedReportGate;
  let releaseDelayedReport;
  let delayedStarted = false;
  let delayedFinished = false;
  let reportBEmpty = false;
  const reportRequests = [];
  const timeline = [];
  const mark = (state, detail = {}) => timeline.push({ state, at: new Date().toISOString(), ...detail });
  const retestStarts = [];
  const retestPath = `/api/exploration/findings/${finding.id}/retests`;
  const retestPathB = `/api/exploration/findings/${findingB.id}/retests`;
  const target = (url) => url.origin === origin && (
    ["/api/report", "/api/llm/profiles", "/api/stats", "/api/intercept/task", "/api/exploration/findings", retestPath, retestPathB].includes(url.pathname)
    || [taskA.id, taskB.id].some((id) => url.pathname === `/api/tasks/${id}`)
    || (url.pathname.startsWith("/api/exploration/") && [taskA.id, taskB.id].includes(url.searchParams.get("task")))
  );
  const inFlight = new Set();
  const respond = async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const pathname = url.pathname;
    if (pathname === retestPath && request.method() === "POST") {
      retestStarts.push(request.postDataJSON());
      if (startGate) await startGate;
      if (startFails) return route.fulfill({ status: 500, json: { error: "검사 전용 재검증 시작 실패" } });
      const running = { ...completed, id: 99224, conversation_id: 99231, status: "running", verdict: "", summary: "", evidence: "", notes: retestStarts.at(-1).notes, finished_at: null };
      records = [running, ...records];
      return route.fulfill({ json: { retest: running, created: true } });
    }
    if (request.method() !== "GET") return route.fulfill({ status: 405, json: { error: "검사 전용 메서드 차단" } });
    if (pathname === "/api/report") {
      const id = url.searchParams.get("task");
      reportRequests.push(id);
      mark("report requested", { task: id, phase: id === taskB.id ? (reportBEmpty ? "empty" : "ready") : reportPhase });
      if (id === taskB.id) return route.fulfill({ contentType: "text/markdown; charset=utf-8", body: reportBEmpty ? "" : "# 검사 전용 보고서 B\n\n현재 작업 B의 보고서입니다." });
      if (reportPhase === "delayed") {
        delayedStarted = true;
        await delayedReportGate;
        await route.fulfill({ contentType: "text/markdown; charset=utf-8", body: "# 검사 전용 늦은 보고서 A\n\n이 응답은 B를 덮으면 안 됩니다." });
        delayedFinished = true;
        mark("late A response delivered");
        return;
      }
      if (reportPhase === "loading") await reportGate;
      if (reportPhase === "error") return route.fulfill({ status: 500, contentType: "text/plain", body: "검사 전용 보고서 실패" });
      return route.fulfill({ contentType: "text/markdown; charset=utf-8", body: "# 검사 전용 보고서 A\n\n키보드 재시도 후 복구된 보고서입니다." });
    }
    if (pathname === retestPath) {
      if (retestPhase === "loading") await retestGate;
      if (retestPhase === "error") return route.fulfill({ status: 500, json: { error: "검사 전용 재검증 기록 실패" } });
      return route.fulfill({ json: { retests: records } });
    }
    if (pathname === retestPathB) return route.fulfill({ json: { retests: [] } });
    if (pathname === "/api/exploration/findings") {
      const id = url.searchParams.get("task") || url.searchParams.get("task_id");
      return route.fulfill({ json: { items: [id === taskB.id ? findingB : finding], total: 1, page: 1, page_size: 20 } });
    }
    if (pathname === "/api/llm/profiles") return route.fulfill({ json: { profiles: [] } });
    if (pathname === "/api/stats") return route.fulfill({ json: { active_task: null, engine_mode: "idle" } });
    if (pathname === `/api/tasks/${taskA.id}`) return route.fulfill({ json: taskA });
    if (pathname === `/api/tasks/${taskB.id}`) return route.fulfill({ json: taskB });
    if (pathname === "/api/exploration/main-sessions") return route.fulfill({ json: { sessions: [], current: 0 } });
    if (pathname === "/api/exploration/activity/history") return route.fulfill({ json: { items: [], has_more: false, snapshot_cursor: 0 } });
    if (pathname.endsWith("/stream")) return route.fulfill({ contentType: "text/event-stream", body: ": synthetic UI fixture\n\n" });
    return route.fulfill({ json: [] });
  };
  const handler = async (route) => {
    const work = respond(route);
    inFlight.add(work);
    try {
      await work;
    } finally {
      inFlight.delete(work);
    }
  };
  await page.route(target, handler);
  try {
    await page.goto(`${origin}/function/tasks/detail/?id=${taskA.id}`);
    await expect(page.getByRole("heading", { name: taskA.name, exact: true })).toBeVisible();
    await page.getByRole("tab", { name: "보고서", exact: true }).click();
    const report = page.getByRole("tabpanel", { name: "보고서", exact: true });
    await expect(report.getByText("불러오는 중…", { exact: true })).toBeVisible();
    await expect(report.getByText("보고서 없음", { exact: true })).toHaveCount(0);
    await expect(report.getByRole("button", { name: "복사", exact: true })).toHaveCount(0);
    await capture(page, testInfo, `task-report-loading-${mode}`);
    mark("A report loading; no empty or copy");
    reportPhase = "error";
    releaseReport();
    const reportError = report.getByRole("alert").filter({ hasText: "보고서 불러오기 실패: report: 500" });
    await expect(reportError).toBeVisible();
    await expect(report.getByText("보고서 없음", { exact: true })).toHaveCount(0);
    await capture(page, testInfo, `task-report-error-${mode}`);
    mark("A report 500; no empty");
    reportPhase = "ready";
    await reportError.getByRole("button", { name: "다시 시도", exact: true }).focus();
    await page.keyboard.press("Enter");
    await expect(report.getByText("검사 전용 보고서 A", { exact: true })).toBeVisible();
    await expect(reportError).toHaveCount(0);
    await expect(report.getByRole("button", { name: "복사", exact: true })).toBeVisible();
    await capture(page, testInfo, `task-report-recovered-${mode}`);
    mark("A report recovered by keyboard retry");

    await page.getByRole("tab", { name: "재검증", exact: true }).click();
    const retests = page.getByRole("tabpanel", { name: "재검증", exact: true });
    await expect(retests.getByRole("button", { name: `취약점 선택：${finding.name}`, exact: true })).toBeVisible();
    await expect(retests.locator('[data-slot="skeleton"]')).not.toHaveCount(0);
    await expect(retests.getByText("재검증 기록 없음", { exact: true })).toHaveCount(0);
    await capture(page, testInfo, `task-retests-loading-${mode}`);
    mark("A retest history loading; no empty");
    retestPhase = "error";
    releaseRetests();
    const retestError = retests.getByRole("alert").filter({ hasText: "검사 전용 재검증 기록 실패" });
    await expect(retestError).toBeVisible();
    await expect(retests.getByText("재검증 기록 없음", { exact: true })).toHaveCount(0);
    await capture(page, testInfo, `task-retests-error-${mode}`);
    mark("A retest history 500; no empty");
    retestPhase = "ready";
    await retestError.getByRole("button", { name: "재시도", exact: true }).focus();
    await page.keyboard.press("Enter");
    await expect(retests.getByText(completed.summary, { exact: true })).toBeVisible();
    await expect(retests.getByText("재검증 실패", { exact: true })).toBeVisible();
    await expect(retests.getByText("중지됨", { exact: true })).toBeVisible();
    await retests.getByText("재검증 증거", { exact: true }).click();
    await expect(retests.getByText("검사 전용 재검증 증거", { exact: true })).toBeVisible();
    await capture(page, testInfo, `task-retests-long-results-${mode}`);
    mark("A completed/failed/stopped history recovered; long evidence expanded");
    const evidenceCode = retests.locator("pre").filter({ hasText: "0123456789abcdef".repeat(120) });
    await evidenceCode.scrollIntoViewIfNeeded();
    await expect(evidenceCode).toBeInViewport();
    const codeScroll = await evidenceCode.evaluate((element) => {
      element.scrollLeft = element.scrollWidth - element.clientWidth;
      return { clientWidth: element.clientWidth, scrollWidth: element.scrollWidth, scrollLeft: element.scrollLeft };
    });
    expect(codeScroll.scrollLeft).toBeGreaterThanOrEqual(codeScroll.scrollWidth - codeScroll.clientWidth - 1);
    await capture(page, testInfo, `task-retests-evidence-code-end-${mode}`);
    const failedRecord = retests.getByText(records[1].error, { exact: true }).locator("..");
    await failedRecord.scrollIntoViewIfNeeded();
    const stoppedRecord = retests.getByText("중지됨", { exact: true }).locator("../..");
    await stoppedRecord.evaluate((element) => element.scrollIntoView({ block: "end" }));
    await expect(failedRecord.getByText("재검증 실패", { exact: true })).toBeInViewport();
    await expect(retests.getByText("중지됨", { exact: true })).toBeInViewport();
    await capture(page, testInfo, `task-retests-failed-stopped-${mode}`);
    mark("long code scrolled to its actual end; failed error and stopped record in viewport", { codeScroll });

    await retests.getByRole("button", { name: "재검증 시작", exact: true }).click();
    const dialog = page.getByRole("dialog");
    const notes = "검사 전용 제한: 합성 기록만 사용하며 실제 대상을 실행하지 않습니다.";
    await dialog.getByRole("textbox", { name: "추가 설명(선택)", exact: true }).fill(notes);
    startGate = new Promise((resolve) => { releaseStart = resolve; });
    await dialog.getByRole("button", { name: "재검증 시작", exact: true }).click();
    await expect(dialog.getByRole("button", { name: /생성 중…$/ })).toBeDisabled();
    await expect(dialog.getByRole("button", { name: "취소", exact: true })).toBeDisabled();
    await expect(dialog.getByRole("textbox", { name: "추가 설명(선택)", exact: true })).toBeDisabled();
    await page.keyboard.press("Escape");
    await expect(dialog).toBeVisible();
    mark("A retest create pending; submit/cancel/notes disabled and Escape close blocked");
    await capture(page, testInfo, `task-retest-create-pending-${mode}`);
    releaseStart();
    startGate = null;
    await expect(page.getByText("재검증 시작 실패: 검사 전용 재검증 시작 실패", { exact: true })).toBeVisible();
    await expect(dialog.getByRole("textbox", { name: "추가 설명(선택)", exact: true })).toHaveValue(notes);
    mark("A retest create 500 preserves notes");
    await capture(page, testInfo, `task-retest-create-error-${mode}`);
    startFails = false;
    await dialog.getByRole("button", { name: "재검증 시작", exact: true }).click();
    await expect(dialog).toHaveCount(0);
    expect(retestStarts).toEqual([{ notes }, { notes }]);
    await expect(retests.getByRole("link", { name: "재검증 중", exact: true })).toBeVisible();
    await expect(retests.getByRole("button", { name: "재검증 시작", exact: true })).toHaveCount(0);
    await capture(page, testInfo, `task-retests-running-${mode}`);
    mark("A retest create retry shows running record");
    records[0] = { ...records[0], status: "completed", verdict: "fixed", summary: "검사 전용 재검증 완료 전환", evidence: completed.evidence, finished_at: timestamp };
    await expect(retests.getByText("검사 전용 재검증 완료 전환", { exact: true })).toBeVisible({ timeout: 10_000 });
    await expect(retests.getByRole("button", { name: "재검증 시작", exact: true })).toBeVisible();
    mark("A running retest polled to completed");

    reportPhase = "delayed";
    delayedReportGate = new Promise((resolve) => { releaseDelayedReport = resolve; });
    await page.getByRole("tab", { name: "보고서", exact: true }).click();
    await expect.poll(() => delayedStarted).toBe(true);
    await expect(report.getByText("불러오는 중…", { exact: true })).toBeVisible();
    const documentTimeOrigin = await page.evaluate(() => performance.timeOrigin);
    // Next's supported history integration changes the task prop in the same
    // document, retaining the selected report tab rather than reloading the app.
    await page.evaluate((id) => {
      const url = new URL(location.href);
      url.searchParams.set("id", id);
      history.pushState(null, "", url);
    }, taskB.id);
    await expect(page.getByRole("heading", { name: taskB.name, exact: true })).toBeVisible();
    expect(await page.evaluate(() => performance.timeOrigin)).toBe(documentTimeOrigin);
    await expect(report.getByText("검사 전용 보고서 B", { exact: true })).toBeVisible();
    mark("same document switched to B and rendered B report", { documentTimeOrigin });
    releaseDelayedReport();
    await expect.poll(() => delayedFinished).toBe(true);
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    await expect(report.getByText("검사 전용 늦은 보고서 A", { exact: true })).toHaveCount(0);
    await expect(report.getByText("검사 전용 보고서 B", { exact: true })).toBeVisible();
    await capture(page, testInfo, `task-report-late-response-${mode}`);
    mark("late A response ignored; B report preserved");
    reportBEmpty = true;
    await page.getByRole("tab", { name: "재검증", exact: true }).click();
    await expect(retests.getByRole("button", { name: `취약점 선택：${findingB.name}`, exact: true })).toBeVisible();
    await expect(retests.getByText("재검증 기록 없음", { exact: true })).toBeVisible();
    mark("B own finding has empty retest history", { task: taskB.id, finding: findingB.id });
    await page.getByRole("tab", { name: "보고서", exact: true }).click();
    await expect(report.getByText("보고서 없음", { exact: true })).toBeVisible();
    await expect(report.getByRole("alert")).toHaveCount(0);
    await expect(report.getByRole("button", { name: "복사", exact: true })).toHaveCount(0);
    await capture(page, testInfo, `task-report-empty-${mode}`);
    mark("B empty report; no error or copy");
    await saveEvidence(testInfo, `task-report-retest-ui-fixture-${mode}`, { source: "synthetic page.route task/report/retest records; no Engine/target execution", taskFindings: [{ task: taskA.id, finding: finding.id }, { task: taskB.id, finding: findingB.id }], timeline, reportRequests, retestStarts, delayedStarted, delayedFinished });
  } finally {
    releaseReport();
    releaseRetests();
    releaseStart?.();
    releaseDelayedReport?.();
    // Complete released fixture requests before unroute can continue them.
    await Promise.allSettled([...inFlight]);
    await page.unroute(target, handler);
  }
}

module.exports = { verifyTaskStates };
