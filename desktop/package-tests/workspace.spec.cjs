const { test, expect, _electron } = require("@playwright/test");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

test("실제 포터블 작업 파일 CRUD·앱 데이터 경계·재시작 보존", async ({}, testInfo) => {
  test.setTimeout(120_000);
  const bundle = process.env.ARTEX_PACKAGE_TEST_BUNDLE || path.resolve(__dirname, `../dist/ARTEX-win32-${process.arch}`);
  expect(path.isAbsolute(bundle)).toBe(true);
  const home = fs.mkdtempSync(path.join(os.tmpdir(), "ARTEX 작업 경계 한글 #-"));
  const data = path.join(home, "data");
  fs.mkdirSync(path.join(data, "tasks/old"), { recursive: true });
  const sentinel = path.join(data, "private.txt");
  const oldFile = path.join(data, "tasks/old/notes.txt");
  fs.writeFileSync(sentinel, "관리 파일 보존");
  fs.writeFileSync(oldFile, "이전 작업 파일 보존");
  const env = { ...process.env };
  for (const key of Object.keys(env)) if (key.startsWith("ARTEX_") || /(?:API_?KEY|TOKEN|SECRET|PASSWORD)/i.test(key)) delete env[key];
  let app, page;
  const pageErrors = [];
  const checks = [];
  async function launch() {
    app = await _electron.launch({ executablePath: path.join(bundle, "ARTEX.exe"), args: [`--artex-home=${home}`], env, chromiumSandbox: true, timeout: 45_000 });
    page = await app.firstWindow();
    page.on("pageerror", (error) => pageErrors.push(error.message));
    await page.waitForURL(/\/function\/tasks\/?$/, { timeout: 40_000 });
    await expect.poll(() => page.evaluate(() => Boolean(localStorage.getItem("artex_token")))).toBe(true);
  }
  async function api(operation, name, content) {
    return page.evaluate(async ({ operation, name, content }) => {
      const method = operation === "delete" ? "DELETE" : ["write", "mkdir", "upload"].includes(operation) ? "POST" : "GET";
      const headers = { Authorization: `Bearer ${localStorage.getItem("artex_token")}` };
      let body;
      if (operation === "upload") {
        body = new FormData();
        body.append("file", new Blob([content]), "uploaded.txt");
      } else if (method === "POST") {
        headers["Content-Type"] = "application/json";
        body = JSON.stringify({ path: name, content });
      }
      const response = await fetch(`/api/workspace/${operation}?path=${encodeURIComponent(name)}`, { method, headers, body });
      return { status: response.status, text: await response.text() };
    }, { operation, name, content });
  }
  try {
    await launch();
    expect(JSON.parse((await api("list", "")).text).entries).toEqual([]);
    expect((await api("read", "artex.sqlite")).status).toBe(404);
    for (const name of ["../artex.sqlite", "../artex.sqlite-wal", "../artex.sqlite-shm", "../../config.json", "../../jwt.key", "../private.txt", "../tasks/old/notes.txt", "..\\artex.sqlite", "folder/../../artex.sqlite", "C:\\Windows", "CON", "file:stream"]) {
      for (const operation of ["list", "read", "download", "write", "upload", "delete", "mkdir"]) {
        expect((await api(operation, name, "경로 보호 검사")).status, `${operation} ${name}`).toBe(400);
      }
    }
    checks.push("all seven APIs reject managed/traversal/device/ADS paths");
    const workspace = path.join(data, "workspace");
    fs.symlinkSync(data, path.join(workspace, "outside"), "junction");
    for (const operation of ["list", "read", "download", "write", "upload", "delete", "mkdir"]) {
      const name = ["list", "upload", "delete", "mkdir"].includes(operation) ? "outside" : "outside/private.txt";
      expect((await api(operation, name, "변경 시도")).status, `junction ${operation}`).toBe(400);
    }
    fs.linkSync(sentinel, path.join(workspace, "hardlink.txt"));
    for (const operation of ["read", "download", "write", "delete"]) expect((await api(operation, "hardlink.txt", "변경 시도")).status).toBe(400);
    expect(JSON.parse((await api("list", "")).text).entries).toEqual([]);
    checks.push("actual Windows junction and hardlink are neither exposed nor changed");
    expect((await api("mkdir", "한글 폴더")).status).toBe(200);
    expect((await api("write", "한글 폴더/산출물.txt", "사용자 파일 저장")).status).toBe(200);
    expect(fs.readFileSync(path.join(workspace, "한글 폴더/산출물.txt"), "utf8")).toBe("사용자 파일 저장");
    expect(JSON.parse((await api("read", "한글 폴더/산출물.txt")).text).content).toBe("사용자 파일 저장");
    expect((await api("download", "한글 폴더/산출물.txt")).text).toBe("사용자 파일 저장");
    expect((await api("upload", "한글 폴더", "업로드 내용")).status).toBe(200);
    expect(fs.readFileSync(path.join(workspace, "한글 폴더/uploaded.txt"), "utf8")).toBe("업로드 내용");
    expect((await api("delete", "한글 폴더/uploaded.txt")).status).toBe(200);
    expect((await api("read", "한글 폴더/uploaded.txt")).status).toBe(404);
    checks.push("mkdir/write/read/download/upload/delete and actual disk contents");
    const origin = new URL(page.url()).origin;
    for (const mode of ["light", "dark"]) {
      await page.evaluate((mode) => { document.cookie = `theme_mode=${mode}; path=/`; }, mode);
      await page.goto(`${origin}/function/workspace/`);
      await expect(page.getByText("한글 폴더", { exact: true })).toBeVisible();
      await expect(page.getByText("artex.sqlite", { exact: true })).toHaveCount(0);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      await page.screenshot({ path: testInfo.outputPath(`workspace-protected-${mode}.png`) });
    }
    await app.close(); app = null;
    await launch();
    expect(JSON.parse((await api("read", "한글 폴더/산출물.txt")).text).content).toBe("사용자 파일 저장");
    expect((await api("delete", "한글 폴더")).status).toBe(200);
    expect(fs.existsSync(path.join(workspace, "한글 폴더"))).toBe(false);
    for (const operation of ["write", "delete", "mkdir"]) expect((await api(operation, "", "변경 시도")).status).toBe(400);
    expect(fs.existsSync(path.join(data, "artex.sqlite"))).toBe(true);
    expect(fs.readFileSync(sentinel, "utf8")).toBe("관리 파일 보존");
    expect(fs.readFileSync(oldFile, "utf8")).toBe("이전 작업 파일 보존");
    expect(pageErrors).toEqual([]);
    checks.push("relaunch preservation, recursive own-folder deletion, immutable root and managed/old files");
    fs.writeFileSync(testInfo.outputPath("workspace-verification.json"), JSON.stringify({ bundle, home, checks, pageErrors, externalCalls: false }, null, 2));
  } finally {
    if (app) await app.close();
    fs.writeFileSync(testInfo.outputPath("temporary-home.txt"), home);
  }
});
