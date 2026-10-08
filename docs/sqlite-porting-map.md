# SQLite 이식 지도

조사 기준: ARTEX `babea04074a090ae66953c43f335a8580ddaa27c` (2026-10-05).
진행 상태는 `development-plan.md`만 관리한다. 이 문서는 이식 범위/순서의 근거다.
현재 업무 DB는 PostgreSQL이며 이 문서/인벤토리 도구 추가는 DB 교체 완료를 뜻하지 않는다.

## 재현 가능한 전체 소스 조사

```sh
go run ./scripts/sqlite-inventory > sqlite-inventory.md
go test -race -count=1 -timeout 60s ./scripts/sqlite-inventory
```

CI의 `storage-inventory` job도 같은 코드를 실행하고 `sqlite-porting-inventory` artifact와 job 로그에 보고서를 남긴다.
DB/LLM/외부 대상에 연결하지 않는다. 전체 Go/SQL 파일을 읽되 .git/node_modules/vendor 및 빌드 출력·조사 도구 자체는 제외한다.
Go AST의 import/문자열/호출을 조사해 직접 SQL, PG 전용 기능, 트랜잭션/연결의 호출 위치, 테스트 skip, 스키마 선언을 보고한다.
Go 주석은 제외한다. 함수 이름·파일·라인만 기록하고 문자열 원문/DSN/키를 출력하지 않는다.

**정적 후보 목록이지 완전한 SQL 분석기나 기능 동등성 증명이 아니다.**
동적 SQL 문자열 조립, 임포트된 라이브러리의 저장소, 메서드 이름 충돌은 수동 검토가 필요하다.
SQL 파일은 텍스트로 조사하므로 주석의 키워드가 후보에 포함될 수 있다.
후보가 없다는 것만으로 이식 완료를 주장하지 않는다. 전체 API/행동 fixture 및 부하 검증이 완료 기준이다.

## 현재 직접 확인한 경계

| 경계 | 파일 | 이식 시 결정 |
| --- | --- | --- |
| 연결/시드/잠금 | `db/db.go`, `db/schema.sql` | pgx·PG 카탈로그·스키마 잠금을 제거하고 새 SQLite 스키마/시드/연결 정책으로 교체 |
| 실행 설정 | `config/config.go`, `server/manager.go`, `cmd/artex/main.go` | PG DSN 대신 ARTEX_HOME 아래 업무 DB 경로, 시작 실패/종료 처리 연결 |
| 배열/주소/검색 | `db/schema.sql`의 assets/company_scope/task_scope | 관계 검색용 배열 정규화, IPv4/IPv6 범위 비교와 인덱스 재설계 |
| LLM 기록 | `llmrec/llmrec.go` → `db.DB` | 기존 별도 SQLite라고 가정하지 말고 업무 DB 이식 범위에 포함 |
| 트래픽 | `traffic/traffic.go` | 기존 modernc SQLite·FTS5·블롭 저장은 별도 유지. 외부 증거/업무 DB 참조도 검증 |
| 테스트 초기화 | `db/db_test.go` | `testDSN`/`TestOpenSeed`의 PG 불가 skip을 SQLite 임시 파일 fixture와 명시적 실패로 교체 |

트래픽 Open은 경로에 `?`가 있을 때 PRAGMA DSN 대신 bare path를 사용하는 분기가 있다.
새 SQLite 경로 정책은 이 분기를 그대로 복제하지 않는다. 모든 연결의 설정과 실제 파일 위치를 URI/특수문자 회귀 테스트로 검증한다.
메인 업무 DB와 트래픽 인덱스를 한 파일로 합치는 것은 이번 요구사항이 아니다.

## 전체 실행 결과에 따라 확정할 이식 묶음

설정/인증, 자산/범위, 작업/탐색/기록, 증거/보관, 직접 SQL 호출자, 테스트 fixture를 분리해서 검토한다.
각 묶음은 스키마만 아니라 Go 쿼리·스캔 타입·트랜잭션 경계·API 사용 경로·실패 테스트를 함께 교체해야 한다.
전체 인벤토리 결과를 확보한 뒤 실제 파일/함수와 공유 경계를 이 절에 추가한다.

최초 실행 경로는 `cmd/artex → server.NewManager → db.Open → 시드 → setup/인증/설정`으로 잡는다.
하지만 Manager가 부팅 중 다른 업무 테이블을 참조한다면 이를 함께 이식해야 한다. 단순 설정 테이블만 만든 별도 데모는 완료 단위가 아니다.
전체 전환을 기능 브랜치에서 검증하고 PG/SQLite 선택 스위치나 문자열 SQL 번역기로 main을 억지로 연결하지 않는다.
