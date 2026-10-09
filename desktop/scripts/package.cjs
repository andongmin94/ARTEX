const fs = require("node:fs");
const path = require("node:path");
const { spawnSync } = require("node:child_process");
const { createDistribution } = require("./distribution.cjs");
const root = path.resolve(__dirname, "..");
if (process.platform !== "win32") throw new Error("현재 패키지 생성은 Windows만 검증 대상으로 지원합니다");
const signed = process.argv.includes("--signed");
const installer = signed || process.argv.includes("--installer");
const output = process.argv.find((arg) => arg.startsWith("--output="))?.slice("--output=".length);
if (output && !path.isAbsolute(output)) throw new Error("출력 경로는 절대 경로여야 합니다");
if (!process.argv.includes("--skip-build")) {
  const build = spawnSync(process.execPath, [path.join(__dirname, "build.cjs")], { stdio: "inherit", windowsHide: true });
  if (build.error) throw build.error;
  if (build.status !== 0) process.exit(build.status ?? 1);
}
const destination = output || path.join(root, "dist", `ARTEX-win32-${process.arch}`);
if (fs.existsSync(destination)) throw new Error("패키지 출력 경로가 이미 있습니다. 기존 결과를 보존하고 다른 경로에서 다시 빌드하세요");
fs.cpSync(path.join(root, "node_modules/electron/dist"), destination, { recursive: true });
const appDir = path.join(destination, "resources/app");
fs.mkdirSync(appDir, { recursive: true });
fs.cpSync(path.join(root, "src"), path.join(appDir, "src"), { recursive: true });
const pkg = JSON.parse(fs.readFileSync(path.join(root, "package.json"), "utf8"));
delete pkg.devDependencies;
delete pkg.scripts;
fs.writeFileSync(path.join(appDir, "package.json"), JSON.stringify(pkg, null, 2));
fs.cpSync(path.join(root, "resources"), path.join(destination, "resources/artex"), { recursive: true });
fs.renameSync(path.join(destination, "electron.exe"), path.join(destination, "ARTEX.exe"));
if (installer) {
  const distribution = createDistribution({ bundle: destination, destination: `${destination}-installer`, version: pkg.version, development: !signed });
  console.log(`${signed ? "서명된" : "개발 검증용 서명되지 않은"} Windows 설치 프로그램: ${distribution.setup}`);
}
console.log(`${signed ? "서명된" : "서명되지 않은"} Windows 실행 패키지: ${destination}`);
