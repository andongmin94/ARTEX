# api-recon — 참조 설명서

Grep 사용 예, `config.json` 템플릿 및 문제 해결 안내입니다. 모든 grep은 `js/` 디렉터리를 대상으로 실행합니다. 번들이 한 줄이면 `js-beautify` 또는 `sed 's/}/}\n/g'`로 정리할 수 있지만, 보통은 앞뒤 문맥을 포함하는 원본 grep으로 충분합니다.

> 코드 예제의 중국어 정규식은 외부 사이트의 원문 응답을 찾는 검색 패턴이므로 그대로 유지합니다. 화면 언어나 실행 지침이 아닙니다.


## 스크립트 안내

`scripts/`의 모든 파일은 **참조 템플릿**입니다. 실행 전에 대상 사이트에 맞게 조정해야 합니다. 일반적인 조정 항목은 다음과 같습니다.

| 스크립트 | 일반적인 조정 항목 |
|---|---|
| `harvest_static.py` | 엔드포인트 정규식, webpack/Vite manifest 해석, 마이크로 프런트엔드 publicPath, 재시도/동시 실행 |
| `runtime_harvest.js` | neutralize 필드명·성공 값, stub 일치 규칙·body 구조, routes 출처, WS 기록, `waitUntil`/`routeTimeout`/`proxy` |
| `preload.js` | `loginPathRe`, L1 stubs, `neutralize.fields`, `apiPattern`, L3 활성화 여부, `recordDetail`, `observe.*`, `neutralizeVueRouter` |
| `spider_mpa.py` | 파괴적인 링크의 `--exclude` 설정, cookie, depth/max, 동일 도메인 필터 |
| `extract_route_map.py` | `routeMap` / `routeLink` 정규식, KEY 이름 패턴 |
| `build_perm_tree.py` | `userRouteAuth` 해석, `ROOTS`/`PREFIX_PARENT` 계층 추정, stub 바깥쪽 필드명 |
| `config.json` | 위 사이트별 매개변수를 설정하는 공통 진입점 |

조정한 파일은 작업 디렉터리(예: `recon/`)에 저장하고, 참조 스크립트에서 무엇을 변경했는지 보고서에 기록하세요.

---

## A. 세 가지 조건 역분석

### A1. 렌더링 조건 — 로그인 여부를 어떻게 판단하는가?

```bash
grep -rhoaE '.{0,40}(isLogin|isAuthenticated|loggedIn|hasLogin|requireAuth)\b.{0,80}' js | head
grep -rhoaE 'function (getUser|getToken|getAuth)[0-9]?\([^)]*\)\{.{0,200}' js | head
grep -rhoaE '(localStorage|sessionStorage)\.getItem\("[^"]+"\)' js | sort -u
grep -rhoaE '(Cookies?|cookie)\.(get|load)\("[^"]+"\)' js | sort -u
grep -rhoaE '\batob\(|JSON\.parse\(|jwt|decode' js | head
```

`isLogin = f(getUser())` → `getUser = decode(storage.read(KEY))` 경로를 찾아 **저장 키**, **저장 위치**(Cookie 또는 localStorage), **인코딩**을 확인합니다.

| 인코딩 | config에서 모의 값을 구성하는 방법 |
|---|---|
| 일반 문자열 / `"1"` / token | `"value": "anything-truthy"` |
| `JSON.parse(x)` | `"value": "json:{\"id\":1,\"username\":\"admin\"}"` |
| `JSON.parse(atob(x))` | `"value": "b64json:{\"id\":1,\"username\":\"admin\"}"` |
| JWT | 서명 없는 `alg:none` JWT 또는 번들에 있는 키로 서명 |
| 암호화(SM2/AES/RSA) | 하드코딩된 키 확인. 렌더링 조건이 디코딩 가능한 blob만 요구하면 모의 값 구성, 그렇지 않으면 정적 조사로 한계 기록 |

→ `cookies` / `localStorage`에 설정합니다.

### A2. 인터셉터 조건 — 무엇이 /login 이동을 유발하는가?

```bash
grep -rhoaE '.{0,60}(interceptors\.response|axios|request\.use).{0,120}' js | head
grep -rhoaE '.{0,40}(response_code|errcode|errno|\bcode\b|\bret\b|\bstatus\b)\s*[=!]==?\s*[\-0-9]{1,4}.{0,60}' js | head -20
grep -rhoaE '.{0,40}(未登录|请重新登录|登录已过期|unauthorized|登录失效|授权|token.{0,10}invalid).{0,40}' js | head
grep -rhoaE '.{0,30}(location\.href|router\.(push|replace)|navigate)\([^)]*login[^)]*\)' js | head
```

**필드명**, **성공 값**(보통 `0` 또는 `200`), **로그인 이동을 유발하는 실패 값**을 확인합니다. 유효하지 않은 세션 값으로 확인합니다.

```bash
curl -sk -X POST -H 'Cookie: <fakekey>=junk' https://target/api/<protected> -d '{}' -H 'Content-Type: application/json'
```

→ `neutralize.fields` + `neutralize.success`에 설정합니다.

### A3. 콘텐츠 조건 — 메뉴와 권한은 어디서 오는가?

```bash
grep -rhoaE '"/api[^"]*(permission|perm|role|menu|acl|resource|nav)[^"]*"' js | sort -u
grep -rhoaE '.{0,30}(menus|permissions|menuList|routeList|authList|role_permissions)\b.{0,120}' js | head
grep -rhoaE 'userRouteAuth|getResultTree|routeMap|routeLink|hasPermission|checkAuth' js | head
grep -rhoaE '([A-Z_][A-Z0-9_]*):\{name:"[^"]*",link:"/[^"]+"\}' js | head
```

**두 계층의 데이터**(일반적인 기업 관리 화면):

| API | 일반적인 payload | 사용처 |
|---|---|---|
| `.../role_permissions` | `{ permissions: string[], role_type }` | 라우트 가드, 버튼 단위 ACL |
| `.../permissions/all` | `tree[{ code, position, children }]` | 사이드바 메뉴 렌더링 |
| 번들의 `userRouteAuth` | `{ CODE: { url, name? } }` | code → 프런트엔드 path |
| 번들의 `routeMap` | `{ KEY: { name, link } }` | 별칭 해석(webpack `o.DASHBOARD`) |

사용처의 코드를 읽어 `getResultTree(tree, permissions)`의 필터 방식과 `v-if` / `hasAuth(code)`가 검사하는 필드를 확인합니다.

**직접 모의 값 구성**(작은 사이트): 허용적인 payload를 구성하여 `stubs`에 설정합니다.

**전체 권한 트리 복원**(큰 사이트에서 사이드바/하위 모듈이 계속 비어 있는 경우): **I절**을 참고하세요.

---

## B. config.json 템플릿

```json
{
  "baseUrl": "https://target/",
  "runtimeMode": "both",
  "chromium": "/usr/bin/chromium",

  "cookies": [
    { "name": "auth", "value": "b64json:{\"id\":1,\"username\":\"admin\",\"role\":\"admin\",\"func\":{},\"permissions\":[\"*\"]}" }
  ],
  "localStorage": { "token": "faketoken", "isLogin": "1" },

  "neutralize": {
    "fields": ["response_code", "code", "errno", "ret", "status"],
    "success": 0,
    "flags": { "success": true, "message": "ok" }
  },
  "forward": true,
  "loginUrlPattern": "/login",
  "apiPattern": "/api/|/rest/|/graphql",

  "mockTier": "L1+L2",
  "recordDetail": true,
  "observe": {
    "storageReads": false,
    "cookieReads": false,
    "xhrHeaders": true
  },
  "neutralizeVueRouter": true,
  "stubs": [
    {
      "match": "permissions/all|/menu|role_permissions",
      "body": {
        "response_code": 0, "code": 0,
        "data": {
          "permissions": ["*"],
          "menus": [
            { "name": "dashboard", "path": "/dashboard", "show": true, "children": [] },
            { "name": "alert", "path": "/alert", "show": true, "children": [] }
          ]
        }
      }
    }
  ],

  "explore": {
    "clickTabs": true,
    "clickTables": true,
    "pushStateFallback": true,
    "maxMenuItems": 50
  },

  "routes": ["/dashboard", "/alert", "/asset", "/device", "/report", "/config", "/system"],
  "waitMs": 1500, "perRouteMs": 900, "headless": true,
  "waitUntil": "domcontentloaded",
  "routeTimeout": 12000,
  "proxy": "",

  "captureResponses": true, "recordWs": true, "respMax": 600
}
```

필드 설명:
- `runtimeMode`：`depth`（Puppeteer）、`coverage`（browser MCP）、`both`
- `cookies[].value` 접두사: `b64json:` → base64(JSON), `json:` → 원본 JSON, 접두사 없음 → 문자열 그대로 사용
- `forward: true`는 실제 요청을 전달하고 코드 필드를 수정하며, `false`는 완전한 오프라인 stub입니다.
- `mockTier`: coverage 모드 preload의 활성 계층. 예: `L1+L2`, `L1+L2+L3`
- `routes`는 `routes.txt`에서 가져오며, 모의 메뉴를 구성하면 실행 도구가 `<a href>`를 자동 추가합니다.
- `captureResponses` / `recordWs`는 depth 모드에서만 적용됩니다.
- `waitUntil`: 큰 SPA에서는 `networkidle2` 대기 정체를 피하도록 `domcontentloaded`를 사용합니다.
- `routeTimeout`: 라우트별 `page.goto` 제한 시간(밀리초)
- `proxy`: Puppeteer의 `--proxy-server`. `HTTP_PROXY` / `HTTPS_PROXY`로도 설정할 수 있습니다.

### B1. 이중 stub 템플릿(role_permissions + permissions/all)

```json
"stubs": [
  {
    "match": "role_permissions",
    "body": {
      "response_code": 0,
      "data": {
        "permissions": ["MONITOR", "MONITOR_ALERT", "THREAT", "ASSETS_RISK"],
        "role_type": "SUPER_ADMIN"
      }
    }
  },
  {
    "match": "permissions/all",
    "body": {
      "response_code": 0,
      "data": [
        {
          "code": "MONITOR",
          "position": 1,
          "children": [
            { "code": "MONITOR_ALERT", "position": 1, "children": [] }
          ]
        }
      ]
    }
  }
]
```

바깥쪽 필드명(`response_code` / `code` / `data`)은 A2 인터셉터 조건과 일치해야 하며, `permissions`는 트리의 모든 말단 code를 포함해야 합니다.

---

## C. coverage 모드: preload 설정

`scripts/preload.js` 맨 위 `CONFIG` 객체를 편집하거나 CDP 주입 전에 다음과 같이 교체합니다.

```javascript
const CONFIG = {
  loginPathRe: /\/(login|signin)(\/|$|\?)/i,
  mockTier: 'L1+L2',
  forward: true,
  recordDetail: true,
  extractUrlsFromResponse: true,
  neutralizeVueRouter: true,
  observe: { storageReads: false, cookieReads: false, xhrHeaders: true },
  neutralize: { fields: ['response_code', 'code'], success: 0 },
  stubs: [ /* config.json의 stubs와 동일 */ ],
  apiPattern: /\/(api|apis|v\d+|dev|internal|graphql)\//i,
};
```

확인 조건: `window.__API_RECON_PRELOAD__ === true`이고 pathname이 안정적으로 유지되어야 합니다.

기록 결과 내보내기:

```javascript
JSON.stringify({
  apis: [...window.__API_RECON_LOG__],
  detail: window.__API_RECON_DETAIL__,
  routes: [...(window.__API_RECON_ROUTES__ || [])],
  observe: window.__API_RECON_OBSERVE__,
}, null, 2)
```

---

## D. preload / runtime Hook 기능

preload(coverage)와 runtime_harvest(depth)에 내장된 브라우저 Hook 기능 및 적용 범위:

| Hook 기능 | API 발견에 주는 정보 | 지원 범위 |
|---|---|---|
| fetch / XHR.open Hook | 요청 URL과 메서드 기록 | ✅ `recordDetail` + `__API_RECON_LOG__` |
| XHR.setRequestHeader Hook | Authorization 등 헤더 확인 | ✅ `observe.xhrHeaders` |
| localStorage/cookie 읽기 Hook | 세션 키 이름 확인 | ⚠️ 선택 설정 `observe.storageReads/cookieReads` |
| Vue 라우트 조회 | frontendRoutes 보완 | ✅ `__API_RECON_ROUTES__`(불러온 라우트) |
| Vue 라우트 가드 중립화 / 로그인 이동 차단 | 모듈을 렌더링하여 API 호출 유도 | ✅ `neutralizeVueRouter` + 기본 이동 중립화 |
| React 라우트 조회 | 라우트 보완 | ⚠️ 정적 조사 + 클릭. 전용 Hook 없음 |
| 페이지 이동 차단(로그인 path) | 현재 페이지에 머물며 분석 | ⚠️ 업무 탐색을 막지 않도록 로그인 path만 차단 |
| 암호화 라이브러리 Hook(CryptoJS/SM 등) | 암호화 매개변수의 평문 API body 확인 | ❌ 암호화 함수 입력을 직접 Hook해야 함. 결과를 config에 기록 |
| 디버깅 방지 처리 | runtime에서 API를 기록할 수 있도록 함 | ❌ 직접 처리 필요. 정적 조사는 계속 가능 |

---

## E. 엔드포인트 추출 정규식(정적 결과가 적을 때)

`harvest_static.py`의 `extract_endpoints` 조건을 넓히거나 다음과 같이 직접 확인합니다.

```bash
grep -rhoaE '"/[a-z][A-Za-z0-9_/\-]{3,}"' js | sort -u
grep -rhoaE '/api/[a-zA-Z0-9_./-]+' js | sort -u
```

---

## F. 문제 해결

| 증상 | 원인과 처리 |
|---|---|
| 정적 API가 적음 | 엔드포인트 표현 방식이 맞지 않음 → 정규식 확장(E절) |
| chunk 수가 manifest보다 훨씬 적음 | CSS 전용이거나 배포되지 않은 chunk일 수 있음. 404는 이미 재시도됨 |
| runtime에서 계속 로그인 화면 표시 | 렌더링 조건 오류 → A1의 키, 저장 위치, 인코딩, domain 재확인 |
| 기본 화면은 보이나 모듈이 비어 있음 | 콘텐츠 조건 → 모의 메뉴 구성(A3). `routes` path도 확인 |
| 라우트마다 bootstrap/locale만 있음 | 권한 코드 부족 → I절 권한 트리 복원. `role_permissions` + `permissions/all` 이중 stub 확인 |
| 사이드바 항목은 있으나 하위 페이지가 비어 있음 | 중간 트리 노드 누락 또는 `userRouteAuth`와 code 불일치 |
| 모든 API가 로그인으로 이동 | 인터셉터 조건 → `neutralize` 확인. 중첩 필드는 순회 로직 확장 필요 |
| WS 프레임 0건 | 사용자 조작 이후에 구독하는지 확인. `perRouteMs` 증가 |
| 응답 본문이 비어 있음 | 실제 응답은 `forward: true`일 때만 제공됨 |
| Chromium 없음 | chromium 설치 또는 `config.chromium` / `CHROMIUM` 설정 |
| Mock이 많아도 로그인으로 돌아감 | Hook 시점이 늦거나 `location.href` setter 누락 → document-start + preload |
| 목록이 모두 비어 있음 | L3 빈 배열은 정상일 수 있음. 탭/설정/상세 조작을 계속 확인 |
| Redux action을 라우트로 오인 | get/set/change/clear/toggle/upload를 포함한 내부 path 필터링 |
| Vue가 계속 로그인으로 이동 | preload 주입을 document-start로 변경하거나 `neutralizeVueRouter: false`이면 가드를 직접 정리 |
| 응답에 URL이 있지만 log에 없음 | `extractUrlsFromResponse` 활성화 또는 `__API_RECON_DETAIL__`에서 직접 추출 |
| Authorization 헤더 이름을 모름 | `observe.xhrHeaders` 활성화 또는 DevTools에서 요청 헤더 확인 |
| runtime이 느리거나 시간 초과 | `waitUntil: domcontentloaded`, `routeTimeout` 감소. `networkidle2`는 피함 |
| 프록시 연결 실패 | `proxy` / 환경 변수 확인. Puppeteer와 curl의 프록시 포트 일치 확인 |

---

## G. 인증이 강화된 대상

서버가 세션을 단계마다 검증하는 경우(모의 값을 만들 수 없는 서명 cookie, stub으로 대체할 수 없는 서버 렌더링 메뉴), runtime은 기본 화면에서 더 진행하지 못할 수 있습니다. 예상되는 결과는 다음과 같습니다.

- **정적 조사로도 엔드포인트를 열거할 수 있습니다.** 모듈 path는 코드에 있습니다.
- 승인 범위가 허용하면 **실제 세션**으로 같은 실행 도구를 사용합니다. `forward: true`, neutralize 없이 실제 methods/params/responses를 기록합니다.

---

## H. 작업별 확인 목록

1. 승인 범위를 확인합니다.
2. `scripts/harvest_static.py`를 **읽고** 대상에 맞게 조정한 뒤 실행하여 `api_static.txt`, `routes.txt`를 검토합니다.
3. **Phase 1b**: path 주변 문맥 및 바인딩 계층 조사 → `param_candidates.json`(J절)
4. A1/A2/A3 역분석 → 사이트별 `config.json` 작성
5. `runtime_harvest.js` / `preload.js`를 **읽고 조정한 뒤** 실행
6. `runtimeMode=depth`: `npm install` → 조정한 harvest 스크립트 실행
7. `runtimeMode=coverage/both`: document-start에 조정한 preload 주입 → 브라우저 MCP 동적 열거 및 **매개변수 호출 행렬**
8. 모듈이 렌더링되지 않으면 **I절 권한 트리 복원** → stubs 수정 → 재실행
9. 여러 매개변수 표본 diff 및 오류 기반 역추적 → `params_merged.json`
10. 통합 → `site_map.json` + `api_merged.txt`. 확인 범위, 누락 범위, 스크립트 변경을 정확히 기록

---

## I. 권한 트리 복원(Phase 4 심화)

단순한 모의 `menus: [{ path, show: true }]`가 효과가 없고 하위 모듈이 여전히 mount되지 않을 때 사용합니다.

### I1. auth 모듈 찾기

```bash
grep -l 'userRouteAuth' js/*.js
grep -l 'routeMap\|routeLink' js/*.js
grep -rhoaE 'getResultTree|role_permissions|permissions/all' js | head
```

**권한 API path**, **응답 필드명**, **이를 사용하는 chunk 파일명**을 기록합니다.

### I2. routeMap 추출

```bash
python3 scripts/extract_route_map.py recon/js recon/
# 결과: recon/route_map.json
```

`[!] no routeMap pattern found`이면 `extract_route_map.py` 정규식을 확장하거나 직접 grep합니다.

```bash
grep -rhoaE '([A-Z_][A-Z0-9_]*):\{name:"[^"]*",link:"/[^"]+"\}' js | head -20
```

### I3. 권한 트리와 stub 구성

```bash
python3 scripts/build_perm_tree.py recon/js recon/ --config recon/config.json
```

스크립트 동작:
1. `userRouteAuth={MONITOR:{url:...},...}` 해석(webpack 별칭 `He=o.DASHBOARD` 포함)
2. `route_map.json`으로 alias → 실제 path 해석
3. code 접두사로 parent 추정(`MONITOR_ALERT` → `MONITOR`)
4. `permissions_tree.json`, `permissions_all_stub.json`, `role_permissions_stub.json` 생성
5. `--config` 사용 시 `config.json`의 `stubs`와 확장 `routes`에 자동 기록

**대상에 맞게 조정할 항목**(스크립트 상단):
- `DEFAULT_ROOTS`: 최상위 모듈 code 목록
- `DEFAULT_PREFIX_PARENT`: `PREFIX_` → parent 매핑
- `DEFAULT_EXTRA_PARENT`: 접두사 관계가 없는 부모 미지정 노드

### I4. stub 일관성 확인

```bash
# permissions 수는 userRouteAuth 항목 수와 비슷해야 합니다.
wc -l recon/perm_codes_all.txt
# routes는 route_map의 모든 link를 포함해야 합니다.
python3 -c "import json; m=json.load(open('recon/route_map.json')); r=set(json.load(open('recon/config.json'))['routes']); print('missing', [v['link'] for v in m.values() if v['link'] not in r])"
```

### I5. runtime 재실행 및 비교

```bash
node recon/runtime_harvest.js recon/config.json
# 구성 전후 runtime_api.json의 항목 수 비교. /attack, /asset 등의 모듈 API 확인
```

| 구성 전 | 구성 후(성공) |
|---|---|
| 라우트마다 같은 bootstrap 3~5건 | 라우트마다 서로 다른 모듈 API 호출 |
| `/api/locale/language`만 있음 | `/api/web/...` 모듈 엔드포인트 등장 |
| `routes.txt`의 라우트가 한 자릿수 | route_map에서 가져온 `routes`가 80~110개 이상 |

### I6. 여전히 실패할 때

- **coverage 모드**: 사이드바와 탭을 클릭합니다. 조작 후에 권한 검사가 요청될 수 있습니다.
- **stub 필드**: 실제 API(curl + 실제 세션)의 응답 중첩 구조와 stub을 비교합니다.
- **추가 가드**: `hasPermission|checkRole|func.` 등의 버튼 단위 검사를 grep하고 `role_permissions.permissions`를 확장합니다.
- **정적 결과 유지**: 모듈 API path는 `api_static.txt`에 남습니다. runtime은 METHOD/body를 보완하며 매개변수는 `param_candidates.json`과 기록한 표본을 유지합니다.

---

## J. 매개변수 역분석(Phase 1b / 5b / 5c)

**범용 스크립트가 아니라 방법론입니다.** path는 정규식으로 찾고, 매개변수는 주변 문맥 + UI 바인딩 경로 + 여러 표본 diff + 오류 역추적으로 확인합니다.

### J1. 주변 문맥 확장 — path에서 요청 구성 객체 찾기

```bash
# Phase 1에서 확인한 path를 기준으로 조사
grep -n '"/api/user/list"' js/*.js
grep -rhoaE '.{0,120}("/api[^"]+").{0,200}' js | head
grep -rhoaE '(params|data|body|payload)\s*:\s*\{' js | head
grep -rhoaE '(get|post|put|delete|patch)\([^,]+,\s*\{' js | head
```

### J2. 래퍼 계층과 전송 형태

```bash
# axios / 공통 request
grep -rhoaE '(axios|request)\.(get|post|put|delete|patch)\(' js | head
grep -rhoaE 'interceptors\.(request|response)' js | head

# GraphQL
grep -rhoaE '(query|mutation)\s+\w+|gql`|graphql\(' js | head
grep -rhoaE '\$[a-zA-Z_]+\s*:\s*(Int|String|Boolean|\[)' js | head

# FormData / multipart
grep -rhoaE 'FormData|\.append\(' js | head

# 경로 매개변수
grep -rhoaE 'path:\s*"/[^"]*:[^"]+"' js | head
grep -rhoaE 'useParams|route\.params|\$route\.params' js | head
```

### J3. 검증 조건 — 필수 여부 / 형식 / 열거 값

```bash
grep -rhoaE '(required|message|pattern|enum|validator)\s*:' js | head
grep -rhoaE 'yup\.|zod\.|async-validator|Form\.Item|a-form-item|el-form-item' js | head
grep -rhoaE 'rules\s*:\s*\[|name:\s*["\'][a-zA-Z_]+["\']' js | head
grep -rhoaE 'label.*value|options\s*:\s*\[' js | head
```

### J4. 바인딩 계층 — 폼에서 API까지

```bash
grep -rhoaE 'onFinish|handleSubmit|getFieldsValue|validateFields' js | head
grep -rhoaE '(pick|omit|transform|dayjs|moment)\(' js | head
```

runtime 보완: DevTools → Network → 요청 → **Initiator**(호출 스택)에서 `fetch`/`send`의 상위 요청 구성 함수를 추적합니다.

### J5. 암호화된 매개변수

```bash
grep -rhoaE 'encrypt|decrypt|sign|CryptoJS|sm2|sm3|sm4|RSA|AES' js | head
```

**암호문에서 필드를 추측하지 마세요.** 암호화 함수의 **입력**을 Hook하여 암호화 전 평문 payload를 기록하고 결과를 `config.json` / `param_candidates.json`에 저장합니다.

### J6. 매개변수 호출 행렬(Phase 3 필수)

모듈마다 다음 조작을 한 번씩 기록하고 요청 body/query를 diff합니다.

| 조작 | 확인 사항 |
|---|---|
| 목록 첫 화면 | 페이지 구분 기본값 |
| 검색 | keyword, filters |
| 고급 필터 | 선택 필드 |
| 생성/편집 | 전체 entity |
| 일괄 작업/내보내기 | `ids[]`, `exportType` |
| 정렬/페이지 이동 | `sortField`, `order` |

`param_samples.json` 결과: `[{ "path", "method", "action": "search", "body", "query", "headers" }]`

### J7. 신뢰도 기준

| 신뢰도 | 조건 |
|---|---|
| **높음** | 정적 호출 지점과 runtime 표본 2개 이상이 일치 |
| **중간** | 정적 결과만 있거나 runtime 표본이 1개 |
| **낮음** | 응답/오류로 역추적했으며 추가 검증하지 않음 |
| **호출 대기** | 정적으로 필드는 확인했지만 UI/권한 때문에 실행하지 못함 |

### J8. 상황별 빠른 설정

| 상황 | 순서 |
|---|---|
| REST 목록 페이지 | J1 요청 객체 → J6 네 가지 diff → J3 rules |
| 생성/편집 폼 | J3 Form name → J4 submit 경로 → runtime 제출 및 빈 값에서 400 확인 |
| GraphQL | J2 variables 선언 → runtime에서 operation별 variables 기록 |
| 암호화 body | J5 입력 Hook → 암호화 전 필드가 실제 params |

### J9. api-recon 단계와의 대응

| api-recon | 매개변수 조사 |
|---|---|
| Phase 1 정적 조사 | J1 주변 문맥 확장 |
| Phase 2 A2 인터셉터 | 전역 주입 필드(tenantId, sign) |
| Phase 3 runtime | J6 호출 행렬 + `param_samples.json` |
| Phase 4 권한 트리 | 모듈마다 폼이 다르므로 충분한 권한 조건을 갖춰야 모든 필드를 호출 |
| Phase 5 통합 | `params_merged.json` + 신뢰도. 표본 하나로 필수 여부를 단정하지 않음 |

### J10. 문제 해결

| 증상 | 처리 |
|---|---|
| 정적 필드명이 runtime에 나타나지 않음 | 「호출 대기」로 표시. 권한 트리 보완 / 고급 필터 클릭 / 연결된 select의 각 option 확인 |
| 같은 path의 body 형태가 다름 | 정상일 수 있음. `action`별로 기록하고 schema를 강제로 합치지 않음 |
| stub 응답이 가짜인데 params를 확인하려 함 | **나가는 요청**의 body/headers를 확인하고 stub 응답에서 역추측하지 않음 |
| 400에서 nested field 오류 발생 | 바깥쪽 `data`/`bizData`/`variables` 래퍼 확인 |
| GraphQL operation 이름만 보임 | `variables` JSON을 펼치고 정적으로 `$var: Type` 확인 |

---
