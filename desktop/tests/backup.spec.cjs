const { test, expect, _electron } = require("@playwright/test");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const environment = { ...process.env };
for (const key of Object.keys(environment)) {
  if (key.startsWith("ARTEX_") || ["OPENAI_API_KEY", "ANTHROPIC_API_KEY"].includes(key)) delete environment[key];
}

test("실제 Electron 백업 IPC·취소·정상 중지·새 홈 복원·재시작·호출자 격리", async ({}, testInfo) => {
  test.setTimeout(180_000);
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "ARTEX 백업 IPC 한글 #%-"));
  const home = path.join(root, "original-home");
  const destination = path.join(root, "snapshots");
  fs.mkdirSync(destination);
  let electron;
  let page;
  const diagnostics = { root, phases: [], dialogs: [], errors: [] };
  const desktop = path.resolve(__dirname, "..");

  async function ready() {
    let status;
    await expect.poll(async () => {
      if (page.isClosed()) throw new Error("백업 검사 중 Electron 창이 닫혔습니다");
      try { status = await page.evaluate(() => window.artexDesktop?.status()); }
      catch (error) { if (error.message.includes("Execution context was destroyed")) return false; throw error; }
      if (status?.state === "failed") throw new Error(JSON.stringify(status));
      return status?.state === "ready" && page.url().startsWith(status.url);
    }, { timeout: 45_000 }).toBe(true);
    return status;
  }

  async function launch() {
    electron = await _electron.launch({ args: [desktop, `--artex-home=${home}`], env: environment, chromiumSandbox: true, timeout: 45_000 });
    const phase = { stdout: "", stderr: "", exit: null };
    diagnostics.phases.push(phase);
    electron.process().stdout.on("data", (chunk) => { phase.stdout += chunk.toString(); });
    electron.process().stderr.on("data", (chunk) => { phase.stderr += chunk.toString(); });
    electron.process().on("exit", (code, signal) => { phase.exit = { code, signal }; });
    page = await electron.firstWindow();
    page.on("pageerror", (error) => diagnostics.errors.push(error.message));
    await ready();
    await page.waitForURL(/\/function\/tasks\/?$/);
  }

  async function dialogResults(results) {
    await electron.evaluate(({ dialog }, results) => {
      globalThis.__backupDialogs = [];
      globalThis.__backupDialogResults = results;
      dialog.showOpenDialog = async (_window, options) => {
        globalThis.__backupDialogs.push(options.title);
        const result = globalThis.__backupDialogResults.shift();
        if (!result) throw new Error("검사 dialog 결과가 없습니다");
        return result;
      };
    }, results);
  }

  try {
    await launch();
    const before = await ready();
    expect(await page.evaluate(() => window.artexDesktop.setAutomaticBackup(false))).toMatchObject({ automatic: false });
    await expect(page.evaluate(() => window.artexDesktop.setAutomaticBackup("true"))).rejects.toThrow(/설정이 올바르지/);
    const key = fs.readFileSync(path.join(home, "jwt.key"));
    fs.writeFileSync(path.join(home, "skills", "사용자.md"), "백업 IPC 사용자 스킬", "utf8");
    const initialization = await page.evaluate(async () => {
      const response = await fetch("/api/auth/init", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ password: "backup-ipc-fixture" }) });
      return response.status;
    });
    expect(initialization).toBe(200);

    await electron.evaluate(async ({ BrowserWindow }, { origin, preload }) => {
      globalThis.__backupForeignWindow = new BrowserWindow({ show: false, webPreferences: { preload, sandbox: true, contextIsolation: true, nodeIntegration: false, webSecurity: true } });
      await globalThis.__backupForeignWindow.loadURL(`${origin}/function/tasks/`);
    }, { origin: before.url, preload: path.resolve(desktop, "src/preload.cjs") });
    const foreign = electron.windows().find((candidate) => candidate !== page);
    expect(foreign).toBeTruthy();
    for (const method of ["backupStatus", "createBackup", "restoreBackup", "openRestoredHome"]) {
      await expect(foreign.evaluate((method) => window.artexDesktop[method](), method)).rejects.toThrow(/허용되지 않은 IPC 호출자/);
    }
    await expect(foreign.evaluate(() => window.artexDesktop.setAutomaticBackup(true))).rejects.toThrow(/허용되지 않은 IPC 호출자/);
    await electron.evaluate(() => { globalThis.__backupForeignWindow.destroy(); delete globalThis.__backupForeignWindow; });

    await dialogResults([{ canceled: true, filePaths: [] }]);
    expect(await page.evaluate(() => window.artexDesktop.createBackup())).toEqual({ cancelled: true });
    expect((await ready()).pid).toBe(before.pid);
    expect(fs.readdirSync(destination)).toEqual([]);

    await page.goto(`${before.url}/system/settings/`);
    await expect(page.getByText("백업과 복원", { exact: true })).toBeVisible();
    await expect(page.getByRole("switch", { name: "하루 첫 정상 종료 시 자동 백업" })).not.toBeChecked();
    await dialogResults([{ canceled: false, filePaths: [destination] }]);
    await page.getByRole("button", { name: "백업 만들기", exact: true }).click();
    await expect.poll(async () => (await ready()).pid, { timeout: 45_000 }).not.toBe(before.pid);
    await page.waitForURL(/\/system\/settings\/?$/);
    const after = await ready();
    expect(() => process.kill(before.pid, 0)).toThrow();
    const saved = await page.evaluate(() => window.artexDesktop.backupStatus());
    expect(saved).toMatchObject({ state: "idle", automatic: false, lastBackup: expect.any(String), files: expect.any(Number) });
    expect(fs.readFileSync(path.join(saved.lastBackup, "payload/skills/사용자.md"), "utf8")).toBe("백업 IPC 사용자 스킬");
    expect(fs.readFileSync(path.join(saved.lastBackup, "payload/jwt.key"))).toEqual(key);
    await expect(page.getByText(/마지막 백업:/)).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath("verified-backup.png"), fullPage: true });

    await dialogResults([{ canceled: false, filePaths: [saved.lastBackup] }, { canceled: true, filePaths: [] }]);
    expect(await page.evaluate(() => window.artexDesktop.restoreBackup())).toEqual({ cancelled: true });
    expect((await page.evaluate(() => window.artexDesktop.backupStatus())).restoredHome).toBeUndefined();
    await dialogResults([{ canceled: false, filePaths: [saved.lastBackup] }, { canceled: false, filePaths: [root] }]);
    await page.getByRole("button", { name: "새 폴더로 복원", exact: true }).click();
    await expect(page.getByRole("button", { name: "복원한 데이터로 재시작", exact: true })).toBeVisible();
    const restored = await page.evaluate(() => window.artexDesktop.backupStatus());
    expect((await ready()).pid).toBe(after.pid);
    expect(fs.readFileSync(path.join(home, "skills/사용자.md"), "utf8")).toBe("백업 IPC 사용자 스킬");
    expect(fs.readFileSync(path.join(restored.restoredHome, "skills/사용자.md"), "utf8")).toBe("백업 IPC 사용자 스킬");
    expect(fs.readFileSync(path.join(restored.restoredHome, "jwt.key"))).toEqual(key);
    await page.screenshot({ path: testInfo.outputPath("verified-new-home.png"), fullPage: true });
    await electron.evaluate(({ app }) => { app.relaunch = () => {}; });
    const exited = new Promise((resolve) => electron.process().once("exit", resolve));
    await page.getByRole("button", { name: "복원한 데이터로 재시작", exact: true }).click();
    await exited;
    expect(JSON.parse(fs.readFileSync(path.join(home, ".active-home.json"), "utf8"))).toEqual({ schema: 1, home: restored.restoredHome });
    electron = null;
    await launch();
    expect(await electron.evaluate(({ app }) => app.getPath("userData"))).toBe(restored.restoredHome);
    expect(await page.evaluate(async () => (await fetch("/api/auth/status")).json())).toEqual({ initialized: true, mode: "desktop" });
    await page.evaluate(() => window.artexDesktop.setAutomaticBackup(false));
    expect(fs.readFileSync(path.join(home, "jwt.key"))).toEqual(key);
    expect(fs.readFileSync(path.join(restored.restoredHome, "jwt.key"))).toEqual(key);
    expect(diagnostics.errors).toEqual([]);

    // Delay only entry into the real restoration method. The real Electron
    // before-quit path must wait; after release the actual Go CLI still restores.
    await electron.evaluate(({}, modulePath) => {
      const { DesktopBackup } = process.getBuiltinModule("module").createRequire(modulePath)(modulePath);
      const restore = DesktopBackup.prototype.restore;
      const gate = new Promise((resolve) => { globalThis.__releaseBackupRestore = resolve; });
      globalThis.__backupRestoreStarted = false;
      DesktopBackup.prototype.restore = async function (...args) {
        globalThis.__backupRestoreStarted = true;
        await gate;
        return restore.apply(this, args);
      };
    }, path.resolve(desktop, "src/backup.cjs"));
    await dialogResults([{ canceled: false, filePaths: [saved.lastBackup] }, { canceled: false, filePaths: [root] }]);
    await page.evaluate(() => { void window.artexDesktop.restoreBackup().catch(() => {}); });
    await expect.poll(() => electron.evaluate(() => globalThis.__backupRestoreStarted)).toBe(true);
    const quittingPID = (await ready()).pid;
    const quitFinished = new Promise((resolve) => electron.process().once("exit", resolve));
    await electron.evaluate(({ app }) => { app.quit(); app.quit(); });
    expect(electron.process().exitCode).toBeNull();
    expect(() => process.kill(quittingPID, 0)).not.toThrow();
    await expect(page.evaluate(() => window.artexDesktop.restoreBackup())).rejects.toThrow(/앱이 준비된 뒤/);
    await electron.evaluate(() => globalThis.__releaseBackupRestore());
    await quitFinished;
    const stateAfterQuit = JSON.parse(fs.readFileSync(path.join(restored.restoredHome, ".desktop-backup.json"), "utf8"));
    expect(fs.readFileSync(path.join(stateAfterQuit.restoredHome, "skills/사용자.md"), "utf8")).toBe("백업 IPC 사용자 스킬");
    expect(() => process.kill(quittingPID, 0)).toThrow();
    electron = null;

    // A rejected maintenance promise must also finish the real backend owner.
    await launch();
    await page.evaluate(() => window.artexDesktop.setAutomaticBackup(false));
    const failurePID = (await ready()).pid;
    await electron.evaluate(({}, modulePath) => {
      const { DesktopBackup } = process.getBuiltinModule("module").createRequire(modulePath)(modulePath);
      const gate = new Promise((resolve) => { globalThis.__releaseFailedRestore = resolve; });
      globalThis.__failedRestoreStarted = false;
      DesktopBackup.prototype.restore = async function () {
        globalThis.__failedRestoreStarted = true;
        await gate;
        throw new Error("제어 검사 복원 실패");
      };
    }, path.resolve(desktop, "src/backup.cjs"));
    await dialogResults([{ canceled: false, filePaths: [saved.lastBackup] }, { canceled: false, filePaths: [root] }]);
    await page.evaluate(() => { void window.artexDesktop.restoreBackup().catch(() => {}); });
    await expect.poll(() => electron.evaluate(() => globalThis.__failedRestoreStarted)).toBe(true);
    const failedQuitFinished = new Promise((resolve) => electron.process().once("exit", resolve));
    await electron.evaluate(({ app }) => { app.quit(); app.quit(); });
    expect(electron.process().exitCode).toBeNull();
    expect(() => process.kill(failurePID, 0)).not.toThrow();
    await electron.evaluate(() => globalThis.__releaseFailedRestore());
    await failedQuitFinished;
    expect(() => process.kill(failurePID, 0)).toThrow();
    expect(JSON.parse(fs.readFileSync(path.join(restored.restoredHome, ".desktop-backup.json"), "utf8")).restoredHome).toBe(stateAfterQuit.restoredHome);
    expect(fs.readFileSync(path.join(home, "skills/사용자.md"), "utf8")).toBe("백업 IPC 사용자 스킬");
    electron = null;
    diagnostics.result = { foreignSenderRejected: true, dialogCancellationPreservedPID: true, originalPID: before.pid, restartedPID: after.pid, newHome: restored.restoredHome, sourcePreserved: true, realGoSQLiteBackupAndRestore: true, relaunchBoundaryStub: true, quitWaitedForRestoration: true, repeatedQuitWaitedForCleanup: true, quittingIPCRefused: true, rejectedMaintenanceStillClosedGo: true, delayedRestoreEntryControlOnly: true };
  } finally {
    if (electron) await electron.close();
    fs.writeFileSync(testInfo.outputPath("backup-diagnostics.json"), JSON.stringify(diagnostics, null, 2));
    await testInfo.attach("backup-diagnostics", { path: testInfo.outputPath("backup-diagnostics.json"), contentType: "application/json" });
  }
});
