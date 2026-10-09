# 개발 계획 — 유일한 현재 작업 목록

목표: **기존 ARTEX + Electron + Go + SQLite**, Docker/PG 없는 로컬 데스크톱 앱.
UI 기준: **[andongmin94/neobrutal-ui](https://github.com/andongmin94/neobrutal-ui)**. 기존 기능·한국어·화면 흐름을 유지하고 시각·상호작용을 통일한다.
작업 기준: **main 단일 브랜치**. 최종 갱신: 2026-10-09.

## 현재 단위 — 2026-10-09 Windows 도구·백업·설치 연결

main `e72ddd27a74f84f11473898f1b2ad00cc0e50715`의 미커밋 변경이 없는 상태에서 시작했다. 사용자 요청으로 남은 구현을 연결한다. 기존 한글화 시작 커밋과 원본 v0.3.15의 부모 관계는 바꾸지 않는다.

- [x] 공식 고정 버전 Windows x64 도구 묶음, manifest·전체 파일 해시·추가 파일·링크 검증과 host PATH 우회 차단
- [x] AppContainer 작업 폴더·네트워크 차단과 Job Object 자손 정리, PowerShell·ConPTY·Python·Node·Git·stdio MCP의 실제 Go 호출부 연결
- [x] 정상 종료/강제 종료 WAL의 실제 modernc 백업·무결성·증거 참조 검사, 새 폴더 복원과 원본 보존
- [x] 최종 Electron의 백업/복원·중복 종료·유지 작업 실패·새 홈 재시작 및 기존 업무/UI 회귀
- [x] 최종 Windows 실행 패키지의 임시 설치·업그레이드·제거·실제 Go/UI 준비·기존 데이터 보존
- [ ] 공인 Windows 코드 서명·운영 HTTPS 업데이트, macOS/Linux 실제 실행, 물리 IME/OS DPI와 전체 상태 수동 검수

PowerShell 7.6.6·Python 3.14.8·Node.js 24.21.0·MinGit 2.56.0.2와 Chrome for Testing 153.0.8010.12를 공식 ZIP의 고정 SHA256으로 준비한다. 실제 셸·PTY·스크립트·MCP·Git은 지정 업무 폴더의 AppContainer 안에서 동작하며 일반 TCP와 호스트 인증 환경변수는 차단한다. 다른 작업/에이전트 세션의 터미널 접근은 거부한다. 정상 에이전트 반환에도 owner cleanup이 남은 프로세스를 종료한다.
장시간 셸 작업은 명시적인 background 요청으로 시작한다. foreground timeout은 작업을 중지하며 자동으로 background로 바꾸지 않는다. 현재 SDK의 세션 알림은 `shell_read`/`shell_list`로 확인한다. stdio MCP는 앱 manifest key 또는 검증된 실행 파일 절대 경로만 받으며 host `npx` 자동 설치를 제거했다. 도구 목록 마지막 페이지의 cursor 생략과 stdin 쓰기/직렬화 대기 중 호출 취소를 검사했다. 현 SDK가 전달하지 못하는 image/audio/resource 콘텐츠는 text를 보존하고 명시적 오류로 반환한다.

**전체 도구 준비 상태는 아직 `ready=false`다.** 셸/PTY/Python/Node/CLI는 `verified` + `available`, 브라우저는 파일만 `verified`이며 실행은 `blocked`다. Chromium의 실제 Windows AppContainer 시작에서 crashpad 네임드 파이프 접근 거부를 확인했다. 승인 대상 네트워크 중계도 준비되지 않아 브라우저 MCP를 활성화하지 않는다. 미지원 OS·누락·변조는 차단하며 호스트 실행을 fallback으로 추가하지 않는다.
대안인 공식 Headless Shell 153.0.8010.12도 파일 해시를 대조한 뒤 별도 임시 AppContainer에서 검사했다. 버전 조회는 성공했으나 내부 sandbox를 유지한 DOM 실행은 `0x80000003`으로 실패했다. 공식 Chromium의 [Windows sandbox 초기화](https://raw.githubusercontent.com/chromium/chromium/153.0.8010.12/sandbox/policy/sandbox.cc)와 [alternate windowstation 코드](https://raw.githubusercontent.com/chromium/chromium/153.0.8010.12/sandbox/win/src/window.cc)는 관련 경로를 보여주지만 정확한 crash stack은 확보하지 않았다. Firefox의 자손 IPC도 실패했으며 종료0에 PNG가 없으면 실패로 처리했다. SID 전용 private windowstation의 일반 권한 생성도 거부됐다. `--no-sandbox`의 진단 성공을 제품에 적용하거나 기존 WinSta0 ACL·네트워크 capability를 바꾸지 않았다. 다음 브라우저 연결에는 내부 sandbox와 AppContainer의 desktop·IPC를 함께 유지하는 실행 경로가 필요하다.
기존 Electron 44.6.0도 별도 임시 worker로 복제해 같은 capability0 AppContainer/Job과 `--enable-sandbox`에서 검사했다. JS 시작 전에 `0x80000003`으로 실패했다. 같은 스크립트의 일반 호스트 대조는 DOM·렌더러 Node 접근 부재·PNG 생성에 성공했으나 제품 fallback으로 사용하지 않았다. 종료 후 probe의 Go/Electron 프로세스가 남지 않았으며 공유 소스/자원과 실제 앱의 프록시 포트는 변경하지 않았다.

백업은 Go를 정상 중지하고 데이터 홈 OS 잠금을 확보한 뒤 실제 SQLite 백업 기능과 설정·JWT 키·스킬·증거/본문·구독 보호 저장을 함께 검증한다. 자동 백업은 하루 첫 정상 종료에 데이터 홈 밖에 새 폴더를 만든다. 복원은 새 홈만 만들고 기존 홈을 덮어쓰지 않는다. Go의 손상·경로 이탈·링크·증거 누락 거부, 동일 parsed manifest 사용, 보호 ACL 및 no-replace 게시를 검사했다. UI 복원 중 종료와 백업 프로세스 timeout은 자식 종료를 기다린다.

설치·업데이트는 버전별 폴더와 실제 ready 확인, shared mutex, 고정 RSA 배포 서명·파일 목록·SHA256·Authenticode를 사용한다. signed native Host는 자신의 Windows 신뢰/고정 서명자와 내장 script SHA를 확인한 뒤 프로세스 범위의 PowerShell 정책을 적용한다. 설치 준비 ACK 뒤 데이터 백업이 성공해야 게시 permit을 남기고 종료한다. 백업 실패/취소에는 permit 회수와 native Host의 Job 자손 종료를 확인한다. ready 실패 버전의 재설치는 소유한 비활성 버전을 설치 루트 밖의 고유 sibling에 보존한다. 제거 후 같은 경로 재설치도 검사한다. 개발 설치물은 자동 업데이트 미구성이며 정식 배포가 아니다. 공인 인증서·운영 배포 주소·다른 OS 환경은 현재 준비돼 있지 않다.

### 실제 검사와 범위

| 검사 | 결과와 범위 |
| --- | --- |
| 전체 native Go | Go 1.26.3·modernc 1.52.0·norma 0.4.3·CGO=0의 `go test -json -p 1 -count=1 -timeout 180s ./...` 종료0, 149.568초. 테스트가 있는 21개 패키지, 최상위861·하위 포함1,378개 통과·실패0·외부 모델 설정을 요구하는 `TestLiveContextReview`만 skip1. `report`는 테스트 파일이 없다. 이후 MCP 취소/지원 콘텐츠 보완의 공식 Node 집중3·하위2 및 `go vet ./server`를 별도 통과했다. |
| Windows 도구 | 최종 공식 묶음 native 상위13·하위18, server 집중6·하위2, agent owner cleanup1 통과·실패/skip0. 실제 PowerShell·Unicode ConPTY·Python·Node·Git·stdio MCP, 외부 파일/TCP/환경변수 거부, 취소/Go 강제 종료의 detached 자손 정리와 Windows 8.3/긴 경로12개 관계 거부·정상 alias의 같은 격리 ID 공유·junction 거부를 확인했다. 영향 패키지 vet도 통과했다. Linux/macOS 3개 패키지 cross-build는 코드 빌드이며 다른 OS의 실행 검사가 아니다. |
| 백업·복원 | Go backup 상위10·하위8, cmd7, Node4 통과. 실제 modernc 정상/강제 종료 WAL의 새 백업·복원·비밀번호/JWT/증거/스킬 보존, 누락·손상·경로 이탈·링크·쓰기 실패·동시 목적지·manifest 교체 거부를 검사했다. Node timeout/UTF-8 제어 fixture는 실제 DB 검사와 구분한다. |
| 웹·정적 검사 | 타입·한국어·입력·unit8, 새 설정 카드/desktop 타입 Biome, `go vet ./...` 및 이후 영향 패키지 vet 통과. Go32개 gofmt·JS18개/PowerShell3개 문법·diff, 문서4개 코드 블록/로컬 링크10개 검사 통과. 의존성/lockfile/수동 전용 workflow를 유지했다. |
| 실제 앱/UI | 백업 Electron1개 15.0초 및 나머지 Electron/Chrome5개 1.4분 통과·실패0·skip0. 실제 백업 IPC·새 홈 재실행·중복 종료 대기·복원 실패 후 Go 종료, 기존 업무/증거/보관/인증/재시작·21페이지×2모드×2폭과 IPC 격리를 확인했다. `app.relaunch` 호출은 제어하고 새 프로세스를 실제 실행했다. 구독 화면/모델/일부 장애는 명시적 로컬 fixture이며 이번에 실계정 OAuth·외부 추론을 반복하지 않았다. |
| 빌드·실행 패키지 | 31페이지 export·최종 embedui Go·CSP·글꼴/스킬/공식 도구/라이선스·Windows 실행 패키지·서명되지 않은 개발 Setup 빌드 통과. 최종 `test:package` 1개 8.4초 통과·실패0·skip0. 실제 SQLite/UI 부팅·재시작·비밀번호/스킬 보존, 사용 가능 도구5개·차단 브라우저1개와 Go health/Electron 패키지 버전 일치를 확인했다. Go 버전은 빌드 시 desktop/package.json에서 주입하며 새 릴리스 태그를 만들지 않는다. |
| 배포 집중 검사 | 최종 Node9개 28.4초 통과·실패0·skip0. 실제 native Setup/Host·mutex·게시 취소·준비 실패 rollback·동일 버전 재시도·제거 후 같은 경로 재설치, 8.3/긴 소유권 경로와 실제 실행 중 프로세스의 설치/제거 거부를 확인했다. 일부 서명/취소 검사는 명시적 로컬 키/제어 fixture이며 공인 서명·운영 HTTPS 업데이트 성공 증거가 아니다. |
| 실제 설치 앱 | 최종 `test:installed` 1개 266.2초 통과·실패0·skip0. 임시 `--no-registration` native Setup 설치→실제 Electron·Go 2.2.0·SQLite와 업무 화면→기존 비밀번호의 실제 로그인 JWT/스킬 보존→업그레이드 healthy 확인→제거 후 DB/스킬 보존을 확인했다. 2.2.1은 동일 Go/앱 코드의 package.json 메타데이터만 바꾼 fixture이며 새 코드 릴리스나 운영 자동 업데이트 검사가 아니다. 실제 시작 메뉴/사용자 제거 등록은 변경하지 않았다. |

초기 전체 Go의 localization 검사는 동시 작성 중 Go 파일의 구문 오류로 실패했다. 소스 동결 후 위 전체 검사를 통과했다. 백업 Electron fixture의 main-process 모듈 접근과 npm의 검사 인수 전달을 수정했다. 첫 실제 설치 검사에서는 Windows 8.3/긴 경로의 문자열 비교가 정상 설치를 거부해 ready 뒤 실패 화면으로 이동하는 제품 결함을 발견했다. 설치 소유권·helper root·실행 중 프로세스와 도구 폴더 관계를 canonical 경로로 검사하도록 보완하고 기존 reparse 차단을 유지했다. 실패 로그와 최종 JSON·PNG·trace·임시 DB는 로컬에 보존한다. 이전 빌드/패키지는 별도 로컬 캐시에 보존했다.

race(C 컴파일러 없음), 공인 코드 서명·운영 HTTPS 업데이트, 실제 시작 메뉴/제거 등록의 OS 통합, macOS/Linux 실제 실행, 물리 IME/OS DPI와 모든 데이터/오류/승인 상태 수동 검수는 미실행이다. 다음 구현 시작점은 **Chromium의 Windows 격리 실행과 승인 대상 네트워크 중계를 유지하는 브라우저 연결**이다. 위 외부 환경 검증과 구분한다.
Actions 실행/재실행·원격 artifact 업로드·태그·배포는 하지 않는다. 원격 반영 SHA는 완료 보고와 이 단위의 Git 커밋에서 확인한다.
변경 범위는 `internal/toolruntime`, `internal/backup`, `server/runtime_*`와 도구 조립/사용자 도구, `cmd/artex`·`db/snapshot.go`, Electron의 실행/백업/업데이트·패키징/설치 스크립트·관련 검사, 설정 카드/desktop 타입, README 두 개·아키텍처·이 계획이다.

## 이전 단위 — 2026-10-09 인증·SQLite·Electron 회귀 검증

Electron 자동 진입과 일반 브라우저 비밀번호 흐름의 인증 보호를 확인하고, SQLite 및 실제 데스크톱 회귀 검사를 수행했다.
아래 결과는 해당 날짜에 실제 수행한 검사 기록이며 전체 제품 출시나 미완료 도구·설치 기능의 완료를 뜻하지 않는다.

- [x] 초기화·변경·기존 비밀번호·Electron 자동 진입의 인증 계약 연결
- [x] 새 비밀번호의 8 Unicode 문자 이상·72 UTF-8 바이트 이하 검증을 초기화/변경 API에 연결
- [x] 인증 저장소 조회 오류의 503, 원자 초기화·변경·기존 비밀번호·Electron 자동 진입 회귀 검사
- [x] setup 상태 조회 실패 시 폼 차단과 재시도 복구, 지정 Go/SQLite·웹·실제 앱 검증 결과 기록

이미 구현된 SQLite의 `SetSettingIfAbsent`·비밀번호 변경 CAS·연결 풀 4개와 공통 인증 상태 화면을 유지한다.
Electron 앱 세션 자동 진입·ChatGPT 구독 연결·한국어·Pretendard·neobrutal-ui와 사용자 데이터 경로를 보존한다.

관련 파일: `server/{auth.go,auth_test.go,auth_sqlite_test.go}`, `db/chatgpt_profiles_test.go`, `desktop/tests/standalone.spec.cjs`, 이 계획.
실제 저장소 읽기 오류는 503으로 응답하고 기존 빈 비밀번호 해시의 손상 상태는 500으로 차단한다. 새 길이 정책은 저장할 새 비밀번호에만 적용한다.

### 인증·데스크톱 회귀 검증

| 검사 | 실제 결과와 범위 |
| --- | --- |
| 지정 Go/SQLite 인증 | 공식 ZIP SHA256을 대조한 Go 1.26.3 Windows amd64·modernc 1.52.0·CGO=0. `go test -count=1 -timeout 120s -run '^(TestValidatePassword\|TestAuth)' ./server` 통과5.163초. 잘못된 초기화의 hash/JWT 부재·잘못된 변경의 기존 hash 보존·ASCII/한글/이모지/72바이트 경계·기존 비밀번호 로그인·동시 초기화/변경·읽기 장애·Electron 앱 세션 경계를 확인했다. |
| 전체 native Go | 최종 `go test -json -p 1 -count=1 -timeout 180s ./...` 종료0. 테스트가 있는 19개 패키지, 최상위832개 통과·하위 포함1,337개 통과·실패0·skip1. skip은 외부 모델 설정을 명시적으로 요구하는 `TestLiveContextReview`이며 실제 모델/대상 호출은 하지 않았다. `report`는 테스트 파일이 없다. |
| 정적 검사 | gofmt·`go vet ./...`·fixture 수정 후 `go vet ./db`·diff 검사 통과. web 타입·한국어·입력 검사·unit8개·standalone JS 문법 검사 통과. 기존 의존성/lockfile/라이선스와 수동 전용 workflow를 유지했다. |
| Windows 빌드/수명 주기 | 31페이지 정적 export·실제 Go/embedui·CSP·글꼴/스킬/라이선스·서명되지 않은 Windows 실행 패키지 빌드 통과. 새 한글 데이터 홈의 실제 SQLite 초기화→ready PID/loopback URL→HTTP health→부모 EOF→종료0 검사 통과. |
| 실제 Electron/Chrome | `npm run test:electron` 5개 통과52.7초·실패0·skip0. setup의 503 응답은 명시적 UI fixture이며 폼/비밀번호 입력 부재와 한국어 오류/재시도를 확인했다. fixture 해제 후 실제 Go의 상태200/폼 복구→설정/로그인/종료도 확인했다. 기존 Electron 자동 진입·업무/증거/보관/재시작·화면·격리와 구독 연결 시작/취소·명시적 연결 UI fixture를 함께 검사했다. 실제 계정 OAuth 완료 검사가 아니다. |
| 실제 Windows 실행 패키지 | `npm run test:package` 1개 통과5.2초·실패0·skip0. 실제 Go/SQLite/UI 부팅·재시작·사용자 스킬/DB 보존·Pretendard·API 경계를 확인했다. 설치·서명·릴리스 검사가 아니다. |

첫 전체 Go 검사에서는 기존 구독 스키마 업그레이드 fixture가 Windows CRLF 파일에서 열 정의를 제거하지 못해 실패했다.
fixture의 정확한 열 정의 매칭에서 줄바꿈 조건만 제거한 뒤 전체 검사를 다시 통과했다. 실제 스키마·업그레이드·사용자 데이터 처리 코드는 바꾸지 않았다.
Electron 검사 명령의 최초 작업 디렉터리 오류로 npm 실행이 한 번 중단됐으며 올바른 `desktop` 디렉터리에서 최종 검사를 통과했다.
처음의 실패 로그와 최종 Go JSON·Playwright PNG/trace·신규 임시 SQLite는 로컬 검증 자료로 보존하고 원격에 업로드하지 않는다. lifecycle 스크립트의 신규 임시 홈은 해당 검사 종료 시 정리했다.
race는 C 컴파일러가 없어 미실행이다. 다른 OS 실제 실행·물리 IME/OS DPI·실계정 OAuth/추론·M4 도구/백업/설치/서명/업데이트는 이번 단위에서 실행하거나 완료 처리하지 않았다.

제품의 다음 구현 시작점은 기존 M4 앱 전용 도구 런타임이다.
Actions 실행/재실행·원격 artifact 업로드·태그·배포는 하지 않는다. 원격 반영 SHA는 완료 보고와 이 단위의 Git 커밋에서 확인한다.

## 이전 단위 — 2026-10-07 공개 문서의 비공개 대상 정보 제거

원격 main과 로컬 미커밋 변경을 확인한 뒤 시작했다. 사용자 요청에 따라 비공개 검토 대상의 식별 정보·소스 구성·경로·환경 설정·분석 결과·시험 기록을 공개 문서와 main 이력에서 제거했다. 사용법은 가상 프로젝트 예시로 안내하며 제품 자체의 구현·검증 결과와 구분한다.

- [x] README의 실제 대상 예시·환경·분석 기록 제거 및 일반 사용법 유지
- [x] 이 계획의 실제 대상 시험·분석 기록과 로컬 증거 참조 제거
- [x] 공개 문서·예시·커밋에 비공개 대상 정보를 기록하지 않는 작업 기준 추가
- [x] 사용자 명시 승인으로 공개 main 이력의 비공개 대상 문서·커밋 메시지 제거 및 코드 보존 검증

변경 파일은 `AGENTS.md`, `README.md`, 이 계획이다. 비공개 검토 산출물과 기존 시험 DB·설정은 저장소 밖의 로컬 자료로 보존한다. Git 추적 파일 699개의 대상 식별 정보 검색은 0건이며 가상 예시·Markdown 로컬 링크 8개·코드 블록·diff 검사는 통과했다. 문서 검사 실패는 없다. 앱 구현 변경이 없어 Go/웹/Electron 빌드·회귀는 재실행하지 않았다.
이력 강제 갱신 금지의 이번 예외를 사용자에게 명시 승인받고 원격의 예상 SHA를 지정해 main을 갱신했다. 기존 488개 커밋 중 앞선 482개는 SHA 그대로이며, 뒤의 6개는 문서 두 파일과 메시지 세 개만 정리했다. 각 커밋의 나머지 파일·작성 정보·부모 연결을 보존했고 마지막 파일 트리는 기존 문서 정리본과 동일하다. 새 main 이력의 식별 정보·메시지·diff 검사와 최종 트리·연결성 검사는 통과했다. 원본 이력 백업과 감사 자료는 저장소 밖에 보존한다. 다른 복제본·호스팅 캐시의 삭제까지 확인한 결과는 아니다.
다음 구현 시작점은 M4 앱 전용 도구 런타임이다. 원격 반영 SHA는 완료 보고와 이 단위 Git 커밋에서 확인한다. Actions·원격 artifact·태그·배포는 없다.

## 이전 단위 — 2026-10-07 실제 ChatGPT 구독 검증

미커밋 변경이 없는 main에서 시작했다.
사용자 요청에 따라 실제 계정의 공식 OAuth·구독 동의부터 모델 추론·소스 읽기까지 검증했다.
ARTEX가 소유한 로그인과 보호 저장을 사용하며 다른 앱의 인증 파일이나 API 키는 사용하지 않았다.

- [x] 실제 로그인·구독 동의·공개 모델 목록 조회와 GPT-6-Astra 활성화
- [x] 같은 데이터 홈 재시작 후 연결·권한·활성 프로필 유지
- [x] 실제 추론 오류 재현·수정과 `OK` 응답 확인
- [x] 실제 MainAgent의 복사본 Read 3건·경로별 승인·결과 대조·한국어 분석 답변 저장
- [x] 관련 Go 검사·Windows 패키지 빌드/회귀와 README·계획 갱신

처음 실제 추론은 HTTP 200의 정상 SSE 본문인데 `Content-Type` 헤더가 없어 거부됐다. 거부 전에 본문을 읽지 않아 기존 Capture가 비어 있던 문제도 확인했다. 헤더가 생략되면 동일 SSE 파서로 처리하고 반드시 `response.completed`와 완료 상태를 확인한다. 명시된 잘못된 미디어 타입은 최대 64KiB의 진단만 기존 Capture에 남기고 실패한다. 원문·헤더 매개변수·OAuth 토큰은 공개 오류에 넣지 않는다. JSON 응답 수용·다른 엔드포인트·유료 API 전환은 추가하지 않았다.

| 검사 | 실제 결과와 범위 |
| --- | --- |
| 실제 OAuth/모델/추론 | 공식 로그인·구독 동의 후 연결/권한 확인, 계정 모델 4개 조회, GPT-6-Astra 활성화 통과. 재시작 2회 연결/프로필 유지. 수정 후 실제 연결 테스트가 3,500ms에 `OK` 반환. Chrome의 callback 완료 페이지는 ERR_BLOCKED_BY_CLIENT를 표시했지만 앱의 callback 처리와 연결은 성공했다. 브라우저의 차단 설정은 변경하지 않았다. |
| 실제 파일 도구/저장 | MainAgent의 실제 Read 호출·경로별 승인·도구 반환 내용 대조·입력 해시 보존·최종 답변 및 활동/사용량 저장 확인. 구체적인 입력 소스와 검토 결과는 공개하지 않는다. 기존 승인·차단 정책과 작업 상태를 유지했다. |
| Go 집중/전체 agent | 지정 Go 1.26.3·CGO=0, ChatGPT 집중 검사 7개/하위 17개 통과0.562초. 정상 헤더 생략·본문·usage·도구 ID/인수, JSON/HTML/빈 본문/중단/잘못된 이벤트 거부, 명시 비SSE 진단 크기·비밀 비노출 회귀 포함. `./agent` 전체 통과5.060초, `go vet ./agent`·gofmt·diff 검사 통과. |
| Windows 실행 패키지 | 31페이지 정적 export·embedui Go·서명되지 않은 패키지 빌드 통과. `npm run test:package` 1개 통과3.8초(전체4.2초)·실패0·skip0. 실제 Go/SQLite/UI 부팅·종료·재시작·자동 진입·사용자 스킬/DB·Pretendard·API 경계 회귀 확인. 이전 출력·실패 진단·실제 계정 증거는 로컬에 보존했다. |

변경 파일은 `agent/chatgpt_provider.go`, `agent/chatgpt_provider_test.go`, README 두 개와 이 계획이다. 실제 계정·토큰·비공개 소스·분석 결과·진단 본문·스크린샷·시험 DB는 저장소 밖에 보존하며 커밋/원격 업로드하지 않는다.
전체 Go/전체 Electron 회귀는 이전 단위 결과이며 이번에 반복하지 않았다. race(C 컴파일러 없음)·다른 OS 실행·실계정 갱신/원격 해제/한도 소진·물리 IME/OS DPI는 미실행이다.
다음 시작점은 기존 M4 앱 전용 도구 런타임이다. OS/네트워크 격리·설치/서명/업데이트는 별도 미완료다.
원격 반영 SHA는 완료 보고와 이 단위의 Git 커밋에서 확인한다. Actions 실행/재실행·원격 artifact·태그·배포는 없다.

## 이전 단위 — 2026-10-07 ChatGPT 구독 연결

아래 기동·미검증·다음 시작점은 이 구현 단위 종료 당시의 기록이다. 후속 실제 계정 검증 결과와 현재 시작점은 상단의 현재 단위를 따른다.

사용자 요청으로 ChatGPT 구독 OAuth 연결을 Go 업무 에이전트에 추가한다.
미커밋 변경이 없는 main에서 시작했다.
공식 Sign in with ChatGPT의 공개 인증·모델·Responses 계약을 확인했다.
다른 앱의 로그인 파일을 가져오지 않고, ARTEX가 직접 PKCE 로그인/동의와 보호된 로컬 토큰 저장·갱신을 소유한다.
API 프로토콜과 인증 방식을 분리하며, 업무 SQLite 버전 2는 기존 버전 1에 `auth_method` 열 하나를 동일 트랜잭션에서 추가한다.
기존 프로필·관계·사용자 데이터와 API 키 방식은 보존한다. PG 데이터 이전이나 DB 이중 지원은 추가하지 않는다.

- [x] Go OAuth/보호 저장·갱신·공개 모델 목록 구현과 실제 로컬 검증
- [x] 구독 Responses의 도구 호출·완료/실패 처리, 업무 호출부 연결과 검사
- [x] 구독 연결 UI·제한된 Electron 외부 로그인 열기와 Playwright 검사
- [x] Windows 앱 빌드·실제 브라우저 로그인 열기, README와 검사 결과 기록
- [x] 사용자 계정의 공식 로그인·구독 동의·실제 모델 목록/추론·파일 도구 호출 확인 — 후속 구독 검증 단위 참조

ARTEX를 별도 데이터 홈에서 실제 실행하고 같은 홈을 다시 열었다. 비밀번호 없는 자동 진입·기존 작업 보존·SQLite 버전 1에서 2로 갱신을 확인했다. 당시 구독 연결은 기본 브라우저 열기와 승인 대기까지 확인했으며 후속 실제 계정 검증 결과는 상단의 구독 검증 기록을 따른다.

도구 호출 검토 중 MainAgent/Planner/목표 분해가 업무 Guard를 전달하지 않던 경로를 연결했다. 실제 도구 실행 전 허용·차단·수동 승인과 작업/역할별 감사 기록을 검사했다. 구독 연결 해제나 권한 상실 시 명시적으로 선택한 구독 프로필이 유료 API 경로로 자동 전환되지 않는다. 병렬 도구 인수와 호출 ID는 전체 검증 후 실행 이벤트로 전달한다.

### 구독 연결 단위의 로컬 검증

| 검사 | 실제 결과와 범위 |
| --- | --- |
| Go 전체·수정 후 검사 | `go test -count=1 -timeout 180s ./...`의 검사 대상 19개 패키지 통과. 이후 추가한 구독 fallback·병렬 도구 검사와 MainAgent/Planner/Goals Guard의 허용·차단·승인 9개 경우 통과. 마지막 서버 전체 검사 38.136초 통과. DB 전체 검사 29.052초 통과. 지정 Go 1.26.3/modernc를 사용했다. |
| OAuth/비밀 저장 | 실제 로컬 HTTP/JWKS fixture의 PKCE·상태·nonce·서명·audience·만료·동의·갱신 회전·취소·로그아웃 및 Windows DPAPI/ACL·변조·자식 프로세스 파일 잠금 검사 통과. 공개 DTO/기록에 토큰을 내보내지 않는다. 공식 OpenAI 서버의 계정 로그인·구독 추론 성공 증거가 아니다. |
| SQLite | 실제 modernc 버전 1 DB의 API 키·관계·설정 보존/버전 2 재개방과 구독 프로필 검증 통과. 별도 제어 드라이버의 ALTER/version 오류 rollback 검사도 통과하며 실제 modernc 검사와 구분한다. 실제 앱의 기존 홈에서 버전 2와 기존 작업 보존을 재확인했다. |
| 정적 검사/빌드 | 타입·한국어·입력·관련 lint·JS 문법·Go vet·gofmt·diff 검사 통과. 31페이지 정적 UI·embedui Go·Windows 실행 패키지 빌드 통과. 새로운 의존성은 없다. 기존 전체 formatter의 CRLF 진단을 없애기 위한 무관한 서식 변경은 하지 않았다. |
| 실제 Electron·일반 Chrome | 최종 `npm run test:electron` 5개 통과55.1초·실패0·skip0. 실제 Go 로그인 시작/취소·JWT/앱 세션·callback/IPC 거부 검사, 명시적 UI fixture의 동의/첫 안내/모델 순서/활성화/로그아웃, 기존 업무/화면/격리와 일반 Chrome 비밀번호 흐름을 확인했다. 계정 연결 화면 fixture를 실제 OAuth 완료로 표현하지 않는다. |
| 실제 실행 패키지 | `npm run test:package` 1개 통과6.3초·실패0·skip0. 실제 Go/SQLite/UI 부팅·재시작·자동 진입·Pretendard·스킬/DB 보존·API 경계 확인. 이전 패키지는 별도 dist 경로에 보존했다. 서명·설치·릴리스 검사가 아니다. |

초기 검사에서 기존 최소 DB fixture의 누락 열, 인증 cookie를 무시한 테스트 전제, 보조 Electron 창의 preload 경로, DB 없는 서버 fixture 접근을 수정한 뒤 위 최종 검사를 통과했다. 실패 흔적과 새 스크린샷·임시 DB·trace는 로컬에 보존한다.
Linux/macOS용 인증 패키지 cross-build는 통과했지만 다른 OS 실제 실행은 하지 않았다. C 컴파일러가 없어 race 검사는 미실행이다. 물리 IME/Windows 디스플레이 배율·실제 OAuth 동의/구독 사용량은 미검증이다.
다음 시작점은 열어 둔 ARTEX의 사용자 로그인·동의 완료 후 모델 활성화와 복사본 파일의 실제 승인/읽기/결과 확인이다. 제품 구현의 다음 단위는 기존 M4 앱 전용 도구 런타임이며 OS/네트워크 격리·설치/서명/업데이트는 완료 처리하지 않는다.
원격 반영 SHA는 완료 보고와 이 단위의 Git 커밋에서 확인한다. Actions 실행/재실행·자동 트리거·원격 artifact·태그·배포는 없다.

## 이전 단위 — 2026-10-07 사용법 안내

README에 실행·모델 활성화·작업 생성·소스 경로/첨부·도구/승인 기록·발견 검토 순서를 구체화했다. Go의 네이티브 파일 도구와 미준비 외부 명령/MCP를 구분하고, 경로 입력은 자동 가져오기·색인이 아니며 실행 환경 미준비는 읽기 전용 모드가 아님을 기록했다.
변경 파일은 README와 이 계획이며 문서의 UI/모델/파일 도구 계약을 현재 ARTEX·지정 SDK 소스와 대조했다. Markdown 로컬 링크·코드 블록·diff 검사는 통과했다. 구현 변경이 없어 앱 빌드·단위/Playwright 검사는 재실행하지 않았다. 비공개 대상별 조사·시험 기록은 공개 문서에 포함하지 않는다.

## 이전 단위 — 2026-10-07 Pretendard 단일 글꼴

사용자 요청으로 한국어·영문·숫자·코드·로그·그래프와 Electron 시작/실패 화면을 로컬 Pretendard Variable 하나로 통일했다.
미커밋 변경이 없는 main/원격 `6d76903330b63f08c9cf0421c74c127a437759af`에서 시작했다.
Noto 파일/라이선스·Geist 의존성·폰트 registry/선택 UI/상태/쿠키 적용 경로를 제거했다. 기존 사용자 설정을 삭제하거나 이행하지 않는다.
`font-mono` 호출을 `font-sans`로 교체하고 code/pre/kbd/samp는 동일 글꼴을 상속한다. G6 canvas는 글꼴 로딩 후 실제 body family로 렌더링한다.
Mono 색상/테두리/섀도와 기존 비밀번호 없는 진입·업무 API는 유지한다. 알림 HTML의 개별 OS 글꼴 지정도 Pretendard로 교체했다.

공식 v1.3.9의 글꼴과 OFL을 원격 Git blob/로컬 SHA256으로 대조했다. 출처·크기·해시·원저작자와 라이선스는 `docs/ui-design.md`에 기록한다.
정적 UI와 시작 화면 각각에서 같은 원본 WOFF2 하나를 제공하며 실행 중 CDN이나 다른 글꼴을 요청하지 않는다.
변경 파일은 웹 글꼴/설정/로더·39개 화면/컴포넌트의 글꼴 클래스·G6 연결·package/lock,
`desktop/{scripts/build.cjs,src/main.cjs,src/startup.html,tests/desktop.spec.cjs,package-tests/portable.spec.cjs}`·`notify/html.go`·OFL 원문 보존용 `.gitattributes`·PRODUCT/README 두 개/UI 기준/이 계획이다.
기존 생성 패키지와 검사 증거는 별도 경로에 보존하고, 새 생성 자원에는 사용하지 않는 글꼴을 포함하지 않는다.
다음 구현 시작점은 M4의 앱 전용 도구 런타임이다. 설치/서명/업데이트·다른 OS 실제 실행·물리 IME/OS DPI 검증은 별도 미완료다.

### 단일 글꼴 단위의 로컬 검증

| 검사 | 실제 결과와 범위 |
| --- | --- |
| 원본/생성 자원 | 공식 Git blob·SHA256 일치. OFL 원문의 기존 줄 끝 공백과 LF도 `.gitattributes`로 보존한다. 최종 `web/out` 글꼴은 Pretendard WOFF2 1개이며 시작 화면/패키지 글꼴과 SHA256이 동일하다. Noto 자원·구 라이선스·Geist package/lock/설치 항목과 구 글꼴 선택 경로가 없다. |
| 정적 검사/빌드 | 타입·한국어·입력·관련 7개 구조 파일 lint·JS 문법·gofmt·diff 검사 통과. 최종 31페이지 정적 export, 지정 Go 1.26.3 embedui와 Windows 실행 패키지 빌드 통과. 전체 Biome formatter 검사는 기존 CRLF 진단으로 미통과이며 관련 없는 자동 서식 변경은 하지 않았다. |
| 실제 Electron + 일반 Chrome | `npm run test:electron` 3개 통과32.0초·실패0·skip0. 로딩된 font face 1개, 본문/컨트롤/코드의 동일 font family, CDP의 한글 실제 렌더링 글꼴이 custom Pretendard임을 확인했다. 글꼴 선택 UI 부재·시작/실패 화면과 진단 pre의 실제 Pretendard 로딩, 기존 자동 진입/업무/보관/재시작/격리와 21페이지×2모드×2폭·상세10탭·입력 조합·확대·모션 회귀도 통과했다. |
| 실제 실행 패키지 | `npm run test:package` 1개 통과3.1초·실패0·skip0. 패키지의 폰트 파일/라이선스 1종, 실제 Pretendard 로딩·자동 진입·재시작·스킬/SQLite 보존·API 경계 확인. |
| 알림 HTML | 실제 `go test -count=1 -timeout 120s ./notify` 통과0.5초. 메일 발송이나 외부 이메일 클라이언트 렌더링 검사는 하지 않았다. |

위 최종 기능/글꼴/빌드 검사에는 실패/skip이 없다. 전체 Go/서버·기존 web unit·race·다른 OS 실행 검사는 반복하지 않았으며 이전 단위 결과와 구분한다.
스크린샷·임시 DB·진단·trace는 로컬에만 보존한다. Actions 실행/재실행·자동 트리거·원격 artifact·태그·배포는 없다.
원격 반영 SHA는 완료 보고와 이 단위의 Git 커밋에서 확인한다.

## 이전 단위 — 2026-10-07 비밀번호 없는 Electron 진입

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

- [x] 공통 Mono 색상 토큰/Pretendard 단일 로컬 글꼴/컨트롤/표/오버레이와 호출부 연결, MIT/OFL/기존 저작권 보존.
- [ ] 셸/전체 업무 화면의 기본 21개 페이지 ×2모드×2폭 검사는 통과. 모든 데이터·오류·승인 상태의 시각 검수는 미완료.
- [x] Electron 메인/렌더러 분리, lockfile/Go/정적 UI, 단일 인스턴스/userData/ready/장애/정상 종료.
- [x] 앱 세션/Origin/Host/IPC/CSP/탐색·부모 EOF와 Windows Go 강제 kill·관리된 도구의 detached 자손 정리 검사. 다른 OS의 자손 정리는 별도 미검증.
- [x] 서명되지 않은 Windows 실행 패키지의 실제 resources/userData/Go/SQLite 부팅·재실행·사용자 스킬 보존·라이선스 검사.
- [x] 실제 Electron의 21개 페이지 × 라이트/다크 × 1280/1440px, 작업 상세 10개 탭, 취약점 증거, 키보드·DOM composition·125/150% Electron 확대·축소 모션 검사.
- [ ] 물리 Windows IME·OS DPI·모든 데이터/오류/승인/오버레이 상태의 수동 시각·접근성 검수. DOM composition/Electron 확대 검사와 구분한다.

빈 창/일부 쇼케이스/mock 캡처로 전체 제품 또는 UI 완료를 표시하지 않는다.

## M4/M5. 도구·신뢰성·배포

- [x] Windows x64 셸/PTY/Python/Node/Git/stdio MCP, 공식 경로·버전·출처·전체 해시·라이선스·누락/변조/미지원 준비 상태 차단.
- [ ] Windows 브라우저 내부 IPC·승인 대상 네트워크 중계와 브라우저 MCP의 실제 호출부 연결. 전체 도구 `ready=false`를 유지한다.
- [x] Windows AppContainer의 작업 공간/네트워크/인증 환경변수 차단, Job 자손 정리·kill·손상·쓰기 실패와 일관된 DB+증거 백업/새 폴더 복원.
- [ ] 전체 다중 기록 부하와 모든 디스크 오류, macOS/Linux 도구/PTY/네트워크/자손 격리의 실제 실행.
- [x] Go 자체 업데이트/시작 스크립트/Docker/PG 안내·의존성·구 배포 workflow 제거. 사용자 데이터 보존. Electron 자동 업데이트 배포는 별도 미완료.
- [x] README와 개발 안내를 현재 Electron/SQLite 빌드·검사·데이터 경로·미완료 상태로 교체.
- [x] 최종 Windows 임시 설치·업그레이드·제거와 실제 Go/UI 준비·기존 데이터 보존. 2.2.1은 동일 코드의 메타데이터 검사 fixture.
- [ ] 공인 Windows 코드 서명·운영 HTTPS 업데이트, macOS/Linux 실제 지원 검증, 전체 스타일/접근성 수동 확인.

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
