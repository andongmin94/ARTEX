# 개발 계획 — 유일한 현재 작업 목록

목표: **기존 ARTEX + Electron + Go + SQLite**, Docker/PG 없는 로컬 데스크톱 앱.
UI 기준: **[andongmin94/neobrutal-ui](https://github.com/andongmin94/neobrutal-ui)**. 기능/한국어/화면 흐름을 유지하고 시각·상호작용을 통일한다.
제품 기준: `main`. 전환 코드: `work/sqlite-business-store`. 최종 갱신: 2026-10-05.

## 현재 상태와 다음 시작점

작업 시작 때 main과 전환 브랜치의 최신 ref를 모두 확인한다.
이번 시작 기준은 main `5017b37194a7d364cd9fec193f6b4bb12db4b7ee`, 전환 브랜치 `11b732cc9a8fb3cf73d87d34dad89f0267acf28c`다.
진행 중인 브랜치를 무시하고 main에서 이식을 다시 만들거나 옛 ZIP으로 덮어쓰지 않는다.
미완성 전환 코드는 main에 병합하지 않는다. main에는 현재 계획만 갱신한다.

**이번 M2.2f:** 모델 삭제와 에이전트/대화의 모델 연결, 대화 저장 SQL을 SQLite에 맞게 이식했다.
`DeleteProfileContext`는 기존 업무 opener의 IMMEDIATE 트랜잭션 안에서 활성 모델 보호 → 영향받는 작업 조회 → 삭제/FK 정리 → 다음 모델/리비전 갱신을 수행한다.
PG 행 잠금, 참조 변경 재시도 루프, 그 전용 오류와 보조 함수를 제거했다.
삭제한 커서보다 뒤의 ready 모델만 선택한다. 다음 모델이 없으면 명시적 체인을 비우며 앞의 모델이나 할당량을 소진한 모델을 되살리지 않는다.
체인 없는 직접 참조도 리비전을 갱신한다. 에이전트/대화 참조는 FK로 비우되 대화와 활동 기록은 보존한다.
`SetAgentLLMProfile`, `CreateConversation`, `UpdateConversationProfile`은 같은 쓰기 트랜잭션에서 모델 존재를 확인한다.
대화 제목/고정 수정은 SQLite AFTER 트리거가 갱신한 시간을 같은 트랜잭션에서 다시 읽어 응답한다.
대화 일괄 삭제는 검증된 int64 목록의 JSON TEXT 매개변수와 json_each를 사용한다. PG 배열/불리언 캐스트/now()/시간 캐스트를 제거했다.

**전체 앱의 SQLite 부팅은 아직 미완료다.** main은 여전히 PostgreSQL이며, 전환 브랜치의 NewManager는 아직 기존 DSN을 전달한다.
작업의 모델 체인 교체/조회 전체, 자산/범위, 작업/탐색, 증거/보관, 재검증 직접 SQL, LLM 별도 DDL, server.New 오류 반환과 프록시 bind는 남아 있다.
이번 변경은 모델 삭제와 에이전트/대화 연결의 저장 단위이며, 전체 모델 실행·재검증·도구 시드의 완료가 아니다.
초기화 실패를 무시하거나 빈 결과로 바꾸어 ready를 내보내지 않는다.

**즉시 다음 작업:** 같은 전환 브랜치에서 `server/finding_retests.go:seedFindingRetester`의 직접 tools.agents/PG 잠금을 관계 테이블 기반으로 이식한다.
LLM 기록/사용량의 중복 PG DDL과 startup 호출을 제거하고 새 기본 스키마만 사용하게 한다.
작업 모델 체인과 재검증/로그/알림/작업 복원 쿼리를 연결한 뒤 NewManager/New의 파일 경로, 오류 반환, 종료 정리, 실제 부팅 검사를 완성한다.
이후 자산/범위·탐색·증거/보관까지 M2.7 검증 후 main에 병합한다. 이미 끝난 연결/도구/설정 fixture를 반복 구현하지 않는다.

## 검증 자원 정책

사용자 명시 요청 없이 Actions 실행/재실행/자동 트리거 추가, 임시 편집 workflow, 원격 artifact 업로드를 하지 않는다.
`verify`/`sqlite-foundation`은 수동 전용이며 개발 커밋에는 `[skip ci]`를 넣는다. 릴리스 태그도 만들지 않는다.
로컬 의존성 제약을 원격 runner로 우회하거나 Go 버전을 낮추지 않는다.
기존 artifact/cache와 사용자 DB/설정/증거/볼륨을 임의 삭제하지 않는다.
미실행 검사는 실패/성공과 구분하고, SQL 검사나 격리 드라이버 검사를 실제 Go SQLite/전체 앱 검증으로 표시하지 않는다.

## M0/M1 — 이전 완료 범위

- [x] 제품/설계/작업 지침, SQLite 단일 지원, PG 자동 데이터 이전 제외, neobrutal-ui 필수 기준 확정.
- [x] ARTEX_HOME 절대 경로/쓰기 검사와 config/data/skills 연결, 다른 CWD 설정을 암묵적으로 사용하지 않음.
- [x] loopback/동적 포트/ready JSON, 부모 stdin EOF 종료, cmd 치명적 종료 제거 및 정리.
- [x] 당시 설정/HTTP 테스트와 실제 backend smoke, 3개 OS 실행 기반 검증.

Electron, 전체 업무 SQLite, 도구/자손 프로세스, server.New 오류 반환, 패키징은 M1 완료 범위가 아니다.

## M2. 업무 저장소 SQLite 이식

- [x] **M2.1 인벤토리:** 기존 51개 업무 테이블, 부팅/직접 SQL/트랜잭션/fixture 지도. `docs/sqlite-porting-map.md` 참고.
- [ ] **M2.2 실행 가능한 업무 저장 기반:** 전체 스키마/시드/쓰기/취소/닫기를 실제 NewManager/New 부팅과 연결한다.
- [x] **M2.2a 공통 연결/트래픽:** 기존 main의 경로/연결별 PRAGMA/WAL/취소/재열기/FTS 검증. 이후 OpenImmediate 변경은 통합 검증 필요.
- [x] **M2.3a 설정/인증 저장:** 실제 메서드/HTTP fixture의 재열기, 동시 설정 단일 승자, 조회 오류 fail-closed 검증. 전체 부팅은 아니다.
- [ ] **M2.2b/M2.3b 프롬프트·모델:** 원자 저장과 조회/수정 오류 구현. 기존 modernc 7개/전체 빌드 미실행.
- [ ] **M2.2c 부팅/설정 일관성:** manager 동기 오류/정리, 설정 snapshot/묶음 저장 구현. 독립 Go 11개/SQL 7개 통과. 통합 10개 미실행.
- [ ] **M2.2d 업무 스키마/원자 생성:** 58개 테이블(기존 51+관계 7)/시드/식별자/버전/IMMEDIATE 구현. SQL 17개/제어 6개 통과, opener 통합 6개/전체 앱 미실행.
- [ ] **M2.2e 도구 관계/MCP:** tool_agents 기반 실제 CRUD/원자성/집계와 조건부 MCP args/env 갱신 구현. SQL 21개/제어 5+3개 통과. 실제 opener 도구 8개/MCP 4개 미실행.
- [ ] **M2.2f 모델 참조/대화:** 이번 저장 메서드 이식. SQL 15개, 삭제 제어 15개 하위 사례 및 취소를 race 10회 통과. 실제 업무 opener 기반 Go 테스트 7개/전체 회귀 미실행. 작업 체인 전체 및 실제 앱 부팅과 구분한다.
- [ ] **M2.3 설정·인증 통합:** setup/로그인/모델 저장·복원을 부팅과 연결. JWT 분리, 모든 생성 오류/정리와 기존 인증 정책 검증.
- [ ] **M2.4 자산·범위:** 관계/DSL/net/netip, IPv4·IPv6 경계, 기업 귀속/중복 원자성 검증.
- [ ] **M2.5 작업·탐색:** 작업/세션/의도/대화/사실/취약점/승인/재검증/사용량/기록, 다중 저장/취소 검증.
- [ ] **M2.6 증거·보관:** DB/트래픽/본문/보관 연결과 삭제·재검증·증거 동등성. PG JSON 행/배열/시퀀스 복원을 명시적 열/키 매핑으로 교체.
- [ ] **M2.7 통합:** PG 없이 setup → 로컬 모델 fixture → 작업/기록 → 증거 → 종료/재실행. 기존 PG fixture/skip을 SQLite로 교체하고 PG driver/config/서비스/smoke/헬퍼/전환 도구를 제거한 뒤 병합.

main의 PG는 SQLite 오류 fallback이 아니다. DB 선택 옵션, SQL 번역기, 구 데이터/보관 변환기, 임시 호환 계층을 만들지 않는다.

## M3. Electron 실제 UI + neobrutal-ui

UI 작업은 `docs/ui-design.md`와 최신 원본을 먼저 읽는다. 이번 단위에는 UI 소스 변경이 없다.

- [ ] 공통 토큰/타이포그래피/컨트롤/표/오버레이와 호출부를 함께 교체. 출처/저작권 보존.
- [ ] 셸/사이드바부터 대시보드/대화/작업/자산/증거/트래픽/설정 전체, 빈/로딩/오류/선택/비활성/승인 상태 적용.
- [ ] Electron 메인/렌더러 분리, 패키지/lockfile, Go/정적 UI 재사용. Next 서버 추가 금지.
- [ ] 단일 인스턴스/userData/ready/장애 화면/종료/부모 강제 종료, loopback·앱 세션 인증·Origin/Host·제한된 IPC/CSP/탐색 검증.
- [ ] 실제 패키지 사용 흐름, 라이트/다크/1280·1440px/Windows 배율/IME/키보드/축소 모션/긴 텍스트 및 실제 화면 검수.

독립 쇼케이스나 일부 화면만으로 전체 UI 완료를 표시하지 않는다. 이전 mock 캡처를 새 디자인/앱 실행 증거로 재사용하지 않는다.

## M4/M5. 도구·신뢰성·배포

- [ ] 셸/PTY/Python/Node/브라우저/MCP/CLI의 OS별 지원, 전용 경로의 버전/출처/해시/라이선스/복구와 필수 도구 준비 차단을 구현한다.
- [ ] 네트워크/작업 공간/권한과 자손 프로세스 정리, 실제 다중 기록 부하, kill/디스크 오류/손상과 일관된 DB+증거 백업/복원을 검증한다.
- [ ] Electron 업데이트 일원화. 기존 Go 업데이트/시작 스크립트와 Docker/PG 안내·의존성을 제거한다. 사용자 데이터는 삭제하지 않는다.
- [ ] Windows 우선 설치/서명/업데이트, macOS/Linux 실제 지원 검증, README 교체, neobrutal-ui 누락/구 스타일 최종 확인.

## 이전 기록

이전 상세 구현/검사 기록은 `11b732cc9a8fb3cf73d87d34dad89f0267acf28c`의 이 문서 및 `docs/sqlite-business-store.md`에 보존되어 있다.
M1/M2.1/M2.2a/M2.3a의 과거 CI 성공은 새 전환 코드의 빌드/통합 성공이 아니다.
M2.2b 이후 modernc 통합 미실행 기록은 이번에도 그대로 유지한다. 기존 artifact 확인을 위해 CI를 재실행하지 않는다.

## 이번 로컬 검증 — M2.2f

기준: 전환 브랜치 `11b732cc9a8fb3cf73d87d34dad89f0267acf28c`. Linux Go 1.23.2, Python SQLite 3.46.1.
현재 세션에는 전체 clone/프로젝트 지정 Go 1.26.3/modernc 의존성이 없다. GitHub와 proxy.golang.org DNS 연결 실패를 확인했다.
새 소스를 live GitHub에서 읽고 원본 3개 파일의 Git blob SHA를 일치시킨 후 변경했다. 옛 ZIP/패치를 가져오지 않았다.

실행 결과:

- `check-sqlite-profile-references.py`: **SQL 수준 15개 통과**. 실제 새 Go 소스의 SQL과 기준 schema.sql에서 읽은 11개 테이블 및 관련 인덱스/트리거를 사용했다. 로컬 부분 스냅샷은 `--schema`로 전달했다.
- 활성/없는 모델 삭제 거부, 뒤의 ready 모델만 선택, 체인 없는 직접 참조/리비전, 여러 작업, FK 참조 정리/대화 활동 보존, 실패 롤백, 삭제와 연결 변경 경쟁, WAL 읽기, 재열기/무결성을 검사했다.
- 대화 생성 실패의 orphan 방지, 연결 검증/해제, 제목/고정/AFTER 트리거 시간, 일괄 삭제의 중복/없는 ID, result 사용량 집계도 SQL로 검사했다. 전체 58개 스키마/시드나 실제 Go 드라이버 검사가 아니다.
- 실제 `DeleteProfile`/`DeleteProfileContext`와 `profile_references_control_test.go`를 격리해 `GO111MODULE=off go test -race -count=10 -timeout 60s -v .` 실행. **제어 테스트 2개: 15개 하위 사례+취소, 각 10회 통과**. driver는 오류 주입용이다.
- 새 3개 업무 소스와 테스트 전체도 외부 타입/함수 경계를 명시한 격리 패키지에서 컴파일하고 위 제어 테스트만 실행했다. 이 경계의 Open은 사용 불가 오류만 반환하며 통합 테스트를 실행하지 않았다.
- gofmt/Go parser 및 선언 대조 통과. config는 모델 연결/존재 확인 2개 함수만 변경. 프로필의 PG 재시도/잠금 helper와 전용 오류를 제거. 대화의 저장/조회 쿼리 변경 외 활동 페이지/상세 메서드는 유지한다.

미실행:

- `profile_references_sqlite_test.go`의 **실제 Open/전체 schema/seed 기반 Go 테스트 7개**. 모델 삭제/후속 선택/참조/보존/롤백/단일 연결/동시 변경/대화 시간/일괄 삭제/재실행을 검사하도록 작성했다.
- 이전 modernc 테스트, Go 1.26.3 전체 빌드/회귀, NewManager/New 통합, Windows/macOS, Electron/화면/도구/전체 부하/백업.
- 이번 변경에 retester와 LLM DDL 제거를 포함하지 않았다. 다음 시작점에 남긴다.

```sh
# Python 표준 라이브러리로 실제 저장 SQL의 선택된 범위를 검사한다.
python scripts/check-sqlite-profile-references.py
# 지정 Go/의존성이 있는 로컬 저장소에서 실행할 실제 드라이버 검사:
go test -race -count=1 -timeout 120s -run '^(TestProfileReferenceDelete|TestSQLiteProfileReference|TestSQLiteConversationReferences)' ./db
```

Actions 실행/재실행/업로드, 릴리스 태그 생성은 하지 않는다. 부분 검증을 M2.2/M2.7 완료로 표시하지 않는다.
