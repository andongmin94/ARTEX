const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const os = require("node:os");
const { spawnSync } = require("node:child_process");
const { _electron } = require("@playwright/test");
const { createDistribution, powershell } = require("../scripts/distribution.cjs");

test("임시 설치의 실제 Electron·Go·SQLite 부팅, 업그레이드, 제거와 기존 데이터 보존", { timeout: 10 * 60_000 }, async () => {
  if (process.platform !== "win32") throw new Error("이 검사는 실제 Windows에서 실행해야 합니다");
  const source = process.env.ARTEX_INSTALLER_TEST_BUNDLE;
  if (!source || !path.isAbsolute(source) || !fs.existsSync(path.join(source, "ARTEX.exe"))) throw new Error("최종 빌드의 Windows 실행 폴더를 ARTEX_INSTALLER_TEST_BUNDLE 절대 경로로 지정하세요");
  const verifyRegistration = process.env.ARTEX_INSTALLER_VERIFY_REGISTRATION === "1";
  function registration() {
    return JSON.parse(powershell(["-Command", "$ErrorActionPreference='Stop'; [Console]::OutputEncoding=[Text.UTF8Encoding]::new(); $key='HKCU:\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\ARTEX'; $link=Join-Path ([Environment]::GetFolderPath('Programs')) 'ARTEX.lnk'; $item=if(Test-Path -LiteralPath $key){Get-ItemProperty -LiteralPath $key}; $target=if(Test-Path -LiteralPath $link){(New-Object -ComObject WScript.Shell).CreateShortcut($link).TargetPath}; @{registered=[bool]$item; shortcut=[bool](Test-Path -LiteralPath $link); root=$item.InstallLocation; version=$item.DisplayVersion; target=$target} | ConvertTo-Json -Compress"]));
  }
  if (verifyRegistration) {
    const existing = registration();
    assert.equal(existing.registered, false, "기존 ARTEX 제거 등록이 있으면 OS 등록 검사를 실행하지 않습니다");
    assert.equal(existing.shortcut, false, "기존 ARTEX 시작 메뉴 바로가기가 있으면 OS 등록 검사를 실행하지 않습니다");
  }
  const work = fs.mkdtempSync(path.join(os.tmpdir(), "ARTEX 설치 실제 앱 한글-"));
  const installation = path.join(work, "설치 폴더"), home = path.join(work, "보존할 사용자 데이터"); fs.mkdirSync(home);
  const packageJSON = JSON.parse(fs.readFileSync(path.join(source, "resources/app/package.json"), "utf8"));
  const initial = packageJSON.version;
  const password = "설치 보존 검사-12345678";
  const parts = initial.split(".").map(Number); parts[2]++; const upgraded = parts.join(".");
  function distribution(version) {
    const bundle = path.join(work, `bundle-${version}`);
    fs.cpSync(source, bundle, { recursive: true });
    const pkg = path.join(bundle, "resources/app/package.json");
    fs.writeFileSync(pkg, JSON.stringify({ ...packageJSON, version }, null, 2));
    return createDistribution({ bundle, destination: path.join(work, `distribution-${version}`), version, development: true });
  }
  function install(release) {
    const result = spawnSync(release.setup, ["--quiet", ...(!verifyRegistration ? ["--no-registration"] : []), `--root=${installation}`], { windowsHide: true, encoding: "utf8", timeout: 3 * 60_000 });
    assert.equal(result.status, 0, result.stdout + result.stderr);
    if (verifyRegistration) {
      const state = registration();
      assert.equal(state.registered, true);
      assert.equal(state.shortcut, true);
      assert.equal(fs.realpathSync.native(state.root).toLowerCase(), fs.realpathSync.native(installation).toLowerCase());
      assert.equal(state.version, release.manifest.version);
      assert.equal(fs.realpathSync.native(state.target).toLowerCase(), fs.realpathSync.native(path.join(installation, "ARTEX.exe")).toLowerCase());
    }
  }
  let electron;
  const environment = { ...process.env };
  for (const key of Object.keys(environment)) if (key.startsWith("ARTEX_") || ["OPENAI_API_KEY", "ANTHROPIC_API_KEY"].includes(key)) delete environment[key];
  async function waitForUI(page) {
    await page.waitForURL(/\/function\/tasks\/?$/, { timeout: 60_000 });
    await page.locator('[data-slot="sidebar"]').waitFor({ state: "visible", timeout: 60_000 });
    await page.locator("main").waitFor({ state: "visible", timeout: 60_000 });
  }
  async function api(page, route, method = "GET", body) {
    for (let attempt = 0; ; attempt++) {
      try {
        return await page.evaluate(async ({ route, method, body }) => {
          const response = await fetch(`/api${route}`, { method, headers: { Authorization: `Bearer ${localStorage.getItem("artex_token")}`, ...(body ? { "Content-Type": "application/json" } : {}) }, ...(body ? { body: JSON.stringify(body) } : {}) });
          const data = await response.json();
          if (!response.ok) throw new Error(`${method} ${route}: ${response.status} ${JSON.stringify(data)}`);
          return data;
        }, { route, method, body });
      } catch (error) {
        // 후속 UI 이동만 제한적으로 재시도한다. API/HTTP 오류는 그대로 실패한다.
        if (attempt >= 2 || !error.message.includes("Execution context was destroyed")) throw error;
        await waitForUI(page);
      }
    }
  }
  async function launch(version) {
    electron = await _electron.launch({ executablePath: path.join(installation, "versions", version, "ARTEX.exe"), args: [`--artex-home=${home}`], env: environment, chromiumSandbox: true, timeout: 45_000 });
    electron.process().stdout.on("data", (data) => fs.appendFileSync(path.join(work, `electron-${version}.stdout.log`), data));
    electron.process().stderr.on("data", (data) => fs.appendFileSync(path.join(work, `electron-${version}.stderr.log`), data));
    const page = await electron.firstWindow();
    await page.waitForFunction(async () => (await window.artexDesktop?.status())?.state === "ready", null, { timeout: 60_000 });
    await waitForUI(page);
    assert.equal(await electron.evaluate(({ app }) => app.isPackaged), true);
    assert.equal(await electron.evaluate(({ app }) => app.getVersion()), version);
    const actualHome = await electron.evaluate(({ app }) => app.getPath("userData"));
    assert.equal(actualHome, home);
    assert.equal(fs.existsSync(path.join(home, "data/artex.sqlite")), true);
    return page;
  }
  try {
    const first = distribution(initial); install(first);
    const page = await launch(initial);
    await page.screenshot({ path: path.join(work, "installed-ready.png") });
    const skill = path.join(home, "skills/installation-preserved.txt"); fs.writeFileSync(skill, "사용자가 편집한 스킬");
    await api(page, "/auth/init", "POST", { password });
    await electron.close(); electron = null;
    const second = distribution(upgraded); install(second);
    const next = await launch(upgraded);
    assert.equal(fs.readFileSync(skill, "utf8"), "사용자가 편집한 스킬");
    const auth = await api(next, "/auth/status");
    assert.equal(auth.initialized, true);
    const login = await api(next, "/auth/login", "POST", { username: "ARTEX", password });
    assert.equal(login.token.split(".").length, 3, "the existing password must remain usable after upgrade");
    const healthy = path.join(installation, `healthy-${upgraded}.txt`);
    for (let attempt = 0; attempt < 100 && !fs.existsSync(healthy); attempt++) await new Promise((resolve) => setTimeout(resolve, 100));
    assert.equal(fs.readFileSync(path.join(installation, `healthy-${upgraded}.txt`), "utf8"), upgraded, "actual Go ready must confirm upgrade health");
    await next.screenshot({ path: path.join(work, "installed-upgraded.png") });
    await electron.close(); electron = null;
    powershell(["-File", path.join(installation, "install.ps1"), "-Mode", "Uninstall", "-Root", installation]);
    assert.equal(fs.existsSync(path.join(installation, "versions")), false); assert.equal(fs.existsSync(path.join(home, "data/artex.sqlite")), true); assert.equal(fs.readFileSync(skill, "utf8"), "사용자가 편집한 스킬");
    if (verifyRegistration) {
      const state = registration();
      assert.equal(state.registered, false);
      assert.equal(state.shortcut, false);
    }
    fs.writeFileSync(path.join(work, "verification.json"), JSON.stringify({ install: true, actualElectronGoSQLite: true, upgrade: true, metadataVersions: [initial, upgraded], upgradeChanges: "test-only package.json version; app code identical", readinessConfirmed: true, dataPreserved: true, existingPasswordLogin: true, uninstall: true, registrationWritten: verifyRegistration, registrationRemoved: verifyRegistration, signedProduction: false }, null, 2));
    console.log(`실제 설치 앱 검사 증거: ${work}`);
  } catch (error) {
    fs.writeFileSync(path.join(work, "failure.txt"), error.stack || String(error));
    console.error(`실패 검사 증거: ${work}`);
    throw error;
  } finally {
    try { if (electron) await electron.close(); }
    finally {
      // An interrupted OS integration check removes only this owned installation.
      if (verifyRegistration && fs.existsSync(path.join(installation, "installation.json"))) powershell(["-File", path.join(installation, "install.ps1"), "-Mode", "Uninstall", "-Root", installation]);
    }
  }
});
