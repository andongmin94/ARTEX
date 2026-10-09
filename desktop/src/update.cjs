const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
const { spawn } = require("node:child_process");
const { pipeline } = require("node:stream/promises");
const { Readable, Transform } = require("node:stream");

const MAX_ARCHIVE = 2 * 1024 * 1024 * 1024;
function version(value) {
  if (typeof value !== "string" || !/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(value)) throw new Error("정식 앱 버전이 올바르지 않습니다");
  const parts = value.split(".").map(Number);
  if (parts.some((part) => !Number.isSafeInteger(part) || part > 2147483647)) throw new Error("앱 버전이 범위를 벗어났습니다");
  return parts;
}
function compareVersions(a, b) {
  const left = version(a), right = version(b);
  for (let i = 0; i < 3; i++) if (left[i] !== right[i]) return left[i] > right[i] ? 1 : -1;
  return 0;
}
function httpsURL(value) {
  const url = new URL(value);
  if (url.protocol !== "https:" || url.username || url.password || url.hash) throw new Error("업데이트 주소는 자격 증명 없는 HTTPS여야 합니다");
  return url;
}
function safeFile(value) {
  if (typeof value !== "string" || value.includes("\\") || value.includes(":") || value.startsWith("/") || value.split("/").some((part) => !part || part === "." || part === ".." || /[\x00-\x1f<>"|?*]|[. ]$/.test(part) || /^(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\.|$)/i.test(part))) throw new Error("업데이트 파일 경로가 올바르지 않습니다");
  return value;
}
function sameInstalledPath(left, right) {
  for (const value of [left, right]) {
    if (typeof value !== "string" || !path.isAbsolute(value)) return false;
    let cursor = path.resolve(value);
    while (true) {
      if (fs.lstatSync(cursor).isSymbolicLink()) throw new Error("연결된 경로는 업데이트 설치 경로로 사용할 수 없습니다");
      const parent = path.dirname(cursor);
      if (parent === cursor) break;
      cursor = parent;
    }
  }
  // Windows의 긴 이름과 8.3 별칭은 같은 실제 파일로 대조한다.
  return fs.realpathSync.native(left).toLowerCase() === fs.realpathSync.native(right).toLowerCase();
}
function validateManifest(envelope, publicKey, currentVersion, platform = process.platform, arch = process.arch) {
  if (!envelope || typeof envelope.payload !== "string" || typeof envelope.signature !== "string" || envelope.payload.length > 4_000_000 || !/^[A-Za-z0-9+/]+={0,2}$/.test(envelope.payload) || !/^[A-Za-z0-9+/]+={0,2}$/.test(envelope.signature)) throw new Error("서명된 업데이트 정보가 올바르지 않습니다");
  const bytes = Buffer.from(envelope.payload, "base64");
  const key = crypto.createPublicKey({ key: publicKey, format: "jwk" });
  if (key.asymmetricKeyType !== "rsa" || key.asymmetricKeyDetails.modulusLength < 3072 || !crypto.verify("RSA-SHA256", bytes, key, Buffer.from(envelope.signature, "base64"))) throw new Error("업데이트 서명이 신뢰된 배포 키와 일치하지 않습니다");
  const manifest = JSON.parse(bytes.toString("utf8"));
  version(manifest.version);
  if (manifest.schema !== 1 || manifest.product !== "ARTEX" || manifest.platform !== platform || manifest.arch !== arch || !Number.isSafeInteger(manifest.size) || manifest.size < 1 || manifest.size > MAX_ARCHIVE || !/^[a-f0-9]{64}$/.test(manifest.sha256)) throw new Error("업데이트 제품·환경·무결성 정보가 올바르지 않습니다");
  if (compareVersions(manifest.version, currentVersion) <= 0) throw new Error("같은 버전이나 이전 버전으로 업데이트할 수 없습니다");
  httpsURL(manifest.url);
  if (!manifest.files || Object.keys(manifest.files).length < 3 || Object.keys(manifest.files).length > 20_000) throw new Error("업데이트 파일 목록이 올바르지 않습니다");
  const names = new Set();
  for (const [name, hash] of Object.entries(manifest.files)) {
    safeFile(name);
    if (names.has(name.toLowerCase()) || !/^[a-f0-9]{64}$/.test(hash)) throw new Error("중복되거나 손상된 업데이트 파일 목록입니다");
    names.add(name.toLowerCase());
  }
  for (const required of ["ARTEX.exe", "resources/app/package.json", "resources/artex/artex.exe"]) if (!manifest.files[required]) throw new Error("Electron·Go·UI 통합 패키지가 아닙니다");
  return manifest;
}
function waitInstallerExit(child, timeoutMs) {
  if (child.exitCode !== null || child.signalCode !== null) return Promise.resolve(true);
  return new Promise((resolve) => {
    const exited = () => { clearTimeout(timeout); resolve(true); };
    const timeout = setTimeout(() => { child.removeListener("exit", exited); resolve(false); }, timeoutMs);
    child.once("exit", exited);
  });
}
async function stopInstaller(child, control, terminate = (process) => process.kill("SIGKILL"), timeoutMs = 5000) {
  if (control) {
    // 백업이 실패한 부모가 강제 종료돼도 게시 허가가 남지 않게 먼저 회수한다.
    fs.rmSync(control.permitFile, { force: true });
    fs.writeFileSync(control.cancelFile, control.nonce, { encoding: "ascii", flag: "wx" });
  }
  if (!child || await waitInstallerExit(child, timeoutMs)) return;
  // 네이티브 Host의 마지막 Job handle이 닫히면 PS와 모든 자손도 종료된다.
  if (!terminate(child) || !await waitInstallerExit(child, timeoutMs)) throw new Error("업데이트 취소 신호를 남겼지만 설치 도우미 종료를 확인할 수 없습니다. 기존 앱을 유지합니다");
}
function awaitInstallerReady(child, file, nonce, log) {
  return new Promise((resolve, reject) => {
    const finish = (error) => { clearTimeout(timeout); clearInterval(interval); child.removeListener("exit", exited); child.removeListener("error", rejected); error ? reject(error) : resolve(); };
    const diagnostic = () => fs.existsSync(log) ? fs.readFileSync(log, "utf8").replace(/^\uFEFF/, "").slice(-4000).trim() : "설치 도우미를 시작할 수 없습니다";
    const exited = (code) => finish(new Error(`업데이트 준비 실패 (${code}): ${diagnostic()}`));
    const rejected = (error) => finish(error);
    const interval = setInterval(() => { try { if (fs.existsSync(file) && fs.readFileSync(file, "ascii") === nonce) finish(); } catch (error) { finish(error); } }, 100);
    const timeout = setTimeout(() => finish(new Error("설치 도우미의 준비 응답이 없습니다. 기존 앱을 유지합니다")), 120_000);
    child.once("exit", exited); child.once("error", rejected);
  });
}

class DesktopUpdater {
  constructor({ resourceRoot, executable, currentVersion, home }) {
    this.currentVersion = currentVersion;
    this.home = home;
    this.executable = executable;
    this.root = path.resolve(path.dirname(executable), "../..");
    this.state = { state: "unavailable", message: "자동 업데이트 배포가 설정되지 않았습니다", version: currentVersion };
    this.busy = false;
    this.config = null;
    const configFile = path.join(resourceRoot, "update.json");
    if (process.platform !== "win32" || !fs.existsSync(configFile)) return;
    const config = JSON.parse(fs.readFileSync(configFile, "utf8"));
    if (!config.feedURL || !config.publicKey) return;
    httpsURL(config.feedURL);
    crypto.createPublicKey({ key: config.publicKey, format: "jwk" });
    const ownership = path.join(this.root, "installation.json");
    if (!fs.existsSync(ownership)) return;
    const installation = JSON.parse(fs.readFileSync(ownership, "utf8").replace(/^\uFEFF/, ""));
    if (installation.product !== "ARTEX" || installation.schema !== 1 || !sameInstalledPath(installation.root, this.root) || !sameInstalledPath(executable, path.join(this.root, "versions", currentVersion, "ARTEX.exe"))) throw new Error("업데이트 설치 경로가 올바르지 않습니다");
    this.root = fs.realpathSync.native(this.root);
    this.config = config;
    this.state = { state: "idle", message: "업데이트를 확인할 수 있습니다", version: currentVersion };
  }
  status() { return { ...this.state }; }
  async run(work) {
    if (!this.config) return this.status();
    if (this.busy) throw new Error("업데이트 작업이 이미 진행 중입니다");
    this.busy = true;
    try { await work(); return this.status(); }
    catch (error) { this.state = { state: "failed", message: error.message, version: this.currentVersion }; throw error; }
    finally { this.busy = false; }
  }
  async check() {
    return this.run(async () => {
      this.state = { state: "checking", message: "업데이트 정보를 확인하는 중…", version: this.currentVersion };
      const response = await fetch(this.config.feedURL, { redirect: "error", signal: AbortSignal.timeout(30_000), headers: { accept: "application/json" } });
      if (!response.ok) throw new Error(`업데이트 정보 요청 실패 (${response.status})`);
      const length = Number(response.headers.get("content-length"));
      if (length > 6_000_000) throw new Error("업데이트 정보가 너무 큽니다");
      const chunks = []; let size = 0;
      for await (const chunk of response.body) { size += chunk.length; if (size > 6_000_000) throw new Error("업데이트 정보가 너무 큽니다"); chunks.push(chunk); }
      const envelope = JSON.parse(Buffer.concat(chunks).toString("utf8"));
      // 같은 버전의 서명/제품 정보도 확인한 뒤 최신 상태로 표시한다.
      const candidate = JSON.parse(Buffer.from(envelope.payload, "base64").toString("utf8"));
      const manifest = validateManifest(envelope, this.config.publicKey, "0.0.0");
      if (compareVersions(candidate.version, this.currentVersion) < 0) throw new Error("업데이트 서버가 이전 버전을 제공했습니다");
      if (compareVersions(candidate.version, this.currentVersion) === 0) { this.manifest = null; this.state = { state: "current", message: "최신 버전입니다", version: this.currentVersion }; return; }
      this.envelope = envelope;
      this.manifest = manifest;
      this.state = { state: "available", message: `${manifest.version} 업데이트가 있습니다`, version: this.currentVersion, nextVersion: manifest.version };
    });
  }
  async download() {
    return this.run(async () => {
      if (!this.manifest || this.state.state !== "available") throw new Error("먼저 업데이트를 확인하세요");
      this.state = { state: "downloading", message: "통합 앱 업데이트를 내려받는 중…", version: this.currentVersion, nextVersion: this.manifest.version };
      this.stage = fs.mkdtempSync(path.join(this.home, "update-"));
      this.archive = path.join(this.stage, "app.zip");
      fs.writeFileSync(path.join(this.stage, "release.json"), JSON.stringify(this.envelope), { flag: "wx" });
      fs.writeFileSync(path.join(this.stage, "trust.json"), JSON.stringify({ publicKey: this.config.publicKey }), { flag: "wx" });
      const response = await fetch(this.manifest.url, { redirect: "error", signal: AbortSignal.timeout(10 * 60_000) });
      if (!response.ok || !response.body) throw new Error(`업데이트 다운로드 실패 (${response.status})`);
      const hash = crypto.createHash("sha256"); let size = 0;
      const meter = new Transform({ transform: (chunk, _encoding, callback) => { size += chunk.length; if (size > this.manifest.size) return callback(new Error("업데이트 크기가 배포 정보와 다릅니다")); hash.update(chunk); callback(null, chunk); } });
      try {
        await pipeline(Readable.fromWeb(response.body), meter, fs.createWriteStream(this.archive, { flags: "wx" }));
        if (size !== this.manifest.size || hash.digest("hex") !== this.manifest.sha256) throw new Error("업데이트 파일의 SHA256 또는 크기가 일치하지 않습니다");
      } catch (error) { fs.rmSync(this.archive, { force: true }); throw error; }
      this.state = { state: "downloaded", message: "업데이트 준비 완료. 재시작하면 적용됩니다", version: this.currentVersion, nextVersion: this.manifest.version };
    });
  }
  async install({ stopBackend, quit }) {
    if (this.busy || this.state.state !== "downloaded" || !this.stage) throw new Error("검증된 업데이트를 먼저 내려받으세요");
    this.busy = true;
    let child;
    let control;
    try {
      this.state = { state: "preparing", message: "서명과 설치 준비를 확인하는 중…", version: this.currentVersion, nextVersion: this.manifest.version };
      const host = path.join(this.stage, "ARTEX-InstallHost.exe");
      fs.copyFileSync(path.join(this.root, "ARTEX-InstallHost.exe"), host, fs.constants.COPYFILE_EXCL);
      const nonce = crypto.randomBytes(32).toString("hex"), readyFile = path.join(this.stage, `ready-${nonce}.txt`), log = path.join(this.stage, "installer.log");
      control = { nonce, permitFile: path.join(this.stage, `permit-${nonce}.txt`), cancelFile: path.join(this.stage, `cancel-${nonce}.txt`), cancelledFile: path.join(this.stage, `cancelled-${nonce}.txt`) };
      const environment = { ...process.env }; delete environment.PSModulePath;
      child = spawn(host, [`--script=${path.join(this.root, "install.ps1")}`, `--log=${log}`, "-Mode", "Update", "-Root", this.root, "-Archive", this.archive, "-Manifest", path.join(this.stage, "release.json"), "-DataHome", this.home, "-ReadyFile", readyFile, "-ReadyNonce", nonce, "-PermitFile", control.permitFile, "-CancelFile", control.cancelFile, "-CancelledFile", control.cancelledFile, "-ParentPid", String(process.pid), "-Restart"], { windowsHide: true, detached: true, stdio: "ignore", env: environment });
      await awaitInstallerReady(child, readyFile, nonce, log);
      await stopBackend();
      if (child.exitCode !== null || child.signalCode !== null) throw new Error("설치 도우미가 종료되어 기존 앱을 유지합니다");
      fs.writeFileSync(control.permitFile, nonce, { encoding: "ascii", flag: "wx" });
      child.unref();
      this.state = { state: "installing", message: "앱을 종료하고 업데이트를 적용합니다", version: this.currentVersion, nextVersion: this.manifest.version };
      quit();
    } catch (error) {
      try { await stopInstaller(child, control); }
      catch (cancellation) { error = new Error(`${error.message}; ${cancellation.message}`); }
      this.busy = false; this.state = { state: "failed", message: error.message, version: this.currentVersion }; throw error;
    }
  }
  confirmReady() {
    const ownership = path.join(this.root, "installation.json");
    if (!fs.existsSync(ownership) || !fs.existsSync(path.join(this.root, "pending.txt"))) return;
    const installed = JSON.parse(fs.readFileSync(ownership, "utf8").replace(/^\uFEFF/, ""));
    if (installed.product !== "ARTEX" || installed.schema !== 1 || !sameInstalledPath(installed.root, this.root) || !sameInstalledPath(this.executable, path.join(this.root, "versions", this.currentVersion, "ARTEX.exe"))) throw new Error("업데이트 준비 확인 경로가 올바르지 않습니다");
    fs.writeFileSync(path.join(this.root, `healthy-${this.currentVersion}.txt`), this.currentVersion);
  }
}
module.exports = { DesktopUpdater, validateManifest, compareVersions, safeFile, httpsURL, stopInstaller };
