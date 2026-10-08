// 실제 SQLite 백엔드를 새 Unicode home에서 시작하고 부모 EOF 종료를 검증합니다.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createInterface } from 'node:readline';

async function bounded(promise, milliseconds, label) {
  let timer;
  try {
    return await Promise.race([
      promise,
      new Promise((_, reject) => {
        timer = setTimeout(() => reject(new Error(`${label} timed out`)), milliseconds);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}

async function main() {
  assert(process.argv[2], 'Pass the compiled artex executable path');
  const home = await mkdtemp(join(tmpdir(), 'artex 데스크톱-'));
  const child = spawn(resolve(process.argv[2]), [
    '-addr', '127.0.0.1:0', '-proxy', '', '-ready-stdout', '-parent-stdin',
  ], {
    cwd: home,
    env: {
      ...process.env,
      ARTEX_HOME: home,
      ARTEX_CONFIG: '',
      ARTEX_SKILL_DIR: '',
      ARTEX_DESKTOP_SESSION: "",
      ANTHROPIC_API_KEY: '',
      OPENAI_API_KEY: '',
    },
    stdio: ['pipe', 'pipe', 'pipe'],
    windowsHide: true,
  });
  let diagnostic = '';
  child.stderr.setEncoding('utf8');
  child.stderr.on('data', (text) => { diagnostic = (diagnostic + text).slice(-16000); });
  // A failed startup can close stdin before the parent requests shutdown.
  child.stdin.on('error', () => {});
  const exited = new Promise((resolveExit, reject) => {
    child.once('error', reject);
    child.once('close', (code, signal) => resolveExit({ code, signal }));
  });
  const lines = createInterface({ input: child.stdout });
  const ready = new Promise((resolveReady) => {
    lines.on('line', (line) => {
      try {
        const event = JSON.parse(line);
        if (event.event === 'ready') resolveReady(event);
      } catch { /* Human-readable output is not a readiness event. */ }
    });
  });
  try {
    const event = await bounded(Promise.race([
      ready,
      exited.then((result) => { throw new Error(`Backend exited before ready: ${JSON.stringify(result)}`); }),
    ]), 45000, 'Backend readiness');
    assert.equal(event.pid, child.pid);
    const address = new URL(event.url);
    assert.equal(address.protocol, 'http:');
    assert.equal(address.hostname, '127.0.0.1');
    assert(Number(address.port) > 0, 'The ready event must contain the bound port');
    const response = await fetch(new URL('/api/health', address), { signal: AbortSignal.timeout(5000) });
    assert.equal(response.status, 200, 'Real backend health endpoint must respond');
    await response.text();
    child.stdin.end();
    const result = await bounded(exited, 15000, 'Parent-EOF shutdown');
    assert.equal(result.signal, null);
    assert.equal(result.code, 0, 'Parent EOF must perform a normal shutdown');
    console.log('PASS real backend: isolated Unicode home → JSON ready → HTTP health → parent EOF → exit 0');
  } catch (error) {
    console.error(diagnostic);
    throw error;
  } finally {
    lines.close();
    if (child.exitCode === null && child.signalCode === null) {
      child.kill();
      try {
        await bounded(exited, 5000, 'Cleanup');
      } catch {
        child.kill('SIGKILL');
        await bounded(exited.catch(() => {}), 5000, 'Forced cleanup');
      }
    }
    await rm(home, { recursive: true, force: true });
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
