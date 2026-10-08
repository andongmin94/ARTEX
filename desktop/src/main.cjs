const { app, BrowserWindow, ipcMain, protocol, session, shell } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const { randomBytes } = require("node:crypto");
const { Backend } = require("./backend.cjs");

protocol.registerSchemesAsPrivileged([{ scheme: "artex", privileges: { standard: true, secure: true, supportFetchAPI: true } }]);

const homeArg = process.argv.find((arg) => arg.startsWith("--artex-home="));
if (homeArg) {
  const home = homeArg.slice("--artex-home=".length);
  if (!path.isAbsolute(home)) throw new Error("데이터 경로는 절대 경로여야 합니다");
  app.setPath("userData", home);
}

const locked = app.requestSingleInstanceLock();
if (!locked) app.quit();

let window;
let backend;
let origin = null;
let backendSession = null;
let quitting = false;
let status = { state: "starting", message: "로컬 저장소와 백엔드를 준비하는 중…" };
const resourceRoot = app.isPackaged ? path.join(process.resourcesPath, "artex") : path.join(__dirname, "..", "resources");
const trusted = (url) => {
  try { return url === "artex://startup/" || (origin && new URL(url).origin === origin); }
  catch { return false; }
};

async function failure(error) {
  origin = null;
  backendSession = null;
  status = { state: "failed", message: "백엔드를 시작할 수 없습니다. 진단 정보를 확인한 뒤 다시 시도하세요.", details: error.message };
  if (window && !window.isDestroyed()) await window.loadURL("artex://startup/");
}

async function startBackend() {
  if (status.state === "ready") return;
  status = { state: "starting", message: "로컬 저장소와 백엔드를 준비하는 중…" };
  await backend?.stop();
  try {
    const home = app.getPath("userData");
    fs.mkdirSync(home, { recursive: true });
    fs.cpSync(path.join(resourceRoot, "skills"), path.join(home, "skills"), { recursive: true, force: false, errorOnExist: false });
    backendSession = randomBytes(32).toString("hex");
    backend = new Backend({ executable: path.join(resourceRoot, process.platform === "win32" ? "artex.exe" : "artex"), home, sessionToken: backendSession, onFailure: failure });
    const ready = await backend.start();
    if (quitting) return;
    origin = ready.url;
    status = { state: "ready", ...ready };
    const uiOrigin = origin;
    const readyBackend = backend;
    let redirected = false;
    const navigation = (_event, url, _inPlace, mainFrame) => {
      if (mainFrame && new URL(url).origin === uiOrigin && new URL(url).pathname !== "/") redirected = true;
    };
    window.webContents.on("did-start-navigation", navigation);
    try { await window.loadURL(uiOrigin); }
    catch (error) {
      const current = window.webContents.getURL();
      process.stderr.write(`[desktop] UI 탐색 ${error.code ?? error.errno}: ${error.message}; 현재 URL=${current}\n`);
      // 정적 UI의 root→작업→login/setup 전환은 첫 loadURL을 취소할 수 있다.
      const authNavigation = (error.code === "ERR_ABORTED" || error.errno === -3) && redirected &&
        backend === readyBackend && origin === uiOrigin && status.state === "ready" && new URL(current).origin === uiOrigin;
      if (!authNavigation) throw error;
    } finally { window.webContents.removeListener("did-start-navigation", navigation); }
  } catch (error) { await failure(error); }
}

function validateSender(event) {
  if (event.sender !== window?.webContents || event.senderFrame !== window.webContents.mainFrame || !trusted(event.senderFrame.url)) throw new Error("허용되지 않은 IPC 호출자입니다");
}

function validateReadySender(event) {
  validateSender(event);
  if (quitting || status.state !== "ready" || !origin || new URL(event.senderFrame.url).origin !== origin) throw new Error("앱이 준비된 뒤 다시 시도하세요");
}

function validateChatGPTLoginURL(value) {
  if (typeof value !== "string") throw new Error("허용되지 않은 ChatGPT 인증 주소입니다");
  let url;
  try { url = new URL(value); }
  catch { throw new Error("허용되지 않은 ChatGPT 인증 주소입니다"); }
  const prohibited = [...url.searchParams.keys()].some((key) => ["access_token", "refresh_token", "id_token", "id_token_hint"].includes(key.toLowerCase()));
  if (url.origin !== "https://auth.openai.com" || url.pathname !== "/api/accounts/authorize" || url.username || url.password || url.hash || prohibited) throw new Error("허용되지 않은 ChatGPT 인증 주소입니다");
  return url.href;
}

app.on("second-instance", () => { if (window) { if (window.isMinimized()) window.restore(); window.focus(); } });
app.on("window-all-closed", () => app.quit());
app.on("before-quit", (event) => {
  if (quitting) return;
  event.preventDefault();
  quitting = true;
  void Promise.resolve(backend?.stop()).finally(() => app.quit());
});

if (locked) app.whenReady().then(async () => {
  protocol.handle("artex", (request) => {
    if (request.url === "artex://startup/fonts/PretendardVariable.woff2") return new Response(fs.readFileSync(path.join(resourceRoot, "fonts/PretendardVariable.woff2")), { headers: { "content-type": "font/woff2" } });
    const file = request.url === "artex://startup/" ? "startup.html" : request.url === "artex://startup/startup.js" ? "startup.js" : null;
    if (!file) return new Response("찾을 수 없습니다", { status: 404 });
    return new Response(fs.readFileSync(path.join(__dirname, file)), { headers: { "content-type": file.endsWith(".js") ? "text/javascript; charset=utf-8" : "text/html; charset=utf-8" } });
  });
  const scripts = JSON.parse(fs.readFileSync(path.join(resourceRoot, "csp.json"), "utf8"));
  const csp = `default-src 'self'; script-src 'self' ${scripts.join(" ")}; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; media-src 'self' blob:; object-src 'none'; base-uri 'none'; frame-src 'none'; form-action 'self'; frame-ancestors 'none'`;
  const ses = session.defaultSession;
  ses.setPermissionRequestHandler((_contents, _permission, callback) => callback(false));
  ses.setPermissionCheckHandler(() => false);
  ses.webRequest.onBeforeRequest((details, callback) => {
    const internal = details.url === "artex://startup/startup.js" || details.url === "artex://startup/fonts/PretendardVariable.woff2" || details.url.startsWith("devtools://") || details.url.startsWith("data:") || details.url.startsWith("blob:");
    callback({ cancel: !trusted(details.url) && !internal });
  });
  ses.webRequest.onBeforeSendHeaders((details, callback) => {
    const requestHeaders = { ...details.requestHeaders };
    for (const name of Object.keys(requestHeaders)) {
      if (name.toLowerCase() === "x-artex-desktop-session") delete requestHeaders[name];
    }
    if (origin && backendSession && new URL(details.url).origin === origin) requestHeaders["X-Artex-Desktop-Session"] = backendSession;
    callback({ requestHeaders });
  });
  ses.webRequest.onHeadersReceived((details, callback) => callback({ responseHeaders: { ...details.responseHeaders, "Content-Security-Policy": [csp] } }));
  window = new BrowserWindow({
    title: "ARTEX", width: 1440, height: 960, minWidth: 1000, minHeight: 700,
    webPreferences: { preload: path.join(__dirname, "preload.cjs"), nodeIntegration: false, contextIsolation: true, sandbox: true, webSecurity: true, allowRunningInsecureContent: false, webviewTag: false },
  });
  window.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
  window.webContents.on("will-navigate", (event, url) => { if (!trusted(url)) event.preventDefault(); });
  window.webContents.on("will-redirect", (event, url) => { if (!trusted(url)) event.preventDefault(); });
  window.webContents.on("will-attach-webview", (event) => event.preventDefault());
  window.webContents.on("render-process-gone", (_event, details) => { void failure(new Error(`화면 프로세스가 종료됐습니다 (${details.reason})`)); });
  ipcMain.handle("desktop:status", (event) => { validateSender(event); return status; });
  ipcMain.handle("desktop:retry", async (event) => { validateSender(event); if (status.state === "failed") await startBackend(); });
  ipcMain.handle("desktop:quit", (event) => { validateSender(event); app.quit(); });
  ipcMain.handle("desktop:chatgpt-login", async (event, url) => {
    validateReadySender(event);
    try { await shell.openExternal(validateChatGPTLoginURL(url)); }
    catch { throw new Error("ChatGPT 인증 브라우저를 열 수 없습니다. 다시 시도하세요"); }
  });
  ipcMain.handle("desktop:chatgpt-usage", async (event) => {
    validateReadySender(event);
    try { await shell.openExternal("https://chatgpt.com/#settings/Usage"); }
    catch { throw new Error("ChatGPT 사용량 화면을 열 수 없습니다. 다시 시도하세요"); }
  });
  await window.loadURL("artex://startup/");
  await startBackend();
}).catch((error) => { process.stderr.write(error.stack + "\n"); app.exit(1); });
