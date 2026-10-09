const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const os = require("node:os");
const crypto = require("node:crypto");
const { spawn, spawnSync } = require("node:child_process");
const { validateManifest, compareVersions, safeFile, httpsURL, DesktopUpdater } = require("../src/update.cjs");
const { createDistribution, powershell } = require("../scripts/distribution.cjs");

const keys = crypto.generateKeyPairSync("rsa", { modulusLength: 3072 });
const publicKey = keys.publicKey.export({ format: "jwk" });
function shortPath(value) {
  const literal = "'" + value.replaceAll("'", "''") + "'";
  return powershell(["-Command", `Add-Type -TypeDefinition @'
using System; using System.Text; using System.Runtime.InteropServices;
public static class ShortInstallPath {
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern uint GetShortPathName(string path, StringBuilder buffer, uint length);
  public static string Get(string path) { var buffer=new StringBuilder(32768); if(GetShortPathName(path,buffer,(uint)buffer.Capacity)==0) throw new Exception("short path fixture unavailable"); return buffer.ToString(); }
}
'@
[Console]::Write([ShortInstallPath]::Get(${literal}))`]).trim();
}
function release(overrides = {}, key = keys.privateKey) {
  const manifest = { schema: 1, product: "ARTEX", version: "2.2.1", platform: "win32", arch: process.arch, url: "https://example.invalid/app.zip", sha256: "a".repeat(64), size: 123, files: { "ARTEX.exe": "b".repeat(64), "resources/app/package.json": "c".repeat(64), "resources/artex/artex.exe": "d".repeat(64) }, ...overrides };
  const bytes = Buffer.from(JSON.stringify(manifest));
  return { payload: bytes.toString("base64"), signature: crypto.sign("RSA-SHA256", bytes, key).toString("base64") };
}
test("업데이트는 고정 배포 키·제품·플랫폼·완전한 묶음·상향 버전을 검증한다", () => {
  assert.equal(validateManifest(release(), publicKey, "2.2.0").version, "2.2.1");
  assert.throws(() => validateManifest(release(), crypto.generateKeyPairSync("rsa", { modulusLength: 3072 }).publicKey.export({ format: "jwk" }), "2.2.0"), /서명/);
  const corrupt = release(); corrupt.payload = Buffer.from("{}").toString("base64");
  assert.throws(() => validateManifest(corrupt, publicKey, "2.2.0"), /서명/);
  for (const override of [{ product: "Other" }, { platform: "linux" }, { arch: "ia32" }, { size: -1 }, { size: 2 ** 32 }, { sha256: "bad" }, { version: "2.2.0" }, { version: "1.9.9" }, { version: "2.2.1-beta" }, { url: "http://example.invalid/app.zip" }, { url: "https://user:secret@example.invalid/a" }, { files: { "ARTEX.exe": "a".repeat(64), "x": "b".repeat(64), "../x": "c".repeat(64) } }]) assert.throws(() => validateManifest(release(override), publicKey, "2.2.0"));
  assert.equal(compareVersions("2.10.0", "2.9.99"), 1);
  for (const name of ["../x", "C:/x", "a\\x", "/x", "a/CON", "a/b.", "a//b"]) assert.throws(() => safeFile(name));
  assert.throws(() => httpsURL("https://example.invalid/a#fragment"));
});

test("피드가 없는 빌드는 네트워크를 호출하지 않고 업데이트 미설정을 표시한다", async () => {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), "ARTEX-update-disabled-"));
  const updater = new DesktopUpdater({ resourceRoot: home, executable: path.join(home, "ARTEX.exe"), currentVersion: "2.2.0", home });
  const original = global.fetch; global.fetch = () => { throw new Error("unexpected network"); };
  try { assert.equal((await updater.check()).state, "unavailable"); assert.equal((await updater.download()).state, "unavailable"); }
  finally { global.fetch = original; }
});

test("서명된 피드와 스트림 SHA256을 확인하며 손상 다운로드는 적용하지 않는다", { skip: process.platform !== "win32" }, async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "ARTEX 업데이트 한글-"));
  const resources = path.join(root, "versions/2.2.0/resources/artex"); fs.mkdirSync(resources, { recursive: true });
  fs.writeFileSync(path.join(root, "installation.json"), JSON.stringify({ schema: 1, product: "ARTEX", root }));
  fs.writeFileSync(path.join(resources, "update.json"), JSON.stringify({ feedURL: "https://example.invalid/release.json", publicKey }));
  fs.writeFileSync(path.join(root, "versions/2.2.0/ARTEX.exe"), "existing executable path fixture");
  const home = path.join(root, "user-data"); fs.mkdirSync(home);
  const bytes = Buffer.from("실제 스트림 검증"), sha256 = crypto.createHash("sha256").update(bytes).digest("hex");
  const envelope = release({ sha256, size: bytes.length });
  const make = () => new DesktopUpdater({ resourceRoot: resources, executable: path.join(root, "versions/2.2.0/ARTEX.exe"), currentVersion: "2.2.0", home });
  const original = global.fetch;
  try {
    global.fetch = async (url, options) => { assert.equal(options.redirect, "error"); return String(url).endsWith("release.json") ? Response.json(envelope) : new Response(bytes); };
    const good = make(); assert.equal((await good.check()).state, "available"); assert.equal((await good.download()).state, "downloaded"); assert.deepEqual(fs.readFileSync(good.archive), bytes);
    const bad = make(); await bad.check(); global.fetch = async () => new Response(Buffer.alloc(bytes.length));
    await assert.rejects(bad.download(), /SHA256/); assert.equal(bad.status().state, "failed"); assert.equal(fs.existsSync(bad.archive), false);
    await assert.rejects(bad.install({ stopBackend: () => assert.fail("backend must not stop"), quit: () => assert.fail("must not quit") }), /먼저/);
  } finally { global.fetch = original; }
});

test("긴 경로와 실제 Windows 8.3 별칭은 같은 설치로 확인하며 다른 버전·소유권·junction은 거부한다", { skip: process.platform !== "win32" }, () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "ARTEX long installation alias-"));
  const resources = path.join(root, "versions/2.2.1/resources/artex"); fs.mkdirSync(resources, { recursive: true });
  const executable = path.join(root, "versions/2.2.1/ARTEX.exe"); fs.writeFileSync(executable, "existing version fixture");
  fs.writeFileSync(path.join(root, "pending.txt"), "2.2.0\n2.2.1");
  fs.writeFileSync(path.join(resources, "update.json"), JSON.stringify({ feedURL: "https://example.invalid/release.json", publicKey }));
  const shortRoot = shortPath(root);
  const longRoot = fs.realpathSync.native(root);
  assert.notEqual(shortRoot.toLowerCase(), longRoot.toLowerCase(), "the regression must exercise a real native 8.3 alias");
  const ownership = path.join(root, "installation.json");
  for (const [owned, selected] of [[longRoot, shortRoot], [shortRoot, longRoot]]) {
    fs.writeFileSync(ownership, JSON.stringify({ schema: 1, product: "ARTEX", root: owned }));
    const updater = new DesktopUpdater({ resourceRoot: resources, executable: path.join(selected, "versions/2.2.1/ARTEX.exe"), currentVersion: "2.2.1", home: root });
    assert.equal(updater.status().state, "idle"); assert.equal(updater.root, longRoot, "the helper must receive the canonical long installation root"); updater.confirmReady();
    assert.equal(fs.readFileSync(path.join(root, "healthy-2.2.1.txt"), "utf8"), "2.2.1");
    const development = new DesktopUpdater({ resourceRoot: path.join(root, "no-feed"), executable: path.join(selected, "versions/2.2.1/ARTEX.exe"), currentVersion: "2.2.1", home: root });
    development.confirmReady();
  }
  fs.writeFileSync(ownership, JSON.stringify({ schema: 1, product: "ARTEX", root: longRoot }));
  assert.throws(() => new DesktopUpdater({ resourceRoot: resources, executable, currentVersion: "2.2.0", home: root }));
  const wrongVersion = new DesktopUpdater({ resourceRoot: path.join(root, "no-feed"), executable, currentVersion: "2.2.0", home: root });
  assert.throws(() => wrongVersion.confirmReady());
  const linkedRoot = root + "-junction"; fs.symlinkSync(longRoot, linkedRoot, "junction");
  assert.throws(() => new DesktopUpdater({ resourceRoot: resources, executable: path.join(linkedRoot, "versions/2.2.1/ARTEX.exe"), currentVersion: "2.2.1", home: root }), /연결된 경로/);
  const linked = new DesktopUpdater({ resourceRoot: path.join(root, "no-feed"), executable: path.join(linkedRoot, "versions/2.2.1/ARTEX.exe"), currentVersion: "2.2.1", home: root });
  assert.throws(() => linked.confirmReady(), /연결된 경로/);
  for (const invalid of [{ schema: 2, product: "ARTEX", root: longRoot }, { schema: 1, product: "Other", root: longRoot }, { schema: 1, product: "ARTEX", root: "." }, { schema: 1, product: "ARTEX", root: os.tmpdir() }]) {
    fs.writeFileSync(ownership, JSON.stringify(invalid));
    assert.throws(() => new DesktopUpdater({ resourceRoot: resources, executable, currentVersion: "2.2.1", home: root }));
    assert.throws(() => new DesktopUpdater({ resourceRoot: path.join(root, "no-feed"), executable, currentVersion: "2.2.1", home: root }).confirmReady());
  }
  fs.writeFileSync(ownership, JSON.stringify({ schema: 1, product: "ARTEX", root: shortRoot, registered: false }));
  powershell(["-File", path.resolve(__dirname, "../scripts/install.ps1"), "-Mode", "Uninstall", "-Root", longRoot, "-NoRegistration"]);
  assert.equal(fs.existsSync(path.join(root, "versions")), false, "the real PS helper must accept a short ownership record with a long supplied root");
});

test("Windows 설치 EXE의 설치·업그레이드·rollback·재시도·제거·같은 경로 재설치가 사용자 자료를 보존한다", { skip: process.platform !== "win32", timeout: 120_000 }, async () => {
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), "ARTEX 설치 한글 공백-"));
  const installation = path.join(temporary, "app"), userData = path.join(temporary, "사용자 데이터"); fs.mkdirSync(userData); fs.writeFileSync(path.join(userData, "artex.sqlite"), "preserve database fixture");
  function create(version, fail) {
    const bundle = path.join(temporary, `bundle-${version}`), resources = path.join(bundle, "resources/artex"); fs.mkdirSync(resources, { recursive: true }); fs.mkdirSync(path.join(bundle, "resources/app"), { recursive: true });
    fs.writeFileSync(path.join(bundle, "resources/app/package.json"), JSON.stringify({ version })); fs.writeFileSync(path.join(resources, "artex.exe"), "Go executable fixture; this test covers the installer, not real Go startup");
    const source = path.join(temporary, `fixture-${version}.cs`);
    fs.writeFileSync(source, `using System; using System.IO; class Fixture { static int Main() { ${fail ? "return 9;" : `string root=Path.GetFullPath(Path.Combine(AppDomain.CurrentDomain.BaseDirectory, "../..")); File.WriteAllText(Path.Combine(root,"ran-${version}.txt"), "ready"); return 0;`} } }`);
    const compilation = spawnSync(path.join(process.env.SystemRoot, "Microsoft.NET/Framework64/v4.0.30319/csc.exe"), ["/nologo", `/out:${path.join(bundle, "ARTEX.exe")}`, source], { encoding: "utf8", windowsHide: true }); assert.equal(compilation.status, 0, compilation.stdout + compilation.stderr);
    return createDistribution({ bundle, destination: path.join(temporary, `release-${version}`), version, development: true });
  }
  function setup(distribution, root = installation) {
    return spawnSync(distribution.setup, ["--quiet", "--no-registration", `--root=${root}`], { windowsHide: true, encoding: "utf8", timeout: 60_000 });
  }
  const first = create("2.2.0", false), second = create("2.2.1", true);
  // The real Windows verifier must accept the pinned RSA signature before its
  // separate Authenticode gate rejects this explicit unsigned fixture.
  const unsigned = JSON.parse(fs.readFileSync(path.join(first.destination, "release.json"), "utf8"));
  const payload = Buffer.from(unsigned.payload, "base64");
  const signedRelease = path.join(temporary, "signed-release.json"), trust = path.join(temporary, "signed-trust.json");
  fs.writeFileSync(signedRelease, JSON.stringify({ payload: unsigned.payload, signature: crypto.sign("RSA-SHA256", payload, keys.privateKey).toString("base64") }));
  fs.writeFileSync(trust, JSON.stringify({ publicKey }));
  assert.throws(() => powershell(["-File", path.resolve(__dirname, "../scripts/install.ps1"), "-Root", path.join(temporary, "signed-install"), "-Archive", first.archive, "-Manifest", signedRelease, "-Trust", trust, "-NoRegistration"]), /Authenticode signature/);
  const initialInstall = setup(first); assert.equal(initialInstall.status, 0, initialInstall.stdout + initialInstall.stderr);
  assert.equal(fs.readFileSync(path.join(installation, "current.txt"), "utf8"), "2.2.0");
  const runningSource = path.join(temporary, "running-app-fixture.cs"), runningExecutable = path.join(installation, "versions/2.2.0/running-app-fixture.exe"), runningMarker = path.join(temporary, "running-app.pid");
  fs.writeFileSync(runningSource, 'using System; using System.IO; using System.Threading; class RunningFixture { static void Main(string[] args) { File.WriteAllText(args[0], "running"); Thread.Sleep(60000); } }');
  const runningCompilation = spawnSync(path.join(process.env.SystemRoot, "Microsoft.NET/Framework64/v4.0.30319/csc.exe"), ["/nologo", `/out:${runningExecutable}`, runningSource], { encoding: "utf8", windowsHide: true });
  assert.equal(runningCompilation.status, 0, runningCompilation.stdout + runningCompilation.stderr);
  const running = spawn(shortPath(runningExecutable), [runningMarker], { windowsHide: true, stdio: "ignore" });
  const runningExit = new Promise((resolve, reject) => { running.once("exit", resolve); running.once("error", reject); });
  try {
    for (let attempt = 0; attempt < 100 && !fs.existsSync(runningMarker); attempt++) await new Promise((resolve) => setTimeout(resolve, 50));
    assert.equal(fs.readFileSync(runningMarker, "utf8"), "running");
    const nativeProcessPath = powershell(["-Command", `(Get-Process -Id ${running.pid}).Path`]).trim();
    const longRoot = fs.realpathSync.native(installation);
    assert.equal(nativeProcessPath.toLowerCase().startsWith(longRoot.toLowerCase() + path.sep), false, "the live process must actually retain its 8.3 path");
    const blockedUpgrade = setup(second, longRoot); assert.equal(blockedUpgrade.status, 1, "an active 8.3 process must block Setup upgrade");
    assert.throws(() => powershell(["-File", path.resolve(__dirname, "../scripts/install.ps1"), "-Mode", "Uninstall", "-Root", longRoot, "-NoRegistration"]), /ARTEX를 종료/);
    assert.equal(fs.readFileSync(path.join(installation, "current.txt"), "utf8"), "2.2.0");
    assert.equal(fs.existsSync(path.join(installation, "versions/2.2.1")), false);
    assert.equal(fs.existsSync(path.join(installation, "installation.json")), true);
  } finally {
    if (running.exitCode === null) running.kill("SIGKILL");
    await runningExit; fs.unlinkSync(runningExecutable);
  }
  const originalScript = fs.readFileSync(path.join(installation, "install.ps1"));
  fs.appendFileSync(path.join(installation, "install.ps1"), "\n# Tampered helper fixture\n");
  const updateHome = path.join(temporary, "update-data"); fs.mkdirSync(updateHome);
  const refused = new DesktopUpdater({ resourceRoot: path.join(installation, "versions/2.2.0/resources/artex"), executable: path.join(installation, "versions/2.2.0/ARTEX.exe"), currentVersion: "2.2.0", home: updateHome });
  refused.stage = fs.mkdtempSync(path.join(updateHome, "prepare-")); refused.archive = first.archive; refused.manifest = { version: "2.2.1" }; refused.state = { state: "downloaded" };
  let stopped = false, quit = false;
  await assert.rejects(refused.install({ stopBackend: async () => { stopped = true; }, quit: () => { quit = true; } }), /SHA256/);
  assert.equal(stopped, false, "helper preparation failure must retain the backend"); assert.equal(quit, false, "process spawn alone must not authorize app exit"); assert.equal(refused.status().state, "failed");
  fs.writeFileSync(path.join(installation, "install.ps1"), originalScript);

  function asynchronous(command, args) {
    const child = spawn(command, args, { windowsHide: true, stdio: "ignore" });
    return { child, result: new Promise((resolve, reject) => { child.once("exit", resolve); child.once("error", reject); }) };
  }
  const raceRoot = path.join(temporary, "parallel-install");
  const racing = await Promise.all([1, 2].map(() => asynchronous(first.setup, ["--quiet", "--no-registration", `--root=${raceRoot}`]).result));
  assert.deepEqual(racing.sort(), [0, 1], "same-root installers must serialize and reject the stale installation");
  assert.equal(fs.readFileSync(path.join(raceRoot, "current.txt"), "utf8"), "2.2.0");
  assert.equal(fs.realpathSync.native(JSON.parse(fs.readFileSync(path.join(raceRoot, "installation.json"), "utf8").replace(/^\uFEFF/, "")).root), fs.realpathSync.native(raceRoot));

  const canonicalRoot = fs.realpathSync.native(raceRoot).replace(/[\\/]+$/, "").toUpperCase();
  const mutexName = "Local\\ARTEX-Install-" + crypto.createHash("sha256").update(canonicalRoot, "utf8").digest("hex");
  const marker = path.join(temporary, "mutex-held.txt"), releaseLock = path.join(temporary, "release-mutex.txt"), lockScript = path.join(temporary, "hold-lock.ps1");
  const literal = (value) => "'" + value.replaceAll("'", "''") + "'";
  fs.writeFileSync(lockScript, `\uFEFF$mutex = New-Object Threading.Mutex($false, ${literal(mutexName)})\n$mutex.WaitOne() | Out-Null\ntry { [IO.File]::WriteAllText(${literal(marker)}, 'held'); while (-not [IO.File]::Exists(${literal(releaseLock)})) { Start-Sleep -Milliseconds 50 } } finally { $mutex.ReleaseMutex(); $mutex.Dispose() }`);
  const ps = path.join(process.env.SystemRoot, "System32/WindowsPowerShell/v1.0/powershell.exe");
  const locker = asynchronous(ps, ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", lockScript]);
  for (let retry = 0; retry < 100 && !fs.existsSync(marker); retry++) await new Promise((resolve) => setTimeout(resolve, 50));
  assert.equal(fs.existsSync(marker), true);
  const guardedLauncher = asynchronous(path.join(raceRoot, "ARTEX.exe"), []);
  await new Promise((resolve) => setTimeout(resolve, 200));
  assert.equal(guardedLauncher.child.exitCode, null, "launcher must respect the installer's root mutex");
  assert.equal(fs.existsSync(path.join(raceRoot, "ran-2.2.0.txt")), false);
  fs.writeFileSync(releaseLock, "release"); assert.equal(await locker.result, 0); assert.equal(await guardedLauncher.result, 0);
  for (let retry = 0; retry < 50 && !fs.existsSync(path.join(raceRoot, "ran-2.2.0.txt")); retry++) await new Promise((resolve) => setTimeout(resolve, 50));
  assert.equal(fs.readFileSync(path.join(raceRoot, "ran-2.2.0.txt"), "utf8"), "ready");

  const installWhileRemoving = asynchronous(second.setup, ["--quiet", "--no-registration", `--root=${raceRoot}`]);
  const removingWhileInstalling = asynchronous(ps, ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", path.resolve(__dirname, "../scripts/install.ps1"), "-Mode", "Uninstall", "-Root", raceRoot]);
  const raceResults = await Promise.all([installWhileRemoving.result, removingWhileInstalling.result]);
  assert.equal(raceResults.includes(0), true);
  if (fs.existsSync(path.join(raceRoot, "installation.json"))) {
    const active = fs.readFileSync(path.join(raceRoot, "current.txt"), "utf8"); assert.equal(active, "2.2.1");
    assert.equal(JSON.parse(fs.readFileSync(path.join(raceRoot, "versions", active, "resources/app/package.json"), "utf8")).version, active);
    for (const ownedFile of ["ARTEX.exe", "ARTEX-InstallHost.exe", "install.ps1", "trust.json"]) assert.equal(fs.existsSync(path.join(raceRoot, ownedFile)), true);
    powershell(["-File", path.resolve(__dirname, "../scripts/install.ps1"), "-Mode", "Uninstall", "-Root", raceRoot]);
  } else assert.equal(fs.existsSync(path.join(raceRoot, "versions")), false);
  fs.writeFileSync(path.join(installation, "user-note.txt"), "preserve unrelated file");
  const upgradeInstall = setup(second); assert.equal(upgradeInstall.status, 0, upgradeInstall.stdout + upgradeInstall.stderr);
  assert.equal(fs.readFileSync(path.join(installation, "current.txt"), "utf8"), "2.2.1");
  const rollback = spawnSync(path.join(installation, "ARTEX.exe"), [], { windowsHide: true, timeout: 15_000 }); assert.equal(rollback.status, 1);
  assert.equal(fs.readFileSync(path.join(installation, "current.txt"), "utf8"), "2.2.0"); assert.equal(fs.existsSync(path.join(installation, "pending.txt")), false);
  for (let retry = 0; retry < 30 && !fs.existsSync(path.join(installation, "ran-2.2.0.txt")); retry++) await new Promise((resolve) => setTimeout(resolve, 100));
  assert.equal(fs.readFileSync(path.join(installation, "ran-2.2.0.txt"), "utf8"), "ready");
  const failedVersion = path.join(installation, "versions/2.2.1");
  fs.writeFileSync(path.join(failedVersion, "user-note-in-failed-version.txt"), "preserve this unrelated note");
  const retryFailedVersion = setup(second);
  assert.equal(retryFailedVersion.status, 0, retryFailedVersion.stdout + retryFailedVersion.stderr);
  assert.equal(fs.readFileSync(path.join(installation, "current.txt"), "utf8"), "2.2.1", "the same failed version must be installable after rollback");
  const quarantinePrefix = "ARTEX-quarantine-" + crypto.createHash("sha256").update(fs.realpathSync.native(installation).toUpperCase(), "utf8").digest("hex") + "-2.2.1-";
  const quarantined = fs.readdirSync(temporary).find((name) => name.startsWith(quarantinePrefix));
  assert.ok(quarantined, "the failed owned bundle must be preserved in quarantine");
  assert.equal(fs.readFileSync(path.join(temporary, quarantined, "user-note-in-failed-version.txt"), "utf8"), "preserve this unrelated note");
  assert.equal(fs.readdirSync(installation).some((name) => name.startsWith("quarantine-")), false);
  assert.equal(fs.existsSync(path.join(installation, "versions/2.2.1/.artex-ready-failed.txt")), false, "the retry must contain fresh staging files");
  assert.equal(setup(first).status, 1, "same-version/downgrade must fail");
  const occupied = path.join(temporary, "unowned"); fs.mkdirSync(occupied); fs.writeFileSync(path.join(occupied, "keep.txt"), "keep"); assert.equal(setup(first, occupied).status, 1); assert.equal(fs.readFileSync(path.join(occupied, "keep.txt"), "utf8"), "keep");
  const bad = path.join(temporary, "corrupt.zip"); const corrupt = fs.readFileSync(first.archive); corrupt[0] ^= 1; fs.writeFileSync(bad, corrupt);
  assert.throws(() => powershell(["-File", path.resolve(__dirname, "../scripts/install.ps1"), "-Mode", "Install", "-Root", path.join(temporary, "corrupt-install"), "-Archive", bad, "-Manifest", path.join(first.destination, "release.json"), "-Trust", path.join(first.destination, "trust.json"), "-Development", "-NoRegistration"]), /SHA256/);
  const nativeRemoval = spawnSync(path.join(installation, "ARTEX-InstallHost.exe"), [`--script=${path.join(installation, "install.ps1")}`, "-Mode", "Uninstall", "-Root", installation], { windowsHide: true, timeout: 15_000 });
  assert.equal(nativeRemoval.status, 0);
  for (let retry = 0; retry < 100 && fs.existsSync(path.join(installation, "versions")); retry++) await new Promise((resolve) => setTimeout(resolve, 100));
  assert.equal(fs.existsSync(path.join(installation, "versions")), false); assert.equal(fs.readFileSync(path.join(installation, "user-note.txt"), "utf8"), "preserve unrelated file"); assert.equal(fs.readFileSync(path.join(userData, "artex.sqlite"), "utf8"), "preserve database fixture");
  assert.equal(fs.readFileSync(path.join(temporary, quarantined, "user-note-in-failed-version.txt"), "utf8"), "preserve this unrelated note", "uninstall must retain the sibling quarantine");

  // Reproduce the complete lifecycle with only installer-owned root contents.
  // The fixture's readiness output is test-owned, so remove that output before
  // uninstall; unrelated root files continue to be preserved above.
  const reinstallRoot = path.join(temporary, "rollback-retry-reinstall");
  for (const distribution of [first, second]) {
    const result = setup(distribution, reinstallRoot); assert.equal(result.status, 0, result.stdout + result.stderr);
  }
  const reinstallRollback = spawnSync(path.join(reinstallRoot, "ARTEX.exe"), [], { windowsHide: true, timeout: 15_000 });
  assert.equal(reinstallRollback.status, 1);
  assert.equal(fs.readFileSync(path.join(reinstallRoot, "current.txt"), "utf8"), "2.2.0");
  const readinessMarker = path.join(reinstallRoot, "ran-2.2.0.txt");
  for (let retry = 0; retry < 30 && !fs.existsSync(readinessMarker); retry++) await new Promise((resolve) => setTimeout(resolve, 100));
  assert.equal(fs.readFileSync(readinessMarker, "utf8"), "ready"); fs.unlinkSync(readinessMarker);
  fs.writeFileSync(path.join(reinstallRoot, "versions/2.2.1/preserved-note.txt"), "keep this failed-version note");
  const retried = setup(second, reinstallRoot); assert.equal(retried.status, 0, retried.stdout + retried.stderr);
  const reinstallQuarantinePrefix = "ARTEX-quarantine-" + crypto.createHash("sha256").update(fs.realpathSync.native(reinstallRoot).toUpperCase(), "utf8").digest("hex") + "-2.2.1-";
  const reinstallQuarantine = fs.readdirSync(temporary).find((name) => name.startsWith(reinstallQuarantinePrefix));
  assert.ok(reinstallQuarantine);
  const removal = spawnSync(path.join(reinstallRoot, "ARTEX-InstallHost.exe"), [`--script=${path.join(reinstallRoot, "install.ps1")}`, "-Mode", "Uninstall", "-Root", reinstallRoot], { windowsHide: true, timeout: 15_000 });
  assert.equal(removal.status, 0);
  for (let retry = 0; retry < 100 && fs.existsSync(reinstallRoot); retry++) await new Promise((resolve) => setTimeout(resolve, 100));
  assert.equal(fs.existsSync(reinstallRoot), false, "owned quarantine must not leave an unowned nonempty installation root");
  assert.equal(fs.readFileSync(path.join(temporary, reinstallQuarantine, "preserved-note.txt"), "utf8"), "keep this failed-version note");
  const reinstalled = setup(second, reinstallRoot); assert.equal(reinstalled.status, 0, reinstalled.stdout + reinstalled.stderr);
  assert.equal(fs.readFileSync(path.join(reinstallRoot, "current.txt"), "utf8"), "2.2.1");
  assert.equal(fs.readFileSync(path.join(temporary, reinstallQuarantine, "preserved-note.txt"), "utf8"), "keep this failed-version note", "reinstall must not overwrite preserved notes");
  fs.writeFileSync(path.join(temporary, "verification.json"), JSON.stringify({ installer: "actual Windows Setup.exe", install: true, upgrade: true, rollback: true, retry: true, uninstall: true, sameRootReinstall: true, failedVersionNotesPreserved: true, userDataPreserved: true, registrationWritten: false, productBackendExecuted: false }, null, 2));
  console.log(`로컬 설치 검사 증거: ${temporary}`);
});

test("배포 서명 자격 증명이 없으면 공개 배포 프로그램 생성을 거부한다", { skip: process.platform !== "win32" }, () => {
  const keysToClear = ["ARTEX_SIGN_CERTIFICATE", "ARTEX_SIGN_TIMESTAMP", "ARTEX_UPDATE_PRIVATE_KEY", "ARTEX_UPDATE_FEED_URL", "ARTEX_UPDATE_ARCHIVE_URL"], previous = {};
  for (const name of keysToClear) { previous[name] = process.env[name]; delete process.env[name]; }
  try { assert.throws(() => createDistribution({ bundle: "unused", destination: path.join(os.tmpdir(), `ARTEX-no-signing-${crypto.randomUUID()}`), version: "2.2.0" }), /ARTEX_SIGN_CERTIFICATE/); }
  finally { for (const name of keysToClear) if (previous[name] !== undefined) process.env[name] = previous[name]; }
});
