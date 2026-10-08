# 개발 계획 — 유일한 현재 작업 목록

목표: **기존 ARTEX + Electron + Go + SQLite**, Docker/PG 없는 로컬 데스크톱 앱.
UI 기준: **[andongmin94/neobrutal-ui](https://github.com/andongmin94/neobrutal-ui)**. 기능/화면 흐름은 유지하고 시각·상호작용은 이 기준으로 통일한다.
기준 브랜치: `main`. 최초 기준 SHA: `e0354e9a7101ed4f5bb43c9136beec07acafa014`.
최종 갱신: 2026-10-05.

## 현재 상태

**M0/M1, M2.1, M2.2a 공통 SQLite 연결/트래픽, M2.3a 설정/인증 저장 검증을 완료했다.**
그 뒤 모델/프롬프트 저장과 이번 부팅/설정 묶음 저장을 구현했다. 새 단위는 로컬 부분 검사만 수행했으며 전체 통합 완료가 아니다.

**이번 단위 — 기준 main `0545731a1a0f6ab222304df73c9e1cd04df83dc0`:**
`NewManager`의 저장소 열기와 서비스 초기화를 분리했다. LLM 기록/사용량 저장소 생성, 설정 조회, 요청한 트래픽 저장소 열기, browser MCP 동기화 실패를 반환하고 열린 자원을 정리한다.
설정은 한 SELECT의 snapshot으로 복원한다. 웹 검색 설정과 동시 작업 제한은 관련 필드를 한 트랜잭션으로 저장한다. 웹 검색 설정은 커밋 성공 뒤에만 메모리에 게시하며 병렬 저장의 게시 순서를 직렬화한다.
단순히 새 helper를 추가한 것이 아니라 기존 `NewManager`/`SetWebSearch`/`SetConcurrency`가 실제로 사용한다.

**주 업무 DB는 여전히 PostgreSQL이다.** `NewManager`의 opener는 아직 `pgdb.DSN/Open`이며 전체 SQLite 스키마/시드/업무 쿼리 이식은 남아 있다.
`server.New`의 JWT `log.Fatalf`, 모델 조회 오류 무시, 다른 startup 오류 경로도 남아 있다.
트래픽 소켓 바인딩은 여전히 `Traffic.Start`의 비동기 경로다. 이번 변경을 프록시 준비 완료 보장이나 전체 앱 SQLite 부팅으로 표시하지 않는다.
Electron 앱과 neobrutal-ui 적용 화면은 아직 없다. README의 Docker/PG 안내는 현재 제품에 해당한다.

**검증 정책:** Actions 실행·재실행 없이 로컬 검사로 진행한다.
`verify`/`sqlite-foundation`은 수동 실행 전용이며 명시 요청 없이 실행하지 않는다. 개발 커밋은 `[skip ci]`를 사용한다.
로컬 제약을 원격 runner/편집 workflow/artifact 업로드로 우회하지 않는다. 기존 artifact/cache/사용자 데이터는 임의 삭제하지 않는다.
이번에는 workflow와 릴리스 태그를 변경하지 않는다.

**즉시 다음 작업:** M2.2/M2.3의 실제 업무 SQLite 스키마·시드·쓰기 연결 정책과 전체 부팅/모델 저장 연결을 이어간다.
`docs/sqlite-porting-map.md`의 51개 업무 테이블/startup 의존을 기준으로 한다. 조사 도구나 완료된 경로 fixture를 반복 구현하지 않는다.
새 Go 통합 테스트와 이전 모델/프롬프트 7개 테스트는 의존성을 갖춘 로컬 환경에서 실행한다. 미실행 상태를 지우려고 CI를 켜지 않는다.
전체 부팅 전환에서 `server.New`의 오류 반환과 모든 호출부/종료 정리, 프록시 바인딩 실패 전파를 함께 완성한다.

## M0. 방향과 작업 기준

- [x] PRODUCT/아키텍처/개발 절차/에이전트 읽기 순서 작성.
- [x] UI·Go 재사용, SQLite 단일 지원, PG 자동 데이터 이전 제외 확정.
- [x] 기존 트래픽 SQLite와 PG 업무 저장소/LLM 기록 구분.
- [x] neobrutal-ui 필수 UI 기준 및 `docs/ui-design.md`의 원본 SHA/API 차이/전체 화면 검수 조건 확정.

## M1. 데스크톱 실행 기반 — 이전 구현·검증 완료

- [x] `ARTEX_HOME` 절대 경로/쓰기 검사, 설정/data/skills 루트 연결. 홈에 설정이 없어도 CWD의 다른 DB 설정을 사용하지 않음.
- [x] loopback 기본 주소와 동적 실제 포트, 저장소/HTTP 준비 뒤 ready JSON.
- [x] 부모 stdin EOF/오류 시 종료, cmd 시작/서빙의 치명적 종료 제거, manager/HTTP 정리.
- [x] 설정 7개 및 HTTP/부모 파이프 6개 독립 테스트와 race 반복 검사.
- [x] Go 1.26.3 전체 테스트 명령/빌드, 3개 OS 실행 기반, 실제 Linux readiness/EOF 종료 검사.

Electron 창, 주 DB SQLite, 도구 설치/자손 프로세스 정리, 패키징, `server.New` 생성 오류 반환은 M1 완료 범위가 아니다.

## M2. 업무 저장소 SQLite 이식

작업 재개 시 최신 main과 로컬 검증 기록을 확인한다. Actions는 사용자 명시 요청 없이 실행하지 않는다.

- [x] **M2.1 인벤토리:** Go/SQL 332개 조사, 후보 214개 분류. 기본 49 + LLM 기록/사용량 2개, 51개 업무 테이블과 실제 부팅/직접 SQL/트랜잭션/fixture 지도 확정. 후보 수는 수정 파일 수나 동등성 증명이 아니다.
- [ ] **M2.2 실행 가능한 업무 저장 기반:** 전체 SQLite 스키마/시드/쓰기 경로/취소/닫기 구현. `NewManager/New`의 부팅 의존과 오류 반환을 함께 연결. 미사용 저장소를 완료 결과로 제출하지 않음.
- [x] **M2.2a 공통 연결/트래픽:** `internal/sqlitedb.Open`을 실제 `traffic.Open`에 연결. URI 특수문자/연결별 PRAGMA/WAL/취소/파일 보존/재열기/FTS 검증. 전체 업무 스키마와 writer/read pool은 남음.
- [x] **M2.3a 설정/인증 저장:** 실제 설정 메서드와 HTTP fixture의 재열기, 동시 최초 설정/변경 단일 승자, 조회 오류 fail-closed 검증. 전체 앱 부팅은 아님.
- [ ] **M2.2b/M2.3b 프롬프트·모델 저장:** 원자적 초기 프롬프트/연속 버전, 모델 수정·조회 오류 처리 구현. 로컬 부분 검증만 완료. modernc Go 테스트 7개 및 기존 PG 회귀/전체 빌드 미실행. 모델 참조 삭제/바인딩 전체 이식 완료가 아님.
- [ ] **M2.2c 부팅 오류/설정 일관성:** 실제 manager 초기화의 동기 오류 전파/실패 정리, snapshot 복원과 설정 묶음 저장 구현. 로컬 독립 Go 11개 및 SQL 수준 검사 7개 통과. 신규 프로젝트 통합 테스트 10개/전체 빌드 미실행. 아래 검증 기록 참조.
- [ ] **M2.3 설정·인증 통합:** setup/로그인/설정/모델 프로필 저장·복원을 실제 앱 부팅에 연결. API/인증 정책 유지. JWT 작업 공간 분리와 초기화 실패 전파 검사.
- [ ] **M2.4 자산·범위:** 관계 정규화/자산 DSL/IP·CIDR·IPv6 검색/기업 범위 재계산/중복 처리의 실제 fixture 검증.
- [ ] **M2.5 작업·탐색:** 작업/세션/의도/대화/사실/취약점/승인/재검증/사용량/LLM 기록 이식. 다중 에이전트 저장과 취소 검증.
- [ ] **M2.6 증거·보관:** 업무 DB와 트래픽 인덱스/본문/보관 연결, 삭제/재검증/증거 보존 동등성 검증.
- [ ] **M2.7 통합 전환:** PG 없이 setup → 로컬 모델 fixture → 작업/기록 저장 → 증거 조회 → 종료/재실행 검사. 기존 PG 테스트를 SQLite fixture로 옮기고 skip하지 않음. PG driver/config/schema/서비스/smoke 조건과 전환 전용 인벤토리 도구/job 제거.

전체 앱을 깨뜨리는 이식은 기능 브랜치에서 완성한다. main의 기존 PG는 새 앱의 병행 지원이나 SQLite 실패 fallback이 아니다.
새 DB 선택 옵션, SQL 번역기, PG 데이터 자동 이전기를 만들지 않는다.

## M3. Electron 실제 UI 연결 + neobrutal-ui 적용

시각 기준과 화면 범위는 `docs/ui-design.md`를 따른다. 독립 쇼케이스나 대시보드 일부만으로 전체 UI 완료를 표시하지 않는다.

- [ ] **M3.1 공통 UI:** 원본 최신 ref의 토큰/타이포그래피/버튼/입력/탭/카드/표/오버레이 적용. primitive 변경 시 호출부 동시 교체, 출처/저작권 보존.
- [ ] **M3.2 실제 화면:** 셸/사이드바부터 대시보드·대화·작업·자산·증거·트래픽·설정 전체 적용. 빈/로딩/오류/선택/비활성/승인 상태와 라우트별 실제 검수 기록.
- [ ] **M3.3 Electron:** 메인/렌더러 분리, 패키지/lockfile, 기존 Go/정적 UI 재사용. Next 서버 추가 금지.
- [ ] 단일 인스턴스/userData 전달/자식 ready 검증/장애 UI/정상 종료/부모 강제 종료 시험.
- [ ] loopback+앱 세션 인증, Origin/Host, 제한된 preload/IPC/CSP/외부 탐색 보안.
- [ ] **M3.4 검수:** 패키지 앱의 setup → 설정 → 작업 → 기록/증거 → 재시작. 라이트/다크, 1280/1440px, Windows 배율, IME/키보드/축소 모션/긴 텍스트 및 실제 스크린샷 확인.

DB 동등성 검증과 UI 스타일 변경은 별도 단위로 수행한다.

## M4. Docker가 제공하던 도구 환경 대체

- [ ] 셸/PTY/Python/Node/브라우저/MCP/CLI 의존과 OS별 지원표 조사.
- [ ] 필요한 도구만 앱 전용 경로에 버전 고정 설치. 출처/해시/라이선스/실패 복구 검증.
- [ ] 필수 도구 미준비 시 실행 차단·neobrutal-ui 안내. Go에 관리된 PATH/작업 디렉터리 전달.
- [ ] 네트워크/작업 공간/권한 정책, 모든 자손 프로세스 정리를 OS별 검증.

## M5. 데이터 신뢰성·배포·구 경로 제거

- [ ] 동시 에이전트+트래픽 실제 부하에서 잠금 오류/지연/검색 응답 측정.
- [ ] 정상 종료/kill/디스크 오류/손상 감지, 일관된 DB+증거 백업/복원.
- [ ] Electron으로 업데이트 일원화. 기존 Go 업데이트 API/시작/업데이트 스크립트 제거.
- [ ] Dockerfile/Compose/PG 안내·CI·불필요 의존성 제거. 사용자 데이터/볼륨은 삭제하지 않음.
- [ ] Windows 우선 설치/서명/업데이트, macOS/Linux 실제 지원 검증.
- [ ] 새 설치 방법으로 README 교체. 완성 전 Docker-free/SQLite 전환 완료를 표방하지 않음.
- [ ] neobrutal-ui 적용 누락/구 스타일/중복 토큰/호환 래퍼가 없는지 전 화면 확인.

## 검증 기록 — 이전 코드

### M1/M2.1

- M1 로컬 Linux Go 1.23.2에서 설정/HTTP/EOF 13개를 각각 race 20회 검사. 프로젝트 Go 버전은 낮추지 않음.
- M1 코드 `4acff4563df22a2094b7adcc8e4fde7d05267a00`, [verify #55](https://github.com/andongmin94/ARTEX/actions/runs/37213127038): 6개 job 성공. 전체 Go 명령/빌드와 실제 백엔드 smoke 포함.
- M2.1 코드 `a99972d4e8ff1eaec333aad05874374ef7c95510`, [verify #56](https://github.com/andongmin94/ARTEX/actions/runs/37214597987): 7개 job 성공. 인벤토리 artifact `11308130499`.

### M2.2a/M2.3a

- main 구현 `81757868533bcb7b4ba15186bfc326d6ffc8f898`, PR HEAD `4460f0f32790bbf4fb14c8d3837e48f3f7875fcc`, 시험 checkout `294840749fd3057aa4ceba7fd3baca54ba7d1f48`.
- [verify #59](https://github.com/andongmin94/ARTEX/actions/runs/37218191195): 7개 job 성공. [sqlite-foundation #4](https://github.com/andongmin94/ARTEX/actions/runs/37218191191): PG 없이 3개 OS 성공, skip 금지 조건 통과.
- artifact `11309266073` 원본 JSON: Go 최상위 **694 pass / 0 fail / 1 skip**. skip은 외부 개인 모델 설정이 필요한 기존 `TestLiveContextReview`.
- 신규 18개 모두 실행: 연결 9, 설정 4, 인증 HTTP 4, 트래픽 재열기/한글 FTS 1.
- Go 1.26.3 전체 테스트/빌드, 실제 백엔드 readiness/EOF, 프런트 한국어/IME/타입/정적 빌드 성공.
- #58 실패 artifact `11308149113`에서 기존 metadata 테스트가 전체 서버로 다른 작업을 복원·실행하던 문제를 확인해 수정. #59에서 해당 두 HTTP 테스트 통과.
- 연결마다 FK/busy_timeout/synchronous 적용 및 WAL 확인. 손상 데이터 삭제/우회 없음. 인증 bcrypt는 쓰기 구간 밖, 최초 INSERT/조건부 UPDATE 및 조회 실패 전파 적용.
- 위 CI artifact는 당시 14일 보관 설정이다. 새 코드를 검증하기 위해 재실행하지 않는다.

### M2.2b/M2.3b — 모델/프롬프트

- 구현 `0545731a1a0f6ab222304df73c9e1cd04df83dc0`, 기준 `4d0deedcfaf1041538416fcd5fd652a1218bbeb5`. Actions 미사용.
- `SeedPromptIfEmpty`/`SavePrompt`를 단일 트랜잭션으로 변경. 사용자 편집 보존과 버전/현재 포인터 동시 저장, 오류 반환. 모델 없는 ID 수정/조회 오류 처리 개선.
- `db/config.go`의 모델/프롬프트를 `llm_profiles.go`/`prompts.go`로 분리. 원본 63개 선언 누락/중복 없음, 메서드 6개 변경과 helper 2개 추가.
- Go 1.23.2의 실제 prompts 소스 + 동일 DB 래핑 타입 + scripted driver로 트랜잭션 제어 8개 사례 × race 20회 통과.
- Python SQLite 3.46.1의 실제 SQL 수준 13개 검사 통과. 동시 seed/편집/활성화 각 12개, 재열기/키 보존/교체/없는 ID/풀 순서/롤백/무결성 포함.
- `verify`/`sqlite-foundation` trigger만 수동으로 변경. 기존 검사 내용 보존.
- `config_sqlite_test.go`의 modernc Go 통합 테스트 **7개 미실행**. Go 1.26.3 전체 빌드/기존 PG 회귀/전체 앱/Windows/macOS도 미실행.
- 당시 DNS 제한으로 소스 clone/의존성 확보 불가. `go.mod` 하향이나 원격 runner 우회 없음. 부분 검사 성공은 통합 성공이 아니다.

## 검증 기록 — M2.2c 부팅 오류/설정 일관성

기준 main `0545731a1a0f6ab222304df73c9e1cd04df83dc0`. Actions 실행/재실행/업로드 없음.

### 실제 구현 경계

- `db/settings_batch.go`: 단일 SELECT `SettingsSnapshot`, 정렬된 키를 한 트랜잭션에 쓰는 `SetSettingsContext`. 조회/Scan/iteration/close/취소 오류에서 부분 map을 반환하지 않는다.
- `db/settings.go`: 단건·묶음 쓰기가 같은 명시적 upsert SQL을 사용한다. DB 종류 분기나 SQL 변환은 없다.
- `server/manager_startup.go`: 기존 `NewManager`가 저장소를 연 뒤 실제 `newManagerFromDB`로 초기화한다. 실패 시 소유한 DB/트래픽 자원을 닫고 원인과 정리 오류를 함께 반환한다. 미사용/가짜 setup 서버가 아니다.
- `server/manager_startup_state.go`: 기존 설정 키와 부팅 상태 해석, 절대 데이터 경로 준비, browser MCP JSON 검증. 기존 사용자 파일을 삭제/덮어쓰지 않는다.
- 깨진 JSON을 빈 배열/객체로 바꿔 저장하지 않는다. MCP의 다른 옵션은 보존하며 proxy/CA 옵션만 동기화한다. 프록시 인증정보/설정값을 새 오류·로그에 넣지 않는다.
- `server/manager.go`: `SetWebSearch`/`SetConcurrency`의 관련 설정을 원자적으로 저장. 웹 검색은 DB 성공 뒤에만 메모리를 갱신하고 동시 요청의 commit/publication 순서를 고정한다. MCP 동기화 오류는 기존 두 setter에서 반환한다.
- **범위 제한:** `SetTrafficEnabled`/`SetGlobalProxy`의 설정 저장과 MCP 저장 전체를 하나의 트랜잭션으로 만든 것은 아니다. 프록시 비동기 bind, server.New, 전체 스키마와 작업 복원 검증은 남아 있다.

### 실행한 로컬 검사

환경: Linux, Go 1.23.2, Python SQLite 3.46.1. 프로젝트 지정 버전은 변경하지 않았다.

- `manager_startup_state.go`와 해당 테스트를 직접 지정해 `GO111MODULE=off go test -race -count=20 -timeout 60s -v ...` 실행. **최상위 7개 테스트 각각 20회 통과**, skip 없음. 기본/저장값 해석, 손상 불리언, 한글/특수문자 경로, 기존 파일 보존, MCP 옵션 보존/멱등성/잘못된 JSON 및 null 값을 검사했다.
- 실제 `settings.go`/`settings_batch.go`/`settings_batch_control_test.go`를 동일한 `type DB struct{ *sql.DB }`와 격리해 같은 Go/race/count=20으로 실행. **최상위 4개 테스트 각각 20회 통과**, skip 없음. begin/중간 쓰기/commit 실패, 안정된 키 순서, 롤백, 빈 변경/취소, query/Scan/iteration/rows-close 실패를 검사했다. 이 driver는 제어 흐름용이며 modernc가 아니다.
- Python SQLite에서 새 소스의 upsert/SELECT를 그대로 실행. **SQL 수준 7개 검사 통과:** 빈 snapshot, 한글 파일 재열기, 뒤 필드 실패 시 앞 필드 롤백, WAL 이전/커밋 snapshot, 12개 동시 writer + 12개 reader, FK/integrity 확인. Go 드라이버 검사와 구분한다.
- `gofmt`, 모든 새 Go 파일 구문 검사, manager 선언의 AST/토큰 대조. 원본 **97개 선언 모두 보존**, 중복 없음. Manager 타입/기존 함수 7개를 변경하고 상태 타입 1개와 함수 4개를 추가했으며, 나머지 선언은 동일하다.

### 미실행 검사와 로컬 재현 명령

- 신규 `db/settings_batch_sqlite_test.go` **4개**, `server/manager_startup_sqlite_test.go` **6개**의 프로젝트 통합 테스트는 **미실행**이다. 미완성 업무 SQLite의 부팅 거부/실패 후 DB 닫기, 실제 setter 저장·롤백·동시 게시·옵션 보존 등을 검사하도록 추가했다.
- 이전 모델/프롬프트 modernc **7개도 여전히 미실행**이다.
- Go 1.26.3 전체 빌드/PG 회귀, 실제 backend smoke, native Windows/macOS, Electron/화면/도구/전체 부하·백업은 미실행.
- 로컬 DNS/의존성 제약으로 전체 소스 clone과 지정 Go/modernc 확보가 불가능했다. Actions를 우회 수단으로 쓰지 않았다.

의존성을 갖춘 로컬 저장소에서는 다음 명령으로 새 범위를 실행한다. 운영 DB/외부 모델/테스트 대상은 필요하지 않다.

```sh
go test -race -count=1 -timeout 120s -run '^(TestSettingsBatch|TestSettingsSnapshot|TestSQLiteSettingsBatch)' ./db
go test -race -count=1 -timeout 120s -run '^(TestManagerRuntimeSettings|TestManagerDirectory|TestBrowserProxySettings|TestManagerStartup|TestManagerWebSearch|TestManagerConcurrencySettings)' ./server
```

M2.2c와 M2.2/M2.3 전체의 통합 완료 체크박스는 미완료로 유지한다. 새 검사 성공을 이전 커밋의 CI 결과로 대신하지 않는다.
