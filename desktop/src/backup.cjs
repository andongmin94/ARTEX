const fs = require("node:fs");
const path = require("node:path");
const { randomUUID, createHash } = require("node:crypto");
const { spawn } = require("node:child_process");

class DesktopBackup {
  constructor({ executable, home }) {
    this.executable = executable;
    this.home = home;
    this.statePath = path.join(home, ".desktop-backup.json");
    this.state = { state: "idle", automatic: true };
    if (fs.existsSync(this.statePath)) {
      if (!fs.lstatSync(this.statePath).isFile()) throw new Error("백업 설정이 일반 파일이 아닙니다");
      const saved = JSON.parse(fs.readFileSync(this.statePath, "utf8"));
      if (typeof saved.automatic !== "boolean") throw new Error("백업 설정이 올바르지 않습니다");
      this.state = { ...saved, state: "idle" };
    }
  }

  status() { return { ...this.state }; }

  save() {
    fs.mkdirSync(this.home, { recursive: true });
    const temporary = `${this.statePath}.${randomUUID()}.tmp`;
    const file = fs.openSync(temporary, "wx", 0o600);
    try { fs.writeFileSync(file, JSON.stringify(this.state, null, 2) + "\n"); fs.fsyncSync(file); }
    finally { fs.closeSync(file); }
    fs.renameSync(temporary, this.statePath);
  }

  setAutomatic(value) {
    if (typeof value !== "boolean") throw new Error("자동 백업 설정이 올바르지 않습니다");
    this.state.automatic = value;
    this.save();
    return this.status();
  }

  command(args, event) {
    const environment = { ARTEX_HOME: this.home };
    for (const key of ["SystemRoot", "WINDIR", "TEMP", "TMP", "LOCALAPPDATA"]) {
      if (process.env[key]) environment[key] = process.env[key];
    }
    return new Promise((resolve, reject) => {
      const child = spawn(this.executable, args, { env: environment, cwd: this.home, windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
      child.stdout.setEncoding("utf8");
      child.stderr.setEncoding("utf8");
      let output = "", errors = "";
      let timedOut = false, exitDeadline;
      const timeout = setTimeout(() => {
        timedOut = true;
        child.kill();
        exitDeadline = setTimeout(() => reject(new Error("백업 프로세스 강제 종료를 확인할 수 없습니다")), 5_000);
      }, 120_000);
      child.stdout.on("data", (chunk) => { output = (output + chunk).slice(-64 * 1024); });
      child.stderr.on("data", (chunk) => { errors = (errors + chunk).slice(-8192); });
      child.once("error", (error) => { clearTimeout(timeout); clearTimeout(exitDeadline); reject(error); });
      child.once("close", (code) => {
        clearTimeout(timeout);
        clearTimeout(exitDeadline);
        if (timedOut) return reject(new Error("백업 작업 제한 시간(2분)을 초과했습니다"));
        if (code !== 0) return reject(new Error(errors.trim() || `백업 작업 종료 코드: ${code}`));
        try {
          const result = JSON.parse(output.trim());
          if (result.event !== event || !path.isAbsolute(result.path) || !Number.isSafeInteger(result.files) || result.files < 1) throw new Error("백업 결과를 검증할 수 없습니다");
          resolve(result);
        } catch (error) { reject(error); }
      });
    });
  }

  defaultDirectory() {
    const identity = createHash("sha256").update(path.resolve(this.home)).digest("hex").slice(0, 12);
    return path.join(path.dirname(this.home), "ARTEX-backups", identity);
  }

  async create(parent = this.defaultDirectory()) {
    if (!path.isAbsolute(parent)) throw new Error("백업 폴더는 절대 경로여야 합니다");
    this.state.state = "creating";
    try {
      fs.mkdirSync(parent, { recursive: true });
      const destination = path.join(parent, `ARTEX-${new Date().toISOString().replaceAll(":", "-")}-${randomUUID().slice(0, 8)}`);
      const result = await this.command(["-backup", destination], "backup-complete");
      this.state = { ...this.state, state: "idle", lastBackup: result.path, lastBackupAt: new Date().toISOString(), files: result.files, bytes: result.bytes, error: undefined };
      this.save();
      return this.status();
    } catch (error) {
      this.state = { ...this.state, state: "idle", error: error.message };
      this.save();
      throw error;
    }
  }

  async restore(source, parent) {
    if (!path.isAbsolute(source) || !path.isAbsolute(parent)) throw new Error("복원 경로는 절대 경로여야 합니다");
    const destination = path.join(parent, `ARTEX-restored-${randomUUID()}`);
    const result = await this.command(["-restore", source, "-restore-home", destination], "restore-complete");
    this.state = { ...this.state, restoredHome: result.path, restoredAt: new Date().toISOString() };
    this.save();
    return this.status();
  }

  async onQuit() {
    if (!this.state.automatic || !fs.existsSync(path.join(this.home, "data/artex.sqlite"))) return;
    // 하루 첫 정상 종료 때만 새 스냅샷을 만들며 이전 백업은 삭제하지 않는다.
    if (this.state.lastBackupAt && new Date(this.state.lastBackupAt).toDateString() === new Date().toDateString()) return;
    await this.create();
  }
}

module.exports = { DesktopBackup };
