# 개발 계획 — 유일한 현재 작업 목록

목표: **기존 ARTEX + Electron + Go + SQLite**, Docker/PG 없는 로컬 데스크톱 앱.
기준 브랜치: `main`. 최초 기준 SHA: `e0354e9a7101ed4f5bb43c9136beec07acafa014`.
최종 갱신: 2026-10-05.

## 현재 상태

**설계와 데스크톱 실행 기반 구현을 시작했다. 주 DB는 아직 PostgreSQL이며 Electron 앱은 아직 없다.**
현재 README의 Docker/PG 실행 안내는 현 제품에 해당하며, 목표 아키텍처의 완료 증거가 아니다.
이번 작업은 데이터 변환이 아니라 실행/저장 아키텍처 전환의 첫 단위다.

## M0. 방향과 작업 기준

- [x] PRODUCT/아키텍처/개발 절차/에이전트 읽기 순서 작성.
- [x] UI·Go 재사용, SQLite 단일 지원, PG 자동 데이터 이전 제외 확정.
- [x] 기존 트래픽 SQLite와 PG 업무 저장소/LLM 기록 구분.

## M1. 데스크톱 실행 기반 — 구현 및 독립 검사 완료, 전체 CI 확인 필요

- [x] `ARTEX_HOME` 절대 경로 검증·쓰기 검사, 설정/기본 data/skills 루트 연결.
- [x] 홈에 설정이 없을 때 CWD의 다른 DB 설정으로 넘어가지 않음.
- [x] 기본 HTTP 주소를 loopback으로 제한. `-addr 127.0.0.1:0`의 실제 포트 사용.
- [x] `-ready-stdout`으로 저장소/HTTP 준비 후 ready JSON 출력.
- [x] `-parent-stdin`으로 부모 EOF/파이프 오류 시 정상 종료 경로 실행.
- [x] 시작/서빙 오류의 `log.Fatal` 제거, 종료 요청 뒤 manager 정리, HTTP timeout 후 연결 정리.
- [x] 설정 7개 + HTTP/부모 파이프 6개 독립 테스트와 race 반복 검사.
- [x] Windows/macOS/Linux 실행 기반 CI와 실제 백엔드 smoke 검사 연결.
- [ ] 프로젝트 지정 Go 버전의 전체 CI/실제 백엔드 smoke 결과 확인. 실패하면 M2보다 먼저 수정.

Electron 창, 주 DB SQLite 교체, 도구 설치, 앱 업데이트, 패키징, 전체 자손 프로세스 정리는 M1 완료 범위가 아니다.

## M2. 업무 저장소 SQLite 이식 — 다음 구현 단위

M1의 실제 CI 결과부터 확인하고, 다음 순서로 진행한다.

- [ ] **M2.1 인벤토리:** 전체 `db/`, `server/`, `agent/`, `llmrec/`, `report/` 등의 raw SQL/PG 타입/트랜잭션 경계/테스트 헬퍼를 조사. `docs/sqlite-porting-map.md`에 실제 파일과 이식 단위를 기록한다. 이 문서는 목록의 근거이며 별도 진행표를 만들지 않는다.
- [ ] **M2.2 실행 가능한 저장 기반:** 기존 modernc 드라이버로 업무 DB 스키마/연결 초기화, 연결별 PRAGMA, 쓰기 경로, 명시적 취소/닫기, Unicode/특수문자 경로 검사. 미사용 추상 저장소를 완료 결과로 제출하지 않는다.
- [ ] **M2.3 설정·인증:** 최초 setup/로그인/설정/LLM 프로필 저장·재실행 복원 경로 이식. 기존 인증 정책과 API 필드 유지.
- [ ] **M2.4 자산·범위:** 관계 테이블, 자산 DSL, IP/CIDR/IPv6 포함 검색, 기업 범위 재계산/중복 처리를 실제 fixture로 검증.
- [ ] **M2.5 작업·탐색:** 작업/세션/의도/대화/사실/취약점/승인/재검증/사용량/LLM 기록 이식. 다중 에이전트 저장과 취소 검증.
- [ ] **M2.6 증거·보관:** 업무 DB와 기존 트래픽 인덱스/본문/보관 패키지 연결, 삭제/재검증/증거 보존 동등성 검증.
- [ ] **M2.7 통합 전환:** Docker/PG 없이 setup → 모델 설정(로컬 fixture) → 작업/기록 저장 → 증거 조회 → 종료 → 재실행 검증. 기존 PG 테스트를 skip하지 말고 SQLite fixture로 이전. PG driver/config/schema/테스트 서비스와 smoke의 PG 조건을 제거.

개별 이식 도중 앱이 깨지는 변경은 기능 브랜치에서 작업하고 M2.7을 통과한 단위로 반영한다.
현재 main에 PG를 유지하는 것은 새 앱의 병행 지원이 아니다. 새 구현에 DB 선택 스위치·PG fallback·SQL 번역기를 만들지 않는다.

## M3. Electron 실제 UI 연결

- [ ] Electron 메인/렌더러 경계와 패키지·lockfile 추가. Go/정적 UI 재사용, Next 서버 추가 금지.
- [ ] 단일 앱 인스턴스, userData 전달, 자식 시작/ready 검증/장애 화면/정상 종료 및 부모 강제 종료 시험.
- [ ] loopback+앱 세션 인증, Origin/Host 검사, 제한된 preload/IPC/CSP/외부 탐색 보안.
- [ ] 실제 setup → 작업 생성 → 기록/증거 조회 → 재시작을 패키지 앱에서 검증.

## M4. Docker가 제공하던 도구 환경 대체

- [ ] 기존 셸/PTY/Python/Node/브라우저/MCP/CLI 의존과 OS별 지원표 조사.
- [ ] 필요한 도구만 앱 전용 경로에 버전 고정 설치. 출처·해시·라이선스·실패 복구 검증.
- [ ] 필수 도구 준비 전 실행 차단/안내, Go에 관리된 PATH·작업 디렉터리 전달.
- [ ] 네트워크/작업 공간/권한 정책과 모든 자손 프로세스 정리를 각 OS에서 검증.

## M5. 데이터 신뢰성·배포·구 경로 제거

- [ ] 동시 에이전트+트래픽 기록에서 잠금 오류/지연/검색 응답을 실제 부하로 측정.
- [ ] 정상 종료/프로세스 kill/디스크 오류/손상 감지와 일관된 DB+증거 백업/복원 시험.
- [ ] 앱 업데이트를 Electron으로 일원화하고 기존 Go 업데이트 API/시작/업데이트 스크립트 제거.
- [ ] Dockerfile/Compose/PG 설치 안내·CI·불필요 의존성 제거. 사용자 데이터/볼륨은 삭제하지 않음.
- [ ] Windows 우선 설치 패키지/서명/업데이트 검증, macOS/Linux 실제 지원 검증.
- [ ] README를 새 설치 방법으로 교체. 완성 전 Docker-free 또는 SQLite 전환 완료라고 표시하지 않음.

## 검증 기록 — 2026-10-05

로컬 환경: Linux, Go 1.23.2, Node 22.16.0. 원격 소스는 GitHub 연결로 조회했다.
로컬 git clone은 네트워크 DNS 제한으로 실패했고 프로젝트 Go 1.26.3/전체 의존성을 받지 못했다.
`go.mod`는 변경하지 않았다.

실행 성공:

```text
GO111MODULE=off go test -race -count=20 -timeout 60s ./config
GO111MODULE=off go test -race -count=20 -timeout 60s cmd/artex/http_lifecycle.go cmd/artex/http_lifecycle_test.go
node --check scripts/check-backend-lifecycle.mjs
gofmt (변경 Go 파일)
```

두 Go 검사 합계: 서로 다른 테스트 13개, 각각 20회 반복. 실제 HTTP loopback 연결과 EOF 동작을 검사했다.
미검증: 전체 Go 컴파일/전체 suite, 실제 ARTEX+PG smoke, Windows/macOS 실행, Electron GUI, SQLite 업무 DB.
원격 CI 결과가 나오면 이 항목을 실제 run과 결과로 갱신한다.
