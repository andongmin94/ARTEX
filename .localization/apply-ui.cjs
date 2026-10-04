// One-time source transformation. Removed after the localization is verified.
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const cp = require('node:child_process');
const { createRequire } = require('node:module');
const ts = createRequire(path.resolve('web/package.json'))('typescript');
const BASE = '325a07e9fdb3f06ff7dfdf70ef732cd1a0824d8e';
const original = fs.mkdtempSync(path.join(os.tmpdir(), 'artex-original-'));
cp.execFileSync('tar', ['-xf', '-', '-C', original], {input: cp.execFileSync('git', ['archive', '--format=tar', BASE], {maxBuffer: 100 * 1024 * 1024})});
const han = /[\u3400-\u9fff]/;
const normal = s => s.trim().replace(/\s+/g, ' ');
function sourceFiles(dir) {
  const result = [];
  for (const e of fs.readdirSync(dir, {withFileTypes:true})) {
    const p = path.join(dir,e.name);
    if (e.isDirectory()) result.push(...sourceFiles(p));
    else if (/\.[tj]sx?$/.test(p)) result.push(p);
  }
  return result;
}
function parse(p, source) {
  return ts.createSourceFile(p, source, ts.ScriptTarget.Latest, true, p.endsWith('x') ? ts.ScriptKind.TSX : ts.ScriptKind.TS);
}
function translatable(n) {
  return ts.isStringLiteral(n) || ts.isNoSubstitutionTemplateLiteral(n) || ts.isTemplateHead(n) || ts.isTemplateMiddle(n) || ts.isTemplateTail(n) || ts.isJsxText(n);
}
const originals = new Map();
for (const p of sourceFiles(path.join(original,'web/src'))) {
  const sf = parse(p, fs.readFileSync(p,'utf8'));
  function visit(n) {
    if (translatable(n) && han.test(n.text)) originals.set(normal(n.text), true);
    ts.forEachChild(n,visit);
  }
  visit(sf);
}
const keys = [...originals.keys()];
if (keys.length !== 1882) throw new Error(`Unexpected baseline string count: ${keys.length}`);
const translations = new Map();
const seen = new Set();
for (const name of fs.readdirSync('.localization').filter(n => /^ui-\d+\.tsv$/.test(n)).sort()) {
  for (const line of fs.readFileSync(path.join('.localization',name),'utf8').trimEnd().split('\n')) {
    const split = line.indexOf('\t');
    if (split < 0) throw new Error(`Malformed translation in ${name}`);
    const id = Number(line.slice(0,split));
    if (!Number.isInteger(id) || id < 0 || id >= keys.length || seen.has(id)) throw new Error(`Invalid or duplicate translation: ${id}`);
    const value = line.slice(split+1).replace(/\\n/g,'\n').replaceAll('그래프프','그래프');
    if (!value.trim() || han.test(value)) throw new Error(`Untranslated entry: ${id}`);
    seen.add(id);
    translations.set(keys[id],value);
  }
}
if (seen.size !== keys.length) throw new Error(`Missing translations: ${keys.length-seen.size}`);
function escapeJsx(s,attr=false) {
  return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/{/g,'&#123;').replace(/}/g,'&#125;').replace(attr ? /"/g : /$^/g,'&quot;');
}
let files=0, changes=0;
for (const p of sourceFiles('web/src')) {
  const text=fs.readFileSync(p,'utf8'), sf=parse(p,text), edits=[];
  function visit(n) {
    if (translatable(n) && translations.has(normal(n.text))) {
      const start=ts.isJsxText(n)?n.pos:n.getStart(sf);
      let s=n.text.match(/^\s*/)[0]+translations.get(normal(n.text))+n.text.match(/\s*$/)[0];
      let raw;
      if (ts.isJsxText(n)) raw=escapeJsx(s);
      else if (ts.isStringLiteral(n)) raw=n.parent && ts.isJsxAttribute(n.parent)?'"'+escapeJsx(s,true)+'"':JSON.stringify(s);
      else {
        s=s.replace(/\\/g,'\\\\').replace(/`/g,'\\`').replace(/\$\{/g,'\\${');
        raw=(ts.isTemplateMiddle(n)||ts.isTemplateTail(n)?'}':'`')+s+(ts.isTemplateHead(n)||ts.isTemplateMiddle(n)?'${':'`');
      }
      edits.push({start,end:n.end,raw});
    }
    ts.forEachChild(n,visit);
  }
  visit(sf);
  let next=text;
  for(const e of edits.sort((a,b)=>b.start-a.start)) next=next.slice(0,e.start)+e.raw+next.slice(e.end);
  next=next.replaceAll('"zh-CN"','"ko-KR"').replaceAll('lang="en"','lang="ko"').replaceAll('초과같음','이상').replaceAll('미만같음','이하').replaceAll('\\[模型\\]','\\[모델\\]');
  if(parse(p,next).parseDiagnostics.length>sf.parseDiagnostics.length) throw new Error(`Syntax error introduced in ${p}`);
  if(next!==text){fs.writeFileSync(p,next);files++;changes+=edits.length;}
}
// Keep the producer and consumer of the approval source marker in agreement.
function alignMarkers(dir) {
  for (const e of fs.readdirSync(dir,{withFileTypes:true})) {
    if(e.name.startsWith('.') || e.name==='node_modules') continue;
    const p=path.join(dir,e.name);
    if(e.isDirectory()) alignMarkers(p);
    else if(e.name.endsWith('.go')) {
      const before=fs.readFileSync(p,'utf8');
      const after=before.replaceAll('[模型]','[모델]').replaceAll('【上传文件（绝对路径）】','【업로드 파일(절대 경로)】');
      if(after!==before) fs.writeFileSync(p,after);
    }
  }
}
alignMarkers('.');
const workflow='.github/workflows/verify.yml';
fs.writeFileSync(workflow,fs.readFileSync(workflow,'utf8').replace("--health-cmd 'pg_isready -U artex'",'--health-cmd "pg_isready -U artex"'));
let residual=0;
for(const p of sourceFiles('web/src')) {
  const sf=parse(p,fs.readFileSync(p,'utf8'));
  function visit(n){if(translatable(n)&&han.test(n.text)){console.error(p,sf.getLineAndCharacterOfPosition(n.pos).line+1,n.text);residual++;}ts.forEachChild(n,visit);}
  visit(sf);
}
if(residual) throw new Error(`${residual} untranslated UI spans`);
fs.rmSync(original,{recursive:true,force:true});
console.log(JSON.stringify({translations:seen.size,files,changes,residual},null,2));
