const { BrowserWindow, session } = require("electron");
const http = require("node:http");
const { randomBytes, timingSafeEqual } = require("node:crypto");

const MAX_REQUEST = 12 << 20;
const MAX_RESPONSE = 48 << 20;
async function boundedBytes(stream, limit) {
  if (!stream) return Buffer.alloc(0);
  const reader = stream.getReader();
  const chunks = [];
  let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > limit) {
        await reader.cancel();
        throw new Error("브라우저 본문 크기가 제한을 초과했습니다");
      }
      chunks.push(Buffer.from(value));
    }
    return Buffer.concat(chunks, size);
  } finally { reader.releaseLock(); }
}
const object = (properties = {}, required = []) => ({ type: "object", properties, required, additionalProperties: false });
const string = { type: "string" };
const tools = [
  { name: "browser_navigate", description: "작업 범위에 승인된 HTTP/HTTPS 페이지를 엽니다.", inputSchema: object({ url: string }, ["url"]) },
  { name: "browser_snapshot", description: "현재 페이지의 텍스트와 클릭·입력 가능한 요소 참조를 읽습니다.", inputSchema: object() },
  { name: "browser_click", description: "최근 snapshot의 요소 참조를 클릭합니다.", inputSchema: object({ ref: string }, ["ref"]) },
  { name: "browser_type", description: "최근 snapshot의 입력 요소에 문자열을 입력합니다.", inputSchema: object({ ref: string, text: string, submit: { type: "boolean" } }, ["ref", "text"]) },
  { name: "browser_press_key", description: "현재 페이지에 키 입력을 보냅니다.", inputSchema: object({ key: string }, ["key"]) },
  { name: "browser_evaluate", description: "Node 접근이 없는 현재 페이지에서 JavaScript 함수를 실행합니다.", inputSchema: object({ function: string }, ["function"]) },
  { name: "browser_screenshot", description: "현재 페이지의 PNG를 작업 파일에 저장합니다.", inputSchema: object() },
  { name: "browser_wait_for", description: "페이지에 지정한 텍스트가 나타날 때까지 기다립니다(최대 10초).", inputSchema: object({ text: string }, ["text"]) },
  { name: "browser_close", description: "이 실행에 소유된 브라우저와 메모리 프로필을 닫습니다.", inputSchema: object() },
];

const snapshotCode = `(() => {
  const state = { refs: new Map(), next: 0 }; globalThis.__artexBrowserRefs = state;
  const rows = [...document.querySelectorAll('a,button,input,textarea,select,[role="button"],[role="link"],[contenteditable="true"]')]
    .filter(e => e.getClientRects().length && getComputedStyle(e).visibility !== 'hidden').slice(0, 1000).map(e => {
      const ref = 'e' + (++state.next); state.refs.set(ref, e);
      const role = e.getAttribute('role') || e.tagName.toLowerCase();
      const name = e.getAttribute('aria-label') || e.labels?.[0]?.innerText || e.innerText || e.getAttribute('placeholder') || e.getAttribute('name') || '';
      return '[' + ref + '] ' + role + ' ' + name.trim().slice(0, 500);
    });
  return {url: location.href, title: document.title, text: document.body?.innerText.slice(0, 100000) || '', elements: rows};
})()`;

class BrowserBroker {
  constructor({ token, backendOrigin, available }) {
    this.token = token;
    this.backendOrigin = backendOrigin;
    this.available = available;
    this.workers = new Map();
    this.server = null;
    this.proxy = null;
    this.url = null;
    this.stopping = false;
  }

  async start() {
    if (this.server) throw new Error("브라우저 중계가 이미 시작됐습니다");
    // Every Chromium connection that bypasses protocol.handle terminates here.
    // The deny proxy never resolves targets, authenticates or opens a socket.
    this.proxy = http.createServer((_request, response) => { response.writeHead(403, { "content-length": "0" }); response.end(); });
    this.proxy.on("connect", (_request, socket) => socket.end("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n"));
    this.proxy.on("upgrade", (_request, socket) => socket.destroy());
    await new Promise((resolve, reject) => { this.proxy.once("error", reject); this.proxy.listen(0, "127.0.0.1", resolve); });
    this.server = http.createServer((request, response) => { void this.handle(request, response); });
    this.server.requestTimeout = 35_000;
    this.server.headersTimeout = 5_000;
    await new Promise((resolve, reject) => { this.server.once("error", reject); this.server.listen(0, "127.0.0.1", resolve); });
    this.url = `http://127.0.0.1:${this.server.address().port}`;
    return { url: this.url, denyProxyPort: this.proxy.address().port };
  }

  authenticated(request) {
    const token = this.token();
    const provided = request.headers["x-artex-desktop-session"];
    return typeof token === "string" && /^[a-f0-9]{64}$/.test(token) && typeof provided === "string" &&
      /^[a-f0-9]{64}$/.test(provided) && timingSafeEqual(Buffer.from(provided), Buffer.from(token)) &&
      request.headers.host === new URL(this.url).host && !request.headers.origin && !request.headers.cookie &&
      request.socket.remoteAddress === "127.0.0.1";
  }

  async handle(request, response) {
    if (this.stopping || request.method !== "POST" || request.url !== "/rpc" || !this.authenticated(request)) {
      response.writeHead(403); response.end(); return;
    }
    let size = 0, chunks = [], rpc;
    try {
      for await (const chunk of request) { size += chunk.length; if (size > MAX_REQUEST) throw new Error("브라우저 요청 크기가 제한을 초과했습니다"); chunks.push(chunk); }
      rpc = JSON.parse(Buffer.concat(chunks).toString("utf8"));
      if (rpc.jsonrpc !== "2.0" || !Number.isSafeInteger(rpc.id) || rpc.id <= 0 || typeof rpc.method !== "string") throw new Error("브라우저 JSON-RPC 요청이 유효하지 않습니다");
      const abort = new AbortController();
      response.once("close", () => { if (!response.writableEnded) abort.abort(); });
      const timer = setTimeout(() => abort.abort(), 30_000);
      let result;
      try { result = await this.dispatch(rpc.method, rpc.params ?? {}, abort.signal); }
      finally { clearTimeout(timer); }
      const body = JSON.stringify({ jsonrpc: "2.0", id: rpc.id, result });
      if (Buffer.byteLength(body) > MAX_RESPONSE) throw new Error("브라우저 응답 크기가 제한을 초과했습니다");
      response.writeHead(200, { "content-type": "application/json", "cache-control": "no-store" }); response.end(body);
    } catch (error) {
      response.writeHead(200, { "content-type": "application/json", "cache-control": "no-store" });
      response.end(JSON.stringify({ jsonrpc: "2.0", id: rpc?.id ?? null, error: { code: -32000, message: error.message } }));
    }
  }

  async backend(path, body, signal) {
    const origin = this.backendOrigin();
    if (!origin || !this.token()) throw new Error("앱 백엔드가 준비되지 않았습니다");
    const response = await fetch(new URL(path, origin), { method: "POST", signal,
      headers: { "content-type": "application/json", "X-Artex-Desktop-Session": this.token() }, body: JSON.stringify(body), redirect: "error" });
    const bytes = await boundedBytes(response.body, MAX_RESPONSE);
    const result = JSON.parse(bytes.toString("utf8"));
    if (!response.ok) throw new Error(typeof result.error === "string" ? result.error : "브라우저 요청의 작업 승인 또는 격리 검증이 거부됐습니다");
    return result;
  }

  async open(params, signal, probe = false) {
    if (!this.available() || !/^[a-f0-9]{32}$/.test(params.session_id) || !Number.isSafeInteger(params.task_id) || (probe ? params.task_id !== 0 : params.task_id <= 0) || this.workers.has(params.session_id)) throw new Error("브라우저 작업 소유자 또는 OS 격리가 유효하지 않습니다");
    const partition = `artex-browser-${params.session_id}-${randomBytes(16).toString("hex")}`;
    const ses = session.fromPartition(partition, { cache: false });
    if (ses.storagePath !== null) throw new Error("브라우저 프로필이 메모리 전용이 아닙니다");
    const port = this.proxy.address().port;
    await ses.setProxy({ mode: "fixed_servers", proxyRules: `http=127.0.0.1:${port};https=127.0.0.1:${port};socks=127.0.0.1:${port}`, proxyBypassRules: "<-loopback>" });
    ses.setPermissionRequestHandler((_contents, _permission, callback) => callback(false));
    ses.setPermissionCheckHandler(() => false);
    ses.on("will-download", (event) => event.preventDefault());
    ses.on("select-client-certificate", (event, _contents, _url, _certificates, callback) => { event.preventDefault(); callback(); });
    ses.webRequest.onBeforeRequest((details, callback) => {
      let allowed = false;
      try { allowed = ["http:", "https:", "about:", "data:", "blob:"].includes(new URL(details.url).protocol); } catch {}
      const state = this.workers.get(params.session_id);
      if (state?.signal && details.resourceType === "mainFrame") state.mainFrameURLs.add(details.url);
      callback({ cancel: !allowed });
    });
    for (const scheme of ["http", "https"]) ses.protocol.handle(scheme, async (request) => {
      const state = this.workers.get(params.session_id);
      const callSignal = state?.signal;
      try {
        if (!callSignal || callSignal.aborted) throw new Error("승인된 브라우저 호출이 종료됐습니다");
        const headers = {};
        for (const [name, value] of request.headers) if (!name.toLowerCase().startsWith("x-artex-") && !["proxy-authorization", "proxy-authenticate"].includes(name.toLowerCase())) headers[name] = [value];
        // Electron's custom-protocol Request omits network-service Cookie
        // headers. Read only this worker's memory store, preserving a strict
        // same-origin boundary for subrequests and SameSite for navigation.
        const source = new URL(request.url);
        let sameOrigin = false;
        try { sameOrigin = source.origin === new URL(state.window.webContents.getURL()).origin; } catch {}
        const mainFrame = state.mainFrameURLs.has(request.url);
        const safeNavigation = mainFrame && ["GET", "HEAD"].includes(request.method);
        const credentialed = request.credentials !== "omit" && (mainFrame || sameOrigin || request.credentials === "include");
        delete headers.cookie;
        if (credentialed) {
          const cookies = (await ses.cookies.get({ url: source.href })).filter(cookie =>
            cookie.sameSite === "no_restriction" || sameOrigin || (safeNavigation && cookie.sameSite !== "strict"));
          if (cookies.length) headers.cookie = [cookies.map(cookie => `${cookie.name}=${cookie.value}`).join("; ")];
        }
        const bytes = await boundedBytes(request.body, 8 << 20);
        if (state.signal !== callSignal || callSignal.aborted) throw new Error("승인된 브라우저 호출이 종료됐습니다");
        await this.backend(`/api/runtime/browser/verify/${params.session_id}`, { pid: state.window.webContents.getOSProcessId() }, callSignal);
        if (state.signal !== callSignal || callSignal.aborted) throw new Error("승인된 브라우저 호출이 종료됐습니다");
        const result = await this.backend(`/api/runtime/browser/relay/${params.session_id}`, { url: request.url, method: request.method, headers, body_base64: bytes.toString("base64") }, callSignal);
        if (!Number.isInteger(result.status) || result.status < 100 || result.status > 599) throw new Error("브라우저 중계 상태가 유효하지 않습니다");
        for (const cookie of result.cookies ?? []) {
          const domain = cookie.domain?.replace(/^\./, "").toLowerCase();
          const host = source.hostname.toLowerCase();
          if (cookie.partitioned || (domain && host !== domain && !host.endsWith(`.${domain}`))) continue;
          if (cookie.name.startsWith("__Secure-") && (!cookie.secure || source.protocol !== "https:")) continue;
          if (cookie.name.startsWith("__Host-") && (!cookie.secure || source.protocol !== "https:" || cookie.domain || cookie.path !== "/")) continue;
          const details = { url: source.href, name: cookie.name, value: cookie.value, path: cookie.path || source.pathname.slice(0, source.pathname.lastIndexOf("/")) || "/", secure: cookie.secure, httpOnly: cookie.http_only, sameSite: ["unspecified", "unspecified", "lax", "strict", "no_restriction"][cookie.same_site] ?? "unspecified" };
          if (domain) details.domain = domain;
          if (cookie.max_age < 0) details.expirationDate = 0;
          else if (cookie.max_age > 0) details.expirationDate = Date.now() / 1000 + cookie.max_age;
          else if (cookie.expires) details.expirationDate = cookie.expires;
          // Chromium validates public suffixes and cookie attributes. Invalid
          // remote cookies are ignored just as they are in a regular browser.
          try { await ses.cookies.set(details); } catch {}
        }
        const responseHeaders = new Headers();
        for (const [name, values] of Object.entries(result.headers ?? {})) for (const value of values) responseHeaders.append(name, value);
        const body = Buffer.from(result.body_base64 ?? "", "base64");
        if (body.length > 32 << 20) throw new Error("브라우저 중계 본문이 너무 큽니다");
        // No other socket-bearing APIs may bypass the HTTP relay. CSP also
        // applies to frames/workers; the deny proxy remains the OS-facing gate.
        responseHeaders.append("content-security-policy", "connect-src http: https:; object-src 'none'");
        return new Response([204, 205, 304].includes(result.status) || request.method === "HEAD" ? null : body, { status: result.status, headers: responseHeaders });
      } catch (error) {
        if (this.workers.get(params.session_id) === state && state?.signal === callSignal && !callSignal?.aborted) {
          const message = error.message;
          state.relayErrors.push(message);
          if (state.mainFrameURLs.has(request.url)) state.navigationError = message;
        }
        return new Response("브라우저 요청이 작업 승인 또는 대상 범위 정책에 의해 차단됐습니다", { status: 451, headers: { "content-type": "text/plain; charset=utf-8" } });
      }
    });
    ses.protocol.handle("file", () => Response.error());
    const window = new BrowserWindow({ show: false, width: 1280, height: 900,
      webPreferences: { session: ses, offscreen: true, nodeIntegration: false, nodeIntegrationInWorker: false, contextIsolation: true, sandbox: true, webSecurity: true, allowRunningInsecureContent: false, webviewTag: false, devTools: false, backgroundThrottling: false } });
    const state = { window, ses, taskID: params.task_id, signal: null, busy: false, relayErrors: [], mainFrameURLs: new Set(), navigationURL: null, navigationError: null };
    this.workers.set(params.session_id, state);
    window.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
    window.webContents.setWebRTCIPHandlingPolicy("disable_non_proxied_udp");
    window.webContents.on("will-attach-webview", (event) => event.preventDefault());
    window.webContents.on("render-process-gone", () => { void this.closeWorker(params.session_id); });
    window.webContents.on("login", (event, _details, _auth, callback) => { event.preventDefault(); callback(); });
    try {
      await window.loadURL("about:blank");
      const pid = window.webContents.getOSProcessId();
      const isolation = await this.backend(`/api/runtime/browser/verify/${params.session_id}`, { pid }, signal);
      return { pid, isolation, partition, deny_proxy_port: port };
    } catch (error) { await this.closeWorker(params.session_id); throw error; }
  }

  async closeWorker(id) {
    const state = this.workers.get(id);
    if (!state) return;
    this.workers.delete(id);
    state.signal = null;
    if (!state.window.isDestroyed()) state.window.destroy();
    for (const scheme of ["http", "https", "file"]) state.ses.protocol.unhandle(scheme);
    await state.ses.clearStorageData();
    await state.ses.closeAllConnections();
  }

  async dispatch(method, params, signal) {
    if (method === "initialize") return { protocolVersion: "2025-06-18", capabilities: { tools: {} }, serverInfo: { name: "ARTEX task browser", version: "1" }, available: this.available() };
    if (method === "tools/list") return { tools };
    if (method === "sandbox/probe") return this.open(params, signal, true);
    if (method === "sessions/open") return this.open(params, signal);
    if (method === "sessions/close") { await this.closeWorker(params.session_id); return {}; }
    if (method !== "tools/call") throw new Error("지원되지 않는 브라우저 MCP 메서드입니다");
    const state = this.workers.get(params.session_id);
    if (!state || state.window.isDestroyed() || state.busy || !tools.some((tool) => tool.name === params.name)) throw new Error("브라우저 호출의 실행 소유자 또는 도구가 유효하지 않습니다");
    const call = new AbortController();
    const cancelCall = () => call.abort();
    signal.addEventListener("abort", cancelCall, { once: true });
    if (signal.aborted) call.abort();
    state.busy = true; state.signal = call.signal; state.relayErrors = []; state.navigationError = null; state.mainFrameURLs.clear();
    const abort = () => { void this.closeWorker(params.session_id); };
    signal.addEventListener("abort", abort, { once: true });
    try {
      const args = params.arguments ?? {};
      const web = state.window.webContents;
      await this.backend(`/api/runtime/browser/verify/${params.session_id}`, { pid: web.getOSProcessId() }, signal);
      const isolated = (code) => web.executeJavaScriptInIsolatedWorld(731, [{ code }], true);
      let value;
      switch (params.name) {
        case "browser_navigate": {
          const url = new URL(args.url);
          if (!["http:", "https:"].includes(url.protocol) || url.username || url.password) throw new Error("브라우저 주소는 인증정보 없는 HTTP/HTTPS여야 합니다");
          state.navigationURL = url.href;
          await web.loadURL(url.href);
          await this.backend(`/api/runtime/browser/verify/${params.session_id}`, { pid: web.getOSProcessId() }, signal);
          if (state.navigationError) throw new Error(state.navigationError);
          value = await isolated(snapshotCode); break;
        }
        case "browser_snapshot": value = await isolated(snapshotCode); break;
        case "browser_click":
        case "browser_type": {
          if (typeof args.ref !== "string" || !/^e[1-9][0-9]{0,5}$/.test(args.ref)) throw new Error("유효한 snapshot 요소 참조가 필요합니다");
          const operation = params.name === "browser_click" ? "e.click()" : `const setter = Object.getOwnPropertyDescriptor(e.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, 'value')?.set; if (e.isContentEditable) e.textContent = ${JSON.stringify(args.text)}; else if (setter) setter.call(e, ${JSON.stringify(args.text)}); else throw new Error('입력 요소가 아닙니다'); e.dispatchEvent(new Event('input',{bubbles:true})); e.dispatchEvent(new Event('change',{bubbles:true}));`;
          await isolated(`(() => {const e = globalThis.__artexBrowserRefs?.refs.get(${JSON.stringify(args.ref)}); if (!e || !e.isConnected) throw new Error('snapshot 참조가 만료됐습니다'); e.focus(); ${operation}; return true})()`);
          if (args.submit) { web.sendInputEvent({ type: "keyDown", keyCode: "Enter" }); web.sendInputEvent({ type: "keyUp", keyCode: "Enter" }); }
          value = await isolated(snapshotCode); break;
        }
        case "browser_press_key":
          if (typeof args.key !== "string" || args.key.length > 30) throw new Error("키 입력이 유효하지 않습니다");
          web.sendInputEvent({ type: "keyDown", keyCode: args.key }); web.sendInputEvent({ type: "keyUp", keyCode: args.key }); value = await isolated(snapshotCode); break;
        case "browser_evaluate":
          if (typeof args.function !== "string" || args.function.length > 65536) throw new Error("페이지 함수가 너무 크거나 유효하지 않습니다");
          value = await web.executeJavaScript(`(async()=>{const value=await (${args.function})();const json=JSON.stringify(value);if(json!==undefined&&json.length>100000)throw new Error('페이지 함수 반환값이 제한을 초과했습니다');return json===undefined?null:JSON.parse(json)})()`, true); break;
        case "browser_screenshot":
          return { content: [{ type: "image", mimeType: "image/png", data: (await new Promise((resolve, reject) => {
            const finish = (_event, _rect, image) => { if (image.isEmpty()) return; clearTimeout(timer); web.removeListener("paint", finish); resolve(image); };
            const timer = setTimeout(() => { web.removeListener("paint", finish); reject(new Error("브라우저 이미지 생성 시간이 초과됐습니다")); }, 5_000);
            web.on("paint", finish); web.invalidate();
          })).toPNG().toString("base64") }] };
        case "browser_wait_for": {
          if (typeof args.text !== "string" || args.text.length > 10000) throw new Error("대기할 텍스트가 유효하지 않습니다");
          const deadline = Date.now() + 10_000;
          while (!await isolated(`document.body?.innerText.includes(${JSON.stringify(args.text)})`)) { if (Date.now() >= deadline || signal.aborted) throw new Error("텍스트 대기 시간이 초과됐습니다"); await new Promise((resolve) => setTimeout(resolve, 100)); }
          value = await isolated(snapshotCode); break;
        }
        case "browser_close": await this.closeWorker(params.session_id); value = "브라우저를 닫았습니다"; break;
      }
      let text = typeof value === "string" ? value : JSON.stringify(value);
      if (state.relayErrors.length) text += `\n[범위 정책 또는 중계 검증으로 ${state.relayErrors.length}개 추가 요청이 차단됐습니다]`;
      return { content: [{ type: "text", text }] };
    } finally { signal.removeEventListener("abort", abort); signal.removeEventListener("abort", cancelCall); call.abort(); state.signal = null; state.busy = false; state.navigationURL = null; }
  }

  async reset() { await Promise.all([...this.workers.keys()].map((id) => this.closeWorker(id))); }
  async stop() {
    this.stopping = true;
    await this.reset();
    for (const server of [this.server, this.proxy]) if (server) { server.closeAllConnections(); await new Promise((resolve) => server.close(resolve)); }
    this.server = this.proxy = null;
  }
}

module.exports = { BrowserBroker };
