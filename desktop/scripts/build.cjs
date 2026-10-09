const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
const { spawnSync } = require("node:child_process");
const { prepareTools } = require("./prepare-tools.cjs");
const { compareVersions } = require("../src/update.cjs");
const root = path.resolve(__dirname, "../..");
const resources = path.join(root, "desktop/resources");
const appVersion = JSON.parse(fs.readFileSync(path.join(root, "desktop/package.json"), "utf8")).version;
compareVersions(appVersion, "0.0.0");

function run(command, args, cwd = root) {
  const result = spawnSync(command, args, { cwd, stdio: "inherit", windowsHide: true });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} 종료 코드: ${result.status}`);
}

run(process.execPath, [path.join(root, "web/scripts/build-static.cjs")]);
// 빌드가 교체한 정적 청크나 실행 파일이 다음 패키지에 남지 않게 생성물만 정리한다.
const embedded = path.join(root, "server/webui/dist");
for (const generated of [resources, embedded]) {
  const relative = path.relative(root, generated);
  if (!relative || relative.startsWith("..") || path.isAbsolute(relative)) throw new Error("생성물 경로가 프로젝트 밖입니다");
  fs.rmSync(generated, { recursive: true, force: true });
}
fs.mkdirSync(resources, { recursive: true });
const ui = path.join(root, "web/out");
fs.cpSync(ui, embedded, { recursive: true });
const hashes = new Set();
function collect(dir) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const file = path.join(dir, entry.name);
    if (entry.isDirectory()) collect(file);
    else if (entry.name.endsWith(".html")) {
      for (const match of fs.readFileSync(file, "utf8").matchAll(/<script\b([^>]*)>([\s\S]*?)<\/script>/g)) {
        if (!/\bsrc\s*=/.test(match[1])) hashes.add(`'sha256-${crypto.createHash("sha256").update(match[2]).digest("base64")}'`);
      }
    }
  }
}
collect(ui);
fs.writeFileSync(path.join(resources, "csp.json"), JSON.stringify([...hashes]));
fs.cpSync(path.join(root, "skills"), path.join(resources, "skills"), { recursive: true });
fs.cpSync(path.join(root, "web/licenses"), path.join(resources, "licenses"), { recursive: true });
fs.copyFileSync(path.join(root, "web/src/lib/fonts/files/OFL-Pretendard.txt"), path.join(resources, "licenses/OFL-Pretendard.txt"));
fs.mkdirSync(path.join(resources, "fonts"), { recursive: true });
// Go 준비 전 시작·오류 화면도 웹 UI와 같은 로컬 글꼴을 사용한다.
fs.copyFileSync(path.join(root, "web/src/lib/fonts/files/PretendardVariable.woff2"), path.join(resources, "fonts/PretendardVariable.woff2"));
fs.copyFileSync(path.join(root, "LICENSE"), path.join(resources, "licenses/ARTEX-LICENSE.txt"));
const go = process.env.ARTEX_GO ?? "go";
if (process.env.ARTEX_GO && !path.isAbsolute(go)) throw new Error("ARTEX_GO는 개발 도구의 절대 경로여야 합니다");
run(go, ["build", "-tags", "embedui", "-ldflags", `-X main.version=${appVersion}`, "-o", path.join(resources, process.platform === "win32" ? "artex.exe" : "artex"), "./cmd/artex"]);
prepareTools().then(() => console.log("정적 UI·Go·기본 스킬·CSP·라이선스·도구 빌드 완료")).catch((error) => {
  console.error(error.message);
  process.exitCode = 1;
});
