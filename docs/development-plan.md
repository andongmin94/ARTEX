# 개발 계획 — 유일한 현재 작업 목록

목표: **기존 ARTEX + Electron + Go + SQLite**, Docker/PG 없는 로컬 데스크톱 앱.
UI 기준: **[andongmin94/neobrutal-ui](https://github.com/andongmin94/neobrutal-ui)**. 기능/화면 흐름은 유지하고 시각·상호작용은 이 기준으로 통일한다.
기준 브랜치: `main`. 최초 기준 SHA: `e0354e9a7101ed4f5bb43c9136beec07acafa014`.
최종 갱신: 2026-10-05.

## 현재 상태

**M0/M1, M2.1, M2.2a 공통 SQLite 연결/트래픽, M2.3a 설정/인증 저장 검증을 완료했다.**
이전 구현은 PR #2로 squash 병합했다. 구현 main SHA `81757868533bcb7b4ba15186bfc326d6ffc8f898`, 기록 SHA `4d0deedcfaf1041538416fcd5fd652a1218bbeb5`.
이전 PR HEAD `4460f0f32790bbf4fb14c8d3837e48f3f7875fcc`의 `verify #59` 7개 job과 `sqlite-foundation #4` 3개 OS job이 성공했다.
이 성공 기록은 아래 새 변경의 전체 회귀 성공을 뜻하지 않는다.

**이번 단위:** 실제 초기 프롬프트 시드/버전 저장을 하나의 쓰기 트랜잭션으로 묶고, 모델 프로필의 없는 행 수정·조회 오류 처리를 고쳤다.
`db/config.go`에서 모델 저장은 `db/llm_profiles.go`, 프롬프트 버전은 `db/prompts.go`로 분리했다. 기존 호출자가 같은 메서드를 사용한다.
Go 제어 흐름 격리 검사와 Python SQLite SQL 검사는 통과했지만, 새 modernc 통합 테스트 7개와 프로젝트 전체 빌드/회귀는 **미실행**이다.

**주 업무 DB는 아직 PostgreSQL이다. 전체 업무 SQLite 부팅, 모델 참조 삭제/바인딩 이식, Electron 앱과 neobrutal-ui 적용 화면은 아직 없다.**
최소 SQLite fixture의 성공을 전체 `NewManager/New` 부팅 완료로 해석하지 않는다. README의 Docker/PG 안내는 현재 제품에 해당한다.

**검증 정책:** 사용자 요청에 따라 Actions 실행·재실행 없이 로컬 검사로 진행한다.
`verify`/`sqlite-foundation`은 수동 실행 전용으로 바꾸며, 사용자 명시 요청 없이 실행하지 않는다. 개발 커밋은 `[skip ci]`를 사용한다.
원격 runner로 소스를 편집하거나 로컬 네트워크 제한을 우회하지 않는다. 기존 artifact/cache/사용자 데이터는 임의 삭제하지 않는다.

**즉시 다음 작업:** 아래 M2.2/M2.3의 전체 업무 스키마·시드·쓰기 연결 정책과 실제 부팅/모델 저장 연결을 이어간다.
새 Go 모델/프롬프트 테스트는 의존성을 갖춘 로컬 환경에서 실행하고, 미실행 상태를 지우기 위해 CI를 켜지 않는다.
`docs/sqlite-porting-map.md`의 51개 업무 테이블/startup 의존을 기준으로 실제 호출자를 이식한다. 조사 도구나 이미 끝난 연결 fixture를 반복 구현하지 않는다.
`server.New`의 JWT `log.Fatalf`, `loadLLMConfig`의 조회 오류 무시, 기타 startup 실패 전파는 아직 남아 있다. 초기화 실패를 무시하고 ready를 내보내지 않는다.

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
- [ ] **M2.2b/M2.3b 프롬프트·모델 저장:** 원자적 초기 프롬프트/연속 버전, 모델 수정·조회 오류 처리 구현. 로컬 부분 검증만 완료. 새 modernc Go 테스트 7개 및 기존 PG 회귀/전체 빌드 미실행. 활성 모델·참조 삭제/바인딩 전체 이식 완료가 아님.
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

## 검증 기록 — 이번 모델/프롬프트 단위

기준 main `4d0deedcfaf1041538416fcd5fd652a1218bbeb5`. 사용자 요청에 따라 Actions는 실행하지 않았다.

구현:

- `SeedPromptIfEmpty`와 `SavePrompt`가 agent 쓰기를 먼저 수행한 뒤 같은 트랜잭션에서 현재값/버전/본문/포인터를 처리한다. 동시 seed가 사용자 편집을 덮어쓰지 않는다.
- 트랜잭션 도중 parent pool로 재진입하지 않는다. 쓰기·조회·포인터·commit 실패를 반환하고 실패한 버전을 성공으로 알리지 않는다.
- `SaveProfile`의 두 UPDATE는 `RETURNING id`로 실제 수정 행을 확인한다. 없는 ID는 `ErrLLMProfileNotFound`, nil 입력은 오류다. 빈 키 업데이트의 기존 키 보존 의미는 유지한다.
- `ActiveProfile`/`ProfileByID` 조회 실패는 부분적으로 채운 프로필을 반환하지 않는다. `SetActiveProfile`은 RowsAffected 오류도 반환한다.
- 기존 프로필 참조 삭제/에이전트 바인딩의 PG 잠금 로직은 변경하지 않았다. SQLite용 신규 호환 계층을 추가하지 않았다.

실행한 검사:

- `gofmt` 및 Go AST 대조: 원본 63개 선언 누락/중복 없음. 메서드 6개만 변경, 트랜잭션 내부 helper 2개 추가. 그 외 선언은 동일.
- 실제 새 `prompts.go`/`prompts_control_test.go`를 동일한 DB 래핑 타입과 격리해 Go 1.23.2에서 `GO111MODULE=off go test -race -count=20 -timeout 60s -v .` 실행. **트랜잭션 제어 8개 하위 사례 × 20회 통과**. SQL driver는 의도적으로 실패를 주입하는 scripted driver다.
- Python SQLite **3.46.1**에서 새 소스의 SQL과 테스트용 스키마를 그대로 실행: **13개 SQL 수준 검사 통과**. 동시 seed 12개/편집 12개/활성화 12개, 사용자 편집 보존, 포인터·활성화 실패 롤백, 모델 필드 재열기/키 보존/교체/없는 ID/풀 순서 포함. FK/integrity 확인 성공.
- 두 workflow의 YAML 및 원본 blob 대조: trigger만 수동으로 변경했고 기존 job/검사 명령은 동일.

실행하지 못한 검사:

- 새 `config_sqlite_test.go`의 Go modernc 통합 테스트 **7개**. 모델 재열기/키 비노출/활성화 롤백/동시성, 프롬프트 재열기/동시 seed·편집/롤백/닫힌 DB를 검사하도록 추가했지만 실행 결과는 아직 없다.
- Go 1.26.3 전체 빌드/기존 PG 회귀, 실제 전체 앱 부팅, Windows/macOS native 검사.
- 로컬 DNS 제한으로 GitHub clone 및 Go 1.26.3 의존성 다운로드 실패. 프로젝트 `go.mod`는 바꾸지 않았고 원격 runner로 우회하지 않았다.

이번 검사는 모델 API/외부 대상/사용자 DB를 사용하지 않았다. SQL 수준 검사와 scripted-driver 성공을 modernc 또는 전체 앱 성공으로 표시하지 않는다.
M2.2/M2.3 전체 및 새 하위 단위의 통합 검증 체크박스는 미완료로 유지한다.
