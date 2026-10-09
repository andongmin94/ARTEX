const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { createRequire } = require("node:module");
const { EventEmitter } = require("node:events");
const { PassThrough } = require("node:stream");
const definition = require("../../internal/browserports/restricted-ports.json");

// Child protocol control only: no real Go/SQLite or Chromium is run here.
function fixture() {
  const child = new EventEmitter();
  child.pid = 2468;
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  child.stdin = new PassThrough();
  child.exitCode = null;
  child.signalCode = null;
  const exited = new Promise((resolve) => child.once("exit", resolve));
  let stopRequested = false;
  child.stdin.on("finish", () => {
    stopRequested = true;
    child.exitCode = 0;
    child.emit("exit", 0, null);
  });
  const file = path.resolve(__dirname, "../src/backend.cjs");
  const localRequire = createRequire(file);
  const timers = [];
  const failures = [];
  const context = {
    module: { exports: {} }, process, URL,
    require(name) { return name === "node:child_process" ? { spawn: () => child } : localRequire(name); },
    setTimeout(callback, milliseconds) { const timer = { callback, milliseconds }; timers.push(timer); return timer; },
    clearTimeout(timer) { if (timer) timer.cleared = true; },
  };
  vm.runInNewContext(fs.readFileSync(file, "utf8"), context);
  const backend = new context.module.exports.Backend({
    executable: "controlled.exe", home: "controlled-home", sessionToken: "controlled-session",
    onFailure(error) { failures.push(error); },
  });
  return { backend, child, timers, failures, exited, stopped: () => stopRequested };
}

function announce(child, url, overrides = {}) {
  child.stdout.write(JSON.stringify({ event: "ready", pid: child.pid, version: "test", url, ...overrides }) + "\n");
}

test("Chromium 제한 포트 ready는 모두 거부하고 자식을 종료", async (t) => {
  for (const port of definition.ports) {
    await t.test(String(port), async () => {
      const control = fixture();
      const started = control.backend.start();
      announce(control.child, `http://127.0.0.1:${port}`);
      await assert.rejects(started, /금지된 포트/);
      await control.exited;
      assert.equal(control.stopped(), true);
      assert.equal(control.backend.child, null);
      assert.equal(control.failures.length, 0);
      assert.equal(control.timers[0].cleared, true);
    });
  }
});

test("안전한 bound port와 PID는 ready로 연결하고 정상 종료", async () => {
  const control = fixture();
  const started = control.backend.start();
  announce(control.child, "http://127.0.0.1:49152");
  assert.equal((await started).url, "http://127.0.0.1:49152");
  assert.equal(control.stopped(), false);
  assert.equal(control.timers[0].cleared, true);
  await control.backend.stop();
  assert.equal(control.stopped(), true);
  assert.equal(control.failures.length, 0);
});

test("금지 포트 실패 뒤 이어진 ready는 시작 성공으로 바뀌지 않음", async () => {
  const control = fixture();
  const started = control.backend.start();
  announce(control.child, "http://127.0.0.1:6000");
  announce(control.child, "http://127.0.0.1:49152");
  await assert.rejects(started, /금지된 포트/);
  await control.exited;
  assert.equal(control.stopped(), true);
  assert.equal(control.failures.length, 0);
});

test("ready PID·scheme·host·userinfo·경로 경계는 유지", async (t) => {
  for (const [url, overrides] of [
    ["http://127.0.0.1:49152", { pid: 9999 }],
    ["https://127.0.0.1:49152", {}],
    ["http://localhost:49152", {}],
    ["http://192.0.2.1:49152", {}],
    ["http://user:pass@127.0.0.1:49152", {}],
    ["http://127.0.0.1:49152/other", {}],
    ["http://127.0.0.1:49152/?value=1", {}],
    ["http://127.0.0.1:49152/#value", {}],
  ]) {
    await t.test(url + JSON.stringify(overrides), async () => {
      const control = fixture();
      const started = control.backend.start();
      announce(control.child, url, overrides);
      await assert.rejects(started, /loopback/);
      await control.exited;
      assert.equal(control.stopped(), true);
    });
  }
});
