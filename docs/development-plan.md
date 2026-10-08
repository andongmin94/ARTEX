# 개발 계획 — 유일한 현재 작업 목록

목표: **기존 ARTEX + Electron + Go + SQLite**, Docker/PG 없는 로컬 데스크톱 앱.
UI 기준: **[andongmin94/neobrutal-ui](https://github.com/andongmin94/neobrutal-ui)**. 기능/화면 흐름은 유지하고 시각·상호작용은 이 기준으로 통일한다.
제품 기준 브랜치: `main`. 최초 기준 SHA: `e0354e9a7101ed4f5bb43c9136beec07acafa014`.
최종 갱신: 2026-10-05.

## 현재 상태와 재개 위치

**업무 DB 자체의 SQLite 전환을 `work/sqlite-business-store`에서 시작했다.**
작업 브랜치의 시작 기준 main은 `33301d8dd28f709fa98897eb6e7e58708d24315f`다.
작업 전에 main과 이 작업 브랜치의 최신 ref를 모두 확인한다. 진행 중인 브랜치를 무시하고 main에서 동일 구현을 다시 만들지 않는다.
main에는 계획/구현 경계 문서만 갱신하며, 미완성 전환 코드는 병합하지 않는다.

**전환 브랜치의 이번 구현:**
`db/schema.sql`을 58개 업무/관계 테이블의 SQLite 스키마로 교체했다. 51개 기존 업무 테이블과 7개 정규화 관계를 포함한다.
`db/seed.sql`에 최종 기본값을 넣고, `db.Open/OpenContext`에서 파일 연결 → 같은 트랜잭션의 식별자/버전 확인 → 새 스키마/시드 → 커밋을 연결했다.
기존 PostgreSQL 생성/연결/초기화 구현과 db 패키지의 PG TestMain을 제거/대체했다. 새 SQL 번역기·PG fallback·DB 선택 옵션은 없다.
실제 SQLite SQL 검사 17개와 Go 초기화 제어 흐름 6개 × race 10회를 통과했다. **새 Go SQLite 연결 테스트와 전체 앱 검증은 미실행**이다.
구체적인 저장 구조·범위 제한·재현 명령은 `docs/sqlite-business-store.md`에 기록했다.

**중요: main의 주 업무 DB는 여전히 PostgreSQL이고, 작업 브랜치도 아직 실행 가능한 전체 앱이 아니다.**
브랜치의 `NewManager`는 기존 DSN을 넘기는 상태이며 새로운 파일 전용 `db.Open`으로 앱 부팅할 수 없다.
배열/JSONB/PG 잠금/시간/보관 복원 쿼리, 별도 LLM 테이블 생성 호출, `server.New` 오류 전파, 프록시 bind는 아직 이식해야 한다.
초기화 오류를 무시하거나 빈 결과로 바꿔 ready를 내보내지 않는다. 이 상태를 배포하거나 M2.2/M2.7 완료로 표시하지 않는다.
Electron 앱과 neobrutal-ui 적용 화면은 아직 없다. README의 Docker/PG 안내는 main의 현재 제품에 해당한다.

**즉시 다음 작업:** 같은 작업 브랜치에서 설정/모델/프롬프트/도구의 실제 쿼리와 시드를 새 스키마에 연결한다.
`tools.agents` → `tool_agents`, 모델 참조 삭제/바인딩의 PG 행 잠금 제거, LLM 기록/사용량의 별도 PG DDL 제거를 먼저 진행한다.
`NewManager/New`가 호출하는 작업·재검증·로그·알림 초기화를 함께 이식한 뒤 실제 부팅을 연결한다.
이후 자산/범위, 작업/탐색, 증거/보관 이식을 완성해 M2.7 검증 후 main에 병합한다.
조사 도구나 완료한 연결/설정 fixture를 반복 구현하지 않는다. 이전 modernc 미실행 검사도 해당 호출자를 이식하면서 실행한다.

**검증 정책:** 사용자 요청대로 Actions 실행·재실행 없이 로컬 검사로 진행한다.
`verify`/`sqlite-foundation`은 수동 실행 전용이며 별도 명시 요청 없이 실행하지 않는다. 개발 커밋은 `[skip ci]`를 사용한다.
로컬 제약을 원격 runner/편집 workflow/artifact 업로드로 우회하지 않는다. 기존 artifact/cache/사용자 데이터를 임의 삭제하지 않는다.
이번에는 기존 화면 자료를 읽기 전용으로 내려받았을 뿐 새 Actions나 artifact를 생성하지 않았다. 릴리스 태그도 만들지 않는다.

## M0. 방향과 작업 기준

- [x] PRODUCT/아키텍처/개발 절차/에이전트 읽기 순서 작성.
- [x] UI·Go 재사용, SQLite 단일 지원, PG 자동 데이터 이전 제외 확정.
- [x] 기존 트래픽 SQLite와 PG 업무 저장소/LLM 기록 구분.
- [x] neobrutal-ui 필수 UI 기준 및 `docs/ui-design.md`의 원본 SHA/API 차이/전 화면 검수 조건 확정.

## M1. 데스크톱 실행 기반 — 이전 구현·검증 완료

- [x] ARTEX_HOME 절대 경로/쓰기 검사, 설정/data/skills 연결. CWD의 다른 DB 설정 사용 금지.
- [x] loopback 기본 주소와 실제 동적 포트, 저장소/HTTP 준비 뒤 ready JSON.
- [x] 부모 stdin EOF/오류 종료, cmd 치명적 종료 제거와 manager/HTTP 정리.
- [x] 설정 7개와 HTTP/부모 파이프 6개 독립 테스트, race 반복 검사.
- [x] 당시 Go 1.26.3 전체 명령/빌드, 3개 OS 실행 기반 및 실제 Linux readiness/EOF 검사.

Electron, 주 업무 SQLite, 도구 설치/자손 프로세스 정리, 패키징, server.New 오류 반환은 M1 완료 범위가 아니다.

## M2. 업무 저장소 SQLite 이식

- [x] **M2.1 인벤토리:** Go/SQL 332개 조사, 후보 214개 분류. 기존 51개 업무 테이블 및 실제 부팅/직접 SQL/트랜잭션/fixture 지도 확정. 후보 수는 수정 파일 수나 동등성 증명이 아니다.
- [ ] **M2.2 실행 가능한 업무 저장 기반:** 전체 SQLite 스키마/시드/쓰기/취소/닫기를 실제 NewManager/New 부팅과 연결한다. 미사용 저장소만으로 완료 처리하지 않는다.
- [x] **M2.2a 공통 연결/트래픽:** 기존 main에서 URI 특수문자/연결별 PRAGMA/WAL/취소/파일 보존/재열기/FTS 검증 완료. 브랜치의 OpenImmediate 변경은 별도 통합 검증 필요.
- [x] **M2.3a 설정/인증 저장:** 실제 설정 메서드와 HTTP fixture의 재열기/동시 설정 단일 승자/fail-closed 검증. 전체 앱 부팅은 아니다.
- [ ] **M2.2b/M2.3b 프롬프트·모델:** 실제 저장 원자성/없는 행 수정·조회 오류 구현. 이전 Go modernc 7개, 기존 PG 회귀/전체 빌드는 미실행이었다. 참조 삭제/바인딩 이식은 남아 있다.
- [ ] **M2.2c 부팅 오류/설정 일관성:** manager 동기 오류/실패 정리, snapshot 복원/설정 묶음 저장 구현. 로컬 독립 Go 11개와 SQL 7개 통과. 신규 통합 10개/전체 빌드 미실행 상태를 유지한다.
- [ ] **M2.2d 업무 스키마/원자적 생성:** 작업 브랜치에 58개 테이블, 기본값, 식별자/버전/생성 트랜잭션, bounded pool/IMMEDIATE 구현. SQL 17개 및 Go 제어 6개 검증. Go SQLite opener 통합 6개·정규식 DB 테스트·전체 앱은 미실행이다.
- [ ] **M2.3 설정·인증 통합:** setup/로그인/모델 저장·복원을 앱 부팅에 연결. API/인증 정책 유지, JWT 작업 공간 분리, 모든 생성 오류 반환과 호출자 정리 검증.
- [ ] **M2.4 자산·범위:** 관계 테이블 사용, DSL, net/netip 정규화, IPv4/IPv6 경계/범위, 기업 귀속/중복 원자성 검증. 현재 SQL용 범위 fixture만으로 완료 처리하지 않는다.
- [ ] **M2.5 작업·탐색:** 작업/세션/의도/대화/사실/취약점/승인/재검증/사용량/기록 및 다중 에이전트 저장·취소 검증.
- [ ] **M2.6 증거·보관:** 업무 DB와 트래픽/본문/보관 연결, 삭제/재검증/증거 보존 동등성 검증. PG json_populate_record/배열/시퀀스 복원을 명시적 열/키 매핑으로 교체한다.
- [ ] **M2.7 통합 전환:** PG 없이 setup → 로컬 모델 fixture → 작업/기록 → 증거 → 종료/재실행 검사. 기존 PG 테스트를 SQLite로 옮기고 skip을 제거한다. PG driver/config/서비스/smoke 조건/남은 헬퍼와 전환 인벤토리 도구/job 제거 후 병합한다.

main의 기존 PG는 SQLite 오류 fallback이 아니다. 구 PG 데이터/보관 형식 변환기나 임시 호환 계층을 만들지 않는다.
사용자 데이터·설정·증거·볼륨을 삭제하거나 덮어쓰지 않는다.

## M3. Electron 실제 UI + neobrutal-ui

- [ ] **M3.1 공통 UI:** 최신 원본 토큰/타이포그래피/버튼/입력/탭/카드/표/오버레이 적용. primitive와 호출부 함께 교체, 출처/저작권 보존.
- [ ] **M3.2 실제 화면:** 셸/사이드바, 대시보드/대화/작업/자산/증거/트래픽/설정 전체. 빈/로딩/오류/선택/비활성/승인 상태와 라우트별 화면 검수.
- [ ] **M3.3 Electron:** 메인/렌더러 분리, 패키지/lockfile, Go/정적 UI 재사용. Next 서버를 추가하지 않는다.
- [ ] 단일 인스턴스/userData/자식 ready/장애 화면/정상 종료/부모 강제 종료.
- [ ] loopback+앱 세션 인증, Origin/Host, 제한된 preload/IPC/CSP/외부 탐색.
- [ ] **M3.4 검수:** 실제 패키지 setup → 설정 → 작업 → 증거 → 재시작. 라이트/다크·1280/1440px·Windows 배율·IME/키보드/축소 모션/긴 텍스트와 스크린샷 확인.

독립 쇼케이스나 대시보드 일부로 전체 UI 완료를 표시하지 않는다. DB 동등성 검증과 UI 스타일 변경은 별도 단위로 수행한다.

## M4. Docker 도구 환경 대체

- [ ] 셸/PTY/Python/Node/브라우저/MCP/CLI 및 OS별 지원표 조사.
- [ ] 필요한 도구만 앱 전용 경로에 버전 고정 설치. 출처/해시/라이선스/실패 복구 검증.
- [ ] 필수 도구 미준비 시 실행 차단·neobrutal-ui 안내. 관리된 PATH/작업 디렉터리 전달.
- [ ] 네트워크/작업 공간/권한 정책 및 모든 자손 프로세스 정리를 OS별 검증.

## M5. 데이터 신뢰성·배포·구 경로 제거

- [ ] 동시 에이전트+트래픽 부하의 잠금 오류/지연/검색 측정.
- [ ] 정상 종료/kill/디스크 오류/손상 감지, 일관된 DB+증거 백업/복원.
- [ ] Electron 업데이트 일원화. 기존 Go 자체 업데이트 API/시작/업데이트 스크립트 제거.
- [ ] Dockerfile/Compose/PG 안내·CI·불필요 의존성 제거. 사용자 데이터/볼륨은 삭제하지 않는다.
- [ ] Windows 우선 설치/서명/업데이트, macOS/Linux 실제 지원 검증.
- [ ] README 새 설치 방법. 완료 전 Docker-free/SQLite 전환 완료를 표방하지 않는다.
- [ ] neobrutal-ui 누락/구 스타일/중복 토큰/호환 래퍼 전 화면 확인.

## 이전 검증 기록

상세 원본은 main `33301d8dd28f709fa98897eb6e7e58708d24315f`의 이 문서에 보존되어 있다. 아래는 이전 결과이며 새 코드의 통합 성공이 아니다.

- **M1:** 코드 `4acff4563df22a2094b7adcc8e4fde7d05267a00`, verify #55/run 37213127038의 6개 job 성공. 로컬 Go 1.23.2 설정/HTTP/EOF 13개 × race 20회, 당시 Go 1.26.3 전체 명령/빌드/실제 smoke 포함.
- **M2.1:** 코드 `a99972d4e8ff1eaec333aad05874374ef7c95510`, verify #56/run 37214597987의 7개 job 성공. 인벤토리 artifact 11308130499.
- **M2.2a/M2.3a:** main `81757868533bcb7b4ba15186bfc326d6ffc8f898`, PR HEAD `4460f0f32790bbf4fb14c8d3837e48f3f7875fcc`, 시험 checkout `294840749fd3057aa4ceba7fd3baca54ba7d1f48`. verify #59/run 37218191195의 7개 및 sqlite-foundation #4/run 37218191191의 3개 OS 성공. artifact 11309266073 원본은 694 pass/0 fail/1 skip(외부 모델 설정 필요 TestLiveContextReview). 신규 18개는 모두 실행. metadata 테스트의 불필요한 다른 작업 실행 문제도 수정·검증했다.
- **M2.2b/M2.3b:** 코드 `0545731a1a0f6ab222304df73c9e1cd04df83dc0`, Actions 미사용. 초기 프롬프트/버전/현재 포인터 단일 저장, 모델 조회/수정 오류 개선. 원본 63개 선언 보존. Go 제어 8개 사례 × race 20회 및 Python SQLite 3.46.1 SQL 13개 성공. modernc 7개/전체 빌드·PG 회귀·native OS 미실행.
- **M2.2c:** 코드 `33301d8dd28f709fa98897eb6e7e58708d24315f`, Actions 미사용. manager 초기화 오류/닫기, 설정 snapshot, 웹 검색/동시 작업 제한의 원자적 저장·게시 순서 구현. 독립 Go 7+4개 × race 20회, SQL 7개 성공. manager 원본 97개 선언 보존. 새 db 통합 4개/server 통합 6개 및 이전 modernc 7개는 미실행. SetTrafficEnabled/SetGlobalProxy와 MCP 변경 전체의 원자성, 비동기 proxy bind, server.New는 미완료였다.

예전 Actions artifact는 당시 14일 보관 설정이며 확인을 위해 재실행하지 않는다. 프로젝트 Go 버전을 낮추지 않는다.

## 이번 검증 기록 — M2.2d

코드/검사 범위는 `work/sqlite-business-store`, 기준 main `33301d8dd28f709fa98897eb6e7e58708d24315f`다.

```sh
python scripts/check-sqlite-business.py
GO111MODULE=off go test -race -count=10 -timeout 60s db/schema_init.go db/schema_init_control_test.go
```

- Python SQLite 3.46.1 **17개 모두 통과**: 58개 테이블/필수 열, 시드, 생성 실패 전체 롤백, 한글 경로 재열기, 활성 모델 유일성, 순환 FK, 관계 삭제, IP 범위, 재검증 이력/증거, JSON/불리언/키, ordinal, 재귀 트리거, 보관 원본 ID, WAL/24건 동시 저장, FK/integrity.
- Linux Go 1.23.2 **최상위 6개 × race 10회 통과**: 새 생성/재열기 분기, 초기화 각 단계 실패, 다른 DB/미지원 버전 거부, 필수 테이블 누락, begin/commit 오류, 취소. 실제 schema_init.go를 사용하지만 SQL driver는 제어 흐름용 scripted driver다.
- gofmt 검사 통과. 원격 반영한 코드 blob SHA를 검사한 로컬 파일과 대조한다.
- **미실행:** 새 Go SQLite opener 6개와 정규식 DB 테스트, 이전 modernc 17개, 전체 Go 1.26.3 빌드/회귀, 실제 앱/Windows/macOS/Electron/도구/부하/백업.
- 지정 Go 1.26.3 다운로드를 시도했지만 proxy.golang.org DNS 연결 거부로 실패했다. Actions로 우회하지 않았다.

## 현재 UI 확인 — 새 스타일 적용과 구분

이번에는 기존 한국어 검수 artifact 11303654863의 tasks/LLM 등 스크린샷을 사용자에게 보여줬다.
화면의 작업·수치·모델은 mock 샘플 데이터이며 실제 외부 대상이나 모델 호출 결과가 아니다.
같은 검수의 소스 artifact 11304555731에서 계산한 web tree SHA는 `d74ddad9a32a64e3a07b15f493207efedc5ade09`다.
이 값은 기준 main의 web tree와 동일하다. 최신 UI 소스와 일치하는 **기존 캡처**이지 이번에 전체 앱을 새로 실행해 촬영한 화면이 아니다.
현재 화면은 둥근 카드/얇은 테두리의 기존 스타일이며, neobrutal-ui 적용본이나 Electron 패키지 화면이 아니다.
같은 web 소스에서 Node 22.16.0의 `check-korean.cjs`와 `check-input.cjs`를 로컬 실행해 통과했다(중국어 런타임 문구 0건, 입력 조합 10개/편집기 4개).
Next 의존성/정적 UI 번들이 없어 이번 환경에서 새 전체 UI 빌드·브라우저 캡처는 수행하지 못했다.
기존 artifact를 읽기만 했고 새 Actions 실행/재실행/업로드는 없다.
