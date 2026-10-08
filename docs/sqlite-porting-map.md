# SQLite 이식 지도

조사 기준: ARTEX `babea04074a090ae66953c43f335a8580ddaa27c`의 업무 코드.
전체 소스 조사 실행 커밋: `a99972d4e8ff1eaec333aad05874374ef7c95510` (2026-10-05).
진행 상태는 `development-plan.md`만 관리한다. 이 문서는 이식 범위/순서의 근거다.
아래 표는 조사 당시 PostgreSQL 코드의 이식 범위다. 현재 저장소는 SQLite이며 구현·검증 상태는 development-plan.md를 확인한다. 과거 인벤토리 완료와 현재 기능 검증은 다르다.

## 1. 재현 가능한 전체 소스 조사

```sh
go run ./scripts/sqlite-inventory > sqlite-inventory.md
go test -race -count=1 -timeout 60s ./scripts/sqlite-inventory
```

CI의 `storage-inventory` job도 같은 코드를 실행하고 `sqlite-porting-inventory` artifact와 job 로그에 보고서를 남긴다.
DB/LLM/외부 대상에 연결하지 않는다. 전체 Go/SQL 파일을 읽되 .git/node_modules/vendor 및 빌드 출력·조사 도구 자체는 제외한다.
Go AST의 import/문자열/호출을 조사해 직접 SQL, PG 전용 기능, 트랜잭션/연결의 호출 위치, 테스트 skip, 스키마 선언을 보고한다.
Go 주석은 제외한다. 함수 이름·파일·라인만 기록하고 문자열 원문/DSN/키를 출력하지 않는다.

실행 근거: [verify #56](https://github.com/andongmin94/ARTEX/actions/runs/37214597987), job `111472435421`.
Go 1.26.3에서 독립 race 테스트와 전체 소스 조사가 성공했다.
**Go/SQL 소스 332개를 읽어 후보 파일 214개를 보고했다.**
보고서 artifact ID는 `11308130499`이며 보관 기간은 14일이다. 만료 후에도 위 명령과 고정 커밋으로 재현한다.

**214개는 모두 수정해야 하는 파일 수가 아니다.** 저장소 import만 있는 호출자, 테스트, 문자열 오탐도 포함한다.
정적 후보 목록은 완전한 SQL 분석기나 기능 동등성 증명이 아니다.
동적 SQL, 임포트된 라이브러리 내부, 같은 패키지의 간접 호출, 메서드 이름 충돌은 수동 검토가 필요하다.
예를 들어 `server/auth.go`는 같은 패키지의 `s.pg()`와 설정 메서드를 사용해 후보 표에 없지만 이식 검수 대상이다.
`server/update.go`의 `selfupdate.Rollback`, IPv6 문자열의 `::`, 도구 설명의 CIDR/ILIKE 문구는 그대로 DB 이식 항목으로 세지 않는다.
SQL 파일은 텍스트로 조사하므로 주석의 키워드도 후보가 될 수 있다.

## 2. 실제 스키마 경계

| 저장소 | 확인한 선언 위치 | 범위 |
| --- | --- | --- |
| 업무 DB 기본 스키마 | `db/schema.sql` | 일반 테이블 49개, 트리거 선언 22개 |
| LLM 원문 기록 | `db/commands.go:llmRecordsSchema`, `EnsureLLMRecordsTable` | `llm_records` 1개와 인덱스. 기본 schema.sql 밖에서 생성 |
| LLM 사용량 | `db/llm_usage.go:llmUsageSchema`, `EnsureLLMUsageTable` | `llm_usage` 1개와 인덱스. 기본 schema.sql 밖에서 생성 |
| 트래픽 SQLite | `traffic/traffic.go:indexSchema`, `ftsSchema` | `exchanges`, `exchange_bodies`, `blob_refs`, FTS5 `ex_fts`와 외부 본문/블롭 |

따라서 업무 스키마 조사 범위는 **49 + 2 = 51개 일반 테이블**이다.
이 수는 현 코드의 선언 수이며 SQLite 정규화 후 목표 테이블 수가 아니다.
`schema.sql`만 옮기고 LLM 기록/계량을 빠뜨리지 않는다. 두 추가 생성 함수의 실제 호출은 `server.NewManager`에서 확인했다.
새 SQLite 부팅 스키마에는 필요한 업무 테이블/인덱스/시드를 모두 반영하고 구 PG 보정 DDL은 옮기지 않는다.
순환 외래키(agents/current_prompt), 삭제 시 재검증 상태 정리, task_asset_links 동기화처럼 트리거가 담당하던 의미는 따로 검증한다.

트래픽은 이미 SQLite다. 업무 DB와 합쳐서 다시 만드는 대상이 아니다.
다만 `traffic.Open`에는 경로에 `?`가 있으면 PRAGMA URI 대신 bare path를 사용하는 분기가 있다.
새 파일 경로 정책에 이 분기를 복제하지 않는다. 모든 연결의 설정과 실제 파일 위치를 한글·공백·`#`·`%`·OS에서 허용하는 `?`로 검사한다.

## 3. 실제 부팅 경로 — 설정 테이블만으로는 부족하다

```text
cmd/artex.run
  → server.NewManager
      → db.DSN / db.Open / 기본 시드
      → RecoverFindingRetests
      → EnsureLLMRecordsTable / EnsureLLMUsageTable
      → 설정 복원 / intercept / 자산 저장소
      → 트래픽 삭제 임시 상태 복구 (활성 프록시일 때)
      → syncBrowserMCPProxy
  → server.New
      → JWT 키 / LLM health / side questions / retry policy
      → 도구·프롬프트·재검증 에이전트 시드
      → 증거 GC / scheduler / notifier / MCP discovery / 로그 저장
      → 모델 복원 / 작업·의도 복원 / archive worker
  → HTTP 준비 알림 → setup / 로그인 / 설정 API
```

`server/manager.go:NewManager`와 `server/server.go:New`의 실제 코드를 읽어 확인했다.
미이식 테이블의 오류를 무시하거나 빈 결과로 바꿔 setup 화면만 보이게 하는 접근은 금지한다.
모델/도구가 없는 정상 유휴 상태와 저장소 초기화 실패는 구분해야 한다.
LLM 테이블 생성 오류를 로그만 남기는 기존 경로도 새 업무 DB 초기화에서는 실패 전파와 준비 알림 조건을 검토한다.
M1에서 제거한 치명적 종료는 cmd/artex 경로다. `server.New`의 JWT 키 오류 `log.Fatalf`는 아직 남아 있으므로 생성 오류 반환/자원 정리 호출부를 함께 수정해야 한다.
JWT 키는 탐색 가능한 작업 공간과 분리한다. `server/auth.go`의 설정 조회 오류 무시, 최초 비밀번호 설정의 경쟁도 SQLite fixture에서 명시적으로 검사한다.

## 4. 이식 단위와 검증할 의미

| 단위 | 실제 파일/함수 | 검증할 의미 |
| --- | --- | --- |
| 연결·부팅 | `db/db.go`, `db/schema.sql`, `config/config.go`, `cmd/artex/main.go`, `server/manager.go`, `server/server.go` | 파일 URI, 연결별 PRAGMA, 취소/닫기, 쓰기 직렬화, 시드 재실행, 오류 반환, 미완성 상태의 ready 금지 |
| 설정·인증·모델 | `db/settings.go`, `db/config.go`, `db/tools.go`, `server/auth.go`, `server/server_mgmt.go` | setup/로그인/프로필 저장·재실행, JSON/NULL/time 스캔, 활성 프로필 유일성, 프로필 삭제 후 작업/대화/에이전트 참조 |
| 자산·기업·범위 | `db/assets.go`, `companies.go`, `company_scope.go`, `asset_dsl.go`, `task_scope.go`, `task_assets.go`, `task_assets_context.go`, `finding_assets.go` | 배열 관계 정규화, 기존 task_asset_links 재사용 검토, 자연키 중복, ILIKE/정규화, IPv4/IPv6 포함/경계, 기업 범위 재계산과 자산 쓰기 원자성 |
| 작업·탐색·대화 | `db/tasks.go`, `task_context.go`, `task_categories.go`, `task_templates.go`, `exploration.go`, `exploration_sources.go`, `digest.go`, `conversation.go`, `side_questions.go`, `intent_admission.go` | 작업/의도 상태 전이, 단일 실행 승자, 중지·삭제·저장 경쟁, 공유 하위 노드 보존, FIFO, 세션별 사용량, 스냅샷 순서/멱등성 |
| 취약점·재검증·증거 | `db/findings.go`, `finding_retests.go`, `finding_traffic.go`, `finding_traffic_archive.go`, `evidence/store.go` 및 server의 동명 핸들러 | 원문 삭제 후 증거 보존, 재검증 한 건만 실행, fixed 상태 조건, 증거 버전, DB/파일 실패 시 일관성 |
| 작업 보관·복원 | `db/task_archives.go`, `task_archives_restore.go`, `server/task_archives.go`, `server/task_archive_package.go`, `traffic/archive.go` | JSON 행 복원 교체, 명시적 열 매핑, ID 충돌/자산 자연키 매핑, 중간 실패/재시작/삭제 보상 |
| 로그·사용량·기록 | `db/commands.go`, `llm_usage.go`, `logs.go`, `skill_usage.go`, `tool_usage.go`, `llmrec/llmrec.go`, `server/logsink.go` | 원문/계량 구분, 실패·취소 때 한 번만 기록, UTC 정렬/집계, 짧은 트랜잭션과 작은 묶음 쓰기 |
| 승인·알림·예약 | `db/intercept*.go`, `asset_intercept.go`, `task_intercept.go`, `notification.go`, `notification_delivery.go`, `triggers.go` 및 server/intercept/notify 호출자 | 승인·차단 정책 보존, 이벤트와 finding의 원자 저장, 알림 claim/lease/재시도, 트랜잭션 밖 네트워크 실행 |

여기서 파일명이 짧게 적힌 동일 행의 경로는 선두 `db/` 기준이다. 와일드카드는 조사 범위를 나타낸다.
쿼리 문자열 수가 큰 파일은 `db/exploration.go`(81), `db/config.go`(69), `db/task_archives.go`(58), `db/task_archives_restore.go`(49)다.
이는 SQL 문자열 후보 수이지 독립 쿼리 수가 아니다. 특히 보관/복원은 동적으로 조립하는 SQL을 함께 읽어야 한다.

## 5. db/ 밖의 직접 의존 — 빠뜨리면 통합 시 실패한다

- `server/finding_retests.go:seedFindingRetester`: 직접 Begin/INSERT/UPDATE와 `pg_advisory_xact_lock`. 메서드 전체를 단일 SQLite 쓰기 트랜잭션으로 이식한다.
- `server/finding_traffic.go`, `server/finding_workflow.go`: JSONB/배열/캐스트를 쓰는 직접 SQL. 가능하면 해당 업무 저장소 메서드로 이동하면서 교체한다.
- `evidence/store.go`: 업무 DB에 직접 조회/삭제한다. 파일 GC와 증거 참조를 함께 검증한다.
- `server/manager.go`: 직접 SQL과 트래픽 staged commit/rollback이 공존한다. 파일 보상 절차를 DB 트랜잭션으로 오인하지 않는다.
- `server/engine.go:isFKViolation/dropReason`, `db/task_categories.go`, `db/task_templates.go`: SQL 이식 뒤에도 남을 수 있는 pgconn 오류 분류. modernc 오류 코드/업무 오류로 바꾸고 테스트한다.
- `agent/`, `llmrec/`, `report/`, `enrich/`는 주로 db 메서드 호출자다. 저장 결과/상태/스캔 계약을 검사하되 에이전트 전체를 재작성하지 않는다.

`db/task_archives_restore.go`는 `json_populate_record(set)`으로 행을 삽입하고 `nextval(pg_get_serial_sequence(...))`로 충돌 ID를 재배정한다.
단순 캐스트 삭제로 처리할 수 없다. SQLite용 명시적 열 삽입·JSON 해독·키 매핑을 구현하고 기존 행 우선/연결 보존을 검사한다.
PG 구 데이터/보관 형식의 자동 변환기를 추가하지 않는다. 새 SQLite 제품 자체의 보관/복원 동작을 검증한다.

## 6. 트랜잭션과 테스트 경계

핵심 쓰기 단위는 다음 함수에서 확인된다. 정확한 원본 라인은 재생성 보고서를 사용한다.

- 설정/참조: `deleteProfile`, `SetActiveProfile`, `SetAgentLLMProfile`, `CreateConversation`, `ReplaceTaskLLMProfiles`.
- 자산/범위: `withCompanyScopeMutation`, `CreateCompanyWithScope`, `AddScopeInputsChecked`, `RecomputeAttribution`, `AttachAssetsToTask`.
- 상태/삭제: `CreateTaskWithOptions`, `DeleteTaskCascadePrepared`, `SoftDeleteIntent`, `CancelIntent`, `StartSideRequest`.
- 증거/보관: `WithEvidenceTx`, `CreateFindingRetest`, `FinishFindingRetest`, `snapshotTaskArchive`, `restoreTaskArchive`.
- 알림: `FanOutPendingEvents`, `claimDeliveries`.

FOR UPDATE/SHARE와 SKIP LOCKED를 삭제하기만 하지 않는다. 단일 쓰기 소유권, 짧은 트랜잭션, 조건부 UPDATE/RETURNING과 상태 검증으로 업무 의미를 보존한다.
트랜잭션 안에서 공유 풀에 다시 쓰기를 요청해 스스로 기다리는 경우, 읽기 결과를 닫기 전에 단일 연결을 다시 쓰는 경우를 테스트한다.
파일 작업/LLM/외부 명령은 DB 쓰기 락을 잡은 채 기다리지 않는다.

PG를 공유하는 `agent/testmain_test.go`, `db/testmain_test.go`, `server/testmain_test.go`, `evidence/testmain_test.go`의 advisory lock 초기화는 제거 대상이다.
`testDSN`, `testSetup`, `testDB`, `notifyTestDB`, `evidenceFixture`, `newRetestServer`, `newSideHTTPFixture`와 개별 테스트의 DB 불가 skip을 임시 SQLite 파일 fixture로 교체한다.
DB 열기/스키마/시드 실패는 테스트 실패로 처리한다. 연결 실패 때문에 테스트를 건너뛰고 suite 성공으로 보고하지 않는다.
PG 락 순서 테스트는 구체적인 PG 메커니즘 대신 SQLite에서 보장해야 할 경쟁 결과/원자성/취소를 검사하도록 고친다.
OS symlink 제한이나 FTS 지원 확인 같은 별도 skip을 PG skip과 혼동해 무조건 제거하지 않는다. 제품에서 필수인 FTS 기능은 지정 드라이버로 실행 증거를 남긴다.

## 7. 다음 구현 시작점

다음 단위는 M2.2와 M2.3의 부팅/설정 경로다. SQLite 연결/스키마/시드와 임시 파일 fixture를 함께 만들고 위 실제 부팅 경로에 연결한다.
먼저 새 홈에서 DB 생성·닫기·재열기·연결별 PRAGMA·트랜잭션 취소를 검증하고, 실제 auth/status → auth/init → login → 모델 설정 저장 → 재시작을 이어서 검사한다.
부팅이 요구하는 재검증 복구·도구·로그·알림·작업 메서드도 포함한다. 미사용 저장소나 별도 가짜 setup 서버만 만든 결과는 완료 단위가 아니다.
전체 앱이 일시적으로 깨지는 이식은 기능 브랜치에서 진행하고, main은 검증된 경로를 유지한다.
M2.7 통합 검증 뒤 PG driver/config/schema/서비스와 전환용 인벤토리 도구를 제거한다. SQL 번역기/DB 선택 스위치/PG fallback은 만들지 않는다.


## M2.2a 적용 후 변경된 접점

위 인벤토리는 고정 커밋 기준의 조사 기록이다. 현재 트래픽의 bare-DSN 분기는 제거하고 `internal/sqlitedb.Open`에 실제 연결했다.
설정 조회 오류를 무시하던 인증 핸들러는 오류를 전파한다. 최초 비밀번호는 충돌 시 덮어쓰지 않는 INSERT, 변경은 기존 해시가 일치할 때만 UPDATE한다.
SQLite fixture에서 실제 setting 메서드와 HTTP 인증 핸들러를 검증하며, 해당 fixture가 51개 업무 테이블이나 전체 부팅을 대체하지는 않는다.
다음 구현자는 `db/db.go`/전체 스키마/시드/프로필/서버 생성 의존을 이어서 이식한다. 이번 테스트를 전체 앱 SQLite 전환 완료로 읽지 않는다.
