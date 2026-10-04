---
name: api-recon
description: 승인된 웹사이트의 API 경로·메서드·매개변수와 프런트엔드 라우트·기능 진입점을 조사할 때 사용합니다.
---

# API Recon — 프런트엔드 API 조사

**승인된 범위 안에서** 백엔드 API의 경로·메서드·매개변수·응답, 프런트엔드 라우트, 탭·대화상자·테이블 같은 UI 기능의 호출 지점을 가능한 한 완전하게 식별합니다. 결과와 한계는 한국어로 기록하고 API 경로, 코드, 필드명은 원문 그대로 유지하세요.

## 경계와 금지 사항

이 스킬은 **API와 매개변수 조사 전용**입니다. 취약점 검증이나 침투·이용 단계가 아닙니다.

| 영역 | 허용 | 금지 |
| --- | --- | --- |
| 목표 | path, method, 매개변수, 라우트, UI 호출 지점 식별 | SQLi/XSS/권한 우회/무차별 대입/취약점 fuzz, 패킷 변조 공격, 파괴적 작업 |
| 인증 | Hook + stub/mock으로 클라이언트의 로그인 화면 전환 조건 재현 | 사용자에게 계정·비밀번호 요청, 자격증명 추측, 실제 로그인 폼 제출 |
| 실행 | 자격증명 없이 인터페이스를 후킹하고 mock 응답으로 SPA 화면 렌더링 | 실제 백엔드 세션을 얻어야만 계속할 수 있는 흐름 |

### 자격증명 없는 동적 분석

Phase 3의 기본 흐름은 다음과 같습니다.

1. `preload.js` / `runtime_harvest.js`에서 로그인·권한·메뉴 등 초기화 API를 가로채 stub으로 처리합니다.
2. 조회 API에는 구조가 맞고 성공 코드가 있으며 데이터는 비어 있어도 되는 mock body를 반환합니다.
3. 백엔드가 없거나 401을 반환하더라도 로그인 후 프런트엔드 화면을 렌더링하여 XHR/fetch/WebSocket 호출을 관찰합니다.
4. 빈 데이터, 빈 테이블, 자리표시자 UI는 정상입니다. 실제 데이터를 얻기 위해 로그인이나 취약점 테스트로 전환하지 마세요.

즉, mock으로 라우트와 컴포넌트를 활성화하고 **외부로 나가는 요청만 기록**합니다. 실제 업무 응답보다 프런트엔드가 어떤 인터페이스를 호출하는지가 중요합니다.

### 반드시 지킬 실행 순서

| 금지 | 대신 수행할 작업 |
| --- | --- |
| Phase 1 전 주 번들 `index-*.js`를 grep/curl/Read로 읽어 API를 직접 추출 | OUTDIR에 복사한 `harvest_static.py` 실행 |
| harvest 대신 임시 `extract_apis.py` 작성 | OUTDIR의 harvest를 수정하고 재실행 |
| 같은 grep/명령이 두 번 이상 실패해도 반복 | tool_logs 확인, harvest 수정, reference 참조 등으로 방법 변경 |
| 점검 A/B를 생략하고 `scripts/` 원본을 그대로 실행하여 최종 결과로 사용 | 읽고 OUTDIR에 복사한 뒤 대상별 조정 |
| 실제 비밀번호/OTP/OAuth 인증 | 클라이언트 stub/mock 사용 |
| 실제 데이터를 확보하려고 stub을 생략하고 권한 우회·인젝션 수행 | 조사 범위 안에서 outbound 요청만 기록 |
| 삭제, 민감정보 내보내기, 대량 쓰기 등 되돌릴 수 없는 작업 | coverage 클릭에서도 수행하지 않음 |
| 실행 및 동적 열거 없이 모든 페이지/API를 확보했다고 주장 | 완료 조건을 충족하거나 한계를 명시 |
| 매개변수 호출 행렬과 diff 없이 모든 매개변수를 확인했다고 주장 | Phase 3b 호출 행렬과 Phase 5 diff 수행 |
| 실행 표본 하나만으로 필수/선택 필드 단정 | 여러 표본 비교, 검증 규칙 또는 오류 응답 확인 |

## 조사 모델과 실행 모드

| 계층 | 결과 | 한계 |
| --- | --- | --- |
| 정적 JS 번들 | API 경로, 라우트 초안, 요청 생성 지점의 필드 후보 | HTTP 메서드는 확정할 수 없으며 매개변수는 Phase 1b에서 조사. 동적 URL 조합은 놓칠 수 있음 |
| 실행 중 화면 | 메서드, body, 응답, 동적 URL, WS/SSE, 여러 표본의 차이 | 실제 화면을 렌더링해야 호출되며 표본 하나로 필수 여부를 결정할 수 없음 |

`depth`는 Puppeteer 기반 `runtime_harvest.js`로 재현 가능한 일괄 API 기록을 수행합니다. `coverage`는 브라우저와 `preload.js`로 탭·대화상자·테이블을 조작하여 기능별 범위를 넓힙니다. `both`는 depth 후 coverage를 수행합니다.

경로는 harvest/정규식, 매개변수는 **경로 주변 코드 확인 + UI 바인딩 추적 + 여러 표본 diff + 검증 오류 분석**으로 조사합니다. 모든 상황에 통용되는 매개변수 추출 스크립트가 있다고 가정하지 마세요.

## 완료 조건

다음 항목을 모두 충족해야 조사 완료라고 할 수 있습니다.

- 정적: `api_static.txt`, `routes.txt`, `js/` 생성.
- 실행: depth 또는 coverage 수행. coverage/both에서는 Hook과 동적 열거 루프의 실제 작동 확인.
- 화면: 업무 경로 접근 시 `/login`으로 돌아가지 않음(hash 라우트 포함).
- 매개변수: coverage/both의 호출 행렬과 `param_samples.json`, Phase 5의 `params_merged.json` 생성.
- 깊이: 빈 모듈은 Phase 4의 권한 트리를 재구성하고 다시 실행하여 locale/bootstrap만이 아닌 모듈 API를 확인.
- 전달: Phase 5 산출물 생성 및 발견한 모든 서비스·엔드포인트의 `insert_assets` 등록.

## 스크립트 조정과 사전 점검

`scripts/`는 참조 템플릿입니다. **읽기 → 대상에 맞게 조정 → OUTDIR(예: `recon/`)에 저장 → `CHANGES.md` 기록** 순서로 사용합니다. 맞지 않으면 구조만 참고하여 방법론에 맞게 수정하세요.

**A(정적)**: Phase 0 뒤 첫 harvest/spider 전에 `harvest_static.py` / `spider_mpa.py`를 복사합니다. 대부분은 기본 정규식을 사용할 수 있습니다. manifest나 구문이 다른 경우 endpoint 정규식, webpack/Vite publicPath, MPA 제외 경로 등을 조정합니다. 복사·필요한 조정 후 **즉시 실행**하세요. 그 전에 주 번들을 수동 추출하지 마세요.

**B(실행)**: Phase 2 뒤 depth/coverage 전에 `runtime_harvest.js`, `preload.js`, `config.json`을 조정합니다. Cookie/localStorage 키, 성공 코드, stub, 로그인 경로 정규식, API 접두사, hash/history 라우트를 확인하세요.

SPA는 Phase 0 뒤 다음 탐색용 Bash 호출에서 `python3 OUTDIR/harvest_static.py <URL> OUTDIR`를 실행하고, MPA는 `spider_mpa.py`를 실행합니다. Phase 1 전의 주 번들 curl/grep/Read는 금지합니다. Phase 1b부터 OUTDIR의 다운로드된 JS만 조사하세요.

### 도구 출력 제한

100KB를 넘는 `index-*.js`를 Read/grep으로 컨텍스트에 넣지 마세요. OUTDIR 스크립트로 처리합니다. grep 출력은 `| head -20` 또는 `-m 5`로 제한하고 대화에는 경로 요약만 남깁니다. `wc -l`, `ls | wc -l`로 결과를 확인하고 디렉터리 전체를 읽지 마세요. 초기 정규식 확인은 선택 사항으로, 50KB 이하의 작은 chunk나 HTML에 최대 한 번만 수행합니다. 최종 정적 결과는 harvest를 기준으로 합니다.

## 실행 순서

```text
Phase 0 분류 및 OUTDIR
 → 점검 A → Phase 1 정적 harvest
 → Phase 1b 매개변수 추적
 → Phase 2 인증 관련 세 조건 분석 및 config.json
 → 점검 B → Phase 3 실행 기록 및 매개변수 호출 행렬
 → 필요한 경우 Phase 4 권한 트리 재구성 → Phase 3 재실행
 → Phase 5 결과 통합 및 모든 발견 자산 등록
```

이전 단계를 마치기 전에 다음 단계로 넘어가지 마세요.

## Phase 0 — 분류

진입 HTML을 읽고 OUTDIR을 만듭니다. 스킬 내부 `scripts/`를 수정하지 마세요. 빈 화면 구조, `<div id=app>`, chunk가 있으면 SPA로 Phase 1~5를 수행합니다. SSR, `<form>`이 있고 API 번들이 없는 MPA는 점검 A 후 다음을 실행합니다.

```bash
python3 recon/spider_mpa.py <BASE_URL> <OUTDIR> [--cookie "session=..."] [--max 300] [--depth 5] [--exclude "logout|delete|destroy"]
```

`forms.txt`, `links.txt`, `api_inline.txt`가 생성됩니다. SPA에서 form이 거의 없으면 Phase 1로 전환합니다. 새 자격증명을 요청하거나 실제 로그인하지 마세요.

## Phase 1 — 정적 조사

```bash
python3 recon/harvest_static.py <BASE_URL> <OUTDIR>
wc -l OUTDIR/api_static.txt OUTDIR/routes.txt
ls OUTDIR/js | wc -l
```

HTML script → webpack/Vite manifest → lazy chunk 다운로드 → `js/`, `api_static.txt`, `routes.txt`, `chunkmap.txt` 순서입니다. manifest 대비 chunk 수를 확인하고 404이면 harvest를 고쳐 다시 실행합니다. chunk마다 수동 curl을 반복하지 마세요. API가 지나치게 적으면 OUTDIR의 endpoint 정규식을 수정하고 재실행합니다.

### Phase 1b — 매개변수 추적

중요 API마다 필드명, 전송 위치, 추정 타입, 필수 여부, 예시 값, 근거 수준을 기록합니다.

| 전송 형식 | 확인할 곳 |
| --- | --- |
| REST JSON | 경로 주변의 params/data/body 객체, body와 query |
| GraphQL | variables와 gql 선언의 타입 |
| form | `<form>`, urlencoded, FormData |
| 파일 업로드 | multipart와 FormData.append |
| 경로 매개변수 | `/user/:id`, 라우트, useParams / $route.params |
| 암호화·서명 | sign/data를 만드는 암호화 함수의 입력 |

각 API의 transport를 `query|json|form|graphql|encrypted`로 기록합니다.

```bash
grep -n '"/api/user/list"' OUTDIR/js/*.js | head -20
grep -rhoaE '.{0,120}("/api[^"]+").{0,200}' OUTDIR/js/*.js | head -20
grep -rhoaE '(params|data|body|payload)\s*:\s*\{' OUTDIR/js/*.js | head -20
```

axios의 data/params, 공통 request 인터셉터, 생성된 OpenAPI 메서드, React Query/SWR 및 Vue composable의 매개변수를 추적합니다. yup/zod/rules, Form.Item name, 내장 Swagger도 근거가 될 수 있습니다. `param_candidates.json`에 `{path,fields[],source:"static-callsite",confidence}`를 저장합니다.

`Form field → submit → transform → API payload` 경로를 확인합니다. 테이블 검색의 getFieldsValue, 경로의 `:id/?tab=`, 인터셉터의 tenantId/페이지/sign, select의 options도 검토합니다. DevTools에서 fetch/XHR.send의 상위 호출 스택을 따라 요청 생성 지점을 찾을 수 있습니다.

세 질문에 답하세요. **어디서 조립하는가**, **required/pattern/enum으로 어떻게 검증하는가**, **path/query/body/multipart/header 중 어디로 보내는가**. 필수·선택·조건부 의존성은 정적 추측만으로 단정하지 말고 Phase 3 표본과 Phase 5 오류 분석으로 보완하세요.

## Phase 2 — 인증 관련 세 조건

OUTDIR의 JS에서 출력 제한을 지켜 조사하고 config.json에 반영합니다.

| 조건 | 질문 | 단서 |
| --- | --- | --- |
| 렌더링 | 로그인 상태를 무엇으로 판단하는가? | isLogin, getToken, Cookie/localStorage |
| 인터셉터 | 무엇이 /login 전환을 유발하는가? | response_code, errno, axios interceptor |
| 콘텐츠 | 메뉴와 권한은 어디서 오는가? | menu, permission, role, acl, routes |

localStorage의 키 이름을 자격증명으로 간주하지 마세요. chunk와 요청 흐름으로 확인합니다. 결과를 config.json 및 OUTDIR의 runtime/preload에 반영하고 점검 B를 마칩니다.

선택적으로 `recordDetail:true`, `observe.xhrHeaders:true`, `extractUrlsFromResponse:true`, `observe.storageReads/cookieReads:true`, `neutralizeVueRouter:true`를 사용합니다. coverage 라운드마다 `__API_RECON_LOG__`, `__API_RECON_DETAIL__`, `__API_RECON_ROUTES__`, `__API_RECON_OBSERVE__`를 내보냅니다.

## Phase 3 — 실행 중 조사

config.json의 runtimeMode는 depth, coverage 또는 both입니다. 점검 B를 마치고 자격증명 없는 mock 경계를 준수하세요.

L1은 정확한 인증·권한·bootstrap stub, L2는 JSON의 미로그인 업무 코드를 성공으로 교체, L3는 남은 `/api` 등에 빈 성공 응답을 제공하여 UI 렌더링을 유지합니다. 실제 서버 권한을 우회하는 단계가 아닙니다.

depth는 fake auth, forward, stubs 및 hash/history routes를 사용하여 `runtime_api.json`을 생성합니다. coverage는 document-start에 preload.js를 삽입합니다(CDP addScriptToEvaluateOnNewDocument 또는 Userscript). `window.__API_RECON_PRELOAD__`가 있고 업무 경로에서 로그인 화면으로 되돌아가지 않는지 확인하세요.

```bash
cd recon && npm install
node runtime_harvest.js config.json
```

### Phase 3b — 동적 기능 및 매개변수 행렬

메인 메뉴·사이드바, 탭, 테이블 첫 행의 보기·상세·편집 화면, 필터·새 항목 UI 등을 순회합니다. 각 클릭 후 네트워크를 1~3초 관찰하고 모듈마다 API와 라우트를 병합합니다. 실제 삭제, 대량 쓰기, 민감정보 내보내기는 수행하지 마세요. SPA에서는 미검증 routes.txt 경로에 제한적으로 pushState를 사용할 수 있지만 MPA에는 사용하지 않습니다.

모듈마다 목록 최초 표시, 검색, 고급 필터, 생성·편집 양식, 정렬·일괄 기능을 각각 관찰하여 여러 표본을 비교합니다. 페이지·기본 필터, keyword, optional 필드, entity, ids[]/exportType/sortField의 차이를 기록하되 되돌릴 수 없는 요청은 mock으로 처리합니다.

stub을 사용해도 outbound body/header는 프런트엔드가 실제로 구성한 값입니다. 요청을 근거로 `scan_raw.json`, `param_samples.json`, `api_detail.json`을 저장합니다. Vue는 document-start preload와 neutralizeVueRouter, React는 routes.txt와 사이드바 클릭 및 제한적 pushState를 사용합니다. both는 depth 후 coverage를 수행합니다.

## Phase 4 — 권한 트리 재구성

모듈이 비어 있거나 모든 라우트에서 locale/bootstrap만 호출되면 콘텐츠 조건을 통과하지 못한 상태입니다. 로그인 후 화면에 진입했다고 모듈 열거가 완료된 것은 아닙니다. stub 구조·권한 코드, v-if permission, auth 모듈의 누락 경로를 확인하세요.

```bash
grep -rhoaE '"/api[^"]*(permission|perm|role|menu|acl)[^"]*"' OUTDIR/js/*.js | sort -u | head -30
grep -rhoaE 'userRouteAuth|getResultTree|routeMap|routeLink|menuList|authList' OUTDIR/js/*.js | head -20
python3 recon/extract_route_map.py recon/js recon/
python3 recon/build_perm_tree.py recon/js recon/ --config recon/config.json
```

일반적인 흐름은 flat role_permissions + permissions/all 트리 → getResultTree → userRouteAuth[CODE].url입니다. `route_map.json`, `userRouteAuth.json`, `permissions_tree.json`, `*_stub.json`, `perm_codes_all.txt`를 생성합니다. response_code를 인터셉터 조건과 맞추고 flat codes와 tree를 일치시키며 routes에 route_map의 모든 link를 포함합니다. config.json을 갱신한 뒤 **Phase 3을 다시 수행**합니다. 큰 SPA는 waitUntil, routeTimeout, perRouteMs를 조정할 수 있습니다.

## Phase 5 — 통합 및 보고

| 산출물 | 내용 |
| --- | --- |
| js/, api_static.txt, routes.txt, chunkmap.txt | 정적 번들 및 경로 |
| param_candidates.json | 정적 매개변수 후보 |
| config.json | 세 조건과 실행 설정 |
| runtime_api.json | depth 기록, WS/SSE 포함 |
| param_samples.json, scan_raw.json, api_detail.json | 표본·클릭·상세 기록 |
| route_map.json 등 | 수행한 경우 권한 트리 중간 결과 |
| params_merged.json | 통합 매개변수와 근거 수준 |
| api_merged.txt | METHOD /path [params] [static|runtime|both] |
| site_map.json | 라우트·API·매개변수·기능 및 한계 |
| insert_assets | 발견한 모든 서비스·엔드포인트 등록 |

param_samples.json의 여러 표본을 비교하여 필드를 병합합니다. 범용 병합 스크립트가 있다고 가정하지 마세요. 높은/중간/낮은 근거 수준과 미발생 상태를 구분합니다. 승인 범위 안에서 불완전한 요청의 400 응답을 읽어 required나 enum을 확인할 수 있으나 이는 매개변수 조사이며 취약점 테스트가 아닙니다. data/variables 포장과 암호화 전 bizData를 고려하세요.

보고서에는 runtimeMode, 정적·실행 API 수, 매개변수 근거 수준, 미검증 모듈, CHANGES.md의 조정 내역을 포함합니다. 발견한 자산을 insert_assets에 빠짐없이 등록하세요.

```json
{
  "site": "https://example.com",
  "runtimeMode": "both",
  "appType": "vue-spa",
  "routeGuardStrategy": ["nav-neutralize", "L1-auth", "L2-patch", "forward"],
  "apisFromStatic": [],
  "apisFromRuntime": [],
  "apis": [],
  "params": [{"method":"POST","path":"/api/user/list","transport":"json","fields":[]}],
  "frontendRoutes": [],
  "routesVerifiedByClick": [],
  "featuresTriggered": [],
  "limitations": ""
}
```

## 적용 범위와 한계

webpack/Vite/Angular의 lazy load에도 같은 원칙을 적용합니다. REST/JSON, GraphQL, WebSocket, SSE를 다루며 gRPC-web은 범위 밖입니다. SSR의 클라이언트 fetch는 기록할 수 있지만 RSC/Server Actions 전체를 열거한다고 보장할 수 없습니다. JSVMP, WASM, HMAC/mTLS 등은 정적 결과와 한계를 명시하고, 조건부·숨은 매개변수나 WASM 요청 구성은 미발생/접근 불가로 표시합니다. 실행 중 접근이 막혀도 정적 결과는 보존하세요.

세부 정규식, 대상별 설정 예시, Hook 및 문제 해결 자료는 [reference.md](reference.md)에 있습니다. 필요한 절만 확인하고 전체 내용을 중복해서 컨텍스트에 넣지 마세요.
