# 업무 SQLite 저장소 — 구현 경계

초기 구현 기준: `33301d8dd28f709fa98897eb6e7e58708d24315f`.
2026-10-07 사용자 요청으로 작업을 main에 통합했다. 현재 작업 목록은 `development-plan.md`만 관리한다.
과거 work/sqlite-business-store 참조는 이력이며 재개할 브랜치가 아니다. **전체 앱 전환 완료를 뜻하지 않는다.**

## 실제 저장 경로

`db.Open/OpenContext`는 PostgreSQL DSN이 아니라 로컬 파일 경로를 받는다.
`internal/sqlitedb.OpenImmediate`는 기존 modernc 드라이버의 `_txlock=immediate`를 사용한다.
업무 DB 풀은 최대 4개 연결이며 명시적 트랜잭션은 쓰기 잠금을 먼저 얻는다.
일반 SELECT는 트랜잭션 밖에서 WAL의 커밋된 스냅샷을 읽는다. 기존 트래픽 Open의 트랜잭션 정책은 유지한다.
장시간 모델·명령·네트워크 작업을 DB 트랜잭션 안에서 실행하지 않는다.

`schema_init.go`는 한 트랜잭션에서 식별자/버전을 읽고 새 파일에만 스키마와 시드를 생성한다.
application_id는 `0x41525458`, user_version은 1이다.
다른 식별자, 식별자 없이 테이블이 있는 DB, 미지원 버전, 필수 테이블 누락은 오류로 거부한다.
기존 저장소에 시드를 다시 넣거나 사용자의 기본값 편집/삭제를 복원하지 않는다.
오류 시 연결을 닫고 반환한다. 데이터를 지우거나 PG로 우회하지 않는다.
WAL 전환은 식별 검사보다 앞서므로 거부한 다른 SQLite 파일의 바이트가 전혀 변하지 않는다는 보장은 아니다.
필수 테이블 존재 확인은 열/인덱스/전체 데이터 무결성 검사의 대체가 아니다.

## 스키마와 기본값

초기 스키마는 기존 업무 51개와 관계 7개를 포함하는 58개 일반 테이블, 명시적 인덱스 103개, 트리거 23개다.
LLM 기록과 사용량 테이블도 여기에서 생성한다. M2.2h에서 두 별도 PG 생성 함수와 startup 호출을 제거했다.

| 이전 표현 | SQLite 표현 |
| --- | --- |
| assets.task_ids | 기존 task_asset_links를 단일 연결 원본으로 사용 |
| bound_domains/technologies/record_value/open_ports | asset_bound_domains/asset_technologies/asset_records/asset_open_ports |
| findings.asset_ids | finding_assets |
| tools.agents | tool_agents |
| task_archives.source_task_ids | task_archive_sources; 원본 작업 삭제 뒤에도 보관 당시 ID 유지 |
| IP/CIDR | 정규화 텍스트와 family/prefix/고정 길이 BLOB 범위 |
| JSONB/배열 | 검증된 JSON TEXT 또는 검색 가능한 관계 테이블 |
| BIGSERIAL | INTEGER PRIMARY KEY AUTOINCREMENT |

프롬프트 순환 FK, 활성 모델/실행 중 재검증 유일성, 증거 참조와 대화 삭제 시 진행 중 재검증 중단을 반영했다.
시간은 UTC TIMESTAMP다. AFTER 트리거와 RETURNING 시간 차이 및 Go time.Time 스캔은 실제 드라이버로 검증해야 한다.
IP/CIDR 저장 열이 있어도 Go 정규화/자산 DSL/범위 검색의 이식이 완료된 것은 아니다.
기본 에이전트 6개, 승인·차단 규칙 20개(19개 enabled), 자산 차단 규칙 4개, MCP 2개를 한 번 생성한다.
browser/ScopeSentry는 비활성 상태를 유지한다. PG 데이터 자동 이전이나 예전 seed 보정 경로는 지원하지 않는다.

## 현재 미완료 경계

main에 모은 것은 전환 작업의 코드/이력이다. 배포 가능한 전체 앱이라고 주장하지 않는다.
`NewManager`의 파일 경로 연결, `server.New` 오류 전파와 종료 정리, 실제 프록시 bind 검증이 남아 있다.
업무 SQL의 미이식 배열/JSONB/행 잠금/PG 함수/보관 복원을 빈 결과나 무시한 오류로 대체하지 않는다.
`DSN`과 `coordinateWithSchemaMigration`은 아직 미이식 호출자 때문에 남은 코드이며 SQLite fallback이 아니다. 호출자를 이식하면서 제거한다.
다른 패키지의 PG fixture/skip과 전체 업무 테스트도 SQLite로 옮겨야 한다.
main 병합과 실제 부팅/전체 API/Windows·macOS/Electron/도구/부하/백업 검증은 별도다.

## 검증 기록

2026-10-05 초기 코드의 Go 1.23.2 제어 6개 × race 10회 및 Python SQLite 3.46.1 SQL 17개는 당시 통과했다.
이전 기록과 경계는 `fcff01296af3e1f0e8f4677da312d858b3fcba59` 및 이전 커밋의 문서에 보존한다.
현재 구현의 통과/미실행 기록은 development-plan.md를 확인한다. 예전 성공을 새 통합 코드의 성공으로 재사용하지 않는다.
지정 Go/modernc가 없는 격리 검사는 실제 드라이버 검사가 아니다. Actions로 우회하지 않는다.
