# 개발 계획 — 유일한 현재 작업 목록

목표: **기존 ARTEX + Electron + Go + SQLite**, Docker/PG 없는 로컬 데스크톱 앱.
UI 기준: **[andongmin94/neobrutal-ui](https://github.com/andongmin94/neobrutal-ui)**. 기능/한국어/화면 흐름을 유지하고 시각·상호작용을 통일한다.
제품 기준: `main`. 전환 코드: `work/sqlite-business-store`. 최종 갱신: 2026-10-05.

## 현재 상태와 다음 시작점

작업 시작 때 main과 전환 브랜치의 최신 ref를 모두 확인한다.
이번 시작 기준은 main `b5ab9ea8135995f28ca8ad30ad8055d30e94f35d`, 전환 브랜치 `af39b22a9487388896134a8a4ef08288d47c06c4`다.
진행 중인 브랜치를 무시하고 main에서 이식을 다시 만들거나 옛 ZIP으로 덮어쓰지 않는다.
미완성 전환 코드는 main에 병합하지 않는다. main에는 현재 계획만 갱신한다.

**이번 M2.2g:** 재검증 에이전트의 실제 초기화 호출을 하나의 SQLite 쓰기 트랜잭션으로 이식했다.
기존 `seedFindingRetester`는 에이전트가 없는데 먼저 `SeedTool`로 `tool_agents` 연결을 넣어 새 외래키 제약에 실패했다. 이 순서의 실패를 SQLite에서 재현했다.
`db.SeedFindingRetester`가 초기화 상태 확인 → 에이전트 → 프롬프트/현재 포인터 → 두 도구/관계 → 완료 기록을 같은 IMMEDIATE 트랜잭션에서 처리한다.
기존 도구 저장 SQL/JSON 검증을 재사용하고, 트랜잭션 안에서 부모 풀의 SeedTool을 다시 호출하지 않는다.
`server/finding_retests.go`는 실제 두 도구의 설명/스키마와 기존 기본 프롬프트를 전달한다. JSON 직렬화 실패와 저장 오류를 반환한다.
PG advisory lock와 구 `finding_retester_seed_v1` 실행 경로를 제거했다. 새 완료 키 `finding_retester_initialized`는 최초 설치 상태이지 구 데이터 마이그레이션이 아니다.
기존 에이전트/프롬프트/도구/연결/비활성 설정은 덮어쓰지 않는다. 완료 후 사용자가 에이전트나 도구를 삭제하면 다음 시작에서 재생성하거나 재연결하지 않는다.
도구 실행 본문, 세션별 결과 귀속, 승인·차단·대상 범위 정책과 자동 트리거 동작은 변경하지 않았다.

**전체 앱의 SQLite 부팅은 아직 미완료다.** main은 여전히 PostgreSQL이며, 전환 브랜치의 NewManager는 아직 기존 DSN을 전달한다.
작업 모델 체인/조회 전체, 자산/범위, 탐색/증거/보관, 실제 재검증 업무 SQL, LLM 중복 DDL, server.New 오류 반환과 프록시 bind가 남아 있다.
특히 `server.New`는 재검증 시드 오류를 아직 로그만 남긴다. 이번 저장 단위를 전체 startup 오류 전파/ready 차단 완료로 표시하지 않는다.
이번에는 재검증 초기화만 이식했다. LLM DDL 제거, 전체 앱/GUI/실제 드라이버 검증은 하지 않았다.

**즉시 다음 작업:** 같은 전환 브랜치에서 LLM 기록/사용량의 중복 PG DDL과 startup 호출을 제거하고 새 기본 스키마만 사용하게 한다.
`db/commands.go`의 `EnsureLLMRecordsTable`/마이그레이션과 `db/llm_usage.go`의 `EnsureLLMUsageTable`, 실제 호출자/테스트를 함께 정리한다. 사용량 날짜 집계/기록 검색의 PG SQL도 남아 있다.
작업 모델 체인과 재검증/로그/알림/작업 복원 쿼리를 이식한 뒤 NewManager/New의 파일 경로·오류 반환·정리와 실제 부팅 검사를 완성한다.
자산/범위·탐색·증거/보관까지 M2.7 검증 후 main에 병합한다. 이미 끝난 연결/도구/설정/재검증 시드를 반복 구현하지 않는다.
로컬 지정 Go/의존성을 갖추면 아래 신규 6개와 이전 미실행 modernc 테스트를 먼저 실행한다. 실패를 건너뛰거나 격리 검사로 대체하지 않는다.

## 검증 자원 정책

사용자 명시 요청 없이 Actions 실행/재실행/자동 트리거 추가, 임시 편집 workflow, 원격 artifact 업로드를 하지 않는다.
`verify`/`sqlite-foundation`은 수동 전용이며 개발 커밋에는 `[skip ci]`를 넣는다. 릴리스 태그도 만들지 않는다.
로컬 의존성 제약을 원격 runner로 우회하거나 Go 버전을 낮추지 않는다.
기존 artifact/cache와 사용자 DB/설정/증거/볼륨을 임의 삭제하지 않는다.
미실행 검사는 실패/성공과 구분하고, SQL 검사나 격리 드라이버 검사를 실제 Go SQLite/전체 앱 검증으로 표시하지 않는다.

## M0/M1 — 이전 완료 범위

- [x] 제품/설계/작업 지침, SQLite 단일 지원, PG 자동 데이터 이전 제외, neobrutal-ui 필수 기준 확정.
- [x] ARTEX_HOME 절대 경로/쓰기 검사와 config/data/skills 연결, 다른 CWD 설정을 암묵적으로 사용하지 않음.
- [x] loopback/동적 포트/ready JSON, 부모 stdin EOF 종료, cmd 치명적 종료 제거 및 정리.
- [x] 당시 설정/HTTP 테스트와 실제 backend smoke, 3개 OS 실행 기반 검증.

Electron, 전체 업무 SQLite, 도구/자손 프로세스, server.New 오류 반환, 패키징은 M1 완료 범위가 아니다.

## M2. 업무 저장소 SQLite 이식

- [x] **M2.1 인벤토리:** 기존 51개 업무 테이블, 부팅/직접 SQL/트랜잭션/fixture 지도. `docs/sqlite-porting-map.md` 참고.
- [ ] **M2.2 실행 가능한 업무 저장 기반:** 전체 스키마/시드/쓰기/취소/닫기를 실제 NewManager/New 부팅과 연결한다.
- [x] **M2.2a 공통 연결/트래픽:** 기존 main의 경로/연결별 PRAGMA/WAL/취소/재열기/FTS 검증. 이후 OpenImmediate 변경은 통합 검증 필요.
- [x] **M2.3a 설정/인증 저장:** 실제 메서드/HTTP fixture의 재열기, 동시 설정 단일 승자, 조회 오류 fail-closed 검증. 전체 부팅은 아니다.
- [ ] **M2.2b/M2.3b 프롬프트·모델:** 원자 저장과 조회/수정 오류 구현. 기존 modernc 7개/전체 빌드 미실행.
- [ ] **M2.2c 부팅/설정 일관성:** manager 동기 오류/정리, 설정 snapshot/묶음 저장 구현. 독립 Go 11개/SQL 7개 통과. 통합 10개 미실행.
- [ ] **M2.2d 업무 스키마/원자 생성:** 58개 테이블(기존 51+관계 7)/시드/식별자/버전/IMMEDIATE 구현. SQL 17개/제어 6개 통과, opener 통합 6개/전체 앱 미실행.
- [ ] **M2.2e 도구 관계/MCP:** tool_agents 기반 실제 CRUD/원자성/집계와 조건부 MCP args/env 갱신 구현. SQL 21개/제어 5+3개 통과. 실제 opener 도구 8개/MCP 4개 미실행.
- [ ] **M2.2f 모델 참조/대화:** 저장 메서드 이식. SQL 15개, 삭제 제어 15개 하위 사례 및 취소를 race 10회 통과. 실제 업무 opener 기반 Go 7개/전체 회귀 미실행. 작업 체인 전체 및 실제 부팅과 구분한다.
- [ ] **M2.2g 재검증 최초 초기화:** 실제 server→db 호출 연결과 단일 트랜잭션 구현. SQL 10개/Go 제어 3개(22개 하위 사례) 통과. 실제 업무 opener 기반 Go 6개/전체 부팅 미실행.
- [ ] **M2.3 설정·인증 통합:** setup/로그인/모델 저장·복원을 부팅과 연결. JWT 분리, 모든 생성 오류/정리와 기존 인증 정책 검증.
- [ ] **M2.4 자산·범위:** 관계/DSL/net/netip, IPv4·IPv6 경계, 기업 귀속/중복 원자성 검증.
- [ ] **M2.5 작업·탐색:** 작업/세션/의도/대화/사실/취약점/승인/재검증/사용량/기록, 다중 저장/취소 검증.
- [ ] **M2.6 증거·보관:** DB/트래픽/본문/보관 연결과 삭제·재검증·증거 동등성. PG JSON 행/배열/시퀀스 복원을 명시적 열/키 매핑으로 교체.
- [ ] **M2.7 통합:** PG 없이 setup → 로컬 모델 fixture → 작업/기록 → 증거 → 종료/재실행. 기존 PG fixture/skip을 SQLite로 교체하고 PG driver/config/서비스/smoke/헬퍼/전환 도구를 제거한 뒤 병합.

main의 PG는 SQLite 오류 fallback이 아니다. DB 선택 옵션, SQL 번역기, 구 데이터/보관 변환기, 임시 호환 계층을 만들지 않는다.

## M3. Electron 실제 UI + neobrutal-ui

UI 작업은 `docs/ui-design.md`와 최신 원본을 먼저 읽는다. 이번 단위에는 UI 소스 변경이 없다.

- [ ] 공통 토큰/타이포그래피/컨트롤/표/오버레이와 호출부를 함께 교체. 출처/저작권 보존.
- [ ] 셸/사이드바부터 대시보드/대화/작업/자산/증거/트래픽/설정 전체, 빈/로딩/오류/선택/비활성/승인 상태 적용.
- [ ] Electron 메인/렌더러 분리, 패키지/lockfile, Go/정적 UI 재사용. Next 서버 추가 금지.
- [ ] 단일 인스턴스/userData/ready/장애 화면/종료/부모 강제 종료, loopback·앱 세션 인증·Origin/Host·제한된 IPC/CSP/탐색 검증.
- [ ] 실제 패키지 사용 흐름, 라이트/다크/1280·1440px/Windows 배율/IME/키보드/축소 모션/긴 텍스트 및 실제 화면 검수.

독립 쇼케이스나 일부 화면만으로 전체 UI 완료를 표시하지 않는다. 이전 mock 캡처를 새 디자인/앱 실행 증거로 재사용하지 않는다.

## M4/M5. 도구·신뢰성·배포

- [ ] 셸/PTY/Python/Node/브라우저/MCP/CLI의 OS별 지원, 전용 경로의 버전/출처/해시/라이선스/복구와 필수 도구 준비 차단을 구현한다.
- [ ] 네트워크/작업 공간/권한과 자손 프로세스 정리, 실제 다중 기록 부하, kill/디스크 오류/손상과 일관된 DB+증거 백업/복원을 검증한다.
- [ ] Electron 업데이트 일원화. 기존 Go 업데이트/시작 스크립트와 Docker/PG 안내·의존성을 제거한다. 사용자 데이터는 삭제하지 않는다.
- [ ] Windows 우선 설치/서명/업데이트, macOS/Linux 실제 지원 검증, README 교체, neobrutal-ui 누락/구 스타일 최종 확인.

## 이전 기록

M2.2f 상세 기록은 `af39b22a9487388896134a8a4ef08288d47c06c4`의 이 문서에, 그 이전 기록은 `11b732cc9a8fb3cf73d87d34dad89f0267acf28c` 및 `docs/sqlite-business-store.md`에 보존되어 있다.
M1/M2.1/M2.2a/M2.3a의 과거 CI 성공은 새 전환 코드의 빌드/통합 성공이 아니다.
M2.2b 이후 modernc 통합 미실행 기록은 이번에도 그대로 유지한다. 기존 artifact 확인을 위해 CI를 재실행하지 않는다.

## 이번 로컬 검증 — M2.2g

기준: 전환 브랜치 `af39b22a9487388896134a8a4ef08288d47c06c4`. Linux Go 1.23.2, Python SQLite 3.46.1.
현재 세션의 git 접속과 Go 1.26.3 다운로드는 DNS 제약으로 실패했다. 프로젝트 버전/드라이버를 바꾸거나 원격 runner로 우회하지 않았다.
최신 GitHub에서 읽은 server 원본 blob `df311390ae4e1ac592eea68439ec35368fb4ae82`와 재사용 tools.go `eaf9c330202d82905a31f64c131e49d76047d663`를 로컬 Git blob SHA와 일치시켰다.

실행한 검사:

- 실제 새 `finding_retester_seed.go`, 기존 `tools.go`와 제어 테스트를 정확한 DB 래핑 타입/에이전트 키 경계로 격리해 `GO111MODULE=off go test -race -count=10 -timeout=60s -json .` 실행. **최상위 3개 + 하위 22개, 각 10회 통과**, 실패/skip 없음. 드라이버는 오류 주입용이며 modernc가 아니다.
- begin/상태조회/에이전트/프롬프트/현재 포인터/두 도구/두 연결/완료기록/commit 오류, 잘못된 ID 스캔/수정 행 누락, 완료·손상 상태, 기존 항목, 입력/취소를 검사했다. 단일 연결로 부모 풀 재진입이 없음을 확인했다.
- `check-sqlite-retester.py`: **SQL 수준 10개 통과**. 스키마 blob `7e7e264ace63175496d7541ad59948eeb43efff9`에서 확인한 변경 없는 6개 CREATE TABLE(settings/llm_profiles/agents/agent_prompts/tools/tool_agents)과 실제 Go SQL 상수를 사용했다. 전체 58개 스키마 검사가 아니다.
- 기존 도구-우선 순서의 FK 실패, 새 순서/포인터/JSON TEXT, 재열기/사용자 수정·삭제 보존, 손상 완료값, 6개 쓰기 단계 실패 롤백, 12개 동시 초기화의 단일 묶음 생성, WAL 읽기/FK/integrity를 확인했다. 로컬에는 선택 DDL을 --schema로 전달했다.
- 신규 native 테스트 6개의 본문도 격리 패키지에서 컴파일했지만 실행하지 않았다. 격리 Open은 명시적으로 사용 불가 오류를 반환하며 실제 DB 검사로 간주하지 않는다.
- gofmt/구문 검사 및 서버 초기화 함수 외 기존 핸들러·도구 실행 본문 동일성 검사를 수행했다. 사용자 데이터/모델 API/외부 명령/대상 호출은 없다.

미실행:

- `finding_retester_seed_sqlite_test.go`의 **실제 Open/전체 schema/seed/modernc 기반 6개**. 새 생성/재열기/단일 연결, 기존 편집, 삭제, 단계별 롤백, 동시 초기화, 취소/손상 상태/닫힌 DB를 검사하도록 작성했다.
- 이전 modernc 테스트, 전체 Go 1.26.3 빌드/회귀, NewManager/New/HTTP 부팅, Windows/macOS, Electron/화면/도구/전체 부하/백업.
- LLM 중복 DDL과 전체 server.New의 오류 반환은 이번 변경 범위가 아니다.

```sh
# Python 표준 라이브러리의 실제 SQL 검사. 전체 Go 드라이버 검사가 아니다.
python scripts/check-sqlite-retester.py
# 프로젝트 지정 Go와 의존성이 있는 로컬 저장소에서 수행할 실제 드라이버 검사:
go test -race -count=1 -timeout=120s -run '^(TestRetesterSeed|TestSQLiteRetesterSeed)' ./db
```

Actions 실행/재실행/업로드, 릴리스 태그 생성은 하지 않는다. 부분 검증을 M2.2/M2.7 완료로 표시하지 않는다.
