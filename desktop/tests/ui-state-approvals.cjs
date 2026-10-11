const { expect } = require("@playwright/test");
const { assertAccessibleControls } = require("./ui-accessibility.cjs");

// Every approval read and decision below is a synthetic renderer response.
// These IDs have no backend task, command, or execution attached to them.
async function verifyApprovalStates(page, origin, testInfo, mode) {
  const created = "2026-10-01T03:00:00Z";
  const longText = "검사 전용 긴 승인 스냅샷\n" + "동일한 검토 입력과 실행 출력의 줄바꿈 및 영역 스크롤을 확인합니다.\n".repeat(900);
  const rows = ["pending", "pending", "allowed", "denied", "timeout"].map((status, index) => ({
    id: 9001 + index,
    agent_name: `검사 승인 Agent ${index + 1}`,
    tool_name: "fixture_only",
    tool_input: { command: `fixture-only-no-execution-${index + 1}`, note: "화면 검사 전용; 도구 실행 없음" },
    status,
    decision_source: "rule",
    reason: `검사 전용 승인 ${status}`,
    rule_id: 901,
    rule_name: "검사 전용 승인 규칙",
    conv_title: "",
    conv_agent_key: "",
    created_at: created,
    ...(status !== "pending" ? { decided_at: created } : {}),
  }));
  const calls = [];
  let detailFails = true;
  let decisionFails = true;
  let releaseDecision;
  let decisionGate;
  const escapedOrigin = origin.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const patterns = [
    new RegExp(`^${escapedOrigin}/api/intercept/history(?:\\?.*)?$`),
    new RegExp(`^${escapedOrigin}/api/intercept/pending(?:\\?.*)?$`),
    new RegExp(`^${escapedOrigin}/api/intercept/history/900[1-5](?:\\?.*)?$`),
    new RegExp(`^${escapedOrigin}/api/intercept/pending/900[12]/decide(?:\\?.*)?$`),
  ];
  const fulfill = (route, status, body) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
  const handler = async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const pathname = url.pathname;
    calls.push({ method: request.method(), pathname });
    const decision = pathname.match(/\/pending\/(900[12])\/decide$/);
    if (decision) {
      if (request.method() !== "POST") return fulfill(route, 405, { error: "검사 전용 결정 메서드 오류" });
      const input = request.postDataJSON();
      if (!["allowed", "denied"].includes(input.decision)) return fulfill(route, 400, { error: "검사 전용 결정 값 오류" });
      if (decisionGate) await decisionGate;
      if (decisionFails) return fulfill(route, 500, { error: "검사 전용 승인 처리 실패" });
      const row = rows.find((item) => item.id === Number(decision[1]));
      row.status = input.decision;
      row.decided_at = created;
      return fulfill(route, 200, { ok: true });
    }
    if (request.method() !== "GET") return fulfill(route, 405, { error: "검사 전용 조회 메서드 오류" });
    const detail = pathname.match(/\/history\/(900[1-5])$/);
    if (detail) {
      const row = rows.find((item) => item.id === Number(detail[1]));
      if (row.id === 9003 && detailFails) return fulfill(route, 500, { error: "검사 전용 승인 상세 실패" });
      return fulfill(route, 200, { ...row, audit: {
        correlation: "exact", input_digest: "a".repeat(64), config_digest: "b".repeat(64),
        tool_use_id: `fixture-call-${row.id}`, user_message: longText, context: [], captured_at: created,
        initial_action: row.status === "allowed" ? "allow" : row.status === "denied" ? "deny" : "ask",
        initial_reason: longText, effective_action: row.status === "allowed" ? "allow" : "deny",
        decision_reason: "화면 검사 전용 저장 응답; 실제 도구는 실행하지 않았습니다.",
        execution_status: "not_executed", output: longText, rule_name: row.rule_name,
      } });
    }
    if (pathname.endsWith("/pending")) return fulfill(route, 200, { pending: rows.filter((row) => row.status === "pending") });
    const status = url.searchParams.get("status");
    const selected = status ? rows.filter((row) => row.status === status) : rows;
    return fulfill(route, 200, { items: selected, total: selected.length });
  };
  try {
    for (const pattern of patterns) await page.route(pattern, handler);
    await page.goto(`${origin}/system/intercept/approvals/`);
    const queue = page.getByRole("table", { name: "처리 대기 승인", exact: true });
    const history = page.getByRole("table", { name: "승인 기록 목록", exact: true });
    await expect(queue).toBeVisible();
    await expect(history).toBeVisible();
    await expect(history.getByText("허용됨", { exact: true })).toBeVisible();
    await expect(history.getByText("거부됨", { exact: true })).toBeVisible();
    await expect(history.getByText("시간 초과", { exact: true })).toBeVisible();
    await expect(queue.getByText("승인 대기", { exact: true })).toHaveCount(2);

    await history.getByRole("button", { name: "펼치기승인 #9003", exact: true }).click();
    const allowedDetail = history.getByRole("region", { name: "승인 상세 #9003", exact: true });
    await expect(allowedDetail.getByRole("alert").filter({ hasText: "검사 전용 승인 상세 실패" })).toBeVisible();
    detailFails = false;
    await allowedDetail.getByRole("button", { name: "상세 다시 시도", exact: true }).click();
    await expect(allowedDetail.getByRole("alert").filter({ hasText: "검사 전용 승인 상세 실패" })).toHaveCount(0);
    await allowedDetail.getByRole("button", { name: "컨텍스트 및 실행 결과 보기", exact: true }).click();
    await allowedDetail.getByRole("button", { name: "세션 감사 조각 보기", exact: true }).click();
    await expect(allowedDetail.getByRole("region", { name: "실행 출력", exact: true }).locator("pre")).toHaveText(longText);
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await assertAccessibleControls(page, testInfo, `approval-long-snapshot-${mode}`);
    await allowedDetail.screenshot({ path: testInfo.outputPath(`approval-long-snapshot-${mode}.png`) });

    await queue.getByRole("button", { name: "펼치기승인 #9001", exact: true }).click();
    const pendingDetail = queue.getByRole("region", { name: "승인 상세 #9001", exact: true });
    await expect(pendingDetail.getByRole("button", { name: "거부", exact: true })).toBeEnabled();
    decisionGate = new Promise((resolve) => { releaseDecision = resolve; });
    await pendingDetail.getByRole("button", { name: "거부", exact: true }).click();
    await expect(pendingDetail.getByRole("button", { name: "허용", exact: true })).toBeDisabled();
    await expect(pendingDetail.getByRole("button", { name: "거부", exact: true })).toBeDisabled();
    await pendingDetail.screenshot({ path: testInfo.outputPath(`approval-decision-busy-${mode}.png`) });
    releaseDecision();
    decisionGate = null;
    await expect(page.getByText("검사 전용 승인 처리 실패", { exact: true })).toBeVisible();
    await expect(queue.getByRole("button", { name: "접기승인 #9001", exact: true })).toBeVisible();
    await expect(pendingDetail.getByRole("button", { name: "거부", exact: true })).toBeEnabled();
    await expect(queue.getByRole("button", { name: "펼치기승인 #9002", exact: true })).toBeVisible();
    await assertAccessibleControls(page, testInfo, `approval-decision-error-${mode}`);

    decisionFails = false;
    await pendingDetail.getByRole("button", { name: "거부", exact: true }).click();
    await expect(queue.getByRole("button", { name: "접기승인 #9001", exact: true })).toHaveCount(0);
    await queue.getByRole("button", { name: "펼치기승인 #9002", exact: true }).click();
    const second = queue.getByRole("region", { name: "승인 상세 #9002", exact: true });
    await second.getByRole("button", { name: "허용", exact: true }).click();
    await expect(queue).toHaveCount(0);
    const deniedRow = history.getByRole("row").filter({ has: page.getByRole("button", { name: "펼치기승인 #9001", exact: true }) });
    const allowedRow = history.getByRole("row").filter({ has: page.getByRole("button", { name: "펼치기승인 #9002", exact: true }) });
    await expect(deniedRow.getByText("거부됨", { exact: true })).toBeVisible();
    await expect(allowedRow.getByText("허용됨", { exact: true })).toBeVisible();
    expect(calls.filter((call) => call.method === "POST").map((call) => call.pathname)).toEqual([
      "/api/intercept/pending/9001/decide", "/api/intercept/pending/9001/decide", "/api/intercept/pending/9002/decide",
    ]);
    await assertAccessibleControls(page, testInfo, `approval-decisions-complete-${mode}`);
    await page.mouse.move(0, 0);
    await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 12_000 });
    await page.screenshot({ path: testInfo.outputPath(`approval-decisions-complete-${mode}.png`) });
    await testInfo.attach(`approval-fixture-calls-${mode}`, { body: JSON.stringify(calls, null, 2), contentType: "application/json" });
  } finally {
    releaseDecision?.();
    for (const pattern of patterns) await page.unroute(pattern, handler);
  }
}

module.exports = { verifyApprovalStates };
