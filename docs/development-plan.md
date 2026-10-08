# 개발 계획 — 유일한 현재 작업 목록

목표: **기존 ARTEX + Electron + Go + SQLite**, Docker/PG 없는 로컬 데스크톱 앱.
UI 기준: **[andongmin94/neobrutal-ui](https://github.com/andongmin94/neobrutal-ui)**. 기존 기능·한국어·화면 흐름을 유지하고 시각·상호작용을 통일한다.
제품 기준: `main`. 전환 코드: `work/sqlite-business-store`. 최종 갱신: 2026-10-06.

## 현재 상태와 재개 위치

작업 전에 main과 전환 브랜치의 최신 ref 및 로컬 변경을 확인한다.
이번 시작 기준은 main `d1bb7cf6b5dc8360bddd9f742df88602d04b4762`, 전환 브랜치 `71d346588d68dcf65899be44a819c0690049bf29`다.
전환 브랜치를 무시하고 main에서 이식을 다시 만들거나 옛 ZIP으로 덮어쓰지 않는다.
미완성 전환 코드는 main에 병합하지 않는다. main에는 현재 계획만 갱신한다.

**이번 M2.2h — LLM 기록·사용량의 중복 DDL 제거와 저장·조회 이식:**

- `db/commands.go`의 `llmRecordsSchema`/`llmRecordsMigrate`/`EnsureLLMRecordsTable`, `db/llm_usage.go`의 `llmUsageSchema`/`EnsureLLMUsageTable`을 제거했다. 테이블과 인덱스의 생성 원본은 업무 `schema.sql`과 `db.Open`이다. no-op 호환 함수나 대체 DDL을 남기지 않는다.
- 실제 `server/manager_startup.go`의 두 생성 호출을 제거했다. core/side-question/archive/recorder 테스트의 호출도 함께 제거했다. 구 PG 열 추가 테스트는 삭제하고 실제 SQLite 원문 재열기·필수 테이블 누락 거부 검사로 대체했다. 관련 없는 기존 보관·부가 질문의 검증 본문은 그대로다.
- 기록 검색과 도구 기록 검색의 ILIKE를 SQLite LIKE로, 명시적 인자 번호를 `?N`으로 바꿨다. 입력은 계속 바인딩한다. `%`/`_` 와일드카드 및 명시적 역슬래시 escape를 유지한다. ASCII 대소문자 검색과 한국어를 검사했다. SQLite 기본 LIKE는 비ASCII 문자의 로캘별 대소문자 접기를 제공하지 않으며, PG ILIKE의 모든 언어 동등성을 검증했다고 주장하지 않는다.
- UTC 날짜별 사용량/승인 판단 모델 사용량의 `to_char`/`AT TIME ZONE`/`interval`을 `date`/`strftime`으로 이식했다. 실시간 사용량의 기간 조건은 기존 rolling-day 의미를 유지한다. 보관 통계 생성/복원 전체나 live/cold 경계의 정밀도·동시 스냅샷은 이번 완료 범위가 아니다.
- 도구 기록 목록/집계 조인에 exploration_id를 추가했다. 서로 다른 작업이 같은 tool_use_id를 사용하면 결과와 오류 수가 섞이던 문제를 SQL에서 재현하고 분리했다. 같은 작업 안의 중복 기록 정리 정책은 변경하지 않았다.
- nil 기록/사용량 입력은 오류다. 목록 순회 실패에서 부분 결과를 반환하지 않는다. 사용량의 보관 집계 호출 전에 live 조회 결과를 닫는다. 기록 삭제는 정확한 task_id에 한정하고 사용량 원장을 삭제하지 않는다.
- `server/llmrec_raw_test.go`는 별도 Manager/PG가 아니라 실제 SQLite 업무 opener와 로컬 HTTP fixture로 Recorder → provider → 원문 저장을 검사하도록 바꿨다. 원문 관련 기존 assertion은 유지하고 DB 연결 실패 skip은 제거했다. 아직 실행하지 않았으므로 이 경로 성공을 주장하지 않는다.

**전체 앱의 SQLite 부팅은 아직 미완료다.** main은 PostgreSQL이며 전환 브랜치의 NewManager는 아직 기존 DSN을 파일 전용 Open에 전달한다.
작업 모델 체인/조회, 자산/범위, 탐색/재검증/증거/보관, 로그/알림/부가 질문 복원 등 PG SQL이 남아 있다.
`server.New`의 JWT fatal 및 일부 시작 오류 무시, proxy 비동기 bind도 남아 있다. 오류를 무시하거나 빈 결과로 바꾸어 ready를 내보내지 않는다.
`coordinateWithSchemaMigration`은 보관/복원에 아직 호출자가 있어 이번에 제거하지 않았다. SQLite opener가 사용하는 fallback이 아니며 해당 호출자와 함께 없앤다.
UI/Electron 소스는 이번에 변경하지 않았다. mock 미리보기나 예전 캡처를 새 디자인/앱 실행 증거로 쓰지 않는다.

**즉시 다음 작업:** 로컬 지정 Go/의존성이 준비되면 이번 실제 SQLite 테스트 8개와 Recorder 기존 테스트, 이전 미실행 modernc 검사부터 실행한다. 실패를 건너뛰지 않는다.
같은 전환 브랜치에서 작업 모델 체인 및 실제 시작 경로의 재검증 복구·로그·알림·부가 질문·작업 복원 쿼리를 이식한다.
NewManager/New의 파일 경로·생성 오류 반환·호출자 정리·프록시 bind를 실제 부팅 검사와 함께 완성한다.
자산/범위·탐색·증거/보관까지 M2.7 검증 후 main에 병합한다. 끝난 연결/설정/도구/모델 참조/재검증 초기화/LLM 중복 DDL 제거를 다시 구현하지 않는다.

## 검증 자원 정책

사용자 명시 요청 없이 Actions 실행/재실행/자동 트리거 추가, 임시 편집 workflow, 원격 artifact 업로드를 하지 않는다.
`verify`/`sqlite-foundation`은 수동 전용이다. 개발 커밋에는 `[skip ci]`를 넣고 릴리스 태그는 만들지 않는다.
로컬 제약을 원격 runner로 우회하거나 프로젝트 Go 버전/드라이버를 바꾸지 않는다.
사용자 DB/설정/증거/볼륨, 기존 artifact/cache와 미커밋 변경을 임의 삭제하지 않는다.
SQL 검사·격리 드라이버·문법 검사를 실제 modernc/전체 앱 검증으로 표시하지 않는다.

## M0/M1 — 이전 완료 범위

- [x] 제품/설계/작업 지침, SQLite 단일 지원, PG 자동 데이터 이전 제외, neobrutal-ui 필수 기준.
- [x] ARTEX_HOME 절대 경로/쓰기 검사, config/data/skills 연결, 다른 CWD 설정의 암묵적 재사용 금지.
- [x] loopback/동적 포트/ready JSON, 부모 stdin EOF 종료, cmd 치명적 종료 제거 및 정리.
- [x] 당시 설정/HTTP 테스트와 실제 backend smoke, 3개 OS 실행 기반 검증.

Electron, 전체 업무 SQLite, 도구/자손 프로세스, server.New 오류 반환, 패키징은 M1 완료가 아니다.

## M2. 업무 저장소 SQLite 이식

- [x] **M2.1 인벤토리:** 기존 51개 업무 테이블/시작/직접 SQL/트랜잭션/fixture 지도. `docs/sqlite-porting-map.md` 참고.
- [ ] **M2.2 실행 가능한 저장 기반:** 전체 스키마/시드/쓰기/취소/닫기를 실제 NewManager/New에 연결.
- [x] **M2.2a 공통 연결/트래픽:** 이전 main의 경로/연결별 PRAGMA/WAL/취소/재열기/FTS 검사. 이후 OpenImmediate 통합은 별도.
- [x] **M2.3a 설정/인증 저장:** 실제 메서드/HTTP fixture의 재열기/동시 설정 단일 승자/fail-closed. 전체 부팅은 아니다.
- [ ] **M2.2b/M2.3b 프롬프트·모델:** 원자 저장/조회·수정 오류 구현. 기존 modernc 7개/전체 빌드 미실행.
- [ ] **M2.2c 부팅/설정:** manager 오류/정리, snapshot/묶음 저장 구현. 독립 Go 11/SQL 7 통과, 통합 10 미실행.
- [ ] **M2.2d 스키마/원자 생성:** 58개 테이블/시드/식별자/버전/IMMEDIATE. SQL 17/제어 6 통과, opener 통합 6/전체 앱 미실행.
- [ ] **M2.2e 도구 관계/MCP:** 실제 CRUD/관계/집계와 조건부 MCP 저장. SQL 21/제어 5+3 통과, opener 도구 8/MCP 4 미실행.
- [ ] **M2.2f 모델 참조/대화:** 실제 이식, SQL 15/삭제 제어 통과. 업무 opener 기반 7/전체 회귀 미실행.
- [ ] **M2.2g 재검증 초기화:** 단일 트랜잭션/올바른 FK 순서/수정·삭제 보존. SQL 10/제어 3+하위22 통과. 업무 opener 6/전체 시작 미실행.
- [ ] **M2.2h LLM 기록/사용량:** 이번 DDL/호출부 제거, 검색/UTC 집계/작업별 조인/원장 보존 구현. SQL 14, Go 제어 최상위3+하위18 × race10 통과. 새 실제 opener 테스트8와 Recorder 통합/전체 빌드 미실행.
- [ ] **M2.3 설정·인증 통합:** setup/로그인/모델 저장·복원과 부팅 연결. JWT 분리, 모든 생성 오류/정리와 기존 인증 정책.
- [ ] **M2.4 자산·범위:** 관계/DSL/net/netip, IPv4·IPv6 경계/기업 귀속/중복 원자성.
- [ ] **M2.5 작업·탐색:** 작업/세션/의도/대화/사실/취약점/승인/재검증/사용량/기록, 다중 저장/취소.
- [ ] **M2.6 증거·보관:** DB/트래픽/본문/보관 연결과 동등성. PG JSON 행/배열/시퀀스 복원을 명시적 열/키 매핑으로 교체.
- [ ] **M2.7 통합:** PG 없이 setup → 로컬 모델 fixture → 작업/기록 → 증거 → 종료/재실행. 남은 PG fixture/skip/driver/config/서비스/smoke/헬퍼/전환 도구 제거 후 병합.

main의 PG는 SQLite 오류 fallback이 아니다. DB 선택 옵션, SQL 번역기, 구 데이터/보관 변환기, 임시 호환 계층을 만들지 않는다.

## M3. Electron 실제 UI + neobrutal-ui

UI 작업은 `docs/ui-design.md`와 최신 원본을 먼저 읽는다.

- [ ] 공통 토큰/타이포그래피/컨트롤/표/오버레이와 호출부를 함께 적용하고 출처/저작권 보존.
- [ ] 셸/사이드바부터 대시보드/대화/작업/자산/증거/트래픽/설정 전체 및 빈/로딩/오류/선택/비활성/승인 상태.
- [ ] Electron 메인/렌더러 분리, 패키지/lockfile, Go/정적 UI 재사용. Next 서버 추가 금지.
- [ ] 단일 인스턴스/userData/ready/장애/종료/부모 강제 종료, loopback·앱 세션 인증·Origin/Host·제한된 IPC/CSP/탐색.
- [ ] 실제 패키지 사용 흐름, 라이트/다크/1280·1440px/Windows 배율/IME/키보드/축소 모션/긴 텍스트 및 실제 화면 검수.

일부 화면/독립 쇼케이스/빌드 통과만으로 전체 UI 완료를 표시하지 않는다.

## M4/M5. 도구·신뢰성·배포

- [ ] 셸/PTY/Python/Node/브라우저/MCP/CLI의 OS별 지원, 전용 경로의 버전/출처/해시/라이선스/복구 및 미준비 실행 차단.
- [ ] 네트워크/작업 공간/권한/자손 정리, 다중 기록 부하, kill/디스크 오류/손상과 일관된 DB+증거 백업/복원.
- [ ] Electron 업데이트 일원화. 기존 Go 업데이트/시작 스크립트·Docker/PG 안내/불필요 의존성 제거. 사용자 데이터는 삭제하지 않는다.
- [ ] Windows 우선 설치/서명/업데이트, macOS/Linux 실제 지원 확인, README 교체, neobrutal-ui 누락/구 스타일 최종 검사.

## 이번 로컬 검증 — M2.2h

기준 전환 커밋: `71d346588d68dcf65899be44a819c0690049bf29`.
환경: Linux Go 1.23.2, Python SQLite 3.46.1. 지정 Go 1.26.3/modernc와 전체 clone을 확보하지 못했다.
원본 소스는 live GitHub에서 읽고 Git blob SHA를 대조했다. manager_startup.go는 로컬 보관 파일과 live blob `bba69f34d0963cfb1efc9b290a9400c2891d9aa2`의 일치를 확인한 후 사용했으며, 옛 패치/문서/미리보기를 일괄 적용하지 않았다.

실행 결과:

- `check-sqlite-llm.py`: **14개 통과**. 실제 Go raw SQL/동적 쿼리 조각과 원본 schema.sql에서 읽은 변경 없는 5개 테이블(llm_records/llm_usage/explorations/exploration_nodes/activity)을 사용했다. 부분 checkout에서는 `--schema`로 이 선택 DDL을 전달했다. 전체 58개 스키마/시드 검사가 아니다.
- 원문/정규화 본문과 한글·NUL 재열기, 필터 조합/페이지/ASCII 대소문자/한국어/escape/SQL 주입 문자열, 정확한 작업 삭제와 사용량 보존, 작업 선택 목록, 실패 호출의 사용량, UTC 날짜와 기간/판단 모델 전체·최근 집계를 확인했다.
- 다른 exploration의 동일 tool_use_id로 이전 조인의 2행 혼합을 재현하고 새 조인의 1행 분리를 확인했다. 결과 미도착 행도 유지한다. 쓰기 실패의 기존 기록 보존, WAL 읽기, 24개 동시 사용량 저장, FK/integrity 확인을 포함한다.
- 실제 새 commands.go/llm_usage.go와 `llm_storage_control_test.go`를 DB 래핑 타입 및 명시적 미지원 archive/Open 경계와 격리해 Go `-race -count=10 -timeout=60s`로 실행했다. **최상위 3개 + 하위 18개, 각 10회 통과**, 실패/skip 없음. SQL 드라이버는 오류 주입용이다. 바인딩/원문 전달/nil·쓰기 오류/부분 읽기 오류를 검사하며 실제 DB 엔진을 대신하지 않는다.
- 첫 제어 검사에서 모델 통계 fixture의 열 개수를 잘못 지정한 테스트 오류를 수정한 뒤 다시 통과했다. 실제 SELECT/Scan의 6열 계약을 바꾸어 테스트에 맞추지 않았다.
- 새 SQLite 테스트 8개의 본문도 같은 격리 경계에서 컴파일했지만 **실행하지 않았다**. 격리 Open은 명시적으로 오류만 반환하며 저장소 대용으로 사용하지 않는다.
- gofmt/구문 및 원본 SHA/변경 파일 대조를 수행했다. 테스트의 중복 생성 호출 제거 외 기존 보관/부가 질문 assertion은 유지한다.

미실행: 신규 실제 Open/전체 schema/seed/modernc 테스트 8개, 변경한 Recorder→로컬 HTTP→SQLite 테스트, 이전 modernc, 전체 Go 빌드/회귀와 NewManager/New/HTTP 부팅, native OS/Electron/UI/도구/전체 부하/백업.
목록의 total과 page는 기존처럼 별도 SELECT이며 동일 시점 보장을 새로 구현하지 않았다. 보관 통계 생성/복원과 실시간/보관 간 동시 일관성을 이 검사로 검증했다고 해석하지 않는다.

```sh
# Python 표준 라이브러리로 선택 범위의 실제 SQL 검사
python scripts/check-sqlite-llm.py
# 지정 Go/의존성이 있는 로컬 저장소에서 실행할 실제 드라이버 검사
go test -race -count=1 -timeout=120s -run '^(TestLLMStorageControl|TestSQLiteLLMStorage)' ./db
go test -race -count=1 -timeout=120s -run '^TestRecorderPersistsRawWireBodies$' ./server
```

## 이전 기록

M2.2g 및 이전 상세 기록은 `71d346588d68dcf65899be44a819c0690049bf29`의 이 문서, 그 이전 커밋과 `docs/sqlite-business-store.md`에 보존되어 있다.
과거 CI 성공을 새 전환 코드의 통합 성공으로 재사용하지 않는다. 이전 modernc 미실행 기록은 여전히 유효하며 확인을 위해 Actions를 실행하지 않는다.
