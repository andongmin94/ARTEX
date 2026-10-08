# 업무 SQLite 저장소 — 전환 브랜치의 구현 경계

기준 main: `33301d8dd28f709fa98897eb6e7e58708d24315f`.
작업 브랜치: `work/sqlite-business-store`. 작업 목록은 `development-plan.md`만 관리한다.
이 문서는 저장 구조의 결정과 검증 경계를 설명하며, 전체 앱의 SQLite 전환 완료를 뜻하지 않는다.

## 실제 저장 경로

`db.Open/OpenContext`의 PostgreSQL 생성/연결/초기화 코드를 로컬 파일 연결로 교체했다.
`internal/sqlitedb.OpenImmediate`는 기존 드라이버의 `_txlock=immediate`를 사용한다.
업무 DB 풀은 최대 4개 연결이며 명시적 트랜잭션은 쓰기 잠금을 먼저 얻는다.
일반 SELECT는 트랜잭션 밖에서 WAL의 커밋된 값을 읽을 수 있다.
기존 트래픽의 `Open` 트랜잭션 정책은 바꾸지 않았다.
장시간 모델·명령·네트워크 작업을 DB 트랜잭션 안에서 실행하지 않는다.

`schema_init.go`는 같은 트랜잭션에서 식별자/버전을 읽고, 새 파일에만 스키마와 시드를 생성한다.
application_id는 `0x41525458`, user_version은 1이다.
식별자가 다른 DB, 식별자 없이 테이블이 있는 DB, 지원하지 않는 버전, 필수 테이블 누락은 오류다.
지원하는 기존 저장소에는 시드를 다시 넣지 않아 사용자가 편집하거나 삭제한 기본값을 복원하지 않는다.
초기화 실패에서는 연결을 닫고 오류를 반환한다. 데이터를 지우거나 PG로 우회하지 않는다.
공통 opener의 WAL 전환이 식별 검사보다 먼저이므로 거부한 타 SQLite의 바이트가 전혀 변하지 않는다는 보장은 하지 않는다. 행/스키마를 덮어쓰지 않는 것이 현재 계약이다.
필수 테이블 검사는 열 정의·인덱스·데이터 전체 무결성 검사의 대체가 아니다.

## 스키마

기존 업무 51개 테이블과 정규화 관계 7개를 포함해 58개 일반 테이블, 명시적 인덱스 103개, 트리거 23개를 정의했다.
`llm_records`와 `llm_usage`도 기본 업무 스키마에 포함한다. 기존 두 별도 PG 생성 함수는 후속 호출자 이식 때 제거한다.

| 이전 표현 | 새 표현 |
| --- | --- |
| assets.task_ids | 기존 task_asset_links를 단일 연결 원본으로 사용 |
| bound_domains / technologies / record_value / open_ports | asset_bound_domains / asset_technologies / asset_records / asset_open_ports |
| findings.asset_ids | finding_assets |
| tools.agents | tool_agents |
| task_archives.source_task_ids | task_archive_sources; 원본 task가 삭제돼도 보관 당시 ID 유지 |
| IP/CIDR | 정규화 텍스트와 family/prefix/고정 길이 BLOB 범위, 검색 인덱스 |
| 동적 JSONB 본문 | json_valid로 검사한 TEXT; 필요한 배열/객체 형태 추가 검사 |
| BIGSERIAL | INTEGER PRIMARY KEY AUTOINCREMENT |

`side_question_requests`는 문자열 id를 NOT NULL UNIQUE로 유지하고 ordinal을 자동 증가 PK로 사용한다.
프롬프트의 순환 외래키, 활성 프로필/실행 중 재검증의 유일성, 증거 참조 및 삭제 의미를 스키마에 반영했다.
대화 삭제 시 진행 중 재검증만 stopped로 변경하고 과거 completed/verdict는 유지한다.
시간은 UTC TIMESTAMP 문자열로 저장한다. SQLite AFTER 트리거에서 변경한 시간과 RETURNING 결과의 차이, Go time.Time 스캔/정렬/집계는 각 업무 쿼리 이식 때 검증해야 한다.
IP/CIDR용 저장 열과 SQL fixture를 추가한 것이며, Go net/netip 정규화와 기존 자산 DSL/범위 검색은 아직 이식하지 않았다.

## 기본값

기본 에이전트 6개, 승인·차단 규칙 20개(19개 enabled), 자산 차단 규칙 4개와 MCP 2개를 seed.sql에 넣었다.
browser와 ScopeSentry는 비활성 상태를 유지한다. 삭제/API 차단 정규식과 규칙 우선순위·활성 상태를 보존했다.
기존 db/db.go의 최종 기본값을 바탕으로 새 DB에 한 번 삽입하며, 과거 배포용 seed v1/v2/v3 보정 경로는 이식하지 않는다.
한 차단 안내 문구에는 원래 정규식에 이미 포함된 /truncate /drop /destroy 예시를 명시했다. 차단 범위는 확장하지 않았다.

## 아직 앱을 실행하면 안 되는 이유

이 브랜치는 배포 대상이 아니다. main의 기존 제품 실행 코드는 변경하지 않는다.
`NewManager`는 아직 기존 DSN을 넘기므로 새 파일 전용 Open으로 앱 부팅할 수 없다.
업무 SQL에는 배열/JSONB/행 잠금/PG 함수/보관 복원 경로가 남아 있으며, 이들을 빈 결과나 무시한 오류로 대체하지 않는다.
`DSN`과 `coordinateWithSchemaMigration`은 아직 이식하지 않은 호출자 때문에 남은 기존 코드다. SQLite opener가 선택하는 호환 경로나 fallback이 아니며 호출자와 함께 제거해야 한다.
db 패키지의 PG 전용 TestMain과 초기화 재시도 테스트는 제거/대체했지만, 다른 패키지의 PG fixture/skip은 아직 남아 있다.
전체 스키마 존재를 전체 API·앱 부팅 성공으로 해석하지 않는다. M2.7 검증 전 main에 병합하지 않는다.

## 로컬 검증 — 2026-10-05

```sh
# SQLite 엔진의 실제 SQL 검사; Python 표준 라이브러리만 필요
python scripts/check-sqlite-business.py

# 프로젝트 의존성 없이 실제 초기화 함수의 제어 흐름 검사
GO111MODULE=off go test -race -count=10 -timeout 60s db/schema_init.go db/schema_init_control_test.go

# 아래는 지정 Go/modernc 의존성이 있는 환경에서 실행할 미검증 범위
# 구 PG 테스트까지 성공했다는 뜻이 아니므로 이름을 제한한다.
go test -race -count=1 -timeout 120s -run '^(TestOpenSeed|TestBusiness|TestDeleteEndpointPathPattern)' ./db
```

Linux Go 1.23.2: 초기화 제어 흐름 최상위 6개 × 10회 race 통과. 의도적으로 실패하는 scripted driver이며 SQLite 엔진 검사는 아니다.
Python SQLite 3.46.1: 실제 스키마/시드 SQL 17개 검사 통과. FK/integrity, 초기 생성 롤백, 관계 삭제, 재검증 보존, JSON/불리언/키, IPv4/IPv6 범위, ordinal, WAL 읽기와 24건 동시 저장 포함.
새 Go SQLite opener 통합 테스트 6개와 변경된 정규식 DB 테스트는 미실행이다. 전체 Go 빌드/회귀, Windows/macOS, NewManager/New 통합, Electron/도구/부하/백업도 미실행이다.
지정 Go 1.26.3 다운로드가 로컬 DNS 제약으로 실패했다. go.mod를 낮추거나 Actions로 우회하지 않았다.
새/재실행 Actions, artifact 업로드, 릴리스 태그 생성은 하지 않았다.
