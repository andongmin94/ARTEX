# ARTEX Electron 실행 기반

Go 업무 로직과 SQLite를 그대로 사용하고, 정적 UI를 포함한 Go 프로세스를 Electron이 관리한다.
Node가 DB에 연결하지 않는다. 시작 오류는 한국어 화면에 표시하며 사용자가 다시 시도하거나 종료할 수 있다.

```powershell
cd web
npm ci
cd ../desktop
npm ci
npm run build
npm start
npm run test:electron
```

`npm run package`는 Windows 실행 폴더를 생성한다. 서명·설치 프로그램·자동 업데이트는 아직 포함하지 않는다.
Go가 PATH에 없으면 개발 빌드에서 `$env:ARTEX_GO='<지정 Go 실행 파일 절대 경로>'`를 사용한다.
출력은 `desktop/dist/ARTEX-win32-<arch>`이며 기존 출력을 자동 삭제하거나 덮어쓰지 않는다.
개발·패키지 검사는 `--artex-home=<절대 경로>`로 폐기 가능한 새 데이터 경로를 명시할 수 있다.
기본 실행은 Electron의 `userData`를 사용하며 기존 설정·DB·스킬을 자동 삭제하지 않는다.
준비가 끝나면 비밀번호 입력 없이 작업 화면으로 바로 진입한다. 기존 비밀번호 설정과 사용자 데이터는 유지한다.
기록 프록시는 `127.0.0.1:8788`을 사용한다. 포트 충돌은 시작 실패로 표시하며 해소 후 다시 시도할 수 있다.

UI의 Mono는 색상 테마 이름이며 글꼴은 Pretendard Variable 하나를 사용한다. 웹 UI의 글꼴은 Go에 내장하고, Go 준비 전 시작·오류 화면은 동일한 `PretendardVariable.woff2`를 `resources/fonts`에서 읽는다. 코드·진단 로그도 같은 글꼴을 사용하며 글꼴 선택 설정은 제공하지 않는다.
빌드에는 로컬 글꼴과 `resources/licenses/OFL-Pretendard.txt`의 저작권·SIL Open Font License를 포함한다. 실행 중 원격 글꼴을 요청하지 않는다.

렌더러는 sandbox/contextIsolation을 유지하고 Node·파일·명령 API를 제공하지 않는다.
IPC는 메인 프레임과 URL을 검증한 뒤 상태 조회·실패 재시도·앱 종료만 허용한다.
Go ready PID/loopback URL을 검증하며 외부 탐색/새 창/권한은 차단한다.
백엔드 시작마다 메인 프로세스가 새 앱 세션 키를 만들고 환경변수로 Go에 전달한다.
실제 backend origin의 HTTP 요청에만 `X-Artex-Desktop-Session`을 주입한다.
앱 세션 키는 렌더러·IPC·URL·로그로 전달하지 않는다. Go가 검증된 앱 요청에만 JWT를 자동 발급하며 기존 API의 JWT 검증도 유지한다.
CSP는 빌드한 HTML의 인라인 스크립트 해시를 사용한다.
앱 종료 시 부모 stdin을 닫고 Go 종료를 기다린 뒤 제한 시간을 넘기면 자식 프로세스를 종료한다.

Playwright는 실제 Electron과 임시 userData의 Go/SQLite를 사용한다. 호스트의 ARTEX·모델 인증 환경변수를 제거하고 검사 전용 loopback 모델/HTTP 서버만 사용한다.
`npm run test:electron`은 새 홈/기존 비밀번호 데이터의 자동 진입·잘못된 토큰 복구·인증 실패 재시도·모델 저장·작업 생성/일시 중지·자산 등록/수정/삭제/연결·실제 HTTP 캡처·취약점 증거 연결·보관/복원·재시작 후 보존을 검사한다.
21개 업무 화면을 밝은/어두운 모드와 1280/1440 폭으로 확인하고, 작업 상세 10개 탭·취약점 증거 화면·한국어 입력 조합 이벤트·125/150% 확대·reduced motion을 검사한다. 물리 IME/OS DPI 검사는 별도다.
렌더러 격리·CSP·외부 탐색/새 창 차단·단일 인스턴스·앱 세션 없는 HTTP 차단·Go 종료·프록시 충돌 후 시작 실패/재시도도 확인한다.
`npm run package` 후 `npm run test:package`는 실제 `app.isPackaged` 실행 경로에서 자동 진입·DB 생성·기존 비밀번호 데이터 재시작·사용자 스킬 파일 보존·미준비 상태 표시와 포함 라이선스를 검사한다.
일반 브라우저용 회귀 검사는 설치된 Chrome을 검사 도구로 사용해 Go 단독 실행의 비밀번호 설정·로그인·약관을 확인한다. Chrome 설치를 앱의 도구 런타임 준비로 간주하지 않는다.
실제 앱 부팅 실패는 실패로 반환하며 mock 또는 검사 skip으로 바꾸지 않는다.
스크린샷/trace/임시 DB는 로컬 검사 증거로 남으며 커밋하지 않는다.
현재 앱 전용 shell·PTY·Python·Node·browser·CLI 묶음은 미준비다. 백엔드는 외부 명령을 차단하고 설정 화면에 준비 상태를 표시한다.
Electron renderer 격리만으로 외부 도구 자손 프로세스나 운영 대상 실행 정책을 검증한 것은 아니다. 실제 모델/대상 실행·설치·서명·자동 업데이트 검증은 별도 작업이다.
