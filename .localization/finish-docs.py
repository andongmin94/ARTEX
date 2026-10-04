from pathlib import Path
import re

# Translate prose without rewriting executable examples or external-input patterns.
p = Path('skills/api-recon/reference.md')
lines = p.read_text(encoding='utf-8').splitlines()
if lines[0] == '# api-recon — 参考手册':
    translated = r'''1	# api-recon — 참조 설명서
3	Grep 사용 예, `config.json` 템플릿 및 문제 해결 안내입니다. 모든 grep은 `js/` 디렉터리를 대상으로 실행합니다. 번들이 한 줄이면 `js-beautify` 또는 `sed 's/}/}\n/g'`로 정리할 수 있지만, 보통은 앞뒤 문맥을 포함하는 원본 grep으로 충분합니다.
5	## 스크립트 안내
7	`scripts/`의 모든 파일은 **참조 템플릿**입니다. 실행 전에 대상 사이트에 맞게 조정해야 합니다. 일반적인 조정 항목은 다음과 같습니다.
9	| 스크립트 | 일반적인 조정 항목 |
11	| `harvest_static.py` | 엔드포인트 정규식, webpack/Vite manifest 해석, 마이크로 프런트엔드 publicPath, 재시도/동시 실행 |
12	| `runtime_harvest.js` | neutralize 필드명·성공 값, stub 일치 규칙·body 구조, routes 출처, WS 기록, `waitUntil`/`routeTimeout`/`proxy` |
13	| `preload.js` | `loginPathRe`, L1 stubs, `neutralize.fields`, `apiPattern`, L3 활성화 여부, `recordDetail`, `observe.*`, `neutralizeVueRouter` |
14	| `spider_mpa.py` | 파괴적인 링크의 `--exclude` 설정, cookie, depth/max, 동일 도메인 필터 |
15	| `extract_route_map.py` | `routeMap` / `routeLink` 정규식, KEY 이름 패턴 |
16	| `build_perm_tree.py` | `userRouteAuth` 해석, `ROOTS`/`PREFIX_PARENT` 계층 추정, stub 바깥쪽 필드명 |
17	| `config.json` | 위 사이트별 매개변수를 설정하는 공통 진입점 |
19	조정한 파일은 작업 디렉터리(예: `recon/`)에 저장하고, 참조 스크립트에서 무엇을 변경했는지 보고서에 기록하세요.
23	## A. 세 가지 조건 역분석
25	### A1. 렌더링 조건 — 로그인 여부를 어떻게 판단하는가?
35	`isLogin = f(getUser())` → `getUser = decode(storage.read(KEY))` 경로를 찾아 **저장 키**, **저장 위치**(Cookie 또는 localStorage), **인코딩**을 확인합니다.
37	| 인코딩 | config에서 모의 값을 구성하는 방법 |
39	| 일반 문자열 / `"1"` / token | `"value": "anything-truthy"` |
42	| JWT | 서명 없는 `alg:none` JWT 또는 번들에 있는 키로 서명 |
43	| 암호화(SM2/AES/RSA) | 하드코딩된 키 확인. 렌더링 조건이 디코딩 가능한 blob만 요구하면 모의 값 구성, 그렇지 않으면 정적 조사로 한계 기록 |
45	→ `cookies` / `localStorage`에 설정합니다.
47	### A2. 인터셉터 조건 — 무엇이 /login 이동을 유발하는가?
56	**필드명**, **성공 값**(보통 `0` 또는 `200`), **로그인 이동을 유발하는 실패 값**을 확인합니다. 유효하지 않은 세션 값으로 확인합니다.
62	→ `neutralize.fields` + `neutralize.success`에 설정합니다.
64	### A3. 콘텐츠 조건 — 메뉴와 권한은 어디서 오는가?
73	**두 계층의 데이터**(일반적인 기업 관리 화면):
75	| API | 일반적인 payload | 사용처 |
77	| `.../role_permissions` | `{ permissions: string[], role_type }` | 라우트 가드, 버튼 단위 ACL |
78	| `.../permissions/all` | `tree[{ code, position, children }]` | 사이드바 메뉴 렌더링 |
79	| 번들의 `userRouteAuth` | `{ CODE: { url, name? } }` | code → 프런트엔드 path |
80	| 번들의 `routeMap` | `{ KEY: { name, link } }` | 별칭 해석(webpack `o.DASHBOARD`) |
82	사용처의 코드를 읽어 `getResultTree(tree, permissions)`의 필터 방식과 `v-if` / `hasAuth(code)`가 검사하는 필드를 확인합니다.
84	**직접 모의 값 구성**(작은 사이트): 허용적인 payload를 구성하여 `stubs`에 설정합니다.
86	**전체 권한 트리 복원**(큰 사이트에서 사이드바/하위 모듈이 계속 비어 있는 경우): **I절**을 참고하세요.
90	## B. config.json 템플릿
153	필드 설명:
155	- `cookies[].value` 접두사: `b64json:` → base64(JSON), `json:` → 원본 JSON, 접두사 없음 → 문자열 그대로 사용
156	- `forward: true`는 실제 요청을 전달하고 코드 필드를 수정하며, `false`는 완전한 오프라인 stub입니다.
157	- `mockTier`: coverage 모드 preload의 활성 계층. 예: `L1+L2`, `L1+L2+L3`
158	- `routes`는 `routes.txt`에서 가져오며, 모의 메뉴를 구성하면 실행 도구가 `<a href>`를 자동 추가합니다.
159	- `captureResponses` / `recordWs`는 depth 모드에서만 적용됩니다.
160	- `waitUntil`: 큰 SPA에서는 `networkidle2` 대기 정체를 피하도록 `domcontentloaded`를 사용합니다.
161	- `routeTimeout`: 라우트별 `page.goto` 제한 시간(밀리초)
162	- `proxy`: Puppeteer의 `--proxy-server`. `HTTP_PROXY` / `HTTPS_PROXY`로도 설정할 수 있습니다.
164	### B1. 이중 stub 템플릿(role_permissions + permissions/all)
196	바깥쪽 필드명(`response_code` / `code` / `data`)은 A2 인터셉터 조건과 일치해야 하며, `permissions`는 트리의 모든 말단 code를 포함해야 합니다.
200	## C. coverage 모드: preload 설정
202	`scripts/preload.js` 맨 위 `CONFIG` 객체를 편집하거나 CDP 주입 전에 다음과 같이 교체합니다.
214	  stubs: [ /* config.json의 stubs와 동일 */ ],
219	확인 조건: `window.__API_RECON_PRELOAD__ === true`이고 pathname이 안정적으로 유지되어야 합니다.
221	기록 결과 내보내기:
234	## D. preload / runtime Hook 기능
236	preload(coverage)와 runtime_harvest(depth)에 내장된 브라우저 Hook 기능 및 적용 범위:
238	| Hook 기능 | API 발견에 주는 정보 | 지원 범위 |
240	| fetch / XHR.open Hook | 요청 URL과 메서드 기록 | ✅ `recordDetail` + `__API_RECON_LOG__` |
241	| XHR.setRequestHeader Hook | Authorization 등 헤더 확인 | ✅ `observe.xhrHeaders` |
242	| localStorage/cookie 읽기 Hook | 세션 키 이름 확인 | ⚠️ 선택 설정 `observe.storageReads/cookieReads` |
243	| Vue 라우트 조회 | frontendRoutes 보완 | ✅ `__API_RECON_ROUTES__`(불러온 라우트) |
244	| Vue 라우트 가드 중립화 / 로그인 이동 차단 | 모듈을 렌더링하여 API 호출 유도 | ✅ `neutralizeVueRouter` + 기본 이동 중립화 |
245	| React 라우트 조회 | 라우트 보완 | ⚠️ 정적 조사 + 클릭. 전용 Hook 없음 |
246	| 페이지 이동 차단(로그인 path) | 현재 페이지에 머물며 분석 | ⚠️ 업무 탐색을 막지 않도록 로그인 path만 차단 |
247	| 암호화 라이브러리 Hook(CryptoJS/SM 등) | 암호화 매개변수의 평문 API body 확인 | ❌ 암호화 함수 입력을 직접 Hook해야 함. 결과를 config에 기록 |
248	| 디버깅 방지 처리 | runtime에서 API를 기록할 수 있도록 함 | ❌ 직접 처리 필요. 정적 조사는 계속 가능 |
252	## E. 엔드포인트 추출 정규식(정적 결과가 적을 때)
254	`harvest_static.py`의 `extract_endpoints` 조건을 넓히거나 다음과 같이 직접 확인합니다.
263	## F. 문제 해결
265	| 증상 | 원인과 처리 |
267	| 정적 API가 적음 | 엔드포인트 표현 방식이 맞지 않음 → 정규식 확장(E절) |
268	| chunk 수가 manifest보다 훨씬 적음 | CSS 전용이거나 배포되지 않은 chunk일 수 있음. 404는 이미 재시도됨 |
269	| runtime에서 계속 로그인 화면 표시 | 렌더링 조건 오류 → A1의 키, 저장 위치, 인코딩, domain 재확인 |
270	| 기본 화면은 보이나 모듈이 비어 있음 | 콘텐츠 조건 → 모의 메뉴 구성(A3). `routes` path도 확인 |
271	| 라우트마다 bootstrap/locale만 있음 | 권한 코드 부족 → I절 권한 트리 복원. `role_permissions` + `permissions/all` 이중 stub 확인 |
272	| 사이드바 항목은 있으나 하위 페이지가 비어 있음 | 중간 트리 노드 누락 또는 `userRouteAuth`와 code 불일치 |
273	| 모든 API가 로그인으로 이동 | 인터셉터 조건 → `neutralize` 확인. 중첩 필드는 순회 로직 확장 필요 |
274	| WS 프레임 0건 | 사용자 조작 이후에 구독하는지 확인. `perRouteMs` 증가 |
275	| 응답 본문이 비어 있음 | 실제 응답은 `forward: true`일 때만 제공됨 |
276	| Chromium 없음 | chromium 설치 또는 `config.chromium` / `CHROMIUM` 설정 |
277	| Mock이 많아도 로그인으로 돌아감 | Hook 시점이 늦거나 `location.href` setter 누락 → document-start + preload |
278	| 목록이 모두 비어 있음 | L3 빈 배열은 정상일 수 있음. 탭/설정/상세 조작을 계속 확인 |
279	| Redux action을 라우트로 오인 | get/set/change/clear/toggle/upload를 포함한 내부 path 필터링 |
280	| Vue가 계속 로그인으로 이동 | preload 주입을 document-start로 변경하거나 `neutralizeVueRouter: false`이면 가드를 직접 정리 |
281	| 응답에 URL이 있지만 log에 없음 | `extractUrlsFromResponse` 활성화 또는 `__API_RECON_DETAIL__`에서 직접 추출 |
282	| Authorization 헤더 이름을 모름 | `observe.xhrHeaders` 활성화 또는 DevTools에서 요청 헤더 확인 |
283	| runtime이 느리거나 시간 초과 | `waitUntil: domcontentloaded`, `routeTimeout` 감소. `networkidle2`는 피함 |
284	| 프록시 연결 실패 | `proxy` / 환경 변수 확인. Puppeteer와 curl의 프록시 포트 일치 확인 |
288	## G. 인증이 강화된 대상
290	서버가 세션을 단계마다 검증하는 경우(모의 값을 만들 수 없는 서명 cookie, stub으로 대체할 수 없는 서버 렌더링 메뉴), runtime은 기본 화면에서 더 진행하지 못할 수 있습니다. 예상되는 결과는 다음과 같습니다.
292	- **정적 조사로도 엔드포인트를 열거할 수 있습니다.** 모듈 path는 코드에 있습니다.
293	- 승인 범위가 허용하면 **실제 세션**으로 같은 실행 도구를 사용합니다. `forward: true`, neutralize 없이 실제 methods/params/responses를 기록합니다.
297	## H. 작업별 확인 목록
299	1. 승인 범위를 확인합니다.
300	2. `scripts/harvest_static.py`를 **읽고** 대상에 맞게 조정한 뒤 실행하여 `api_static.txt`, `routes.txt`를 검토합니다.
301	3. **Phase 1b**: path 주변 문맥 및 바인딩 계층 조사 → `param_candidates.json`(J절)
302	4. A1/A2/A3 역분석 → 사이트별 `config.json` 작성
303	5. `runtime_harvest.js` / `preload.js`를 **읽고 조정한 뒤** 실행
304	6. `runtimeMode=depth`: `npm install` → 조정한 harvest 스크립트 실행
305	7. `runtimeMode=coverage/both`: document-start에 조정한 preload 주입 → 브라우저 MCP 동적 열거 및 **매개변수 호출 행렬**
306	8. 모듈이 렌더링되지 않으면 **I절 권한 트리 복원** → stubs 수정 → 재실행
307	9. 여러 매개변수 표본 diff 및 오류 기반 역추적 → `params_merged.json`
308	10. 통합 → `site_map.json` + `api_merged.txt`. 확인 범위, 누락 범위, 스크립트 변경을 정확히 기록
312	## I. 권한 트리 복원(Phase 4 심화)
314	단순한 모의 `menus: [{ path, show: true }]`가 효과가 없고 하위 모듈이 여전히 mount되지 않을 때 사용합니다.
316	### I1. auth 모듈 찾기
324	**권한 API path**, **응답 필드명**, **이를 사용하는 chunk 파일명**을 기록합니다.
326	### I2. routeMap 추출
330	# 결과: recon/route_map.json
333	`[!] no routeMap pattern found`이면 `extract_route_map.py` 정규식을 확장하거나 직접 grep합니다.
339	### I3. 권한 트리와 stub 구성
345	스크립트 동작:
346	1. `userRouteAuth={MONITOR:{url:...},...}` 해석(webpack 별칭 `He=o.DASHBOARD` 포함)
347	2. `route_map.json`으로 alias → 실제 path 해석
348	3. code 접두사로 parent 추정(`MONITOR_ALERT` → `MONITOR`)
349	4. `permissions_tree.json`, `permissions_all_stub.json`, `role_permissions_stub.json` 생성
350	5. `--config` 사용 시 `config.json`의 `stubs`와 확장 `routes`에 자동 기록
352	**대상에 맞게 조정할 항목**(스크립트 상단):
353	- `DEFAULT_ROOTS`: 최상위 모듈 code 목록
354	- `DEFAULT_PREFIX_PARENT`: `PREFIX_` → parent 매핑
355	- `DEFAULT_EXTRA_PARENT`: 접두사 관계가 없는 부모 미지정 노드
357	### I4. stub 일관성 확인
360	# permissions 수는 userRouteAuth 항목 수와 비슷해야 합니다.
362	# routes는 route_map의 모든 link를 포함해야 합니다.
366	### I5. runtime 재실행 및 비교
370	# 구성 전후 runtime_api.json의 항목 수 비교. /attack, /asset 등의 모듈 API 확인
373	| 구성 전 | 구성 후(성공) |
375	| 라우트마다 같은 bootstrap 3~5건 | 라우트마다 서로 다른 모듈 API 호출 |
376	| `/api/locale/language`만 있음 | `/api/web/...` 모듈 엔드포인트 등장 |
377	| `routes.txt`의 라우트가 한 자릿수 | route_map에서 가져온 `routes`가 80~110개 이상 |
379	### I6. 여전히 실패할 때
381	- **coverage 모드**: 사이드바와 탭을 클릭합니다. 조작 후에 권한 검사가 요청될 수 있습니다.
382	- **stub 필드**: 실제 API(curl + 실제 세션)의 응답 중첩 구조와 stub을 비교합니다.
383	- **추가 가드**: `hasPermission|checkRole|func.` 등의 버튼 단위 검사를 grep하고 `role_permissions.permissions`를 확장합니다.
384	- **정적 결과 유지**: 모듈 API path는 `api_static.txt`에 남습니다. runtime은 METHOD/body를 보완하며 매개변수는 `param_candidates.json`과 기록한 표본을 유지합니다.
388	## J. 매개변수 역분석(Phase 1b / 5b / 5c)
390	**범용 스크립트가 아니라 방법론입니다.** path는 정규식으로 찾고, 매개변수는 주변 문맥 + UI 바인딩 경로 + 여러 표본 diff + 오류 역추적으로 확인합니다.
392	### J1. 주변 문맥 확장 — path에서 요청 구성 객체 찾기
395	# Phase 1에서 확인한 path를 기준으로 조사
402	### J2. 래퍼 계층과 전송 형태
405	# axios / 공통 request
416	# 경로 매개변수
421	### J3. 검증 조건 — 필수 여부 / 형식 / 열거 값
430	### J4. 바인딩 계층 — 폼에서 API까지
437	runtime 보완: DevTools → Network → 요청 → **Initiator**(호출 스택)에서 `fetch`/`send`의 상위 요청 구성 함수를 추적합니다.
439	### J5. 암호화된 매개변수
445	**암호문에서 필드를 추측하지 마세요.** 암호화 함수의 **입력**을 Hook하여 암호화 전 평문 payload를 기록하고 결과를 `config.json` / `param_candidates.json`에 저장합니다.
447	### J6. 매개변수 호출 행렬(Phase 3 필수)
449	모듈마다 다음 조작을 한 번씩 기록하고 요청 body/query를 diff합니다.
451	| 조작 | 확인 사항 |
453	| 목록 첫 화면 | 페이지 구분 기본값 |
454	| 검색 | keyword, filters |
455	| 고급 필터 | 선택 필드 |
456	| 생성/편집 | 전체 entity |
457	| 일괄 작업/내보내기 | `ids[]`, `exportType` |
458	| 정렬/페이지 이동 | `sortField`, `order` |
460	`param_samples.json` 결과: `[{ "path", "method", "action": "search", "body", "query", "headers" }]`
462	### J7. 신뢰도 기준
464	| 신뢰도 | 조건 |
466	| **높음** | 정적 호출 지점과 runtime 표본 2개 이상이 일치 |
467	| **중간** | 정적 결과만 있거나 runtime 표본이 1개 |
468	| **낮음** | 응답/오류로 역추적했으며 추가 검증하지 않음 |
469	| **호출 대기** | 정적으로 필드는 확인했지만 UI/권한 때문에 실행하지 못함 |
471	### J8. 상황별 빠른 설정
473	| 상황 | 순서 |
475	| REST 목록 페이지 | J1 요청 객체 → J6 네 가지 diff → J3 rules |
476	| 생성/편집 폼 | J3 Form name → J4 submit 경로 → runtime 제출 및 빈 값에서 400 확인 |
477	| GraphQL | J2 variables 선언 → runtime에서 operation별 variables 기록 |
478	| 암호화 body | J5 입력 Hook → 암호화 전 필드가 실제 params |
480	### J9. api-recon 단계와의 대응
482	| api-recon | 매개변수 조사 |
484	| Phase 1 정적 조사 | J1 주변 문맥 확장 |
485	| Phase 2 A2 인터셉터 | 전역 주입 필드(tenantId, sign) |
486	| Phase 3 runtime | J6 호출 행렬 + `param_samples.json` |
487	| Phase 4 권한 트리 | 모듈마다 폼이 다르므로 충분한 권한 조건을 갖춰야 모든 필드를 호출 |
488	| Phase 5 통합 | `params_merged.json` + 신뢰도. 표본 하나로 필수 여부를 단정하지 않음 |
490	### J10. 문제 해결
492	| 증상 | 처리 |
494	| 정적 필드명이 runtime에 나타나지 않음 | 「호출 대기」로 표시. 권한 트리 보완 / 고급 필터 클릭 / 연결된 select의 각 option 확인 |
495	| 같은 path의 body 형태가 다름 | 정상일 수 있음. `action`별로 기록하고 schema를 강제로 합치지 않음 |
496	| stub 응답이 가짜인데 params를 확인하려 함 | **나가는 요청**의 body/headers를 확인하고 stub 응답에서 역추측하지 않음 |
497	| 400에서 nested field 오류 발생 | 바깥쪽 `data`/`bizData`/`variables` 래퍼 확인 |
498	| GraphQL operation 이름만 보임 | `variables` JSON을 펼치고 정적으로 `$var: Type` 확인 |'''
    for item in translated.splitlines():
        number, value = item.split('\t', 1)
        index = int(number) - 1
        if not re.search('[\u3400-\u9fff]', lines[index]):
            raise RuntimeError(f'Reference source moved at line {number}')
        lines[index] = value
    remaining = [line for line in lines if re.search('[\u3400-\u9fff]', line)]
    if len(remaining) != 1 or not remaining[0].startswith('grep '):
        raise RuntimeError(f'Untranslated reference prose: {remaining}')
    lines.insert(3, '\n> 코드 예제의 중국어 정규식은 외부 사이트의 원문 응답을 찾는 검색 패턴이므로 그대로 유지합니다. 화면 언어나 실행 지침이 아닙니다.\n')
    p.write_text('\n'.join(lines) + '\n', encoding='utf-8')
elif lines[0] != '# api-recon — 참조 설명서':
    raise RuntimeError('Unexpected api-recon reference')

Path('docs/finding-traffic-evidence.md').write_text('''# 취약점의 다중 트래픽 증거

취약점 상세의 「트래픽 연결」에서 여러 페이지의 기록을 선택하고 용도·설명·순서를 지정하거나 연결을 해제할 수 있습니다. 트래픽 화면에서도 여러 기록을 선택하여 기존 취약점 하나에 연결할 수 있습니다. 상속된 취약점 증거는 읽기 전용이며, 수정하려면 소스 작업으로 이동해야 합니다.

「에이전트 트래픽 자동 연결」은 기본적으로 꺼져 있으며 `/api/settings`의 `agent_traffic_binding`으로 읽고 변경합니다. 켜면 요청·응답 확인, 도구 호출, 지침에 따른 Token 사용량이 증가하며 다음 에이전트 실행부터 적용됩니다. 끄면 자동 연결 매개변수·추가 연결 도구·지침이 숨겨지고 실행 중인 세션의 새로운 자동 연결도 거부합니다. 수동 연결, 트래픽 캡처, 기존 증거 조회·내보내기는 영향을 받지 않습니다.

활성화한 경우 기본 흐름은 **취약점 저장 → 보고서 에이전트 자동 실행 → 트래픽 확인 및 연결 → 최신 증거 버전으로 보고서 작성**입니다. 발견 에이전트는 `evidence`에 검증 명령, 주요 출력, 실제 트래픽 ID와 용도를 남깁니다. 보고서 에이전트가 `traffic_search` / `traffic_get`으로 확인하고 `bind_finding_traffic`으로 연결한 뒤 최신 `version`을 읽어 보고서를 저장합니다. 비활성화 상태에서는 보고서 에이전트에 원본 트래픽 검색·조회 도구가 추가되지 않지만, 수동 연결된 스냅샷을 읽어 보고서를 작성할 수 있습니다.

`report_finding`의 `traffic_refs` / `evidence_hint_id`로 즉시 연결할 수도 있습니다. 연결은 선택 사항입니다. TCP 등 HTTP가 아닌 취약점, 캡처하지 않은 경우, 정확한 기록을 찾지 못한 경우에는 참조 없이도 보고할 수 있습니다. 명령 출력·로그 등 다른 검증 가능한 증거와 연결하지 못한 이유를 남기되, 필수 필드를 새로 만들지 않습니다. 제출한 모든 ID가 유효하고 본문이 온전해야 합니다. 한 항목이라도 실패하면 해당 연결 작업을 롤백하며, 보고와 함께 명시적으로 연결하다 실패하면 보고 전체를 롤백합니다. 같은 스냅샷을 다시 추가해도 연결 수나 기존 설명은 바뀌지 않습니다.

## ID와 보고서 버전

`traffic_refs` 배열의 순서가 초기 증거 순서입니다.

```json
{
  "traffic_refs": [
    {"traffic_id": "실제 트래픽 ID", "role": "baseline", "note": "정상 계정 요청"},
    {"traffic_id": "다른 실제 트래픽 ID", "role": "proof", "note": "재현 요청"}
  ]
}
```

용도는 `baseline`(정상 대조군), `proof`(취약점 입증), `verification`(추가 검증), `supporting`(보조 증거, 기본값)입니다. 실제 기록을 먼저 확인하세요. 도메인과 시각은 후보를 찾는 조건일 뿐 작업 소속이나 증거 관련성을 입증하지 않습니다. ID를 추측하거나 패킷을 채우기 위해 불필요한 추가 탐색을 수행하지 마세요.

원본의 지침 참고 자료는 [CyberStrikeAI의 취약점 보고 도구](https://github.com/RuoJi6/CyberStrikeAI/blob/54d56774b8bd285817d16d48b70a4a5e6e0963f7/internal/app/vulnerability_tools.go)입니다. ARTEX는 패킷 연결을 선택 사항으로 유지하며, 패킷이 없다는 이유로 보고를 막지 않습니다.

반환 첫 줄은 `finding recorded: <탐색 노드 ID>`입니다. 뒤의 JSON에 독립 취약점 ID인 `finding_id`, 탐색 노드 ID인 `finding_node_id`, 연결 요약이 포함됩니다. 두 ID는 서로 바꾸어 사용할 수 없습니다.

- `get_finding_traffic(finding_id)`는 **독립 취약점 ID**를 사용하며 정렬된 목록과 `version`을 반환합니다. `binding_id`, `side=request|response`, `offset`, `length`로 본문을 최대 8192바이트씩 읽을 수 있습니다.
- `update_finding_report`의 `finding_id`는 **탐색 노드 ID**입니다. `evidence_version`에 실제 읽은 버전을 넣습니다. 작성 중 증거가 바뀌면 이전 버전의 저장은 거부되므로 다시 읽고 작성해야 합니다.
- 증거 버전을 생략한 보고서가 기존 트래픽 증거를 반영했다고 간주하지 않습니다. 연결·설명·용도·순서가 바뀌면 기존 보고서에 갱신 필요 상태가 표시됩니다.

자동 연결 활성화 시 `add_hint` / `add_task_hint`의 단일 힌트나 `hints` 배열의 각 항목에 `traffic_refs`를 저장할 수 있습니다. 계획 에이전트는 `evidence_hint_id`로 현재 작업의 힌트를 명시적으로 지정하여 대신 보고할 수 있지만 상속된 힌트는 사용할 수 없습니다. 시스템은 도메인·시각·조회 이력으로 자동 추측하지 않으며 제출 실패 시 일부 취약점을 남기거나 보고서를 먼저 실행하지 않습니다.

기존 취약점에 증거만 추가할 때는 `bind_finding_traffic(finding_id, traffic_refs)`를 사용합니다. `list_findings`, `list_task_findings`, `node_detail`, `get_task_node_detail`은 두 ID를 명확히 반환하며 기존 `id`의 탐색 노드 의미는 유지됩니다.

시작 시 도구 schema에 선택 속성을 추가하고 기본 트래픽 도구를 보고서 에이전트에도 연결합니다. 사용자 정의 연결 목록·프롬프트·설명·활성 상태는 유지합니다. 최종 도구 구성 뒤 공통 지침을 추가하여 발견 역할은 증거 인계를, 보고서 역할은 검증·연결·작성을 담당합니다. 작업 컨텍스트가 없는 플랫폼 대화는 구조화된 힌트로 해당 작업에 인계합니다. 완료 판정 전 기존 증거를 인계하되, 없는 패킷을 기다리지는 않습니다. 실패한 `report_finding`은 보고서 에이전트를 실행하지 않습니다.

## HTTP API

기본 경로는 `/api/exploration/findings/{finding_id}/traffic`이며 **독립 취약점 ID**를 사용합니다. 기존 인증과 `context_task`의 작업 가시성·상속 읽기 전용 검사를 적용합니다.

| 메서드 / 상대 경로 | 요청 또는 반환 |
| --- | --- |
| `GET` | 정렬된 요약, 증거 버전, 보고서가 사용한 버전 |
| `POST` | `{"traffic_refs":[...]}` 일괄 추가 |
| `PATCH /{binding_id}` | `{"version":1,"role":"proof","note":"설명"}` |
| `DELETE /{binding_id}` | `{"version":1}` |
| `PUT /order` | `{"version":1,"binding_ids":["2","1"]}`. 전체 목록이어야 함 |
| `GET /{binding_id}` | 스냅샷 메타데이터 및 제한된 본문 미리보기 |
| `GET /{binding_id}/body` | `side`, `offset`, `length`. `download=1`이면 원본 바이트 전체 다운로드 |

버전·순서 집합 충돌 또는 보관 중 쓰기는 `409`, 상속 데이터 쓰기는 `403`, 없거나 해당 취약점 소속이 아닌 연결은 `404`를 반환합니다. 트래픽·첨부 파일 읽기 및 무결성 검증 실패도 명시적인 오류로 반환합니다.

## 저장 구조와 정리

시작 시 PostgreSQL에 `traffic_evidence_snapshots`, `finding_traffic_bindings`, `findings.evidence_version` / `report_evidence_version`을 준비합니다. 버전의 기본값은 0이며 과거 문장을 해석하여 증거를 추측 연결하지 않습니다.

스냅샷에는 원본 트래픽 ID, 수집 시각, URL, 메서드, 상태, 요청·응답 헤더, 본문 길이와 SHA-256이 저장됩니다. 본문은 `<data>/evidence/blobs/<해시 앞 두 자리>/<hash>.bin`에 저장하며 정리 가능한 `data/traffic`과 독립적입니다. 여러 취약점이 스냅샷과 본문을 공유할 수 있습니다. 스냅샷 내용 수정 API는 없으며 해시가 일치하지 않으면 조회·내보내기가 실패합니다.

원본 트래픽의 쓰기 잠금을 유지한 채 큰 blob과 기존 디렉터리 기록까지 포함한 본문 전체를 읽습니다. 파일을 저장·검증한 후 PostgreSQL 트랜잭션 하나로 탐색 노드, 의도 관계, 취약점, 스냅샷과 연결을 저장하고 커밋 후에만 계획 에이전트에 알립니다. 실패하면 참조 없는 파일이 남을 수 있지만 일부 업무 레코드만 저장되는 일은 없습니다.

advisory lock `7337741004`가 증거 파일과 SQL 참조를 조정하고 작업 행 잠금이 보관 대기 이후의 증거 변경을 막습니다. 복원은 본문 설치부터 메타데이터 커밋까지 증거 잠금을 유지합니다. 취약점 삭제 시 해당 연결도 연쇄 삭제됩니다.

정리기는 매시간 실행되며 참조도 없고 진행 중인 작업에서도 사용하지 않는 내용만 최소 24시간 이후 회수합니다. 일반 트래픽 정리는 증거 디렉터리에 영향을 주지 않습니다. 백업할 때는 PostgreSQL과 `data/evidence`를 함께 백업하세요.

## 내보내기와 보관

Markdown에는 정렬된 증거 목록과 버전, JSON에는 메타데이터, CSV에는 개수와 연결 ID가 포함됩니다. `md-zip`에는 취약점 Markdown과 다음 파일이 들어갑니다.

```text
evidence/<finding_id>/<binding_id>/
  manifest.json
  request.http
  response.http
  request.bin
  response.bin
```

Markdown은 상대 링크로 요청·응답을 참조합니다. 다운로드 응답 전에 첨부 복사, 해시 검사, 압축, 디스크 동기화, 모든 ZIP 항목의 CRC 읽기 검증을 완료합니다. 누락·손상 시 다운로드 전체가 실패하며 첨부는 바이너리 원본 바이트를 유지합니다.

보관 v3는 취약점 연결을 기준으로 스냅샷과 본문을 수집하며 원본 트래픽이나 도메인에 의존하지 않습니다. 패키지 검증 후에만 활성 데이터를 정리하고 공유 증거는 유지합니다. 복원은 본문 검증·설치 후 메타데이터와 연결을 트랜잭션으로 복원하며 실패 시 재시도할 수 있습니다. 기존 v1/v2 복원 동작은 유지하고 없는 새 필드는 0으로 채웁니다.

## 검증과 한계

테스트 패키지마다 별도의 새 PostgreSQL 테스트 DB를 `ARTEX_PG_DSN`으로 지정하여 남은 작업·모델 데이터가 백그라운드 실행을 유발하지 않게 하세요. 명시적으로 설정한 DB 연결 실패를 건너뛰기로 숨기지 않습니다.

```sh
go test ./<package> -count=1
go test -race -p 1 ./evidence ./db ./agent ./server -run 'TestEvidence|TestFindingTraffic|TestFindingEvidence|TestReportFindingAtomicContract|TestTaskArchive'
```

프런트엔드는 TypeScript, 수정 파일의 Biome 검사와 정적 빌드를 확인합니다. 현재 개발 서버의 캐시를 덮어쓰지 않도록 독립 디렉터리를 사용하세요. 통합 점검은 별도 포트·임시 데이터 디렉터리·통제된 로컬 HTTP/HTTPS 대상으로 연결 진입점 두 곳, 여러 페이지 선택, 순서·설명 편집, 오류, 상속 읽기 전용, 다운로드, 원본 트래픽 삭제 후 내보내기·보관·복원과 해시를 확인합니다.

현재 전역 증거 잠금을 사용하므로 대량 연결·내보내기 또는 느린 첨부 다운로드 중 다른 증거 작업이 기다릴 수 있습니다. 원본 기록이나 완전한 본문이 없으면 증거를 새로 지어낼 수 없습니다. 이 기능은 캡처 스위치를 변경하지 않으며 HTTPS에서 IP 주소로 직접 접속할 때의 인증서 문제도 해결하지 않습니다.
''', encoding='utf-8')
old = Path('docs/漏洞流量证据.md')
if old.exists():
    old.unlink()

p = Path('README.md')
text = p.read_text(encoding='utf-8')
link = '\n상세 사용법: [다중 트래픽 증거](docs/finding-traffic-evidence.md), [API 조사 참조 설명서](skills/api-recon/reference.md), [별도 질문 /btw](sidequestion/README.md).\n'
if link.strip() not in text:
    text = text.replace('## 에이전트, MCP, 스킬', link + '\n## 에이전트, MCP, 스킬')
p.write_text(text, encoding='utf-8')

# These are upstream historical evidence, not new verification claims.
for name in ['CHANGELOG.md', 'sidequestion/VALIDATION.md', 'sidequestion/CONTEXT_BUDGET.md']:
    p = Path(name)
    text = p.read_text(encoding='utf-8')
    notice = '> 원본 프로젝트의 과거 변경·검증 기록을 보존한 문서입니다. 아래의 날짜와 검증 결과는 이번 한국어판의 검증 결과를 의미하지 않습니다. 한국어판 사용법은 루트 README를 참고하세요.\n\n'
    if not text.startswith(notice):
        p.write_text(notice + text, encoding='utf-8')
print('Korean reference and evidence guide completed; historical evidence preserved')
