# 개발 절차

## 현재 작업 방식

2026-10-07 사용자 요청으로 작업 브랜치의 내용을 `main`에 통합한다. 이후 재개 기준은 최신 `main`이다.
과거의 기능 브랜치 유지/통합 검증 뒤 main 병합 지침은 종료한다. 새 장기 전환 브랜치를 만들지 않는다.
main의 실제 SQLite/Electron 검증 범위는 development-plan.md에 기록한다. main 병합만으로 배포 가능한 버전이라고 판단하지 않는다.
최신 ref와 미커밋 변경을 확인하고 유일한 현재 작업 목록인 `docs/development-plan.md`의 앞선 미완료 단위를 진행한다.
한 단위는 구현 → 실제 호출자 연결 → 검사 → 계획 갱신 → 원격 반영이다.

순서는 `실행 기반 → SQLite 기능 동등성/실제 부팅 → Electron/UI → 도구 환경 → 배포 정리`다.
오류를 숨기는 우회로, 이중 저장, DB fallback, 임시 SQL 번역기를 만들지 않는다.
사용자 데이터 대신 폐기 가능한 임시 파일과 로컬 HTTP/모델 fixture로 검사한다.

## Actions 금지

2026-10-05 사용자 요청으로 `verify`와 `sqlite-foundation`은 workflow_dispatch만 허용한다.
명시 요청 없는 실행/재실행, push/PR 트리거 추가, 원격 runner 우회, 임시 편집 workflow/artifact 업로드를 금지한다.
커밋에는 `[skip ci]`를 넣고 태그/배포는 만들지 않는다. 기존 검사 내용과 artifact/cache는 임의 삭제하지 않는다.

## 로컬 검사

프로젝트 지정 Go 버전과 기존 modernc 의존성을 확보한 환경에서 실제 SQLite 검사부터 실행한다.
아래 명령은 제어 검사와 실제 업무 Open/schema/seed 검사를 함께 실행한다. 실패를 skip으로 바꾸지 않는다.

```sh
go test -race -count=1 -timeout 120s -run '^(TestTaskChainControl|TestSQLiteTaskChain)' ./db
go test -race -count=1 -timeout 120s -run '^(TestLLMStorageControl|TestSQLiteLLMStorage)' ./db
go test -race -count=1 -timeout 120s -run '^TestRecorderPersistsRawWireBodies$' ./server
```

이전 미실행 SQLite 테스트의 범위와 결과는 계획과 해당 구현 커밋을 확인한다.
다음 검사는 독립된 실제 SQLite 임시 파일로 실행한다. 전체 통과 여부는 현재 소스의 실제 결과로 기록한다.

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

`check-backend-lifecycle.mjs`는 새 한글/공백 SQLite 데이터 홈에서 실제 ready/HTTP/부모 EOF 종료를 검사한다.
`desktop`의 `npm run test:electron`은 실제 Go와 정적 UI를 포함한 Electron을 실행한다.
외부 모델이나 실제 대상에 작업을 실행하지 않는다. 요청하지 않은 운영 DB를 테스트에 사용하지 않는다.

## 환경 제약

Windows 브라우저의 실제 통합 검사는 `ARTEX_TEST_BROWSER_EXECUTABLE`에 공개 Electron 실행 파일을 지정하고 `TestWindowsBrowserBrokerIntegration`을 실행한다. 제품의 `-prepare-browser-runtime EXE -browser-runtime-home HOME` 경로로 공개 실행 폴더만 준비하며 Chromium 전용 SID ACL이나 사용자 홈 권한을 추가하지 않는다. 이 검사는 실제 renderer 토큰·9개 MCP 도구·Go 대상 범위·저장소/파일/프로세스 격리를 사용하며 실행 파일을 지정하지 않으면 skip이다.

`ARTEX_TEST_TOOL_ROOT`와 `ARTEX_TEST_TOOL_MANIFEST_SHA256`은 빌드한 도구 폴더와 해당 manifest의 SHA256을 지정한다. 별도 Chrome은 포함하지 않으며 실제 브라우저는 Electron을 사용한다. `ARTEX_STABILITY_SECONDS=120`의 `TestDesktopBusinessSoakPreservesDataAfterRestart`는 8개 업무 lane과 로컬 모델 fixture의 초기 Engine 실행·취소2회, 재열기 후 행/본문/integrity/FK를 검사한다. Engine이 전체120초 내내 실행되는 부하나 물리 디스크 장애로 표현하지 않는다. Windows `-race`에는 별도로 확인한 C 컴파일러를 프로세스 환경에만 지정한다.

지정 Go/의존성 다운로드가 실패하면 버전을 낮추거나 Actions로 우회하지 않는다.
표준 라이브러리만 쓰는 정확한 업무 함수의 격리 검사는 가능하지만 경계 타입/오류 주입 드라이버를 명시한다.
Python SQLite에서 SQL을 실행한 결과는 Go 스캔/드라이버/전체 앱 부팅의 증거가 아니다.
`python scripts/check-sqlite-task-context.py`는 실제 Go SQL과 선택된 9개 테이블만 검사한다. 전체 스키마 테스트가 아니다.

## 통합과 브랜치 정리

원격 변경 전 최신 main/대상 브랜치 HEAD를 다시 확인한다. 비교 기준이 달라졌으면 새 변경부터 검토한다.
이미 squash 병합한 브랜치는 트리 동일성을 확인해 구 소스로 되돌리지 않는다.
여러 브랜치를 통합할 때 각 부모 이력을 보존하고 최신 파일을 기준으로 충돌을 해결한다.
브랜치 삭제 전 해당 HEAD가 main의 조상임을 확인한다. 이름만 옮기거나 삭제 실패를 성공으로 표시하지 않는다.
원격 blob과 검사한 로컬 파일의 SHA를 대조하고 실제 ref/Actions 상태를 읽어 마무리한다.
