# 개발 계획 — 유일한 현재 작업 목록

목표: **기존 ARTEX + Electron + Go + SQLite**, Docker/PG 없는 로컬 데스크톱 앱.
UI 기준: **[andongmin94/neobrutal-ui](https://github.com/andongmin94/neobrutal-ui)**. 기존 기능·한국어·화면 흐름을 유지하고 시각·상호작용을 통일한다.
작업 기준: **main 단일 브랜치**. 최종 갱신: 2026-10-07.

## 현재 상태 — 통합과 제품 완성은 다르다

사용자가 모든 브랜치의 main 반영과 삭제를 명시적으로 요청했다. 과거의 미완성 전환 별도 브랜치 유지 지침은 종료한다.
이번 시작 기준은 main `c9778d5ebbf7c01521a1d19d2b1179edb8be2e30`, 업무 전환 `fcff01296af3e1f0e8f4677da312d858b3fcba59`, foundation `4460f0f32790bbf4fb14c8d3837e48f3f7875fcc`다.
foundation HEAD와 이미 main에 squash 병합한 `81757868533bcb7b4ba15186bfc326d6ffc8f898`의 트리는 `b6963c5d3cdf314b0a6624d47db54e0747445935`로 완전히 같다. 구 구현을 최신 파일 위에 덮어쓰지 않는다.
업무 전환과 main이 각자 수정한 두 문서도 현재 blob이 동일함을 확인했다. 통합은 업무 전환의 최신 트리와 이번 변경을 사용하고 세 HEAD를 부모로 보존한다.
앞으로는 최신 main에서 이어간다. 옛 ZIP/패치나 잔존 work 브랜치를 작업 기준으로 선택하지 않는다.

**현재 main은 SQLite 전환 중인 개발 상태다. 전체 앱 부팅/기능 동등성/설치 패키지는 아직 검증되지 않았다.**
`NewManager`는 기존 DSN을 파일 전용 Open에 전달하는 미완료 경로이며, 작업 생성/자산/탐색/재검증/증거/보관/알림/부가 질문 등에 PG SQL이 남아 있다.
`server.New`의 JWT fatal/일부 시작 오류 무시와 비동기 프록시 bind도 남아 있다. 빈 결과나 무시한 오류로 ready를 내보내지 않는다.
main 통합을 배포/완료로 해석하지 않는다. 실행 가능한 제품을 위해 아래 미완료 경로를 실제 테스트와 함께 연결해야 한다.

원격 브랜치 이름 삭제는 현재 연결의 GitHub 기능에 delete-ref/branch가 없어 이 변경 작성 시 미수행이다.
원격 통합 확인 후 각 이전 HEAD가 main에 포함된 것을 확인하고 브랜치 이름만 삭제한다. 삭제 여부는 실제 목록으로 확인한다.
사용자 DB/증거/설정/볼륨, 릴리스 태그, artifact/cache를 정리 대상으로 삼지 않는다.

## 이번 M2.2i — 작업 모델 목록과 전환 저장

- `db/task_context.go`의 실제 목록 교체/할당량 소진 전환에서 PG 행 잠금과 now()를 제거했다. 기존 OpenImmediate의 짧은 쓰기 트랜잭션 안에서 검증/관계 변경/현재 모델/리비전을 함께 저장한다.
- 명시적 `?N` 매개변수를 사용한다. 일괄 조회의 PG 배열은 int64 목록을 JSON TEXT로 바인딩한 json_each 조회로 교체했다. 세 일괄 조회는 유지하며 N+1 조회나 이중 저장을 추가하지 않았다.
- 모델 전환 도중 SELECT/UPDATE/COMMIT이 실패하면 반환하는 전환 구조체를 비운다. 커밋되지 않은 Advanced/NextProfileID를 성공한 전환으로 반환하지 않는다.
- 이전 리비전의 늦은 오류는 새 목록을 바꾸지 않는다. 현재 커서 뒤의 ready 모델만 고르고, 소진 후 앞의 모델을 되살리지 않는 동작을 유지한다. 삭제된 작업의 늦은 전환과 음수 활성 모델 입력을 거부한다.
- 같은 파일의 의도 차단/재개 시간 SQL과 RowsAffected 오류 전파를 이식했다. 기존 대상/승인/차단 정책이나 도구 실행 본문은 변경하지 않는다.
- PG 프로세스/행 잠금 관찰에만 의존하던 `task_context_lock_test.go`를 제거하고 SQLite 경합/원자성 검사로 대체했다. 기존 모델 참조의 SQLite 테스트도 유지한다.

**즉시 다음 작업:** main에서 지정 Go/의존성을 확보해 신규 실제 SQLite 6개와 이전 미실행 modernc 검사부터 실행한다. 실패를 skip으로 바꾸지 않는다.
이후 실제 시작 경로의 재검증 복구·로그·알림·부가 질문·작업 복원 SQL과 작업 생성/자산 관계를 이식한다.
NewManager/New의 파일 경로·오류 반환·호출자/자원 정리·프록시 bind를 연결하고 실제 setup → 로그인 → 저장 → 종료 → 재실행을 검증한다.
끝난 설정/도구/모델 참조/재검증 시드/LLM DDL 제거/이번 모델 목록 저장을 다시 만들지 않는다.

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
- [ ] **M2.2 실행 가능한 기반:** 전체 스키마/시드/쓰기/취소/닫기를 실제 NewManager/New에 연결.
- [x] **M2.2a 연결/트래픽:** 이전 경로/PRAGMA/WAL/취소/재열기/FTS 검증. OpenImmediate 전체 통합은 별도.
- [x] **M2.3a 설정/인증 저장:** 실제 메서드/HTTP fixture의 재열기/동시 최초 설정/fail-closed. 전체 부팅 아님.
- [ ] **M2.2b/M2.3b 프롬프트·모델:** 원자 저장/조회·수정 오류 구현. 실제 드라이버/전체 빌드 미실행 범위 남음.
- [ ] **M2.2c 부팅/설정:** manager 오류/정리, snapshot/묶음 저장. 전체 생성/실제 부팅 미완료.
- [ ] **M2.2d 스키마:** 58개 테이블/시드/식별자/버전/IMMEDIATE 구현. 실제 opener 통합/전체 앱 미검증.
- [ ] **M2.2e 도구/MCP:** 관계 CRUD/집계/원자성/조건부 MCP 저장 구현. 실제 드라이버 미실행 기록 유지.
- [ ] **M2.2f 모델 참조/대화:** 모델 삭제/에이전트·대화 연결/대화 저장 이식. 실제 드라이버/전체 회귀 미검증.
- [ ] **M2.2g 재검증 시드:** 올바른 FK 순서/단일 트랜잭션/사용자 편집·삭제 보존. 전체 시작 미검증.
- [ ] **M2.2h LLM 기록/사용량:** 중복 PG DDL/호출 제거, 검색/UTC 집계/작업별 조인/원장 보존. 실제 opener/Recorder 통합 미실행.
- [ ] **M2.2i 작업 모델 목록:** 이번 실제 저장/조회/오류 반환 이식. SQL 10/제어 3+하위11 통과. 실제 SQLite 6개/전체 앱 미실행.
- [ ] **M2.3 설정·인증 통합:** 실제 setup/로그인/모델 저장·재실행, JWT 분리/생성 오류/정리/인증 정책.
- [ ] **M2.4 자산·범위:** 관계/DSL/net/netip, IPv4·IPv6 경계/기업 귀속/중복 원자성.
- [ ] **M2.5 작업·탐색:** 작업/의도/대화/사실/취약점/승인/재검증/사용량/기록/다중 저장/취소.
- [ ] **M2.6 증거·보관:** DB/트래픽/본문/보관/복원 동등성. PG JSON/배열/시퀀스 대신 명시적 열/키 매핑.
- [ ] **M2.7 통합:** PG 없이 로컬 모델 fixture → 작업/기록/증거 → 종료/재실행. PG fixture/skip/driver/config/서비스/smoke/헬퍼/전환 도구 제거.

DB 선택 스위치/SQL 번역기/PG fallback/구 데이터 자동 이전기/임시 호환 계층은 추가하지 않는다.

## M3. Electron 실제 UI + neobrutal-ui

UI 작업 전에 `docs/ui-design.md`와 최신 지정 원본을 읽는다. 이번에는 UI/Electron 소스 변경 없음.

- [ ] 공통 토큰/타이포그래피/컨트롤/표/오버레이와 호출부를 함께 교체, 출처/저작권 보존.
- [ ] 셸/사이드바/대시보드/대화/작업/자산/증거/트래픽/설정 전체와 빈/로딩/오류/선택/비활성/승인 상태.
- [ ] Electron 메인/렌더러 분리, lockfile/Go/정적 UI 재사용, 단일 인스턴스/userData/ready/장애/종료.
- [ ] loopback·앱 세션 인증·Origin/Host·제한된 IPC/CSP/탐색과 부모 강제 종료 검증.
- [ ] 실제 패키지/라이트·다크/1280·1440px/Windows 배율/IME/키보드/축소 모션/긴 텍스트 검수.

빈 창/일부 쇼케이스/mock 캡처로 전체 제품 또는 UI 완료를 표시하지 않는다.

## M4/M5. 도구·신뢰성·배포

- [ ] OS별 셸/PTY/Python/Node/브라우저/MCP/CLI, 전용 경로·버전·출처·해시·라이선스·복구/준비 상태 차단.
- [ ] 권한/작업 공간/네트워크/자손 프로세스, 다중 기록 부하, kill/디스크 오류/손상, 일관된 DB+증거 백업/복원.
- [ ] Electron 업데이트 일원화와 Go 자체 업데이트/시작 스크립트/Docker/PG 안내·의존성 제거. 사용자 데이터 보존.
- [ ] Windows 우선 설치/서명/업데이트, macOS/Linux 실제 지원 검증, README 교체, 전체 스타일 확인.

## 이번 로컬 검증 — M2.2i

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
