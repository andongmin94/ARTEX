const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
const { pipeline } = require("node:stream/promises");
const { Readable } = require("node:stream");
const { spawnSync } = require("node:child_process");

const desktop = path.resolve(__dirname, "..");
const lock = JSON.parse(fs.readFileSync(path.join(desktop, "tools.lock.json"), "utf8"));

async function digest(file) {
  const hash = crypto.createHash("sha256");
  for await (const chunk of fs.createReadStream(file)) hash.update(chunk);
  return hash.digest("hex");
}

function run(command, args) {
  const env = { ...process.env };
  if (path.basename(command).toLowerCase() === "powershell.exe") delete env.PSModulePath;
  const result = spawnSync(command, args, { windowsHide: true, encoding: "utf8", timeout: 120_000, env });
  if (result.error || result.status !== 0) throw new Error(`도구 준비 실패: ${result.error?.message ?? result.stderr ?? result.status}`);
  return result.stdout;
}

function powershell(args) {
  return run(path.join(process.env.SystemRoot, "System32/WindowsPowerShell/v1.0/powershell.exe"), ["-NoProfile", "-NonInteractive", ...args]);
}

function quoted(value) { return `'${value.replaceAll("'", "''")}'`; }

async function download(archive, cache) {
  const file = path.join(cache, archive.file);
  if (fs.existsSync(file)) {
    if (await digest(file) !== archive.sha256) throw new Error(`${archive.name} 캐시의 SHA256이 고정된 값과 다릅니다. 기존 파일은 보존했습니다`);
    return file;
  }
  const partial = `${file}.${crypto.randomUUID()}.partial`;
  const response = await fetch(archive.url, { signal: AbortSignal.timeout(180_000) });
  if (!response.ok || !response.body || new URL(response.url).protocol !== "https:") throw new Error(`${archive.name} 공식 배포물 다운로드 실패`);
  await pipeline(Readable.fromWeb(response.body), fs.createWriteStream(partial, { flags: "wx" }));
  if (await digest(partial) !== archive.sha256) throw new Error(`${archive.name} 다운로드 SHA256 불일치: 검증 실패 파일을 보존했습니다`);
  fs.renameSync(partial, file);
  return file;
}

async function inventory(root, directory) {
  const files = [];
  for (const entry of fs.readdirSync(directory, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name, "en"))) {
    const file = path.join(directory, entry.name);
    const stat = fs.lstatSync(file);
    if (stat.isSymbolicLink()) throw new Error("도구 묶음에는 링크를 포함할 수 없습니다");
    if (stat.isDirectory()) files.push(...await inventory(root, file));
    else if (stat.isFile()) files.push({ path: path.relative(root, file).split(path.sep).join("/"), sha256: await digest(file) });
    else throw new Error("도구 묶음에 일반 파일이 아닌 항목이 있습니다");
  }
  return files;
}

async function prepareTools(destination = path.join(desktop, "resources/tools")) {
  if (process.platform !== "win32" || process.arch !== "x64") throw new Error("현재 도구 묶음은 Windows x64에서만 검증합니다");
  if (path.resolve(destination) !== path.join(desktop, "resources/tools")) throw new Error("도구 생성물은 지정된 resources/tools에만 만들 수 있습니다");
  if (fs.existsSync(destination)) throw new Error("기존 도구 생성물이 있습니다. 빌드 절차에서 기존 결과를 보존한 뒤 다시 준비하세요");
  const cache = process.env.ARTEX_TOOL_CACHE ?? path.join(process.env.LOCALAPPDATA, "ARTEX-development-tools/windows-amd64-20261009");
  if (!path.isAbsolute(cache)) throw new Error("ARTEX_TOOL_CACHE는 절대 경로여야 합니다");
  fs.mkdirSync(cache, { recursive: true });
  fs.mkdirSync(path.dirname(destination), { recursive: true });
  const stage = fs.mkdtempSync(path.join(path.dirname(destination), ".tools-stage-"));
  const metadata = new Map();
  for (const archive of lock.archives) {
    const file = await download(archive, cache);
    const directory = path.join(stage, archive.name);
    fs.mkdirSync(directory);
    powershell(["-Command", `Expand-Archive -LiteralPath ${quoted(file)} -DestinationPath ${quoted(directory)} -ErrorAction Stop`]);
    if (archive.archiveRoot) {
      const nested = path.join(directory, archive.archiveRoot);
      for (const entry of fs.readdirSync(nested)) fs.renameSync(path.join(nested, entry), path.join(directory, entry));
      fs.rmdirSync(nested);
    }
    if (!fs.existsSync(path.join(stage, archive.entrypoint))) throw new Error(`${archive.name} 실행 파일이 배포물에 없습니다`);
    metadata.set(archive.name, archive);
  }
  // Chrome의 전체 제3자 고지는 실행 파일 내 chrome://credits에서 제공된다.
  // 저장소 도구의 Apache 라이선스를 브라우저 바이너리의 라이선스로 바꾸지 않는다.
  const browser = metadata.get("browser");
  fs.writeFileSync(path.join(stage, "browser/NOTICE.txt"), `Chrome for Testing ${browser.version}\n공식 출처: ${browser.url}\nChrome 추가 약관: https://www.google.com/chrome/terms/\n전체 저작권·제3자 라이선스: 포함한 브라우저의 chrome://credits\nChromium 소스: https://chromium.googlesource.com/chromium/src/+/refs/tags/${browser.version}/\n`);
  const inventories = new Map();
  for (const archive of lock.archives) inventories.set(archive.name, await inventory(stage, path.join(stage, archive.name)));
  const components = [
    ["shell", "pwsh", "pwsh/pwsh.exe"], ["pty", "pwsh", "pwsh/pwsh.exe"],
    ["python", "python", "python/python.exe"], ["node", "node", "node/node.exe"],
    ["browser", "browser", browser.entrypoint], ["cli", "git", "git/cmd/git.exe"],
  ].map(([key, name, entrypoint]) => {
    const archive = metadata.get(name);
    return { key, version: archive.version, source: archive.url, license: archive.license, entrypoint, files: inventories.get(name) };
  });
  fs.writeFileSync(path.join(stage, "manifest.json"), JSON.stringify({ schema: 1, platform: lock.platform, components }, null, 2) + "\n");
  fs.renameSync(stage, destination);
  console.log(`공식 고정 버전 도구 묶음 준비: ${destination}`);
  return { root: destination, sha256: await digest(path.join(destination, "manifest.json")) };
}

if (require.main === module) prepareTools().catch((error) => { console.error(error.message); process.exitCode = 1; });
module.exports = { prepareTools, digest };
