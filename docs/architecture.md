# Electron + Go + SQLite 전환 설계

결정일: 2026-10-05. 최초 조사 기준: `e0354e9a7101ed4f5bb43c9136beec07acafa014`.
이 문서는 목표 구조와 근거를 설명한다. 진행 상태는 `development-plan.md`만 관리한다.

## 1. 현재 코드에서 확인한 출발점

- `web/package.json`의 `build:static`과 Dockerfile의 `embedui` 빌드는 기존 웹 UI를 Go에 포함한다. Next.js 서버를 데스크톱에 별도로 띄울 필요가 없다.
- `db/db.go`, `db/schema.sql`은 PostgreSQL 업무 저장소다. 자산/탐색 그래프와 LLM 기록도 이 저장소를 쓴다. `llmrec/llmrec.go`의 저장 대상은 PG다.
- `traffic/traffic.go`는 별도의 SQLite 인덱스와 본문/블롭 파일을 소유한다. `go.mod`에는 `modernc.org/sqlite`가 이미 있다.
- `db/schema.sql`에는 배열, JSONB, INET/CIDR, GIN/GiST, 트리거 및 PG 전용 함수가 있다. 드라이버만 교체해서는 안 된다.
- `cmd/artex/main.go`, `config/config.go`의 데이터 경로·서버 수명 주기는 데스크톱 부모와 연결해야 한다.
- Dockerfile은 Python, Node, Chromium/Playwright 및 여러 CLI를 준비한다. Docker 제거는 이 실행 환경의 명시적 재구성이기도 하다.
- `selfupdate/`, 시작/업데이트 스크립트, CI의 PostgreSQL 서비스 및 Docker 검사는 최종 배포 정리 대상이다.

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
내부 기존 API 계약과 화면은 유지하되, 제거되는 배포/DB 경로의 호환 모드는 만들지 않는다.
Electron 앱 자원(읽기 전용 Go 바이너리·기본 스킬·도구 배포물)과 쓰기 가능한 사용자 데이터는 분리한다.

## 3. 데이터 경로와 프로세스 계약

Electron은 `app.getPath('userData')` 아래의 앱 전용 디렉터리를 절대 경로 `ARTEX_HOME`으로 전달한다.
현재 실행 기반은 이 환경 변수가 지정되면 config/data/기본 skills의 루트를 고정한다.
루트 초기화는 쓰기 가능 여부를 검사하고 상대 경로나 파일 경로를 오류로 처리한다.
홈에 설정이 없다는 이유로 CWD의 다른 `config.json`을 선택하지 않는다.
`ARTEX_CONFIG`/`ARTEX_SKILL_DIR`은 명시적 override이며, Electron은 부모 셸에서 상속한 임의 override를 제거하고 자기가 관리하는 값만 전달해야 한다.

최종 배치 예정:

```text
userData/
  config.json
  data/artex.sqlite
  data/<기존 작업·증거·트래픽 구조>
  skills/
  tools/<고정 버전의 런타임·도구>
  logs/
  backups/
```

DB 파일명과 도구/로그/백업 하위 폴더는 목표다. 폴더가 문서에 있다고 구현된 것으로 간주하지 않는다.
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

이 이벤트는 setup/로그인이 끝났다는 뜻도, 인증 토큰도 아니다.
부모는 자식 PID와 loopback URL을 검증하고 기존 인증을 유지한다. 배너는 이 모드에서 생략되며 로그는 stderr로 수집한다.
부모가 stdin 파이프를 닫거나 죽어서 EOF가 나면 Go가 정상 종료 경로를 실행한다.
HTTP 종료 제한 시간 후에는 남은 연결을 닫는다. 생성 실패/포트 충돌/ready 출력 실패는 오류 종료한다.

이 계약만으로 단일 인스턴스, 모든 도구 자손 프로세스 정리, 저장소 초기화의 timeout 또는 Electron 보안이 완성되지는 않는다.
이 항목은 Electron 및 도구 단계에서 실제 OS별로 검증해야 한다.

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
네트워크 공유/동기화 중인 폴더를 live WAL DB의 기본 위치로 사용하지 않는다.

## 5. Electron과 도구 보안

렌더러 `nodeIntegration: false`, `contextIsolation: true`, sandbox 활성화를 기본으로 한다.
범위를 제한한 preload/IPC, 호출자 검증, CSP, 탐색/새 창/외부 링크 검증을 적용한다.
백엔드는 loopback에만 바인딩한다. 로컬 웹사이트나 다른 로컬 프로세스도 위협 모델에 포함하고 Origin/Host 검증 및 앱 세션 인증을 연결한다.
기존 로그인·승인·차단은 포장 편의를 위해 제거하지 않는다. 로컬 토큰은 로그/URL query에 노출하지 않는다.

도구는 OS/아키텍처별 고정 버전·다운로드 출처·무결성·라이선스·설치 결과를 관리한다.
Electron의 Node 런타임이 외부 MCP용 일반 `node`/`npm` 명령을 제공한다고 가정하지 않는다.
Windows의 셸/PTY/프로세스 트리와 Linux 명령 의존을 별도 검증한다. 필수 도구 누락은 기능 실행 전 명확히 표시한다.
렌더러 sandbox는 Go가 실행하는 외부 명령의 sandbox가 아니다. 별도 권한·작업 공간·네트워크 제한이 필요하다.

최종 업데이트 소유자는 Electron 하나다. 서명/업데이트/Go/정적 UI가 같은 배포 버전으로 움직인다.
이때 기존 Go 자체 업데이트 API와 스크립트 경로를 제거한다. 현재 실행 기반 단계는 아직 그 전환을 완료하지 않았다.

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
연결별 PRAGMA는 드라이버 DSN에 있고 WAL 전환은 열린 파일에서 확인한다. 부모 폴더는 소유자가 준비한다.
새 파일은 0600으로 만들고 기존 파일을 자르거나 손상 데이터를 초기화하지 않는다. Windows ACL이나 악성 로컬 사용자의 경로 경합까지 격리하는 API는 아니다.
호출자가 풀/트랜잭션/쓰기 조정/스키마를 소유한다. 현재 트래픽의 wmu와 트랜잭션 경계를 유지한다.
업무 SQLite 부팅과 writer/read pool 정책은 이 연결 함수를 사용하는 후속 M2.2에서 실제 호출자와 함께 검증해야 한다.
