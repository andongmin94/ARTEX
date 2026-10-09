const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
const { spawnSync } = require("node:child_process");
const { compareVersions, safeFile } = require("../src/update.cjs");
const scripts = __dirname;
function run(command, args) {
  const environment = { ...process.env };
  if (path.basename(command).toLowerCase() === "powershell.exe") delete environment.PSModulePath;
  const result = spawnSync(command, args, { windowsHide: true, encoding: "utf8", maxBuffer: 8 * 1024 * 1024, env: environment });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${path.basename(command)} 실패 (${result.status}): ${result.stderr || result.stdout}`);
  return result.stdout;
}
function powershell(args) { return run(path.join(process.env.SystemRoot || "C:\\Windows", "System32/WindowsPowerShell/v1.0/powershell.exe"), ["-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", ...args]); }
function compiler() {
  const candidate = process.env.ARTEX_CSC || path.join(process.env.SystemRoot || "C:\\Windows", "Microsoft.NET/Framework64/v4.0.30319/csc.exe");
  if (!path.isAbsolute(candidate) || !fs.existsSync(candidate)) throw new Error(".NET Framework C# 컴파일러가 없습니다. ARTEX_CSC에 공식 csc.exe 절대 경로를 지정하세요");
  return candidate;
}
function sign(file) { powershell(["-File", path.join(scripts, "sign.ps1"), "-File", file]); }
function fileManifest(root) {
  const files = {};
  function visit(directory) {
    for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
      const file = path.join(directory, entry.name);
      if (entry.isSymbolicLink()) throw new Error("배포물에는 심볼릭 링크를 넣을 수 없습니다");
      if (entry.isDirectory()) visit(file);
      else if (entry.isFile()) files[safeFile(path.relative(root, file).split(path.sep).join("/"))] = hashFile(file);
      else throw new Error("지원하지 않는 배포 파일입니다");
    }
  }
  visit(root); return files;
}
function hashFile(file) {
  const hash = crypto.createHash("sha256"), buffer = Buffer.alloc(128 * 1024), descriptor = fs.openSync(file, "r");
  try { let length; while ((length = fs.readSync(descriptor, buffer, 0, buffer.length, null)) > 0) hash.update(buffer.subarray(0, length)); }
  finally { fs.closeSync(descriptor); }
  return hash.digest("hex");
}
function createDistribution({ bundle, destination, version, development = false }) {
  if (process.platform !== "win32") throw new Error("설치 프로그램은 현재 Windows에서만 생성합니다");
  compareVersions(version, "0.0.0");
  if (fs.existsSync(destination)) throw new Error("기존 배포 결과를 덮어쓸 수 없습니다");
  let publicKey, privateKey;
  if (!development) {
    if (!process.env.ARTEX_SIGN_CERTIFICATE || !process.env.ARTEX_SIGN_TIMESTAMP || !process.env.ARTEX_UPDATE_PRIVATE_KEY || !process.env.ARTEX_UPDATE_FEED_URL || !process.env.ARTEX_UPDATE_ARCHIVE_URL) throw new Error("서명 배포에는 ARTEX_SIGN_CERTIFICATE, ARTEX_SIGN_TIMESTAMP, ARTEX_UPDATE_PRIVATE_KEY, ARTEX_UPDATE_FEED_URL, ARTEX_UPDATE_ARCHIVE_URL가 필요합니다");
    if (!path.isAbsolute(process.env.ARTEX_UPDATE_PRIVATE_KEY)) throw new Error("배포 서명 키는 절대 경로여야 합니다");
    privateKey = crypto.createPrivateKey(fs.readFileSync(process.env.ARTEX_UPDATE_PRIVATE_KEY));
    if (privateKey.asymmetricKeyType !== "rsa" || privateKey.asymmetricKeyDetails.modulusLength < 3072) throw new Error("배포 서명에는 최소 RSA 3072비트 키가 필요합니다");
    publicKey = crypto.createPublicKey(privateKey).export({ format: "jwk" });
    for (const name of ["ARTEX_UPDATE_FEED_URL", "ARTEX_UPDATE_ARCHIVE_URL"]) require("../src/update.cjs").httpsURL(process.env[name]);
  }
  const installer = path.join(bundle, "resources/artex/installer");
  fs.mkdirSync(installer, { recursive: true });
  run(compiler(), ["/nologo", "/target:winexe", "/reference:System.Windows.Forms.dll", `/out:${path.join(installer, "ARTEX.exe")}`, path.join(scripts, "launcher.cs")]);
  fs.copyFileSync(path.join(scripts, "install.ps1"), path.join(installer, "install.ps1"));
  fs.writeFileSync(path.join(bundle, "resources/artex/update.json"), JSON.stringify(development ? { development: true } : { feedURL: process.env.ARTEX_UPDATE_FEED_URL, publicKey }, null, 2));
  if (!development) for (const file of [path.join(bundle, "ARTEX.exe"), path.join(bundle, "resources/artex/artex.exe"), path.join(installer, "ARTEX.exe"), path.join(installer, "install.ps1")]) sign(file);
  const integrity = path.join(installer, "installer-integrity.txt");
  fs.writeFileSync(integrity, `${hashFile(path.join(installer, "install.ps1"))}\n${development ? "" : process.env.ARTEX_SIGN_CERTIFICATE.replaceAll(" ", "")}\n${development ? "development" : "signed"}`, "ascii");
  const host = path.join(installer, "ARTEX-InstallHost.exe");
  run(compiler(), ["/nologo", "/target:winexe", "/main:InstallerHost", ...(development ? ["/define:DEVELOPMENT"] : []), `/out:${host}`, `/resource:${integrity},installer-integrity.txt`, path.join(scripts, "installer-host.cs")]);
  if (!development) sign(host);
  fs.mkdirSync(destination, { recursive: true });
  const archive = path.join(destination, "app.zip");
  powershell(["-File", path.join(scripts, "zip.ps1"), "-Source", bundle, "-Destination", archive]);
  const manifest = { schema: 1, product: "ARTEX", version, platform: "win32", arch: process.arch, url: development ? "https://example.invalid/app.zip" : process.env.ARTEX_UPDATE_ARCHIVE_URL, size: fs.statSync(archive).size, sha256: hashFile(archive), files: fileManifest(bundle) };
  const payload = Buffer.from(JSON.stringify(manifest));
  fs.writeFileSync(path.join(destination, "release.json"), JSON.stringify({ payload: payload.toString("base64"), signature: development ? "" : crypto.sign("RSA-SHA256", payload, privateKey).toString("base64") }));
  fs.writeFileSync(path.join(destination, "trust.json"), JSON.stringify(development ? { development: true } : { publicKey }));
  fs.copyFileSync(path.join(installer, "install.ps1"), path.join(destination, "install.ps1"));
  const setup = path.join(destination, development ? "ARTEX-Setup-development.exe" : "ARTEX-Setup.exe");
  run(compiler(), ["/nologo", "/target:winexe", "/main:Setup", "/reference:System.Windows.Forms.dll", ...(development ? ["/define:DEVELOPMENT"] : []), `/out:${setup}`, `/resource:${integrity},installer-integrity.txt`, ...["app.zip", "release.json", "trust.json", "install.ps1"].map((name) => `/resource:${path.join(destination, name)},${name}`), path.join(scripts, "setup.cs"), path.join(scripts, "installer-host.cs")]);
  if (!development) sign(setup);
  fs.writeFileSync(path.join(destination, "SHA256SUMS.txt"), ["app.zip", "release.json", path.basename(setup)].map((name) => `${hashFile(path.join(destination, name))}  ${name}`).join("\n") + "\n");
  return { archive, setup, manifest, destination };
}
module.exports = { createDistribution, fileManifest, run, powershell };
