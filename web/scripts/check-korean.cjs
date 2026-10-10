const fs = require('node:fs');
const path = require('node:path');
const ts = require('typescript');
const han = /[\u3400-\u9fff]/;
const errors = [];
function scan(dir) {
  for (const entry of fs.readdirSync(dir, {withFileTypes: true})) {
    const file = path.join(dir, entry.name);
    if (entry.isDirectory()) { scan(file); continue; }
    if (!/\.tsx?$/.test(file)) continue;
    const source = ts.createSourceFile(file, fs.readFileSync(file, 'utf8'), ts.ScriptTarget.Latest, true);
    const visit = (node) => {
      if ((ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node) || ts.isTemplateHead(node) || ts.isTemplateMiddle(node) || ts.isTemplateTail(node) || ts.isJsxText(node)) && han.test(node.text)) {
        errors.push(`${file}:${source.getLineAndCharacterOfPosition(node.pos).line + 1}: ${node.text.slice(0, 120)}`);
      }
      ts.forEachChild(node, visit);
    }
    visit(source);
  }
}
scan(path.resolve(__dirname, '../src'));
if (errors.length) { console.error(errors.join('\n')); process.exit(1); }
console.log('한국어 UI 검사 통과: 중국어 런타임 문구 0건');
