const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawn, spawnSync } = require("node:child_process");
const { createInterface } = require("node:readline");
const { DesktopBackup } = require("../src/backup.cjs");

const executable = process.env.ARTEX_BACKUP_TEST_EXE || path.resolve(__dirname, "../resources", process.platform === "win32" ? "artex.exe" : "artex");
const environment = { ...process.env };
for (const key of Object.keys(environment)) {
  if (key.startsWith("ARTEX_") || ["OPENAI_API_KEY", "ANTHROPIC_API_KEY"].includes(key)) delete environment[key];
}

async function boot(home) {
  const child = spawn(executable, ["-addr", "127.0.0.1:0", "-proxy", "", "-ready-stdout", "-parent-stdin"], {
    env: { ...environment, ARTEX_HOME: home }, stdio: ["pipe", "pipe", "pipe"], windowsHide: true,
  });
  let diagnostic = "";
  child.stderr.setEncoding("utf8").on("data", (chunk) => { diagnostic += chunk; });
  const lines = createInterface({ input: child.stdout });
  const ready = await new Promise((resolve, reject) => {
    const timer = setTimeout(() => { child.kill(); reject(new Error("ready timeout: " + diagnostic)); }, 30_000);
    child.once("error", (error) => { clearTimeout(timer); reject(error); });
    child.once("exit", (code) => { clearTimeout(timer); reject(new Error(`startup exit ${code}: ${diagnostic}`)); });
    lines.once("line", (line) => {
      clearTimeout(timer);
      try { const result = JSON.parse(line); assert.equal(result.event, "ready"); resolve(result); }
      catch (error) { child.kill(); reject(error); }
    });
  });
  return { child, ready, async stop() {
    if (child.exitCode !== null) return;
    const exited = new Promise((resolve, reject) => { child.once("exit", (code) => code === 0 ? resolve() : reject(new Error(`shutdown ${code}: ${diagnostic}`))); });
    child.stdin.end(); await exited; lines.close();
  } };
}

test("실제 Go CLI: 실행 중 차단·검증 백업·새 홈 복원·자동 종료 백업·원본 보존", { timeout: 90_000 }, async (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "ARTEX backup 한글 #%-"));
  const home = path.join(root, "user-data");
  const parent = path.join(root, "manual-backups");
  fs.mkdirSync(home);
  const backend = await boot(home);
  t.after(async () => { if (backend.child.exitCode === null) await backend.stop(); });
  const password = "backup-fixture-password";
  const initialize = await fetch(`${backend.ready.url}/api/auth/init`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ password }) });
  assert.equal(initialize.status, 200);
  fs.writeFileSync(path.join(home, "skills", "사용자.md"), "보존할 사용자 스킬", "utf8");
  const key = fs.readFileSync(path.join(home, "jwt.key"));
  const credential = fs.readFileSync(path.join(home, "chatgpt/credentials"));
  const backups = new DesktopBackup({ executable, home });
  await assert.rejects(backups.create(parent), /사용 중/);
  assert.equal(fs.readdirSync(parent).length, 0);
  await backend.stop();
  const saved = await backups.create(parent);
  assert.equal(saved.state, "idle");
  assert.equal(saved.error, undefined);
  assert.ok(saved.files >= 4 && saved.bytes > 0);
  assert.equal(fs.readFileSync(path.join(saved.lastBackup, "payload/skills/사용자.md"), "utf8"), "보존할 사용자 스킬");
  assert.deepEqual(fs.readFileSync(path.join(saved.lastBackup, "payload/jwt.key")), key);
  assert.deepEqual(fs.readFileSync(path.join(saved.lastBackup, "payload/chatgpt/credentials")), credential);
  assert.equal(fs.existsSync(path.join(saved.lastBackup, "payload/.desktop-backup.json")), false);
  const verified = spawnSync(executable, ["-verify-backup", saved.lastBackup], { env: { ...environment, ARTEX_HOME: home }, encoding: "utf8", windowsHide: true });
  assert.equal(verified.status, 0, verified.stderr);
  assert.equal(JSON.parse(verified.stdout).event, "backup-verified");
  const restored = await backups.restore(saved.lastBackup, root);
  assert.notEqual(restored.restoredHome, home);
  assert.deepEqual(fs.readFileSync(path.join(restored.restoredHome, "jwt.key")), key);
  assert.deepEqual(fs.readFileSync(path.join(restored.restoredHome, "chatgpt/credentials")), credential);
  const restoredBackend = await boot(restored.restoredHome);
  try {
    const login = await fetch(`${restoredBackend.ready.url}/api/auth/login`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ username: "ARTEX", password }) });
    assert.equal(login.status, 200);
    assert.ok((await login.json()).token);
  } finally { await restoredBackend.stop(); }
  assert.equal(fs.readFileSync(path.join(home, "skills/사용자.md"), "utf8"), "보존할 사용자 스킬");
  assert.equal(backups.defaultDirectory().startsWith(home + path.sep), false);
  await backups.onQuit();
  assert.equal(backups.status().lastBackup, saved.lastBackup, "same day snapshot should not repeat");
  backups.state.lastBackupAt = "2000-01-01T00:00:00Z";
  await backups.onQuit();
  assert.notEqual(backups.status().lastBackup, saved.lastBackup);
  assert.ok(fs.existsSync(saved.lastBackup), "automatic backup must not delete older snapshots");
  backups.setAutomatic(false);
  const last = backups.status().lastBackup;
  backups.state.lastBackupAt = "2000-01-01T00:00:00Z";
  await backups.onQuit();
  assert.equal(backups.status().lastBackup, last);
  assert.equal(new DesktopBackup({ executable, home }).status().automatic, false);
  fs.writeFileSync(path.join(saved.lastBackup, "payload/skills/사용자.md"), "damaged", "utf8");
  await assert.rejects(backups.restore(saved.lastBackup, root), /손상/);
  assert.equal(fs.readFileSync(path.join(home, "skills/사용자.md"), "utf8"), "보존할 사용자 스킬");
  fs.writeFileSync(path.join(root, "result.json"), JSON.stringify({ home, originalSnapshot: saved.lastBackup, restoredHome: restored.restoredHome, files: saved.files, bytes: saved.bytes, realGoCLI: true, passwordAndSigningKeyPreserved: true, sourcePreserved: true, corruptedRestoreRefused: true }, null, 2));
  t.diagnostic(`로컬 증거: ${root}`);
});

test("한국시간 자정을 지난 첫 종료는 UTC 날짜가 같아도 새 자동 백업", async () => {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), "ARTEX backup calendar-"));
  fs.mkdirSync(path.join(home, "data"));
  fs.writeFileSync(path.join(home, "data/artex.sqlite"), "policy fixture");
  const backups = new DesktopBackup({ executable, home });
  backups.state.lastBackupAt = "2026-10-09T14:30:00.000Z"; // 한국시간 10월 9일 23:30
  let created = 0;
  backups.create = async () => { created++; };
  const nativeDate = Date;
  const previousTimezone = process.env.TZ;
  class FixedDate extends nativeDate {
    constructor(...args) { super(...(args.length ? args : ["2026-10-09T15:30:00.000Z"])); }
  }
  try {
    process.env.TZ = "Asia/Seoul";
    global.Date = FixedDate;
    await backups.onQuit();
    assert.equal(created, 1);
  } finally {
    global.Date = nativeDate;
    if (previousTimezone === undefined) delete process.env.TZ;
    else process.env.TZ = previousTimezone;
  }
});
