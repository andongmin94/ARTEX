# 개발 계획 — 유일한 현재 작업 목록

목표: **기존 ARTEX + Electron + Go + SQLite**, Docker/PG 없는 로컬 데스크톱 앱.
UI 기준: **[andongmin94/neobrutal-ui](https://github.com/andongmin94/neobrutal-ui)**. 기능/화면 흐름은 유지하고 시각·상호작용은 이 기준으로 통일한다.
기준 브랜치: `main`. 최초 기준 SHA: `e0354e9a7101ed4f5bb43c9136beec07acafa014`.
최종 갱신: 2026-10-05.

## 현재 상태

**M0/M1, M2.1, M2.2a 공통 SQLite 연결/트래픽, M2.3a 설정/인증 저장 검증을 완료했다.**
이번 구현은 [PR #2](https://github.com/andongmin94/ARTEX/pull/2)로 squash 병합했다.
main 구현 커밋: `81757868533bcb7b4ba15186bfc326d6ffc8f898`.
검증된 PR HEAD: `4460f0f32790bbf4fb14c8d3837e48f3f7875fcc`.
해당 HEAD의 `verify #59` 7개 job과 `sqlite-foundation #4` 3개 OS job이 모두 성공했다.

**주 업무 DB는 아직 PostgreSQL이다. 전체 업무 SQLite 부팅, LLM 프로필 이식, Electron 앱과 neobrutal-ui 적용 화면은 아직 없다.**
SQLite 연결은 실제 트래픽 경로에서 사용한다. 설정/인증은 실제 메서드와 HTTP 핸들러를 최소 SQLite fixture로 검증했으며, 전체 `NewManager/New` 부팅 완료가 아니다.
README의 Docker/PG 실행 안내는 현 제품에 해당하며 목표 아키텍처의 완료 증거가 아니다.

**즉시 다음 작업:** M2.2/M2.3의 남은 업무 스키마·시드·쓰기 연결 정책과 실제 부팅/모델 프로필 저장을 이식한다.
`docs/sqlite-porting-map.md`의 51개 업무 테이블과 startup 호출 의존을 기준으로 한다.
조사 도구를 더 만드는 대신 실제 저장소/호출자를 이식하고, 초기화 실패를 무시한 채 ready를 내보내지 않는다.
M2.2a/M2.3a를 반복 구현하거나 최소 fixture 성공을 전체 앱 전환 완료로 표시하지 않는다.

## M0. 방향과 작업 기준

- [x] PRODUCT/아키텍처/개발 절차/에이전트 읽기 순서 작성.
- [x] UI·Go 재사용, SQLite 단일 지원, PG 자동 데이터 이전 제외 확정.
- [x] 기존 트래픽 SQLite와 PG 업무 저장소/LLM 기록 구분.
- [x] neobrutal-ui를 필수 UI 기준으로 확정. 원본 SHA/실제 토큰·버튼 API 차이/전체 화면 검수 조건을 `docs/ui-design.md`에 기록.

## M1. 데스크톱 실행 기반 — 구현·CI 검증 완료

- [x] `ARTEX_HOME` 절대 경로 검증·쓰기 검사, 설정/기본 data/skills 루트 연결.
- [x] 홈에 설정이 없을 때 CWD의 다른 DB 설정으로 넘어가지 않음.
- [x] 기본 HTTP 주소를 loopback으로 제한. `-addr 127.0.0.1:0`의 실제 포트 사용.
- [x] `-ready-stdout`으로 저장소/HTTP 준비 후 ready JSON 출력.
- [x] `-parent-stdin`으로 부모 EOF/파이프 오류 시 정상 종료 경로 실행.
- [x] `cmd/artex` 시작/서빙 오류의 `log.Fatal` 제거, 종료 요청 뒤 manager 정리, HTTP timeout 후 연결 정리. `server.New`의 JWT 오류 종료는 아직 남아 있어 M2에서 함께 정리한다.
- [x] 설정 7개 + HTTP/부모 파이프 6개 독립 테스트와 race 반복 검사.
- [x] Windows/macOS/Linux 실행 기반 CI와 실제 백엔드 smoke 검사 연결.
- [x] Go 1.26.3의 전체 테스트 명령·빌드와 실제 백엔드 smoke 확인. `verify #55`, run `37213127038`.

Electron 창, 주 DB SQLite 교체, 도구 설치, 앱 업데이트, 패키징, 전체 자손 프로세스 정리는 M1 완료 범위가 아니다.

## M2. 업무 저장소 SQLite 이식

작업 재개 시 최신 main과 CI를 확인한다. 아래 순서로 진행한다.

- [x] **M2.1 인벤토리:** 전체 Go/SQL 332개 조사 및 후보 214개 분류. `verify #56` 보고서와 핵심 코드 검토로 `docs/sqlite-porting-map.md`의 51개 업무 테이블/부팅 경계/직접 SQL/트랜잭션/테스트 fixture 이식 지도를 확정했다. 후보 수는 수정 파일 수나 기능 동등성 증명이 아니다.
- [ ] **M2.2 실행 가능한 업무 저장 기반:** 기존 modernc 드라이버로 업무 DB 스키마/연결 초기화, 쓰기 경로, 명시적 취소/닫기를 구현. `server.NewManager/New`의 전체 부팅 의존과 생성 오류 반환을 함께 연결한다. 미사용 추상 저장소를 완료 결과로 제출하지 않는다.
- [x] **M2.2a 공통 연결/트래픽:** `internal/sqlitedb.Open`을 실제 `traffic.Open`에 연결. URI 특수문자, 연결별 PRAGMA, WAL, 취소, 파일 보존, 재실행/FTS 검증 완료. 전체 업무 스키마와 writer/read pool 정책은 M2.2에 남아 있다.
- [x] **M2.3a 설정/인증 저장:** 실제 설정 메서드의 SQLite 저장/재실행, 동시 최초 설정/비밀번호 변경의 단일 승자, 조회 오류 fail-closed를 실제 HTTP fixture로 검증 완료. 전체 NewManager/New 부팅이나 LLM 프로필 이식 완료가 아니다.
- [ ] **M2.3 설정·인증 통합:** 최초 setup/로그인/설정/LLM 프로필 저장·재실행 복원 경로를 실제 앱 부팅과 연결한다. 기존 인증 정책과 API 필드 유지. JWT 키의 작업 공간 분리와 초기화 실패 전파를 검증한다.
- [ ] **M2.4 자산·범위:** 관계 테이블, 자산 DSL, IP/CIDR/IPv6 포함 검색, 기업 범위 재계산/중복 처리를 실제 fixture로 검증.
- [ ] **M2.5 작업·탐색:** 작업/세션/의도/대화/사실/취약점/승인/재검증/사용량/LLM 기록 이식. 다중 에이전트 저장과 취소 검증.
- [ ] **M2.6 증거·보관:** 업무 DB와 기존 트래픽 인덱스/본문/보관 패키지 연결, 삭제/재검증/증거 보존 동등성 검증.
- [ ] **M2.7 통합 전환:** Docker/PG 없이 setup → 모델 설정(로컬 fixture) → 작업/기록 저장 → 증거 조회 → 종료 → 재실행 검증. 기존 PG 테스트를 skip하지 말고 SQLite fixture로 이전. PG driver/config/schema/테스트 서비스와 smoke의 PG 조건을 제거. 전환 전용 인벤토리 도구/job도 제거한다.

개별 이식 도중 앱이 깨지는 변경은 기능 브랜치에서 작업하고 M2.7을 통과한 단위로 반영한다.
현재 main에 PG를 유지하는 것은 새 앱의 병행 지원이 아니다. 새 구현에 DB 선택 스위치·PG fallback·SQL 번역기를 만들지 않는다.

## M3. Electron 실제 UI 연결 + neobrutal-ui 적용

시각 기준과 화면 범위는 `docs/ui-design.md`를 따른다. 기존 스타일 고정이나 임의 네오브루탈 재해석이 목표가 아니다.

- [ ] **M3.1 공통 UI:** 원본 최신 ref 검토, 토큰/타이포그래피/버튼·입력·탭·카드·표·오버레이 적용. 변경한 primitive의 호출부를 같이 교체하고 출처/저작권 표기를 보존한다.
- [ ] **M3.2 실제 화면:** 사이드바/상단 셸부터 대시보드·대화·작업·자산·증거·트래픽·설정 전체로 확장. 빈/로딩/오류/선택/비활성/승인 화면도 포함. 라우트별 실제 화면 검수 기록을 남긴다.
- [ ] **M3.3 Electron:** 메인/렌더러 경계와 패키지·lockfile 추가. Go/정적 UI 재사용, Next 서버 추가 금지.
- [ ] 단일 앱 인스턴스, userData 전달, 자식 시작/ready 검증/장애 화면/정상 종료 및 부모 강제 종료 시험.
- [ ] loopback+앱 세션 인증, Origin/Host 검사, 제한된 preload/IPC/CSP/외부 탐색 보안.
- [ ] **M3.4 검수:** 패키지 앱의 setup → 설정 → 작업 → 기록/증거 → 재시작 검증. 라이트/다크·1280/1440px·Windows 배율·한글 IME·키보드·축소 모션·긴 텍스트 확인. 실제 스크린샷으로 neobrutal-ui 일관성을 검수한다.

대시보드/버튼 몇 개 또는 독립 쇼케이스만 완성해 전체 UI 완료로 표시하지 않는다.
DB 동등성 검증과 UI 스타일 변경은 별도 단위로 수행한다.

## M4. Docker가 제공하던 도구 환경 대체

- [ ] 기존 셸/PTY/Python/Node/브라우저/MCP/CLI 의존과 OS별 지원표 조사.
- [ ] 필요한 도구만 앱 전용 경로에 버전 고정 설치. 출처·해시·라이선스·실패 복구 검증.
- [ ] 필수 도구 준비 전 실행 차단/안내, Go에 관리된 PATH·작업 디렉터리 전달. 새 안내 화면도 neobrutal-ui 기준 적용.
- [ ] 네트워크/작업 공간/권한 정책과 모든 자손 프로세스 정리를 각 OS에서 검증.

## M5. 데이터 신뢰성·배포·구 경로 제거

- [ ] 동시 에이전트+트래픽 기록에서 잠금 오류/지연/검색 응답을 실제 부하로 측정.
- [ ] 정상 종료/프로세스 kill/디스크 오류/손상 감지와 일관된 DB+증거 백업/복원 시험.
- [ ] 앱 업데이트를 Electron으로 일원화하고 기존 Go 업데이트 API/시작/업데이트 스크립트 제거.
- [ ] Dockerfile/Compose/PG 설치 안내·CI·불필요 의존성 제거. 사용자 데이터/볼륨은 삭제하지 않음.
- [ ] Windows 우선 설치 패키지/서명/업데이트 검증, macOS/Linux 실제 지원 검증.
- [ ] README를 새 설치 방법으로 교체. 완성 전 Docker-free 또는 SQLite 전환 완료라고 표시하지 않음.
- [ ] neobrutal-ui 적용 누락과 구 스타일/중복 토큰/호환 래퍼가 남지 않았는지 최종 전 화면 확인.

## 검증 기록 — 2026-10-05

### M1 — 이전 검증

Linux, Go 1.23.2, Node 22.16.0. 네트워크 DNS 제한으로 clone/Go 1.26.3 의존성 확보 실패.
`go.mod`를 바꾸지 않고 설정/HTTP/EOF 독립 테스트 13개를 각각 race 20회 검사했다.

```text
GO111MODULE=off go test -race -count=20 -timeout 60s ./config
GO111MODULE=off go test -race -count=20 -timeout 60s cmd/artex/http_lifecycle.go cmd/artex/http_lifecycle_test.go
node --check scripts/check-backend-lifecycle.mjs
```

- 커밋 `4acff4563df22a2094b7adcc8e4fde7d05267a00`, [verify #55](https://github.com/andongmin94/ARTEX/actions/runs/37213127038): 6개 job success.
- Go 1.26.3 전체 테스트 명령·빌드, 실제 Linux 백엔드의 한글/공백 홈 → ready → health → 부모 EOF → exit 0 성공. Windows/macOS/Linux 독립 실행 기반 race 검사 성공.
- 프런트엔드 한국어/IME/TypeScript/정적 빌드와 기존 스크립트/Compose 설정 검사 성공. Docker 이미지 빌드나 GUI 검증은 아니다.
- PG 서비스 로그의 스키마/런타임 조회 deadlock은 별도 위험으로 남긴다. 성공한 전체 명령을 동시성 무결점의 근거로 삼지 않는다.
- 결과 기록 커밋 `babea04074a090ae66953c43f335a8580ddaa27c`는 문서 전용이다.

### M2.1/UI 기준 — 이전 검증

- ARTEX 기준 `babea04074a090ae66953c43f335a8580ddaa27c`, neobrutal-ui `b4da2463fe710a77bf464c65125a1a7f40424722`.
- Go AST 읽기 전용 인벤토리와 테스트 8개. 로컬 독립 race 20회 성공. SQL 변환기나 DB 실행 기능이 아니다.
- 커밋 `a99972d4e8ff1eaec333aad05874374ef7c95510`, [verify #56](https://github.com/andongmin94/ARTEX/actions/runs/37214597987): 7개 job success.
- Go/SQL 332개 → 후보 214개. job `111472435421`, artifact `11308130499`(14일 보관). DSN/키 원문을 출력하지 않는다.
- schema.sql 49개 테이블 + LLM 기록/사용량 2개, 부팅·직접 SQL·pgconn 분류·트랜잭션·테스트 skip을 검토했다. `server/auth.go`처럼 정적 조사에서 빠지는 간접 호출도 수동 확인했다.
- UI 원본 소스/토큰/버튼 API를 검토했고, 화면 변경·GUI·스크린샷 검수는 수행하지 않았다.
- 결과/이식 지도 기록 커밋 `c1c49e74685b49e310aa8f7e71aac5b9c95e4b9a`는 문서 전용이다.

### M2.2a/M2.3a — 이번 구현과 검증

- 구현 main SHA: `81757868533bcb7b4ba15186bfc326d6ffc8f898`, PR #2 squash merge.
- 검증 PR HEAD: `4460f0f32790bbf4fb14c8d3837e48f3f7875fcc`.
- PR 병합 시험 checkout SHA: `294840749fd3057aa4ceba7fd3baca54ba7d1f48`.
- [verify #59](https://github.com/andongmin94/ARTEX/actions/runs/37218191195): backend/frontend/deployment/storage-inventory + desktop-foundation 3개 OS, **7개 job 모두 success**.
- [sqlite-foundation #4](https://github.com/andongmin94/ARTEX/actions/runs/37218191191): PG 서비스 없이 Windows/macOS/Linux **3개 job 모두 success**. 연결 race 검사와 설정/인증/트래픽 실제 fixture 검사, skip 금지 조건 통과.
- `backend-test-results` artifact `11309266073`의 원본 JSON과 checkout 목록을 내려받아 확인했다. 최상위 Go 테스트 **694 pass / 0 fail / 1 skip**. 생략된 기존 `TestLiveContextReview`는 외부 개인 모델 설정 `ARTEX_REVIEW_LIVE_CONFIG`가 필요한 live 검사다. 새 테스트는 생략하지 않았다.
- 신규 테스트 18개: 연결/경로/WAL/외래키/취소 9개, 설정 저장/경쟁/재열기 4개, 실제 인증 HTTP 4개, 트래픽 저장/재열기/한글 FTS 1개. 하위 경로/오류 사례는 별도 subtest로 포함한다.
- Go 1.26.3 전체 suite와 `go build ./cmd/artex`, 실제 백엔드 readiness/EOF 종료 smoke 성공. 기존 한국어·IME·TypeScript·정적 UI 빌드도 성공했다.

실제 적용한 내용:

- `internal/sqlitedb.Open`을 기존 트래픽 생성 경로에 연결. URI 인코딩 없는 DSN과 `?` bare-path 분기를 제거했다.
- 연결마다 foreign_keys=1, busy_timeout=5000, synchronous=FULL을 적용하고 WAL 적용 결과를 확인한다. 손상 파일을 지우거나 PG로 우회하지 않는다.
- 설정 최초 INSERT/조건부 변경을 실제 인증 핸들러에 연결했다. bcrypt는 쓰기 구간 밖에서 수행한다. 조회 오류/비어 있는 손상 해시는 미설정으로 처리하지 않는다.
- 최소 settings 테이블의 폐기 가능한 SQLite에서 HTTP 설정/로그인/변경과 재열기를 검증했다. 이 fixture는 전체 NewManager/New 부팅이나 51개 업무 테이블을 대체하지 않는다.
- `verify #58`의 원본 artifact `11308149113`에서 `TestTaskMetadataPatchReturnsRenameAndPin` 종료 후 임시 폴더 쓰기 실패를 확인했다. 기존 metadata/batch-delete HTTP 테스트가 전체 `New(context.Background())`를 호출해 공유 DB의 다른 작업을 복원·실행하던 원인을 제거했다. 실제 router/auth/DB assertions는 유지했고 수정한 두 테스트는 #59에서 통과했다.
- CI는 전체 Go 테스트별 JSON 결과와 checkout SHA/소스 목록을 14일 artifact로 보관한다. 기존 오류를 재실행만으로 숨기거나 테스트 성공 조건을 완화하지 않았다.
- 작업 브랜치의 일회성 편집 workflow/script는 최종 tree에서 제거했다. SQL 번역기/DB 선택 옵션/데이터 자동 이전기는 없다.

로컬은 Go 1.23.2와 DNS 제한으로 전체 소스/의존성 실행이 불가능해 형식/구문만 검사했다. 프로젝트 Go 버전은 바꾸지 않았다.
새 fixture는 로컬 HTTP·합성 트래픽·폐기 가능한 DB를 사용한다. 사용자 데이터는 수정/삭제하지 않았다.
이번 변경에 UI/에이전트 기능 확장은 없다. neobrutal-ui 필수 기준은 유지한다.

남은 미검증 범위: 전체 업무 SQLite와 실제 앱 부팅/LLM 프로필, Electron GUI/패키지, Windows/macOS 전체 ARTEX/도구, neobrutal-ui 화면, 부하·백업·복원.
이 후속 커밋은 검증 결과와 계획만 갱신하며 `[skip ci]`를 사용한다. 위 성공 결과는 명시한 PR 코드와 시험 checkout의 결과다.
