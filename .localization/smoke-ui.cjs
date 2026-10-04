// Local mock UI only. External network requests are blocked at the browser.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('playwright');
const origin = 'http://127.0.0.1:5174';
const out = path.resolve('verification/korean-ui');
const han = /[\u3400-\u9fff]/u;
const routes = [
  ['/dashboard/', 'dashboard'], ['/chat/', 'chat'],
  ['/function/tasks/', 'tasks'], ['/function/findings/', 'findings'],
  ['/function/assets/', 'assets'], ['/function/traffic/', 'traffic'],
  ['/function/commands/', 'commands'], ['/function/llm-records/', 'llm-records'],
  ['/function/sync/', 'sync'], ['/function/workspace/', 'workspace'],
  ['/system/llm/', 'llm'], ['/system/agents/', 'agents'],
  ['/system/mcp/', 'mcp'], ['/system/skills/', 'skills'],
  ['/system/tools/', 'tools'], ['/system/notify/', 'notify'],
  ['/system/intercept/', 'intercept'], ['/system/intercept/assets/', 'asset-intercept'],
  ['/system/intercept/approvals/', 'approvals'], ['/system/settings/', 'settings'],
  ['/system/logs/', 'logs'], ['/function/tasks/detail/?id=t-acme-web', 'task-detail'],
];
(async () => {
  fs.mkdirSync(out, { recursive: true });
  const browser = await chromium.launch({ headless: true });
  const result = { mode: 'mock; no real target requests', desktop: [], mobile: [], dialogs: [], failures: [], pageErrors: [] };
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, locale: 'ko-KR' });
    await context.route('**/*', route => {
      const url = new URL(route.request().url());
      if (url.origin === origin || url.protocol === 'data:' || url.protocol === 'blob:') return route.continue();
      return route.abort();
    });
    const page = await context.newPage();
    page.setDefaultTimeout(10000);
    page.on('pageerror', err => result.pageErrors.push({ url: page.url(), message: err.message }));
    async function check(name, fn) {
      try { await fn(); }
      catch (error) {
        result.failures.push({ name, message: error.message });
        console.error(name + ': ' + error.message);
        await page.screenshot({ path: path.join(out, name + '-error.png'), fullPage: true });
      }
    }
    async function content(name) {
      assert.equal(await page.locator('html').getAttribute('lang'), 'ko', name + ': document language');
      const text = await page.locator('body').innerText();
      assert.ok(!han.test(text), name + ': Chinese runtime text: ' + text.match(/[\u3400-\u9fff].{0,80}/u)?.[0]);
      assert.ok(!text.includes('Application error:'), name + ': application error');
    }
    for (const [url, name] of routes) {
      await check(name, async () => {
        await page.goto(origin + url, { waitUntil: 'domcontentloaded' });
        await page.waitForFunction(() => document.body.innerText.length > 100);
        await page.waitForTimeout(900);
        await content(name);
        result.desktop.push(name);
        if (['dashboard', 'tasks', 'llm', 'intercept', 'notify', 'task-detail'].includes(name)) {
          await page.screenshot({ path: path.join(out, name + '.png'), fullPage: true });
        }
      });
    }
    await check('new-task', async () => {
      await page.goto(origin + '/function/tasks/', { waitUntil: 'domcontentloaded' });
      await page.getByRole('button', { name: '새 작업', exact: true }).click();
      await page.locator('#name').fill('사내 솔루션 한글 입력 확인');
      await content('new-task');
      await page.screenshot({ path: path.join(out, 'new-task.png'), fullPage: true });
      result.dialogs.push('new-task');
      await page.keyboard.press('Escape');
    });
    await check('preferences', async () => {
      await page.goto(origin + '/function/tasks/', { waitUntil: 'domcontentloaded' });
      await page.getByRole('button', { name: '화면 설정', exact: true }).click();
      await page.getByText('기본값 복원', { exact: true }).waitFor();
      await content('preferences');
      await page.screenshot({ path: path.join(out, 'preferences.png'), fullPage: true });
      result.dialogs.push('preferences');
      await page.keyboard.press('Escape');
    });
    await page.setViewportSize({ width: 390, height: 844 });
    for (const [url, name] of [['/function/tasks/', 'tasks'], ['/system/llm/', 'llm'], ['/system/settings/', 'settings']]) {
      await check(name + '-mobile', async () => {
        await page.goto(origin + url, { waitUntil: 'domcontentloaded' });
        await page.waitForTimeout(900);
        await content(name);
        const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
        assert.ok(overflow <= 2, name + ': mobile page overflows by ' + overflow + 'px');
        await page.screenshot({ path: path.join(out, name + '-mobile.png'), fullPage: true });
        result.mobile.push(name);
      });
    }
    assert.deepEqual(result.pageErrors, [], 'Browser runtime exceptions');
    assert.deepEqual(result.failures, [], 'UI verification failures');
    assert.equal(result.desktop.length, 22);
    assert.equal(result.mobile.length, 3);
    assert.equal(result.dialogs.length, 2);
    console.log('Korean browser verification passed: 22 desktop pages, 3 mobile pages, 2 dialogs');
  } finally {
    fs.writeFileSync(path.join(out, 'summary.json'), JSON.stringify(result, null, 2));
    await browser.close();
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
