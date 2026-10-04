// Run the actual TypeScript handlers with synthetic IME events; no browser or API.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const ts = require('typescript');
const root = path.resolve(__dirname, '../src');
const compile = (text) => ts.transpileModule(text, { compilerOptions: { target: ts.ScriptTarget.ES2020 } }).outputText;
const read = (name) => ts.createSourceFile(name, fs.readFileSync(path.join(root, name), 'utf8'), ts.ScriptTarget.Latest, true);
const source = read('lib/chat-send-mode.ts');
const fn = source.statements.find((node) => ts.isFunctionDeclaration(node) && node.name?.text === 'shouldSubmitOnKey');
assert.ok(fn, 'Chat submission handler missing');
const submit = vm.runInNewContext(compile(fn.getText(source).replace(/^export /, '')) + '\nshouldSubmitOnKey;');
function event(extra = {}) {
  return { key: 'Enter', shiftKey: false, ctrlKey: false, metaKey: false,
    nativeEvent: { isComposing: false, keyCode: 13 }, preventDefault() {},
    currentTarget: { blur() {} }, ...extra };
}
for (const [mode, input, want] of [
  ['enter', {}, true], ['enter', { shiftKey: true }, false],
  ['enter', { key: 'a' }, false], ['ctrl-enter', {}, false],
  ['ctrl-enter', { ctrlKey: true }, true], ['ctrl-enter', { metaKey: true }, true],
  ['enter', { nativeEvent: { isComposing: true, keyCode: 13 } }, false],
  ['enter', { nativeEvent: { isComposing: false, keyCode: 229 } }, false],
  ['ctrl-enter', { ctrlKey: true, nativeEvent: { isComposing: true, keyCode: 13 } }, false],
  ['ctrl-enter', { metaKey: true, nativeEvent: { isComposing: false, keyCode: 229 } }, false],
]) assert.equal(submit(event(input), mode), want, JSON.stringify({ mode, input }));
let handlers = 0;
for (const name of ['app/(main)/function/tasks/page.tsx', 'app/(main)/chat/page.tsx']) {
  const source = read(name);
  function visit(node) {
    if (ts.isArrowFunction(node) && ts.isJsxAttribute(node.parent?.parent) &&
        node.parent.parent.name.getText(source) === 'onKeyDown' &&
        /onCommitRename|createAndSelect|saveCategory|cancelOnBlurRef/.test(node.getText(source))) {
      let actions = 0;
      const scope = { matchCount: 0, trimmed: '한국어', conv: { id: 'test' }, renameText: '한국어',
        onCommitRename() { actions++; }, createAndSelect() { actions++; }, saveCategory() { actions++; },
        cancelOnBlurRef: { current: false }, cancelRenameRef: { current: false }, onCancelRename() {} };
      const handler = vm.runInNewContext(compile('(' + node.getText(source) + ')'), scope);
      for (const nativeEvent of [{ isComposing: true, keyCode: 13 }, { isComposing: false, keyCode: 229 }]) {
        handler(event({ nativeEvent, currentTarget: { blur() { actions++; } } }));
        assert.equal(actions, 0, `${name}: composing Enter committed prematurely`);
      }
      handler(event({ currentTarget: { blur() { actions++; } } }));
      assert.equal(actions, 1, `${name}: normal Enter did not commit once`);
      handlers++;
    }
    ts.forEachChild(node, visit);
  }
  visit(source);
}
assert.equal(handlers, 4, 'Rename/category IME regression coverage changed');
console.log('한국어 입력 검사 통과: 채팅 키 조합 10개, 이름/분류 편집기 4개');
