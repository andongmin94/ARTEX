# 개발 절차

## 작업 단위

`main`이 현재 기준이다. 원격 최신 ref와 계획을 읽고 가장 앞의 미완료 단위를 선택한다.
하나의 단위는 구현, 호출 경로 연결, 회귀 검사, 계획 갱신으로 끝낸다.
전환 도중 전체 앱을 깨뜨릴 수 있는 DB 변경은 기능 브랜치에서 완성한 뒤 병합한다.
작동하지 않는 브랜치를 배포하지 않으며, 이를 이유로 PG fallback이나 이중 저장을 만들지 않는다.

전환 순서는 `실행 기반 → SQLite 저장소와 실제 기능 동등성 → Electron 연결 → 도구 환경 → 배포 정리`다.
주 DB 이식이 끝나기 전에 Electron 포장만 완료했다고 보고하지 않는다.

## 검증 자원 정책

2026-10-05 사용자 요청으로 GitHub Actions 자동 실행을 사용하지 않는다.
`verify.yml`과 `sqlite-foundation.yml`은 `workflow_dispatch`만 허용한다.
수동 실행·재실행도 사용자가 명시적으로 요청한 경우에만 수행한다.
로컬에서 불가능한 검사를 원격 runner로 우회하거나, 일회성 소스 편집 workflow를 만들지 않는다.
커밋에는 `[skip ci]`를 사용하고 검사 실패·미실행을 그대로 보고한다.
기존 테스트 명령과 수동 workflow는 보존한다. 검증 생략을 테스트 성공으로 바꾸지 않는다.
태그 릴리스 workflow는 이번 정책 변경에서 수정하지 않았으며, 별도 요청 없이 태그를 만들지 않는다.

## 기본 로컬 검사

```sh
go test -p 1 -count=1 -timeout 180s ./...
go build ./cmd/artex
cd web
npm ci
npm run check:korean
npm run test:input
npx tsc --noEmit
npm run build:static
```

전체 Go 테스트는 현재 PG 테스트 DB가 필요하다. M2가 완료되면 DB 서비스 없이 같은 범위를 실행하도록 교체한다.

실행 기반의 독립 검사는 다음과 같다.

```sh
go test -race -count=1 -timeout 60s ./config
go test -race -count=1 -timeout 60s cmd/artex/http_lifecycle.go cmd/artex/http_lifecycle_test.go
node --check scripts/check-backend-lifecycle.mjs
```

모델/프롬프트 저장의 새 Go 검사에는 프로젝트 지정 Go와 기존 modernc 의존성이 필요하다.
폐기 가능한 SQLite 파일만 사용하며 모델 API나 외부 대상에 연결하지 않는다.

```sh
go test -race -count=1 -timeout 120s -run '^(TestSQLiteProfile|TestSQLitePrompt|TestPromptTransactionControl)' ./db
```

실제 백엔드의 준비/종료 검사는 `ARTEX_SMOKE_PG_DSN`으로 **이름이 `artex_desktop_smoke`인 폐기 가능한 DB**를 명시하고 실행한다.
일반 `ARTEX_PG_DSN`을 암묵적으로 재사용하지 않는다. 운영 DB나 기존 사용자 데이터로 실행하지 않는다.

```sh
ARTEX_SMOKE_PG_DSN='postgres://artex:TEST_PASSWORD@127.0.0.1:5432/artex_desktop_smoke?sslmode=disable' \
  node scripts/check-backend-lifecycle.mjs ./artex
```

이 검사는 새 한글/공백 경로에서 실제 Go 실행 파일을 시작하고 ready JSON, HTTP health, stdin EOF에 의한 정상 종료를 확인한다.
새 작업이나 외부 대상 요청은 실행하지 않는다. M2 완료 시 이 검사에서 PG 준비 조건을 제거한다.

## 환경 제약 기록

프로젝트 지정 Go 버전이나 의존성을 사용할 수 없으면 `go.mod` 버전을 임의로 낮추지 않는다.
표준 라이브러리만 사용하는 독립 범위에 한해 `GO111MODULE=off`로 격리 검증할 수 있다.
대체 SQL 드라이버를 쓴 제어 흐름 검사와 Python SQLite 실행은 Go modernc 통합 검사가 아니다.
실제 Go/SQLite 버전, 가져온 소스, 명령, 실행하지 못한 범위를 계획에 적는다.
전체 빌드와 native OS 검증은 해당 환경에서 수행한다. CI 사용에는 위 사용자 승인 조건을 적용한다.

## 원격 반영

최신 ref를 다시 확인하고 정상 fast-forward 또는 PR로 반영한다. 동시 변경은 먼저 병합/재검토한다.
함수 이동처럼 함께 적용해야 컴파일되는 변경은 하나의 tree/commit으로 반영한다.
원격 blob SHA가 검사한 파일과 일치하는지 확인한다.
이번에 수행한 로컬 검사와 이전 커밋의 CI 성공 기록을 구분하고, 다음 단위와 남은 위험을 함께 남긴다.
