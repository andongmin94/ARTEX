const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const os = require("node:os");
const crypto = require("node:crypto");
const { spawn, spawnSync } = require("node:child_process");
const { DesktopUpdater, stopInstaller } = require("../src/update.cjs");

const csc = path.join(process.env.SystemRoot || "C:\\Windows", "Microsoft.NET/Framework64/v4.0.30319/csc.exe");
function compile(args) { const result = spawnSync(csc, ["/nologo", ...args], { windowsHide: true, encoding: "utf8" }); assert.equal(result.status, 0, result.stdout + result.stderr); }
async function waitFor(check) { for (let i = 0; i < 100; i++) { if (check()) return; await new Promise((resolve) => setTimeout(resolve, 50)); } assert.fail("fixture state was not reached"); }
function exited(child) { if (child.exitCode !== null || child.signalCode !== null) return Promise.resolve(child.exitCode); return new Promise((resolve) => child.once("exit", resolve)); }
function alive(pid) { try { process.kill(pid, 0); return true; } catch { return false; } }
function fixture(delay = 0) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "ARTEX updater cancellation-"));
  const actual = fs.readFileSync(path.resolve(__dirname, "../scripts/install.ps1"), "utf8");
  // Run the exact production cancellation/permit guards with the real native
  // job host. Signing/staging are covered separately, without test bypasses in
  // the production installer.
  const guards = actual.slice(actual.indexOf("function Assert-NotCancelled"), actual.indexOf("function Acquire-InstallLock"));
  const waiting = actual.slice(actual.indexOf("function Wait-ParentExit"), actual.indexOf("function Assert-FileName"));
  const script = path.join(root, "install.ps1");
  fs.writeFileSync(script, `\uFEFFparam([string]$Mode,[string]$Root,[string]$Archive,[string]$Manifest,[string]$DataHome,[string]$ReadyFile,[string]$ReadyNonce,[string]$PermitFile,[string]$CancelFile,[string]$CancelledFile,[int]$ParentPid,[switch]$Restart)\n$ErrorActionPreference='Stop'\n${guards}\n${waiting}\n$script:parentExited=$false\n$child=Start-Process -FilePath (Join-Path $env:SystemRoot 'System32\\WindowsPowerShell\\v1.0\\powershell.exe') -ArgumentList '-NoProfile -NonInteractive -Command "Start-Sleep -Seconds 120"' -WindowStyle Hidden -PassThru\n[IO.File]::WriteAllText((Join-Path $Root 'descendant.pid'), [string]$child.Id)\n[IO.File]::WriteAllText($ReadyFile,$ReadyNonce)\ntry { Start-Sleep -Milliseconds ${delay}; Wait-ParentExit; Assert-PublishPermitted; [IO.File]::WriteAllText((Join-Path $Root 'published.txt'),'published') } catch { if ($_.Exception -is [OperationCanceledException]) { [IO.File]::WriteAllText($CancelledFile,$ReadyNonce) }; exit 130 }`);
  const integrity = path.join(root, "integrity.txt"); fs.writeFileSync(integrity, `${crypto.createHash("sha256").update(fs.readFileSync(script)).digest("hex")}\n\ndevelopment`, "ascii");
  compile(["/target:winexe", "/main:InstallerHost", "/define:DEVELOPMENT", `/out:${path.join(root, "ARTEX-InstallHost.exe")}`, `/resource:${integrity},installer-integrity.txt`, path.resolve(__dirname, "../scripts/installer-host.cs")]);
  const nonce = crypto.randomBytes(32).toString("hex"), control = { nonce, permitFile: path.join(root, "permit.txt"), cancelFile: path.join(root, "cancel.txt"), cancelledFile: path.join(root, "cancelled.txt") }, ready = path.join(root, "ready.txt");
  return { root, script, ready, control };
}
function start(fixture, parentPid) {
  return spawn(path.join(fixture.root, "ARTEX-InstallHost.exe"), [`--script=${fixture.script}`, "-Mode", "Update", "-Root", fixture.root, "-ReadyFile", fixture.ready, "-ReadyNonce", fixture.control.nonce, "-PermitFile", fixture.control.permitFile, "-CancelFile", fixture.control.cancelFile, "-CancelledFile", fixture.control.cancelledFile, "-ParentPid", String(parentPid)], { windowsHide: true, stdio: "ignore" });
}
function parent(fixture) {
  const source = path.join(fixture.root, "parent.cs"); fs.writeFileSync(source, "using System.Threading; class Parent { static void Main() { Thread.Sleep(120000); } }");
  const file = path.join(fixture.root, "parent.exe"); compile([`/out:${file}`, source]); return spawn(file, [], { windowsHide: true, stdio: "ignore" });
}

test("백업 실패는 게시 허가를 남기지 않고 취소 ACK와 PS 자손 소멸을 확인한다", { skip: process.platform !== "win32", timeout: 30_000 }, async () => {
  const f = fixture();
  const home = path.join(f.root, "home"); fs.mkdirSync(home);
  const updater = new DesktopUpdater({ resourceRoot: f.root, executable: path.join(f.root, "versions/2.2.0/ARTEX.exe"), currentVersion: "2.2.0", home });
  updater.root = f.root; updater.stage = path.join(home, "update"); fs.mkdirSync(updater.stage); updater.archive = "unused"; updater.manifest = { version: "2.2.1" }; updater.state = { state: "downloaded" };
  let quit = false;
  await assert.rejects(updater.install({ stopBackend: async () => { throw new Error("backup failed fixture"); }, quit: () => { quit = true; } }), /backup failed/);
  assert.equal(quit, false); assert.equal(fs.existsSync(path.join(f.root, "published.txt")), false);
  const names = fs.readdirSync(updater.stage); assert.equal(names.some((name) => name.startsWith("permit-")), false);
  const cancelled = names.find((name) => name.startsWith("cancelled-")); assert.ok(cancelled, "helper cancellation ACK must exist");
  const pid = Number(fs.readFileSync(path.join(f.root, "descendant.pid"), "utf8")); await waitFor(() => !alive(pid));
});

test("취소 강제 종료 실패를 숨기지 않으며 부모 종료 뒤에도 허가 없는 게시를 차단한다", { skip: process.platform !== "win32", timeout: 30_000 }, async () => {
  const f = fixture(500), p = parent(f), host = start(f, p.pid);
  try {
    await waitFor(() => fs.existsSync(f.ready));
    await assert.rejects(stopInstaller(host, f.control, () => false, 50), /종료를 확인/);
    assert.equal(fs.readFileSync(f.control.cancelFile, "ascii"), f.control.nonce); assert.equal(fs.existsSync(f.control.permitFile), false);
    p.kill("SIGKILL"); await exited(p); await exited(host);
    assert.equal(fs.existsSync(path.join(f.root, "published.txt")), false); assert.equal(fs.readFileSync(f.control.cancelledFile, "ascii"), f.control.nonce);
    const pid = Number(fs.readFileSync(path.join(f.root, "descendant.pid"), "utf8")); await waitFor(() => !alive(pid));
  } finally { if (p.exitCode === null && p.signalCode === null) p.kill("SIGKILL"); if (host.exitCode === null && host.signalCode === null) host.kill("SIGKILL"); }
});

test("부모 강제 종료와 Host 강제 종료에서도 게시 허가와 Job 수명이 경계를 지킨다", { skip: process.platform !== "win32", timeout: 30_000 }, async () => {
  const f = fixture(), p = parent(f), host = start(f, p.pid);
  try {
    await waitFor(() => fs.existsSync(f.ready)); p.kill("SIGKILL"); await exited(p); await exited(host);
    assert.equal(fs.existsSync(path.join(f.root, "published.txt")), false); assert.equal(fs.readFileSync(f.control.cancelledFile, "ascii"), f.control.nonce);
    const g = fixture(5000), second = start(g, process.pid); await waitFor(() => fs.existsSync(g.ready));
    const descendant = Number(fs.readFileSync(path.join(g.root, "descendant.pid"), "utf8"));
    await stopInstaller(second, g.control, (child) => child.kill("SIGKILL"), 50); await waitFor(() => !alive(descendant));
    assert.equal(fs.existsSync(path.join(g.root, "published.txt")), false);
  } finally { if (p.exitCode === null && p.signalCode === null) p.kill("SIGKILL"); if (host.exitCode === null && host.signalCode === null) host.kill("SIGKILL"); }
});
