const { spawn } = require("node:child_process");
const { createInterface } = require("node:readline");

class Backend {
  constructor({ executable, home, sessionToken, onFailure }) {
    this.executable = executable;
    this.home = home;
    this.sessionToken = sessionToken;
    this.onFailure = onFailure;
    this.child = null;
    this.stopping = false;
  }

  async start() {
    if (this.child) throw new Error("백엔드가 이미 실행 중입니다");
    const env = { ...process.env, ARTEX_HOME: this.home, ARTEX_DESKTOP_SESSION: this.sessionToken };
    for (const key of ["ARTEX_CONFIG", "ARTEX_SKILL_DIR", "ARTEX_PG_DSN"]) delete env[key];
    this.stopping = false;
    const child = spawn(this.executable, ["-addr", "127.0.0.1:0", "-proxy", "127.0.0.1:8788", "-ready-stdout", "-parent-stdin"], {
      cwd: this.home,
      env,
      stdio: ["pipe", "pipe", "pipe"],
      windowsHide: true,
    });
    this.child = child;
    let log = "";
    child.stderr.setEncoding("utf8");
    child.stderr.on("data", (data) => {
      log = (log + data).slice(-8192);
      process.stderr.write(data);
    });
    const lines = createInterface({ input: child.stdout });
    return new Promise((resolve, reject) => {
      let ready = false;
      const fail = (error) => {
        if (!ready) reject(error);
        else if (!this.stopping) this.onFailure(error);
      };
      const timer = setTimeout(() => {
        fail(new Error("백엔드 준비 제한 시간(30초)을 초과했습니다"));
        void this.stop();
      }, 30_000);
      child.once("error", (error) => {
        clearTimeout(timer);
        this.child = null;
        fail(error);
      });
      child.once("exit", (code, signal) => {
        clearTimeout(timer);
        lines.close();
        this.child = null;
        fail(new Error(`백엔드가 종료됐습니다 (${signal ?? code}). ${log.trim()}`));
      });
      lines.on("line", (line) => {
        if (ready) return;
        try {
          const event = JSON.parse(line);
          const url = new URL(event.url);
          if (event.event !== "ready" || event.pid !== child.pid || url.protocol !== "http:" || url.hostname !== "127.0.0.1" || !url.port || url.username || url.password || url.pathname !== "/" || url.search || url.hash) {
            throw new Error("백엔드 ready 응답의 PID 또는 loopback 주소가 유효하지 않습니다");
          }
          ready = true;
          clearTimeout(timer);
          resolve({ url: url.origin, pid: child.pid, version: event.version });
        } catch (error) {
          fail(error);
          void this.stop();
        }
      });
    });
  }

  async stop() {
    const child = this.child;
    if (!child) return;
    this.stopping = true;
    await new Promise((resolve) => {
      const timer = setTimeout(() => { child.kill(); }, 8_000);
      child.once("exit", () => { clearTimeout(timer); resolve(); });
      child.stdin.end();
    });
  }
}

module.exports = { Backend };
