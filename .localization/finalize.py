from pathlib import Path
import json
import re
import subprocess


def replace(name, old, new):
    p = Path(name)
    text = p.read_text()
    if old in text:
        p.write_text(text.replace(old, new))
    elif new not in text:
        raise RuntimeError(f'Unexpected source in {name}: {old[:100]}')


def write(name, content):
    p = Path(name)
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(content)

# HTTP status is a machine contract; localized prose must not control behavior.
write('web/src/lib/api-error.ts', '''export class ApiError extends Error {
  constructor(public readonly status: number, message: string) {
    super(message);
    this.name = "ApiError";
  }
}
''')
replace('web/src/lib/api.ts', 'import { auth } from "./auth";', 'import { auth } from "./auth";\nimport { ApiError } from "./api-error";')
replace('web/src/lib/api.ts', 'throw new Error(message);', 'throw new ApiError(r.status, message);')
replace('web/src/lib/api.ts', 'throw new Error(body?.error || `업로드 실패(${r.status})`);', 'throw new ApiError(r.status, body?.error || `업로드 실패(${r.status})`);')
for name in ['web/src/app/(main)/function/tasks/page.tsx', 'web/src/app/(main)/system/skills/page.tsx']:
    replace(name, 'import { api } from "@/lib/api";', 'import { api } from "@/lib/api";\nimport { ApiError } from "@/lib/api-error";')
replace('web/src/app/(main)/function/tasks/page.tsx', 'if (!state.reason.message.includes("보관본이 존재하지 않습니다")) return;', 'if (!(state.reason instanceof ApiError) || state.reason.status !== 404) return;')
replace('web/src/app/(main)/system/skills/page.tsx', 'if (!overwrite && msg.includes("이미 존재합니다")) {', 'if (!overwrite && e instanceof ApiError && e.status === 409) {')
p = Path('web/src/lib/mock/handler.ts')
text = p.read_text()
if 'import { ApiError }' not in text:
    text = 'import { ApiError } from "../api-error";\n' + text
text = text.replace('throw new Error("보관본이 존재하지 않습니다")', 'throw new ApiError(404, "보관본이 존재하지 않습니다")')
p.write_text(text)

# Native locale and accessibility text must also match the application language.
replace('web/src/components/date-range-picker.tsx', '"Select date"', '"날짜 선택"')
replace('web/src/components/date-range-picker.tsx', '"d MMM yyyy"', '"yyyy.MM.dd"')
replace('web/src/components/ui/calendar.tsx', 'import { DayPicker, getDefaultClassNames } from "react-day-picker";', 'import { DayPicker, getDefaultClassNames } from "react-day-picker";\nimport { ko } from "react-day-picker/locale";')
replace('web/src/components/ui/calendar.tsx', '    <DayPicker\n', '    <DayPicker\n      locale={ko}\n')
replace('web/src/app/globals.css', '--font-sans: var(--font-inter);', '--font-sans: var(--font-inter), "Apple SD Gothic Neo", "Malgun Gothic", sans-serif;')
for old, new in [('Page not found.', '페이지를 찾을 수 없습니다.'), ('The page you are looking for could not be found.', '요청한 페이지가 없거나 이동되었습니다.'), ('Go back home', '작업 목록으로')]:
    replace('web/src/app/not-found.tsx', old, new)
labels = {'Close':'닫기', 'Toggle Sidebar':'사이드바 전환', 'Previous slide':'이전 슬라이드', 'Next slide':'다음 슬라이드', 'Go to previous page':'이전 페이지로', 'Go to next page':'다음 페이지로', 'More pages':'더 많은 페이지', 'More':'더 보기', 'Previous':'이전', 'Next':'다음', 'Loading...':'불러오는 중…', 'Search...':'검색…'}
for p in Path('web/src/components/ui').glob('*.tsx'):
    text = p.read_text()
    for old, new in labels.items():
        # Only literal visible JSX text or accessibility attributes, not identifiers.
        text = re.sub(r'(?<=>)(\s*)'+re.escape(old)+r'(\s*)(?=<)', lambda m: m[1]+new+m[2], text)
        for attr in ['aria-label', 'title', 'placeholder']:
            text = text.replace(f'{attr}="{old}"', f'{attr}="{new}"')
    p.write_text(text)

# Regression found by existing upstream tests: overview omitted company scope.
write('agent/tools_scope.go', '''package agent

// scopeOverview exposes the existing read-only scope without changing membership.
func (t *ToolSet) scopeOverview() []map[string]any {
  if t.as == nil || t.taskID <= 0 { return nil }
  rows, err := t.as.ListTaskScopeWithSources(t.taskID)
  if err != nil { return nil }
  out := make([]map[string]any, 0, len(rows))
  for _, row := range rows {
    item := map[string]any{"id": row.ID, "kind": row.Kind, "domain": row.Domain, "net": row.Net, "value": row.Value, "reason": row.Reason, "source": row.Source}
    if row.TaskID != t.taskID { item["source_task_id"] = row.TaskID; item["inherited"] = true }
    if row.CompanyID != nil {
      item["company_id"] = *row.CompanyID
      item["company_name"] = row.CompanyName
      if rules, err := t.as.Companies().GetScope(*row.CompanyID); err == nil {
        details := make([]map[string]any, 0, len(rules))
        keywords := []string{}
        for _, rule := range rules {
          details = append(details, map[string]any{"kind": rule.Kind, "raw": rule.Raw, "domain": rule.Domain, "net": rule.Net, "value": rule.Value, "reason": rule.Reason})
          if rule.Kind == "keyword" { keywords = append(keywords, rule.Value) }
        }
        item["company_scope"] = details
        item["company_keywords"] = keywords
      }
    }
    out = append(out, item)
  }
  return out
}
''')
replace('agent/tools.go', 'out["related_tasks"] = t.relatedTaskOverviews()', 'out["related_tasks"] = t.relatedTaskOverviews()\n\tout["scope"] = t.scopeOverview()')
# Reuse the existing isolated-schema fixture, rather than starting an unbounded
# server against the shared default schema for a single HTTP assertion.
p = Path('server/task_templates_test.go')
text = p.read_text()
start = text.index('func TestConversationPatchReturnsPinState(t *testing.T) {')
end = text.index('\n\tconversation, err :=', start)
text = text[:start] + 'func TestConversationPatchReturnsPinState(t *testing.T) {\n\t_, s := newRetestTestServer(t)\n\tm := s.m' + text[end:]
p.write_text(text)

# The Korean fork must never replace itself with an upstream Chinese binary.
replace('selfupdate/github.go', 'const Repo = "Autumn-27/artex"', 'const Repo = "andongmin94/ARTEX"')
for p in Path('selfupdate').glob('*_test.go'):
    p.write_text(p.read_text().replace('Autumn-27/artex', 'andongmin94/ARTEX').replace('Autumn-27/ARTEX', 'andongmin94/ARTEX'))
p = Path('.dockerignore')
text = p.read_text().rstrip() + '\n.env\n.env.*\nconfig.json\nconfig.*.json\n.localization/\n.github/\nsource.tar\n*-test.log\n'
p.write_text(text)
write('.env.example', '''# PostgreSQL 비밀번호는 반드시 설정하세요. 영문·숫자 조합을 권장합니다.
# URI 예약 문자가 있으면 DSN에 맞게 인코딩해야 합니다.
POSTGRES_PASSWORD=
# 모델 Key는 여기서 설정하거나 실행 후 LLM 설정 화면에 입력할 수 있습니다.
ANTHROPIC_API_KEY=
OPENAI_API_KEY=
''')
p = Path('config.example.json')
data = json.loads(p.read_text())
data['_comment'] = 'config.json으로 복사하세요(다른 경로는 ARTEX_CONFIG). database와 skill_dir만 설정하며 LLM 등 나머지는 환경 변수 또는 웹 화면에서 설정합니다.'
data['_comment_database'] = 'PostgreSQL 연결. ARTEX_PG_DSN이 이 파일보다 우선합니다. 개별 필드를 입력하거나 database.dsn에 전체 연결 문자열을 입력하세요.'
data['_comment_database_dsn'] = 'database.dsn이 비어 있지 않으면 개별 연결 필드는 사용하지 않습니다.'
data['_comment_skill_dir'] = '스킬 루트 디렉터리. ARTEX_SKILL_DIR > 이 필드 > 실행 파일 옆 skills/ 순서입니다. 비워두면 기본 경로를 사용합니다.'
data['skill_dir'] = ''
p.write_text(json.dumps(data, ensure_ascii=False, indent=2)+'\n')

write('install.sh', r'''#!/usr/bin/env bash
# 한국어 포크 설치: 로컬 소스 Docker 빌드 또는 프런트엔드를 포함한 직접 빌드.
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"
umask 077
info(){ printf '[*] %s\n' "$*"; }
die(){ printf '[오류] %s\n' "$*" >&2; exit 1; }
ask(){ local value; read -rp "$1 [$2]: " value; printf '%s' "${value:-$2}"; }
require(){ command -v "$1" >/dev/null 2>&1 || die "$1 설치 후 다시 실행하세요."; }
ensure_docker(){ require docker; docker compose version >/dev/null 2>&1 || die 'Docker Compose를 설치하세요.'; }
build_local(){
  require go; require npm
  (cd web && npm ci && npm run build:static)
  mkdir -p server/webui/dist
  cp -R web/out/. server/webui/dist/
  CGO_ENABLED=0 go build -tags embedui -trimpath -o artex ./cmd/artex
}
echo 'ARTEX 한국어판 설치'
echo '  1) Docker로 한국어 소스 빌드 및 실행'
echo '  2) 로컬 빌드 및 실행(Node.js 22, Go, PostgreSQL 필요)'
case "$(ask '설치 방식' 1)" in
  1)
    ensure_docker
    if [ ! -f .env ]; then
      password="$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"
      printf 'POSTGRES_PASSWORD=%s\nANTHROPIC_API_KEY=\nOPENAI_API_KEY=\n' "$password" > .env
      info '로컬 테스트용 DB 비밀번호를 .env에 생성했습니다. 모델 Key는 웹 화면에서 설정하세요.'
    fi
    info '이 포크의 소스로 한국어 이미지를 빌드합니다.'
    docker compose up -d --build
    info '접속: http://localhost:8787 / 로그: docker compose logs -f artex'
    ;;
  2)
    require go; require npm; require node
    if [ ! -f config.json ]; then
      export DB_HOST DB_PORT DB_USER DB_PASS DB_NAME DB_SSL
      DB_HOST="$(ask 'PostgreSQL 주소' 127.0.0.1)"
      DB_PORT="$(ask '포트' 5432)"
      DB_USER="$(ask '계정' artex)"
      read -rsp 'PostgreSQL 비밀번호: ' DB_PASS; printf '\n'
      DB_NAME="$(ask '데이터베이스 이름' artex)"
      DB_SSL="$(ask 'SSL 모드(disable/require)' disable)"
      node - <<'JS'
const fs = require('node:fs');
const env = process.env;
const port = Number(env.DB_PORT);
if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('포트가 유효하지 않습니다');
fs.writeFileSync('config.json', JSON.stringify({database:{host:env.DB_HOST,port,user:env.DB_USER,password:env.DB_PASS,dbname:env.DB_NAME,sslmode:env.DB_SSL}}, null, 2)+'\n', {mode:0o600});
JS
      unset DB_PASS
    fi
    info '프런트엔드와 백엔드를 함께 빌드합니다.'
    build_local
    info '실행합니다. 종료하려면 Ctrl+C를 누르세요.'
    ./start.sh -addr 127.0.0.1:8787 -proxy 127.0.0.1:8788
    ;;
  *) die '1 또는 2를 선택하세요.' ;;
esac
''')
write('update.sh', r'''#!/usr/bin/env bash
# 원본 이미지를 내려받지 않고 현재 한국어 포크의 소스로 업데이트합니다.
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"
die(){ printf '[오류] %s\n' "$*" >&2; exit 1; }
echo '업데이트 전 DB, data/, skills/, 설정 파일을 백업하세요.'
echo '실행 중인 작업을 중지한 뒤 업데이트하세요. DB 구조는 되돌리지 않습니다.'
read -rp '현재 브랜치의 최신 코드를 가져올까요? (y/n) [y]: ' pull
if [ "${pull:-y}" = y ]; then
  git pull --ff-only || die '코드 동기화 실패. 로컬 변경이나 분기 상태를 해결한 뒤 다시 실행하세요.'
fi
echo '  1) Docker 소스 빌드 및 재시작'
echo '  2) 로컬 소스 빌드'
read -rp '업데이트 방식 [1]: ' mode
case "${mode:-1}" in
  1)
    docker compose version >/dev/null 2>&1 || die 'Docker Compose를 설치하세요.'
    [ -f .env ] || die '먼저 install.sh로 설치하거나 .env를 설정하세요.'
    docker compose up -d --build artex
    echo '한국어판 업데이트 완료: http://localhost:8787'
    ;;
  2)
    command -v go >/dev/null 2>&1 || die 'Go를 설치하세요.'
    command -v npm >/dev/null 2>&1 || die 'Node.js 22와 npm을 설치하세요.'
    (cd web && npm ci && npm run build:static)
    mkdir -p server/webui/dist
    cp -R web/out/. server/webui/dist/
    CGO_ENABLED=0 go build -tags embedui -trimpath -o artex ./cmd/artex
    echo '빌드 완료. 실행 중인 ARTEX를 종료하고 start.sh로 다시 시작하세요.'
    ;;
  *) die '1 또는 2를 선택하세요.' ;;
esac
''')
replace('README.md', 'go test -count=1 ./...', 'go test -p 1 -count=1 ./...')

# Keep foreign-language regression fixtures, comments and external protocol inputs.
# Scan only frontend runtime strings/JSX, not source comments or arbitrary data.
write('web/scripts/check-korean.cjs', r'''const fs = require('node:fs');
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
    function visit(node) {
      if ((ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node) || ts.isTemplateHead(node) || ts.isTemplateMiddle(node) || ts.isTemplateTail(node) || ts.isJsxText(node)) && han.test(node.text)) {
        errors.push(`${file}:${source.getLineAndCharacterOfPosition(node.pos).line + 1}: ${node.text.slice(0, 120)}`);
      }
      ts.forEachChild(node, visit);
    }
    visit(source);
  }
}
scan('src');
if (errors.length) { console.error(errors.join('\n')); process.exit(1); }
console.log('한국어 UI 검사 통과: 중국어 런타임 문구 0건');
''')
# Avoid an unused context import after replacing the isolated fixture.
p = Path('server/task_templates_test.go')
text = p.read_text()
if 'context.' not in text:
    p.write_text(text.replace('\n\t"context"',''))
for name in subprocess.check_output(['git', 'diff', '--name-only'], text=True).splitlines():
    if name.endswith('.go'):
        subprocess.run(['gofmt', '-w', name], check=True)
subprocess.run(['gofmt', '-w', 'agent/tools_scope.go'], check=True)
subprocess.run(['bash', '-n', 'install.sh', 'update.sh'], check=True)
print('Korean locale, installation and regression fixes applied')
