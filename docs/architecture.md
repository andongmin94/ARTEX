# Electron + Go + SQLite 전환 설계

결정일: 2026-10-05. 최초 조사 기준: `e0354e9a7101ed4f5bb43c9136beec07acafa014`.
이 문서는 목표 구조와 근거를 설명한다. 진행 상태는 `development-plan.md`만 관리한다.

## 1. 구현 경계

기존 Next.js 화면은 정적 export 후 Go embedui에 포함한다. Electron은 Next.js 서버를 실행하지 않는다.
업무 DB는 data/artex.sqlite, 트래픽 인덱스·본문·증거는 기존 별도 저장 구조를 사용한다.
internal/sqlitedb가 고정된 파일 URI와 연결별 PRAGMA를 적용하고 업무 쓰기는 IMMEDIATE 트랜잭션으로 시작한다.
PG 드라이버·DSN·시드 보정·자체 업데이트·Docker 시작 경로는 제거했다.
desktop/scripts/build.cjs가 UI·CSP hash·Go·기본 스킬·글꼴·라이선스를 묶고 package.cjs는 Windows 실행 폴더를 생성한다.
현재 검증 범위와 미완료 제품 요구사항은 development-plan.md에 기록한다.

## 2. 목표 구조와 소유권

```text
Electron main
  ├─ 기존 UI 렌더러 (Node 접근 없음)
  ├─ Go 자식 프로세스 1개 (로컬 API + 인증 + 작업 실행)
  └─ 앱/도구 배포와 수명 주기 관리

Go
  ├─ SQLite 업무 저장소
  ├─ 기존 트래픽 SQLite 인덱스 + 본문/증거 파일
  └─ 관리된 경로의 외부 도구 실행
```

Go만 DB에 접근한다. Electron/렌더러가 별도의 SQLite 연결을 열지 않는다.
ChatGPT 구독 연결도 Go가 공식 OAuth·PKCE/ID 토큰 검증·보호된 토큰 저장/갱신·모델 목록·Responses 호출을 소유한다.
Electron은 준비된 앱의 main frame 요청에서 공식 로그인 URL과 고정 사용량 URL만 기본 브라우저로 연다.
렌더러에는 공개 연결 상태와 모델만 제공하며 API 프로필/SQLite/화면 저장소에 OAuth 토큰을 넣지 않는다.
구독 HTTP 호출은 전체 입력 이력·namespace 도구·스트리밍·store:false를 사용하고 response.completed를 받기 전에 끝난 스트림을 실패로 처리한다.
구독 모델의 전역 자동 폴백은 API 비용으로 전환하지 않는다. 명시적으로 선택한 작업 모델 체인의 전환 정책은 별개다.
내부 기존 API 계약과 화면은 유지하되, 제거되는 배포/DB 경로의 호환 모드는 만들지 않는다.
Electron 앱 자원(읽기 전용 Go 바이너리·기본 스킬·도구 배포물)과 쓰기 가능한 사용자 데이터는 분리한다.

## 3. 데이터 경로와 프로세스 계약

Electron은 `app.getPath('userData')` 아래의 앱 전용 디렉터리를 절대 경로 `ARTEX_HOME`으로 전달한다.
현재 실행 기반은 이 환경 변수가 지정되면 config/data/기본 skills의 루트를 고정한다.
루트 초기화는 쓰기 가능 여부를 검사하고 상대 경로나 파일 경로를 오류로 처리한다.
홈에 설정이 없다는 이유로 CWD의 다른 `config.json`을 선택하지 않는다.
`ARTEX_CONFIG`/`ARTEX_SKILL_DIR`은 명시적 override이며, Electron은 부모 셸에서 상속한 임의 override를 제거하고 자기가 관리하는 값만 전달해야 한다.

현재 Windows 배치:

```text
userData/
  config.json
  jwt.key
  data/artex.sqlite
  data/<앱 관리 증거·트래픽·대화 기록·보관 구조>
  data/workspace/tasks/<작업별 사용자 파일>
  data/workspace/sessions/<대화별 사용자 파일>
  data/workspace/tool-workspaces/<stdio MCP 작업 폴더>
  skills/
  chatgpt/credentials/
  .desktop-backup.json

앱 자원/resources/artex/
  artex.exe
  tools/<공식 고정 버전 도구·manifest·라이선스>

userData의 부모/ARTEX-backups/<홈 식별자>/<새 백업 폴더>/
```

업무 DB는 data/artex.sqlite를 사용한다. 도구 실행 폴더는 Go가 검증한 업무 폴더이며 도구 자체는 앱 자원에 둔다. 자동 백업은 데이터 홈 밖에 새 폴더로 게시하며 기존 백업을 삭제하지 않는다. 새 폴더 복원 뒤 실행 홈 선택은 최초 홈의 `.active-home.json`에 저장한다.
작업 파일 API는 `data/workspace`의 열린 `os.Root`만 사용한다. 목록·읽기·다운로드·쓰기·업로드·폴더 생성·삭제를 같은 경계에 묶고 경로 이탈·링크/Windows junction·다중 하드 링크를 거부한다. 쓰기는 새 파일을 동기화한 뒤 루트 내부 rename으로 게시한다. 에이전트·첨부·셸·MCP의 작업 폴더도 이 루트를 사용하고 DB·증거·대화 기록·보관은 밖에 둔다. 이전 `data/tasks` 등의 파일은 자동 이동·삭제하지 않는다. 이전 작업 파일이 남아 있는 보관 요청은 파일을 누락한 성공으로 처리하지 않는다.
기본 스킬은 패키지 자원에서 설치하되 사용자 수정 파일을 무조건 덮어쓰지 않는다.

부모는 Go를 다음 계약으로 시작한다.

```text
artex -addr 127.0.0.1:0 -ready-stdout -parent-stdin
```

테스트에서 프록시가 필요 없으면 `-proxy ""`를 추가한다.
Go는 저장소 초기화와 실제 HTTP 포트 바인딩 뒤 stdout에 한 줄 JSON을 출력한다.

```json
{"event":"ready","url":"http://127.0.0.1:49152","pid":12345,"version":"dev"}
```

이 이벤트는 API 인증이 끝났다는 뜻도, 인증 토큰도 아니다.
부모는 자식 PID와 loopback URL을 검증한다. Electron은 앱 세션으로 API JWT를 자동 발급받아 비밀번호 없이 진입하며, Go 단독 브라우저 접속의 비밀번호 로그인은 유지한다. 배너는 이 모드에서 생략되며 로그는 stderr로 수집한다.
부모가 stdin 파이프를 닫거나 죽어서 EOF가 나면 Go가 정상 종료 경로를 실행한다.
HTTP 종료 제한 시간 후에는 남은 연결을 닫는다. 생성 실패/포트 충돌/ready 출력 실패는 오류 종료한다.
동적 바인딩은 번들 Electron의 Chromium 고정 제한 목록을 제외한다. 금지된 후보 소켓은 안전한 소켓을 확보할 때까지 유지해 같은 포트의 재배정을 피하고, 제한된 횟수 안에 확보하지 못하면 시작 오류를 반환한다. 명시한 금지 포트도 바인딩 전에 거부한다. Go embed와 Electron ready 검증은 `internal/browserports/restricted-ports.json`을 공유하며 실행 패키지에도 포함한다. 브라우저의 포트 제한을 해제하는 실행 옵션은 사용하지 않는다.

Electron은 단일 인스턴스·30초 ready 제한·종료 대기·시작 오류/재시도 UI를 구현한다.
32바이트 앱 세션 키는 메인 프로세스가 요청 헤더로 넣고 Go가 실제 loopback Host/Origin과 함께 검사한다.
도구 자손 프로세스·네트워크 격리와 OS별 설치 검증은 이 앱 세션 계약과 별도다.

## 4. SQLite 전환 원칙

새 드라이버보다 기존 `modernc.org/sqlite`와 `database/sql`을 먼저 사용한다.
DB 기능이 여러 패키지의 raw SQL에 퍼져 있는지 조사하고 `db/` 이외 호출도 이식 대상에 포함한다.

| PostgreSQL 요소 | SQLite 방향 / 검증 |
| --- | --- |
| BIGSERIAL·시간·불리언 | ID 유지 의미, UTC 시간 직렬화, NULL/기본값, bool 스캔을 명시적으로 정의 |
| 관계 검색에 쓰는 배열 | 관계 테이블 + 외래키 + 인덱스; 중복/순서/빈 목록 의미 검증 |
| 유동적인 부가 정보 JSONB | 필요한 필드만 JSON; 검색 필드는 구조화. PG JSON 연산자 그대로 남기지 않음 |
| INET/CIDR·네트워크 포함 연산 | Go `net/netip` 정규화와 SQLite용 검색 구조; IPv4/IPv6/경계 주소/잘못된 입력 검증 |
| GIN/GiST·검색 DSL | 실제 조회 조건에 맞는 인덱스와 명시적 쿼리; 문자열 치환 번역 금지 |
| advisory lock·동시 쓰기 | Go가 소유하는 쓰기 경로와 짧은 SQLite 트랜잭션. 읽기/쓰기 풀과 취소 동작 검증 |
| 트리거·PL/pgSQL·업서트 | 필요한 동작만 SQLite/Go로 구현. cascade, updated_at, 충돌 의미를 테스트 |

WAL, 연결별 foreign_keys와 busy_timeout을 검증한다. 단일 연결에 PRAGMA를 한 번 실행하고 풀 전체에 적용됐다고 가정하지 않는다.
쓰기 트랜잭션 안에서 LLM/외부 명령/네트워크 완료를 기다리지 않는다.
처음부터 대형 큐를 발명하지 말고 제한된 쓰기 연결과 짧은 트랜잭션부터 검증한다.
필요한 동시 읽기를 막는 전역 잠금, 공유 DB 핸들로 트랜잭션 안에서 재진입하는 교착도 테스트한다.
파일 URI는 한글·공백·`#`·`%`·지원 OS에서 허용되는 `?`를 안전하게 처리해야 한다.

사용자 데이터는 새 SQLite 저장소에서 시작한다. 기존 PG DB/볼륨은 자동 수정·삭제·변환하지 않는다.
현재 PG 경로를 SQLite 오류의 fallback으로 사용하지 않는다.
일반 SQLite 스키마 버전 관리와 구 PG 형식 호환 계층은 구분한다.

백업은 실행 중 `.sqlite` 파일 하나를 복사하는 방식이 아니다.
드라이버가 제공하는 일관된 백업 기능을 먼저 확인하고, DB와 외부 증거 파일을 함께 복원 검증한다.
현재 백업 CLI는 데이터 홈 OS 잠금 아래 modernc의 native backup을 사용한다. Electron은 Go를 정상 중지한 뒤 동일 실행 파일의 CLI로 백업하고 다시 시작한다. 복원은 read-only 무결성·외래키·앱 스키마·파일 해시·증거 참조를 검사해 존재하지 않는 새 홈에만 게시한다.
네트워크 공유/동기화 중인 폴더를 live WAL DB의 기본 위치로 사용하지 않는다.

## 5. Electron과 도구 보안

렌더러 `nodeIntegration: false`, `contextIsolation: true`, sandbox 활성화를 기본으로 한다.
범위를 제한한 preload/IPC, 호출자 검증, CSP, 탐색/새 창/외부 링크 검증을 적용한다.
백엔드는 loopback에만 바인딩한다. 로컬 웹사이트나 다른 로컬 프로세스도 위협 모델에 포함하고 Origin/Host 검증 및 앱 세션 인증을 연결한다.
2026-10-07 사용자 요청으로 Electron의 비밀번호 입력은 앱 세션→JWT 자동 인증으로 교체했다. 자동 인증 엔드포인트도 정확한 앱 세션·Host/Origin 검증을 통과해야 하며 Go 단독 실행에서는 거부한다. 기존 승인·차단·대상 범위와 API JWT 검증은 유지한다. 로컬 토큰은 로그/URL query에 노출하지 않는다.

도구는 OS/아키텍처별 고정 버전·다운로드 출처·무결성·라이선스·설치 결과를 관리한다.
Electron의 Node 런타임이 외부 MCP용 일반 `node`/`npm` 명령을 제공한다고 가정하지 않는다.
Windows의 셸/PTY/프로세스 트리와 Linux 명령 의존을 별도 검증한다. 필수 도구 누락은 기능 실행 전 명확히 표시한다.
렌더러 sandbox는 Go가 실행하는 외부 명령의 sandbox가 아니다. 별도 권한·작업 공간·네트워크 제한이 필요하다.
Windows 도구는 capability 없는 AppContainer와 프로세스 수·메모리 제한 및 KILL_ON_JOB_CLOSE Job Object를 사용한다. 지정 작업 폴더의 쓰기 권한만 부여하고 종료 후 SID ACL을 회수한다. 셸/PTY/Python/Node/Git/stdio MCP는 실제 호출부에 연결했으며 브라우저 내부 IPC·승인 대상 네트워크 중계와 다른 OS의 실행 격리는 미완료 상태로 차단한다.

최종 업데이트 소유자는 Electron 하나다. 서명/업데이트/Go/정적 UI가 같은 배포 버전으로 움직인다.
기존 Go 자체 업데이트 API와 스크립트 경로는 제거했다. Windows 설치/업데이트 코드는 버전별 설치·고정 RSA 배포 서명·파일 SHA256·Authenticode·설치 준비 ACK·데이터 사전 백업·실제 ready 확인 후 실행 포인터 확정을 연결한다. 개발 설치물은 자동 업데이트 미구성이며 공인 서명/운영 배포와 다른 OS 실제 실행은 별도 검증한다.

## 공식 참조

- SQLite WAL: https://sqlite.org/wal.html
- SQLite backup: https://sqlite.org/backup.html
- Go SQLite driver: https://pkg.go.dev/modernc.org/sqlite
- Electron security: https://www.electronjs.org/docs/latest/tutorial/security
- Electron userData: https://www.electronjs.org/docs/latest/api/app#appgetpathname


## 현재 구현된 SQLite 연결 경계

`internal/sqlitedb.Open(ctx, absolutePath)`은 기존 `traffic.Open`에서 사용한다.
파일 경로만 입력받고, URL의 Path와 고정 Query를 분리해 한글/공백/#/%/?를 처리한다.
Windows 드라이브 경로는 file URI로 바꾸며 UNC/장치 경로를 허용하지 않는다. 다른 OS의 네트워크 마운트를 자동 판별한다는 뜻은 아니다.
연결별 foreign_keys는 드라이버 DSN, busy_timeout은 연결 훅에서 설정한다. WAL 초기화는 취소 가능한 제한 시간 안에서 잠금 오류만 재시도한다. 부모 폴더는 소유자가 준비한다.
새 파일은 0600으로 만들고 기존 파일을 자르거나 손상 데이터를 초기화하지 않는다. Windows ACL이나 악성 로컬 사용자의 경로 경합까지 격리하는 API는 아니다.
호출자가 풀/트랜잭션/쓰기 조정/스키마를 소유한다. 현재 트래픽의 wmu와 트랜잭션 경계를 유지한다.
업무 DB는 OpenImmediate의 쓰기 트랜잭션을 사용한다. 업무·트래픽·증거 호출자와 실제 다중 풀/취소/재열기/보관 복원 및 Windows의 일관된 백업·새 홈 복원 검사는 지정 modernc로 통과했다. 전체 부하·다른 OS 격리·운영 배포 검증은 별도다.
