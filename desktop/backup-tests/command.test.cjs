const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const vm = require("node:vm");
const { EventEmitter } = require("node:events");
const { PassThrough } = require("node:stream");

// This controls child lifecycle and deadlines only. Actual Go/SQLite backup and
// restore behavior is exercised separately by backup.test.cjs and backup.spec.
function fixture() {
  const child = new EventEmitter();
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  child.kill = () => { child.killRequested = true; return true; };
  const timers = [];
  const context = {
    module: { exports: {} }, process,
    require(name) { return name === "node:child_process" ? { spawn: () => child } : require(name); },
    setTimeout(callback, milliseconds) { const timer = { callback, milliseconds }; timers.push(timer); return timer; },
    clearTimeout(timer) { if (timer) timer.cleared = true; },
  };
  vm.runInNewContext(fs.readFileSync(path.resolve(__dirname, "../src/backup.cjs"), "utf8"), context);
  const home = fs.mkdtempSync(path.join(os.tmpdir(), "ARTEX backup command control-"));
  const backup = new context.module.exports.DesktopBackup({ executable: path.join(home, "controlled.exe"), home });
  return { backup, child, timers };
}

test("백업 제한시간은 child close로 종료를 확인한 뒤 오류를 반환", async () => {
  const { backup, child, timers } = fixture();
  let completed = false;
  const command = backup.command(["-backup", "controlled"], "backup-complete");
  command.then(() => { completed = true; }, () => { completed = true; });
  assert.equal(timers[0].milliseconds, 120_000);
  timers[0].callback();
  await Promise.resolve();
  await Promise.resolve();
  assert.equal(child.killRequested, true);
  assert.equal(completed, false, "backend must not restart while the maintenance process still owns its home lease");
  child.emit("close", null);
  await assert.rejects(command, /제한 시간/);
});

test("한국어 경로는 UTF-8 chunk 경계와 exit/close 순서에 영향을 받지 않음", async () => {
  const { backup, child } = fixture();
  const destination = path.join(os.tmpdir(), "한국어 백업");
  const command = backup.command(["-backup", "controlled"], "backup-complete");
  const encoded = Buffer.from(JSON.stringify({ event: "backup-complete", path: destination, files: 2, bytes: 128 }) + "\n");
  const split = encoded.indexOf(Buffer.from("한")) + 1;
  child.stdout.write(encoded.subarray(0, split));
  child.emit("exit", 0);
  child.stdout.end(encoded.subarray(split));
  child.emit("close", 0);
  assert.equal((await command).path, destination);
});
