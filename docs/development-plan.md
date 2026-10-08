# 개발 계획 — 유일한 현재 작업 목록

목표: **기존 ARTEX + Electron + Go + SQLite**, Docker/PG 없는 로컬 데스크톱 앱.
UI 기준: **[andongmin94/neobrutal-ui](https://github.com/andongmin94/neobrutal-ui)**. 기존 기능·한국어·화면 흐름을 유지하고 시각·상호작용을 통일한다.
작업 기준: **main 단일 브랜치**. 최종 갱신: 2026-10-07.

## 현재 상태 — 2026-10-07 비밀번호 없는 Electron 진입

사용자 요청으로 Electron의 비밀번호 설정/로그인 화면을 앱 세션 인증으로 교체했다.
미커밋 변경이 없는 main/원격 `6c8183f8cf6a4e25697a7718e1b816882c290445`에서 시작했다.
Go가 정확한 앱 세션 키·바인딩 Host·Origin을 검증한 요청에만 기존 형식의 JWT를 발급한다.
업무 화면은 JWT 발급/저장 뒤 표시하며, 연결 실패는 한국어 오류와 재시도로 남긴다.
새 데이터 홈과 기존 비밀번호 데이터 모두 바로 열리고 기존 비밀번호/작업/증거/스킬은 변경하지 않는다.
Go 단독 브라우저 접속은 비밀번호 설정·로그인·이용 안내를 유지한다. 승인·차단·대상 범위와 일반 API JWT 검증도 유지한다.
두 프로필 메뉴에서 Electron의 비밀번호 변경/로그아웃을 숨겼고 mock 실행 모드와 실행 안내를 함께 갱신했다.

변경 파일: `server/{auth.go,desktop_session.go,server.go,auth_desktop_test.go}`, 웹의 공통 인증 준비/상태/모드 저장과 API/types/mock·layout·login/setup·두 프로필 메뉴,
`desktop/tests/{desktop.spec.cjs,standalone.spec.cjs}`·`package-tests/portable.spec.cjs`, README 두 개·architecture·ui-design·이 계획.
이 단위의 검증 결과는 아래에 기록한다. 다음 구현 시작점은 기존 M4의 앱 전용 도구 런타임이며 정식 릴리스 상태는 아니다.

### 자동 진입 단위의 로컬 검증

| 검사 | 실제 결과와 범위 |
| --- | --- |
| 지정 Go/modernc | 인증·앱 세션 집중 검사와 `go test -count=1 -timeout 180s ./server` 통과(전체 server 39.7초). 새 홈/정상·빈·잘못된 기존 비밀번호 보존, 실제 저장소 오류 전파, 앱 세션·Host/Origin·JWT와 standalone 차단 검사. |
| 정적 검사/빌드 | gofmt·`go vet ./server`·diff 검사 통과. web 타입·한국어·입력 검사·unit8개·신규 인증 파일 Biome 통과. 최종 소스로 31개 페이지 정적 export와 실제 Windows Go/embedui·CSP·라이선스·실행 패키지 빌드 통과. |
| 실제 Electron + 일반 Chrome | 최종 `npm run test:electron` 3개 통과43.4초·실패0·skip0. 자동 진입·두 프로필 메뉴·잘못된 토큰 교체·인증 실패 재시도·JWT 없는 API 거부·업무/증거/보관/복원/기존 데이터 재시작·21개 화면·격리·프록시 충돌 재시도, Go 단독 setup/비밀번호/약관/로그인/정상 종료 포함. 설치된 Chrome은 검사 도구이며 앱 런타임 준비 성공이 아니다. |
| 실제 Windows 실행 패키지 | 최종 `npm run test:package` 1개 통과3.2초·실패0·skip0. `app.isPackaged`의 새 홈/기존 비밀번호 자동 진입, SQLite·사용자 스킬 보존·API 경계·Go 종료·라이선스·미준비 상태 검사. |
| 로컬 증거 | 최종 결과106 PNG와 standalone trace·프로세스 기록·임시 SQLite를 로컬에 보존. 이전 패키지/검사 결과와 실패 trace도 별도 경로에 보존하며 커밋/원격 업로드하지 않는다. |

초기 패키지 생성은 기존 출력 보호로 중단됐다. 기존 폴더를 보존한 뒤 재빌드했다.
새 Playwright 검사의 헤더 메뉴 선택자·중복 trace 시작·Next route announcer와 오류 alert 선택 충돌을 수정한 뒤 최종 검사가 모두 통과했다. 실제 실패를 skip으로 바꾸지 않았다.
이번 단위에서 전체 `./...`·Linux/macOS 교차 빌드는 반복하지 않았으며 아래 이전 단위 결과와 구분한다. race(C 컴파일러 없음)·다른 OS 실제 실행·물리 IME/OS DPI·M4 도구/백업/설치/서명/업데이트는 미실행이다.
Actions 실행/재실행·새 자동 트리거·원격 artifact 업로드·태그·배포는 하지 않는다. 원격 반영 SHA는 완료 보고와 이 단위의 Git 커밋에서 확인한다.

## 이전 단위 — 2026-10-07 실제 SQLite/Electron 연결

이전 브랜치 통합은 main `93f2179928bf735e7ba3570d6a0662dabe7863ad`에 완료됐다. 과거의 미완성 전환 별도 브랜치 유지 지침은 종료한다.
통합 전 main·업무 전환·foundation의 이력은 해당 커밋의 부모와 이전 기록에 보존돼 있다.
이번 작업은 미커밋 변경이 없는 최신 main에서 시작했다. 옛 ZIP/패치나 잔존 work 브랜치를 작업 기준으로 선택하지 않는다.

Go 1.26.3과 modernc 1.52.0을 확보하고 실제 업무 SQLite 전환을 앱에 연결했다.
NewManager는 data/artex.sqlite를 사용하고 server.New는 필수 초기화 오류를 반환하며 자원을 정리한다.
작업·자산·회사 범위·탐색·취약점·재검증·증거·보관·알림·부가 질문의 실제 호출 SQL을 이식했다.
PG 드라이버/DSN/보정 DDL/테스트 fixture·skip, Go 자체 업데이트, Docker·시작 스크립트는 제거했다.
Electron은 정적 한국어 UI/Go를 재사용하고 앱 세션·Host/Origin·JWT·CSP·렌더러 격리·부모 EOF·장애 UI를 연결했다.
neobrutal-ui Mono와 로컬 Noto Sans KR/OFL을 적용했다.

**개발 검증 상태이며 정식 릴리스가 아니다.** 도구 런타임 번들/OS 격리·자동 일관 백업·설치/서명/업데이트와 macOS/Linux 실제 지원은 미완료다.
없는 도구를 준비 완료로 표시하거나 host PATH로 우회 실행하지 않는다.

이번 시작에서 원격 heads는 main만 남았음을 실제 확인했다. 시작 main/원격 SHA는 93f2179928bf735e7ba3570d6a0662dabe7863ad이며 미커밋 변경은 없었다.
사용자 DB/증거/설정/볼륨, 릴리스 태그, artifact/cache를 정리 대상으로 삼지 않는다.

## 이전 M2.2i — 작업 모델 목록과 전환 저장

- `db/task_context.go`의 실제 목록 교체/할당량 소진 전환에서 PG 행 잠금과 now()를 제거했다. 기존 OpenImmediate의 짧은 쓰기 트랜잭션 안에서 검증/관계 변경/현재 모델/리비전을 함께 저장한다.
- 명시적 `?N` 매개변수를 사용한다. 일괄 조회의 PG 배열은 int64 목록을 JSON TEXT로 바인딩한 json_each 조회로 교체했다. 세 일괄 조회는 유지하며 N+1 조회나 이중 저장을 추가하지 않았다.
- 모델 전환 도중 SELECT/UPDATE/COMMIT이 실패하면 반환하는 전환 구조체를 비운다. 커밋되지 않은 Advanced/NextProfileID를 성공한 전환으로 반환하지 않는다.
- 이전 리비전의 늦은 오류는 새 목록을 바꾸지 않는다. 현재 커서 뒤의 ready 모델만 고르고, 소진 후 앞의 모델을 되살리지 않는 동작을 유지한다. 삭제된 작업의 늦은 전환과 음수 활성 모델 입력을 거부한다.
- 같은 파일의 의도 차단/재개 시간 SQL과 RowsAffected 오류 전파를 이식했다. 기존 대상/승인/차단 정책이나 도구 실행 본문은 변경하지 않는다.
- PG 프로세스/행 잠금 관찰에만 의존하던 `task_context_lock_test.go`를 제거하고 SQLite 경합/원자성 검사로 대체했다. 기존 모델 참조의 SQLite 테스트도 유지한다.

위 단위의 실제 Go/modernc 검사는 이번에 통과했다. 설정/도구/모델 참조/재검증 시드/LLM DDL 제거/모델 목록 저장을 다시 만들지 않는다.
다음 시작점은 아래 M4의 관리된 도구 런타임 배포와 OS 격리다. SQLite/Electron 회귀 검사를 먼저 유지한다.

## 검증 자원 정책

사용자 명시 요청 없는 Actions 실행/재실행/자동 트리거 추가, 임시 workflow/원격 artifact 업로드 금지.
verify/sqlite-foundation은 수동 전용이다. 개발 커밋은 `[skip ci]`, 태그/배포 없음.
지정 Go 버전/드라이버를 낮추거나 교체하지 않는다. SQL/격리 검사와 실제 드라이버/전체 앱 검사를 구분한다.

## M0/M1 — 이전 완료 범위

- [x] 제품/아키텍처/작업 절차, SQLite 단일 지원, PG 자동 이전 제외, neobrutal-ui 필수 기준.
- [x] ARTEX_HOME 절대 경로/쓰기 검사, config/data/skills, 다른 CWD 설정의 암묵적 재사용 금지.
- [x] loopback/동적 포트/ready JSON, 부모 stdin EOF 종료, cmd 치명적 종료 제거와 정리.
- [x] 당시 설정/HTTP/실제 backend smoke 및 3개 OS 실행 기반 검증.

이전 성공은 현재 통합 main의 빌드/전체 앱 성공이 아니다.

## M2. 업무 저장소 SQLite 이식

- [x] **M2.1 인벤토리:** 기존 51개 업무 테이블/시작/직접 SQL/트랜잭션/fixture 지도.
- [x] **M2.2 실행 가능한 기반:** 전체 스키마/시드/쓰기/취소/닫기를 실제 NewManager/New에 연결, native 시작/오류 전파 검사.
- [x] **M2.2a 연결/트래픽:** 이전 경로/PRAGMA/WAL/취소/재열기/FTS 검증. OpenImmediate 전체 통합은 별도.
- [x] **M2.3a 설정/인증 저장:** 실제 메서드/HTTP fixture의 재열기/동시 최초 설정/fail-closed. 전체 부팅 아님.
- [x] **M2.2b/M2.3b 프롬프트·모델:** 실제 드라이버 원자 저장/조회·수정 오류 검사.
- [x] **M2.2c 부팅/설정:** manager 오류/정리, 묶음 저장, 실제 server.New 생성/부팅 검사.
- [x] **M2.2d 스키마:** 58개 테이블/시드/식별자/버전/IMMEDIATE 실제 Open·동시 초기화·취소.
- [x] **M2.2e 도구/MCP:** 관계 CRUD/집계/원자성/조건부 MCP 저장의 native 검사. 외부 도구 배포는 M4.
- [x] **M2.2f 모델 참조/대화:** 모델 삭제/에이전트·대화 연결/대화 저장의 native 검사.
- [x] **M2.2g 재검증 시드:** FK 순서/단일 트랜잭션/사용자 편집·삭제 보존, 실제 시작 검사. reporter도 원자화.
- [x] **M2.2h LLM 기록/사용량:** 실제 opener/Recorder·검색·UTC 집계·원장 보존 검사.
- [x] **M2.2i 작업 모델 목록:** 기존 실제 SQLite 6개와 오류 주입 제어 검사를 지정 Go에서 실행.
- [x] **M2.3 설정·인증 통합:** 실제 Electron 앱 세션 자동 진입/모델 저장·재실행, Go 단독 브라우저 setup/로그인, JWT 오류/정리/앱 세션 정책.
- [x] **M2.4 자산·범위:** 관계/DSL/net/netip, IPv4·IPv6 경계/기업 귀속/중복과 실제 다중 풀 경합 검사.
- [x] **M2.5 작업·탐색:** native 작업/의도/대화/사실/취약점/승인/재검증/사용량/기록/취소 검사.
- [x] **M2.6 증거·보관:** 형식4의 열·키·27개 테이블/행 수/JSON/stream 검증, 큰 ID와 공유 자산 병합, 오류 주입·rollback·재시도 및 실제 Electron 증거 보관/복원/재실행 통과.
- [x] **M2.7 통합:** PG 없이 로컬 모델 fixture → 작업/기록/330KB 트래픽/취약점 증거 → 보관/복원 → 종료/자동 진입/재실행 보존. PG fixture/skip/driver/config/서비스/smoke/헬퍼/구 배포 도구 제거.

DB 선택 스위치/SQL 번역기/PG fallback/구 데이터 자동 이전기/임시 호환 계층은 추가하지 않는다.

## M3. Electron 실제 UI + neobrutal-ui

UI 지침과 최신 지정 원본을 읽었고 원본 SHA b4da2463fe710a77bf464c65125a1a7f40424722가 동일함을 확인했다.

- [x] 공통 Mono 토큰/로컬 한글 글꼴/컨트롤/표/오버레이와 호출부 연결, MIT/OFL/기존 저작권 보존.
- [ ] 셸/전체 업무 화면의 기본 21개 페이지 ×2모드×2폭 검사는 통과. 모든 데이터·오류·승인 상태의 시각 검수는 미완료.
- [x] Electron 메인/렌더러 분리, lockfile/Go/정적 UI, 단일 인스턴스/userData/ready/장애/정상 종료.
- [ ] 앱 세션/Origin/Host/IPC/CSP/탐색·부모 EOF 검사는 통과. OS 강제 kill·자손 정리는 M4와 연결해 추가 검증.
- [x] 서명되지 않은 Windows 실행 패키지의 실제 resources/userData/Go/SQLite 부팅·재실행·사용자 스킬 보존·라이선스 검사.
- [x] 실제 Electron의 21개 페이지 × 라이트/다크 × 1280/1440px, 작업 상세 10개 탭, 취약점 증거, 키보드·DOM composition·125/150% Electron 확대·축소 모션 검사.
- [ ] 물리 Windows IME·OS DPI·모든 데이터/오류/승인/오버레이 상태의 수동 시각·접근성 검수. DOM composition/Electron 확대 검사와 구분한다.

빈 창/일부 쇼케이스/mock 캡처로 전체 제품 또는 UI 완료를 표시하지 않는다.

## M4/M5. 도구·신뢰성·배포

- [ ] OS별 셸/PTY/Python/Node/브라우저/MCP/CLI, 전용 경로·버전·출처·해시·라이선스·복구/준비 상태 차단.
- [ ] 권한/작업 공간/네트워크/자손 프로세스, 다중 기록 부하, kill/디스크 오류/손상, 일관된 DB+증거 백업/복원.
- [x] Go 자체 업데이트/시작 스크립트/Docker/PG 안내·의존성·구 배포 workflow 제거. 사용자 데이터 보존. Electron 자동 업데이트 배포는 별도 미완료.
- [x] README와 개발 안내를 현재 Electron/SQLite 빌드·검사·데이터 경로·미완료 상태로 교체.
- [ ] Windows 설치/서명/업데이트, macOS/Linux 실제 지원 검증, 전체 스타일 확인.

## 이전 단위의 로컬 검증 — 2026-10-07 SQLite/Electron 연결

소스 기준은 main `93f2179928bf735e7ba3570d6a0662dabe7863ad`와 이번 변경이다. 지정 Go 1.26.3 Windows amd64 배포물을 공식 SHA256과 대조했고 modernc 1.52.0·norma 0.4.3을 유지했다.

| 검사 | 실제 결과와 범위 |
| --- | --- |
| 전체 native Go | 최종 구현 동결 후 `go test -json -count=1 -timeout 180s ./...` 종료0. 18개 테스트 패키지, 최상위788개 통과·2개 skip·실패0. 하위 테스트 포함1,196개 통과. |
| 추가 archive/증거/연결 회귀 | 최종 archive 무결성 수정 후 `./db ./evidence ./internal/sqlitedb` 모두 통과. malformed manifest/JSON, 늦은 stream 행 수 오류 rollback, 2^53 초과 ID, 공유 자산 관계 병합 포함. |
| Go 정적 검사/빌드 | `go vet ./...` 통과. 실제 Windows embedui 빌드와 backend lifecycle smoke 통과. Linux amd64/macOS arm64는 교차 빌드만 통과, OS 실행 검증 아님. |
| Python SQL | Python 3.12.14/SQLite 3.53.1에서 6개 검사 스크립트 총87개 통과. 실제 modernc 검사와 별도. |
| web | TypeScript·한국어 텍스트·입력 정적 검사·unit8개·정적 export31페이지·추가 상태 UI Biome 통과. web/desktop npm audit 취약점0. |
| 실제 Electron Playwright | Electron 44.6.0/Playwright 1.63.0, 초기 탐색 오류 수정 후 2개 통과41.5초·실패0·skip0. 로컬 HTTP/모델 fixture, 업무 흐름·보관 복원·재시작·격리·단일 인스턴스·프록시 충돌 후 실패/재시도 포함. |
| 실제 Windows 실행 패키지 | 최신 main.cjs를 포함한 `app.isPackaged` 경로의 Playwright 1개 통과5.7초·실패0·skip0. setup/저장/종료/로그인·사용자 편집·실제 Go 종료·라이선스 확인. |
| 패키지 신규 홈 연속 부팅 | 서로 다른 새 데이터 홈 3회 모두 실제 setup 도달·종료0·HTTP 종료·Go PID 소멸. 두 번째 실행에서 실제 ERR_ABORTED(-3)를 다시 발생시켜 정상 이동 처리까지 확인. |
| 로컬 시각 증거 | 총103 PNG: Electron99, 시작 실패2, 패키지2. trace·임시 SQLite와 함께 로컬 검사 결과에만 보존하며 커밋/원격 업로드하지 않는다. |

전체 Go의 skip은 외부 비공개 모델 설정이 필요한 `TestLiveContextReview`와 이 Windows 계정의 symlink 생성 권한이 없는 `TestTaskArchivePackageSkipsSymlink`다. `report`는 테스트 파일이 없는 패키지다. PG 미설정으로 건너뛰는 검사는 없다.
Python 실행 파일은 `ARTEX_TEST_PYTHON`으로 실제 경로를 지정해 확인했다. Windows App Execution Alias만으로 도구 준비 성공을 인정하지 않는다.
race는 Windows C 컴파일러가 없어 미실행이다. Actions 실행/재실행·새 자동 트리거·원격 artifact 업로드·태그·배포는 하지 않았다.

최신 실행 파일 재검사에서 최초 HTTP 로드와 정적 UI의 root→작업→로그인/설정 이동이 겹치며 Electron이 종료되는 문제를 재현했다. 실제 같은 백엔드·동일 origin·메인 프레임의 후속 이동이 확인된 ERR_ABORTED만 정상 이동으로 처리했다. 다른 로드/초기화 오류는 실패로 남긴다. Playwright에는 프로세스 종료 코드·stdout/stderr·창 종료·탐색·마지막 상태 기록을 추가했고, 닫힌 창을 재시도 대기로 숨기지 않는다. 실패 실행의 trace와 임시 DB는 로컬에 보존했다.

업무 DB의 보관 복원과 증거 메타데이터는 같은 IMMEDIATE 트랜잭션에서 rollback한다. 별도 트래픽 저장소/본문 파일은 먼저 준비하므로 실패 시 재시도/GC용 자료가 남을 수 있다. 모든 저장소·파일을 아우르는 하나의 원자적 트랜잭션으로 표현하지 않는다.
go-mitmproxy의 public API에 없는 내부 attacker goroutine 종료는 Go 프로세스 종료에 의존한다. DNS 작업 종료 대기는 최대12초이며 Electron은 종료 대기8초 뒤 프로세스를 종료한다. 자손 도구의 강제 종료/OS 격리는 미검증이다.

다음 시작점은 **M4의 앱 전용 도구 런타임**이다. Windows 우선으로 도구의 버전·출처·해시·라이선스를 고정하고 실제 경로/실행·프로세스/네트워크 격리를 검증한 뒤 현재 미준비 상태와 실행 차단을 해제한다. 일관된 DB+증거 백업/복구와 설치·서명·Electron 업데이트 검증을 이어간다.

## 이전 로컬 검증 — M2.2i

원본 `task_context.go` blob `c1d08d3fe162a390cea4fc5e71b2a7adba01cf5f`를 live GitHub에서 읽고 로컬 SHA를 일치시킨 뒤 수정했다. 옛 ZIP/패치 사용 없음.

- **Go 1.23.2 Linux 제어 검사: 최상위 3개 + 하위 11개, 각각 race 10회 통과.** 실제 수정 task_context.go와 오류 주입용 database/sql driver, 필요한 DB/Task/ExplorationStore 경계 타입 및 실제 insertTaskLLMProfiles helper를 격리했다. 전체 프로젝트/실제 SQLite 검사가 아니다.
- begin/조회/소진 기록/다음 모델 선택/현재 모델 갱신/commit 오류에서 전환 반환값이 비워지는지, 성공/오래된 리비전/소진 상태/UTF-8 길이/음수 활성값을 검사했다. 실패/skip 없음.
- **Python SQLite 3.46.1 SQL 검사 10개 통과.** 실제 Go SQL과 변경 없는 9개 CREATE TABLE을 사용했다. 로컬 부분 체크아웃에는 원본 스키마의 선택 DDL과 실제 tasks.go helper를 명시적 인자로 제공했다.
- 전진 커서/소진 모델 제외/늦은 오류/같은 ID로 목록 교체, 중간 실패 롤백, 12개 동시 오류의 단일 전환, 삭제된 작업, WAL 읽기/재열기, 2^53보다 큰 정수 ID의 일괄 조회, 의도 차단·재개 범위와 FK/integrity를 확인했다.
- Python helper의 `$N` 매개변수에는 이름별 매핑을 사용해 경고 없이 재실행했다. SQL 문자열을 다른 SQL로 번역하지 않았다.
- gofmt 및 원본/변경 파일 대조. 신규 native 테스트 6개는 작성만 했고 실행하지 않았다.

**미실행:** 신규 실제 Open/full-schema/seed/modernc 테스트 6개, 이전 modernc 검사, 전체 Go 1.26.3 빌드/회귀, NewManager/New/HTTP/OS별/Electron/UI/도구/전체 부하/백업.
지정 Go 1.26.3 다운로드는 이번에도 proxy.golang.org DNS 연결 거부로 실패했다. 버전을 낮추거나 Actions로 우회하지 않았다.
세 일괄 조회 전체의 하나의 스냅샷 보장은 추가하지 않았다. 개별 SQL 및 모델-커서 한 문장의 일관성과 전체 앱 검증을 구분한다.

```sh
python scripts/check-sqlite-task-context.py
# 지정 Go와 의존성이 있는 환경에서 실행할 미검증 native 범위:
go test -race -count=1 -timeout 120s -run '^(TestTaskChainControl|TestSQLiteTaskChain)' ./db
```

## 이전 기록

M2.2h와 이전 검증/미실행 기록은 `fcff01296af3e1f0e8f4677da312d858b3fcba59`의 이 문서 및 이전 커밋에 보존한다.
과거 CI 성공을 현재 통합 버전의 성공으로 재사용하지 않는다. 검증 확인을 위해 Actions를 재실행하지 않는다.
