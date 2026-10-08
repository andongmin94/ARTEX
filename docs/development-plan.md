# 개발 계획 — 유일한 현재 작업 목록

목표: **기존 ARTEX + Electron + Go + SQLite**, Docker/PG 없는 로컬 데스크톱 앱.
UI 기준: **[andongmin94/neobrutal-ui](https://github.com/andongmin94/neobrutal-ui)**. 기능/화면 흐름을 유지하고 시각·상호작용을 통일한다.
제품 기준 브랜치: `main`. 최초 기준 SHA: `e0354e9a7101ed4f5bb43c9136beec07acafa014`.
최종 갱신: 2026-10-05.

## 현재 상태와 재개 위치

**업무 SQLite 전환은 `work/sqlite-business-store`에서 이어간다.**
이번 시작 시 확인한 main은 `5a214d21c5be33ce8338a45b94e72357b671b1d0`, 작업 브랜치는 `c160d18adceb5545720e8c4fd96b29ea25230421`이다.
작업 전에 두 브랜치의 최신 ref를 모두 확인한다. 진행 중인 브랜치를 무시하고 main에서 동일 구현을 다시 만들지 않는다.
main에는 현재 계획만 갱신하고, 실행 불가능한 전환 코드를 병합하지 않는다.

**기존 전환 기반:** `db/schema.sql`의 58개 업무/관계 테이블(기존 51 + 정규화 7), `db/seed.sql`, 파일 전용 `db.Open/OpenContext`, 원자적 식별자/버전/초기 생성, bounded pool/IMMEDIATE를 구현했다.
이 기반의 SQL 17개와 Go 초기화 제어 6개 × race 10회는 이전에 통과했다. 세부 경계는 `docs/sqlite-business-store.md`를 참고한다.

**이번 M2.2e 구현:** 실제 `db/tools.go`의 seed/명시적 복원/일반 수정/custom CRUD/추가·제거를 `tool_agents` 관계 테이블에 연결했다.
도구 본문과 연결 목록은 같은 짧은 쓰기 트랜잭션에서 갱신한다. 잘못된 에이전트 연결이나 중간 오류는 전부 롤백한다.
조회는 한 SELECT에서 도구와 정렬된 연결 목록을 함께 읽는다. JSON agents 열의 이중 저장이나 N+1 조회를 추가하지 않았다.
`AgentBindingCounts`도 관계 테이블을 집계하며, 사용자 에이전트/도구 삭제는 FK로 연결을 정리한다.

지난 대화의 미반영 MCP 패치는 현재 브랜치의 `server/manager_startup.go` blob `a443a4acaac0284377658ddfd409768118322d25`와 기준 파일이 일치함을 확인한 뒤 적용했다.
`syncBrowserMCPProxy`는 전체 MCP 설정을 다시 저장하지 않고, 읽었던 설정이 그대로일 때에만 args/env를 변경한다. 사용자 편집/삭제와 충돌하면 오류를 반환한다.
기존 ZIP으로 저장소 전체를 덮어쓰지 않았다. UI 미리보기나 이전 패키지의 문서·다른 소스는 가져오지 않았다.

**전체 앱은 아직 SQLite로 실행할 수 없다.** main의 주 DB는 PostgreSQL이고, 전환 브랜치의 NewManager는 아직 기존 DSN을 전달한다.
모델 참조 삭제/바인딩, 작업·자산·증거·보관 SQL, LLM 별도 DDL, server.New 오류 처리, 프록시 bind가 남아 있다.
특히 `server/finding_retests.go:seedFindingRetester` 등 직접 SQL을 쓰는 시작 경로는 이번 도구 저장 메서드 이식과 별도로 정리해야 한다.
오류를 무시하거나 빈 결과로 바꾸어 ready를 내보내지 않는다. M2.2/M2.7 또는 전체 도구 시드 완료로 표시하지 않는다.
Electron과 neobrutal-ui 화면은 아직 없으며 이번 변경에는 UI 소스가 없다.

**즉시 다음 작업:** 같은 작업 브랜치에서 모델 참조 삭제/바인딩의 PG 행 잠금과 retester의 직접 tools.agents SQL을 이식한다.
LLM 기록/사용량의 별도 PG DDL과 관련 startup 호출을 제거하고, 작업·재검증·로그·알림 초기화를 새 스키마에 연결한다.
그 뒤 NewManager/New의 파일 경로·전체 오류 반환·종료 정리와 실제 부팅을 완성한다. 자산/범위, 작업/탐색, 증거/보관까지 M2.7 검증 후 main에 병합한다.
조사 도구나 이미 구현한 연결·설정·도구 fixture를 반복 구현하지 않는다. 이전 및 이번 modernc 미실행 검사는 해당 호출자를 이식하면서 실행한다.

**검증 정책:** 사용자 명시 요청 없이 Actions 실행·재실행·자동 트리거 추가를 하지 않는다. 개발 커밋은 `[skip ci]`를 사용한다.
로컬 제약을 원격 runner/편집 workflow/artifact 업로드로 우회하지 않는다. 릴리스 태그를 만들거나 기존 artifact/cache/사용자 데이터를 삭제하지 않는다.

## M0. 방향과 작업 기준

- [x] PRODUCT/아키텍처/개발 절차/에이전트 읽기 순서 작성.
- [x] UI·Go 재사용, SQLite 단일 지원, PG 자동 데이터 이전 제외 확정.
- [x] 기존 트래픽 SQLite와 PG 업무 저장소/LLM 기록 구분.
- [x] neobrutal-ui 필수 기준, 원본 SHA/API 차이/전 화면 검수 조건 확정.

## M1. 데스크톱 실행 기반 — 이전 구현·검증 완료

- [x] ARTEX_HOME 절대 경로/쓰기 검사, 설정/data/skills 연결. CWD의 다른 DB 설정 사용 금지.
- [x] loopback 기본 주소/동적 포트, 저장소/HTTP 준비 뒤 ready JSON.
- [x] 부모 stdin EOF/오류 종료, cmd 치명적 종료 제거와 manager/HTTP 정리.
- [x] 설정 7개와 HTTP/부모 파이프 6개 독립 테스트, race 반복 검사.
- [x] 당시 Go 1.26.3 전체 명령/빌드, 3개 OS 실행 기반과 실제 Linux readiness/EOF 검사.

Electron, 주 업무 SQLite, 도구 설치/자손 정리, 패키징, server.New 오류 반환은 M1 완료 범위가 아니다.

## M2. 업무 저장소 SQLite 이식

- [x] **M2.1 인벤토리:** Go/SQL 332개와 후보 214개 분류. 기존 51개 테이블/부팅/직접 SQL/트랜잭션/fixture 지도. 후보 수는 수정 수나 동등성 증명이 아니다.
- [ ] **M2.2 실행 가능한 업무 저장 기반:** 전체 SQLite 스키마/시드/쓰기/취소/닫기를 실제 NewManager/New와 연결한다. 미사용 저장소만으로 완료 처리하지 않는다.
- [x] **M2.2a 공통 연결/트래픽:** 기존 main에서 경로/연결별 PRAGMA/WAL/취소/재열기/FTS 검증. 이후 OpenImmediate 변경은 별도 통합 검증 필요.
- [x] **M2.3a 설정/인증 저장:** 실제 메서드와 HTTP fixture의 재열기/동시 설정 단일 승자/fail-closed 검증. 전체 부팅은 아니다.
- [ ] **M2.2b/M2.3b 프롬프트·모델:** 저장 원자성/없는 행·조회 오류 구현. modernc 7개/전체 빌드·PG 회귀 미실행. 참조 삭제/바인딩 이식은 남음.
- [ ] **M2.2c 부팅 오류/설정 일관성:** manager 동기 오류/실패 정리, snapshot/묶음 저장 구현. 독립 Go 11개와 SQL 7개 통과. 통합 10개/전체 빌드 미실행.
- [ ] **M2.2d 업무 스키마/원자 생성:** 58개 테이블/시드/식별자/버전/생성 트랜잭션, bounded pool/IMMEDIATE 구현. 이전 SQL 17개 및 제어 6개 통과. opener 통합 6개/정규식 DB 테스트/전체 앱 미실행.
- [ ] **M2.2e 도구 관계 저장/MCP 편집 보존:** 이번 실제 저장 메서드 이식과 호출자 연결 구현. SQL 21개, Go 제어 5+3개 × race 10회 통과. 실제 업무 opener 기반 도구 8개 및 MCP modernc 4개 미실행. 직접 retester SQL과 전체 startup은 남음.
- [ ] **M2.3 설정·인증 통합:** setup/로그인/모델 저장·복원과 부팅 연결. API/인증 정책, JWT 분리, 모든 생성 오류/정리 검증.
- [ ] **M2.4 자산·범위:** 관계/DSL/net/netip/IPv4·IPv6 경계/기업 귀속·중복 원자성. SQL 범위 fixture만으로 완료 처리하지 않는다.
- [ ] **M2.5 작업·탐색:** 작업/세션/의도/대화/사실/취약점/승인/재검증/사용량/기록, 다중 저장/취소.
- [ ] **M2.6 증거·보관:** 업무 DB/트래픽/본문/보관 연결, 삭제·재검증·증거 동등성. PG JSON 행/배열/시퀀스 복원을 명시적 열·키 매핑으로 교체.
- [ ] **M2.7 통합 전환:** PG 없이 setup → 로컬 모델 fixture → 작업/기록 → 증거 → 종료/재실행. PG 테스트를 SQLite로 옮기고 skip 제거. PG driver/config/서비스/smoke/남은 헬퍼/전환 인벤토리 제거 후 병합.

main의 PG는 SQLite 오류 fallback이 아니다. DB 선택 옵션, SQL 번역기, 구 데이터/보관 변환기, 임시 호환 계층을 만들지 않는다.
사용자 DB·설정·증거·볼륨을 삭제하거나 덮어쓰지 않는다.

## M3. Electron 실제 UI + neobrutal-ui

- [ ] **M3.1 공통 UI:** 최신 원본 토큰/타이포그래피/컨트롤/표/오버레이. primitive와 호출부 함께 교체, 출처/저작권 보존.
- [ ] **M3.2 실제 화면:** 셸/사이드바, 대시보드/대화/작업/자산/증거/트래픽/설정 전체와 빈/로딩/오류/선택/비활성/승인 상태 검수.
- [ ] **M3.3 Electron:** 메인/렌더러 분리, 패키지/lockfile, Go/정적 UI 재사용. Next 서버 추가 금지.
- [ ] 단일 인스턴스/userData/자식 ready/장애 화면/정상 종료/부모 강제 종료.
- [ ] loopback+앱 세션 인증, Origin/Host, 제한된 preload/IPC/CSP/외부 탐색.
- [ ] **M3.4 검수:** 패키지 setup → 설정 → 작업 → 증거 → 재시작. 라이트/다크·1280/1440px·Windows 배율·IME/키보드/축소 모션/긴 텍스트와 스크린샷.

독립 쇼케이스/일부 대시보드로 전체 UI 완료를 표시하지 않는다. DB 검증과 스타일 변경은 별도 단위로 수행한다.

## M4. Docker 도구 환경 대체

- [ ] 셸/PTY/Python/Node/브라우저/MCP/CLI 및 OS별 지원 조사.
- [ ] 필요한 도구만 전용 경로/고정 버전 설치. 출처/해시/라이선스/복구 검증.
- [ ] 미준비 시 실행 차단·neobrutal-ui 안내. 관리된 PATH/작업 디렉터리.
- [ ] 네트워크/작업 공간/권한 정책과 모든 자손 정리를 OS별 검증.

## M5. 신뢰성·배포·구 경로 제거

- [ ] 동시 에이전트+트래픽 부하의 잠금 오류/지연/검색 측정.
- [ ] 정상 종료/kill/디스크 오류/손상 감지와 일관된 DB+증거 백업/복원.
- [ ] Electron 업데이트 일원화. Go 자체 업데이트 API/구 스크립트 제거.
- [ ] Dockerfile/Compose/PG 안내·CI·불필요 의존성 제거. 사용자 데이터 보존.
- [ ] Windows 설치/서명/업데이트 우선, macOS/Linux 실제 지원 검증.
- [ ] README 새 설치 방법. 완료 전 Docker-free/SQLite 완료 표방 금지.
- [ ] neobrutal-ui 누락/구 스타일/중복 토큰/호환 래퍼 전 화면 확인.

## 이전 검증 기록

상세 기록은 main `33301d8dd28f709fa98897eb6e7e58708d24315f` 및 작업 브랜치 `c160d18adceb5545720e8c4fd96b29ea25230421`의 이 문서에 보존되어 있다. 새 코드의 통합 성공 근거가 아니다.

- **M1:** 코드 `4acff4563df22a2094b7adcc8e4fde7d05267a00`, verify #55/run 37213127038의 6개 job 성공. 로컬 Go 1.23.2 설정/HTTP/EOF 13개 × race 20회, 당시 Go 1.26.3 전체 명령/빌드/smoke.
- **M2.1:** 코드 `a99972d4e8ff1eaec333aad05874374ef7c95510`, verify #56/run 37214597987의 7개 성공. artifact 11308130499.
- **M2.2a/M2.3a:** main `81757868533bcb7b4ba15186bfc326d6ffc8f898`, PR HEAD `4460f0f32790bbf4fb14c8d3837e48f3f7875fcc`, 시험 `294840749fd3057aa4ceba7fd3baca54ba7d1f48`. verify #59/run 37218191195의 7개와 sqlite-foundation #4/run 37218191191의 3개 OS 성공. artifact 11309266073: 694 pass/0 fail/1 skip(외부 모델 TestLiveContextReview); 신규 18개 전부 실행. metadata 테스트의 불필요한 작업 실행 수정·검증.
- **M2.2b/M2.3b:** `0545731a1a0f6ab222304df73c9e1cd04df83dc0`, Actions 미사용. 원본 63개 선언 보존, 프롬프트 원자성/모델 오류 처리. Go 제어 8개 사례 × race 20회, SQLite 3.46.1 SQL 13개 성공. modernc 7개/전체/native 미실행.
- **M2.2c:** `33301d8dd28f709fa98897eb6e7e58708d24315f`, Actions 미사용. manager 오류/닫기, snapshot/설정 원자성/게시 순서. 독립 Go 7+4개 × race 20회, SQL 7개 성공, manager 97개 선언 보존. 통합 db 4/server 6 및 이전 modernc 7 미실행. proxy/MCP 전체 원자성, bind, server.New는 미완료.
- **M2.2d:** `c160d18adceb5545720e8c4fd96b29ea25230421`, Actions 미사용. `python scripts/check-sqlite-business.py`의 실제 스키마/시드 SQL 17개 성공. `GO111MODULE=off go test -race -count=10 -timeout 60s db/schema_init.go db/schema_init_control_test.go`의 초기화 제어 6개 성공. opener 통합 6개/정규식/이전 modernc 17개/전체 앱은 미실행. 이 기록을 이번 변경의 회귀 성공으로 대신하지 않는다.

이전 Actions artifact는 당시 14일 보관 설정이다. 재확인을 위해 Actions를 실행하지 않고 go.mod를 낮추지 않는다.

## 이번 검증 기록 — M2.2e

기준은 작업 브랜치 `c160d18adceb5545720e8c4fd96b29ea25230421`이다. Actions/태그/원격 artifact 생성은 없다.

### 구현 범위

- `db/tools.go`: `tool_agents`만 연결 원본으로 사용. JSON TEXT 인자, 중복 없는 정렬된 연결, 한 SELECT 조회, 본문/연결 동일 트랜잭션, 없는/보호된 수정 오류, custom 삭제 보호.
- `db/config.go`: `AgentBindingCounts`의 tools 집계를 관계 SQL로 교체하고 FK 삭제 의미를 반영. 모델 바인딩의 PG 잠금은 이번에 바꾸지 않았다.
- `db/mcp_proxy.go`, `server/manager_startup.go`: 이전 패치 기준 SHA를 현재 파일과 검증한 뒤 조건부 args/env 저장을 실제 호출자에 연결. 다른 필드/동시 편집/삭제를 덮어쓰지 않는다.
- `db/tools_control_test.go`, `db/tools_business_sqlite_test.go`, MCP 제어/SQLite 테스트, `scripts/check-sqlite-tools.py` 추가. 외부 모델/명령/대상 실행은 없다.

### 실행한 로컬 검사

- Linux Go **1.23.2**, 도구 제어 흐름 최상위 **5개 × race 10회** 통과. 실제 tools.go와 `type DB struct{ *sql.DB }`를 격리했으며 driver는 오류 주입용이다. begin/본문/연결/commit 실패, seed 충돌, 입력 검증/JSON TEXT 전달을 확인했다.
- 같은 환경의 MCP 제어 **3개 × race 10회** 통과. 실제 config.go의 타입과 mcp_proxy.go를 함께 컴파일하고 조건부 쓰기/취소/조회/충돌을 검사했다. 모델 오류 상수만 격리 경계에 선언했다.
- Python SQLite **3.46.1**, `check-sqlite-tools.py`의 **21개 전부 통과**. 실제 Go 소스에서 읽은 SQL과 기준 스키마의 선택된 **8개 CREATE TABLE**을 사용했다. 도구 14/MCP 7개: 관계 저장, seed/편집 보존, TEXT, 잘못된 연결·트리거 실패 롤백, 삭제 cascade/보호, WAL 스냅샷, 12 writer+12 reader, MCP 12건 단일 승자/사용자 편집/재열기, FK/integrity 포함.
- 로컬은 전체 clone이 없어 읽은 8개 DDL을 `--schema` 입력으로 전달했다. 이 SQL 검사는 전체 58개 스키마/시드나 Go modernc 검사가 아니다. 저장소에서 아래 명령을 쓰면 실제 schema.sql에서 동일 범위를 추출한다.
- gofmt/구문 검사, 현재 config.go/manager_startup.go 기준 blob SHA 대조 통과. 가져온 이전 패치 전체를 무조건 적용하지 않았다.

### 미실행 범위와 재현

- **실제 업무 Open/full schema/seed를 쓰는 신규 Go 도구 테스트 8개 미실행.** seed/재열기/사용자 수정/복원/custom CRUD/JSON 타입/실패 롤백/보호/관계 집계/agent cascade/단일 연결/동시 snapshot/닫힌 DB를 검사하도록 작성했다.
- MCP Go SQLite fixture **4개도 미실행**. 이전 modernc 미실행 기록도 그대로다.
- Go 1.26.3 전체 빌드/회귀, NewManager/New 부팅, Windows/macOS, Electron/화면/도구/전체 부하/백업 미실행.
- `GOTOOLCHAIN=go1.26.3 go version`에서 proxy.golang.org DNS 연결 거부를 재확인했다. 프로젝트 지정 버전이나 드라이버를 바꾸지 않았고 Actions로 우회하지 않았다.

```sh
python scripts/check-sqlite-tools.py
# 지정 Go와 의존성을 갖춘 로컬 저장소에서 실행할 미검증 범위:
go test -race -count=1 -timeout 120s -run '^(TestTool|TestSQLiteTool|TestMCPProxy|TestSQLiteMCPProxy)' ./db
```

### UI 상태

UI/일렉트론 소스는 이번에 변경하지 않았다. neobrutal-ui는 필수 기준으로 유지한다.
이전 실제 소스 일치 캡처의 web tree는 `d74ddad9a32a64e3a07b15f493207efedc5ade09`, 원본 artifact는 11303654863/11304555731이었다. 그 자료는 mock 데이터를 사용한 이전 캡처이며 새 앱 부팅·새 디자인 검증이 아니다.
이전 로컬 한국어/입력 검사(중국어 런타임 문구 0건, 입력 조합 10/편집기 4)도 이번 전체 UI 회귀 결과로 재사용하지 않는다.
