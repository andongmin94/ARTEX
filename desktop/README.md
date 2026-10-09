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

`npm run package`는 Windows 실행 폴더를, `npm run package:installer`는 서명되지 않은 개발 설치 프로그램도 생성한다.
Go가 PATH에 없으면 개발 빌드에서 `$env:ARTEX_GO='<지정 Go 실행 파일 절대 경로>'`를 사용한다.
출력은 `desktop/dist/ARTEX-win32-<arch>`이며 기존 출력을 자동 삭제하거나 덮어쓰지 않는다.
개발·패키지 검사는 `--artex-home=<절대 경로>`로 폐기 가능한 새 데이터 경로를 명시할 수 있다.
기본 실행은 Electron의 `userData`를 사용하며 기존 설정·DB·스킬을 자동 삭제하지 않는다.
준비가 끝나면 비밀번호 입력 없이 작업 화면으로 바로 진입한다. 기존 비밀번호 설정과 사용자 데이터는 유지한다.
기록 프록시는 `127.0.0.1:8788`을 사용한다. 포트 충돌은 시작 실패로 표시하며 해소 후 다시 시도할 수 있다.

UI는 기본 Mono를 포함한 neobrutal-ui의 19개 색상 프리셋을 제공하며, 상단 화면 설정에서 라이트/다크/시스템 모드와 함께 저장한다. Mono 다크의 배경·카드·사이드바·그림자는 부드러운 석탄색 계층으로 조정한다. Mono는 색상 테마 이름이며 글꼴은 Pretendard Variable 하나를 사용한다. 웹 UI의 글꼴은 Go에 내장하고, Go 준비 전 시작·오류 화면은 동일한 `PretendardVariable.woff2`를 `resources/fonts`에서 읽는다. 코드·진단 로그도 같은 글꼴을 사용하며 글꼴 선택 설정은 제공하지 않는다.
빌드에는 로컬 글꼴과 `resources/licenses/OFL-Pretendard.txt`의 저작권·SIL Open Font License를 포함한다. 실행 중 원격 글꼴을 요청하지 않는다.

상단 화면 설정의 테마 탭은 19개 색상칩·이름·선택 표시를 격자로 제공하며, 화면 배치 탭은 페이지 너비와 사이드바 등의 설정을 제공한다. Electron의 기능 없는 우측 A 계정 전환과 하단 ARTEX 프로필 메뉴는 제거했다. Go 단독 브라우저는 비밀번호 변경·로그아웃을 사이드바 하단에 직접 표시한다.

렌더러는 sandbox/contextIsolation을 유지하고 Node·파일·명령 API를 제공하지 않는다.
IPC는 메인 프레임과 URL을 검증한 뒤 상태 조회·실패 재시도·앱 종료·백업/새 폴더 복원·업데이트와 지정된 ChatGPT 로그인·사용량 페이지의 기본 브라우저 열기를 허용한다. 백업·업데이트 IPC는 실제 Go origin의 준비된 메인 프레임에서만 호출할 수 있으며 임의 경로나 명령 실행 API는 제공하지 않는다.
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
기존 출력을 보존한 별도 실행 폴더를 검사할 때는 `$env:ARTEX_PACKAGE_TEST_BUNDLE = '<Windows 실행 폴더 절대 경로>'`를 지정한다. 작업 파일 검사는 새 임시 홈에서 파일 CRUD·앱 DB 경계·Windows junction/하드 링크·재시작 보존을 확인한다. 시작 응답 집중 검사는 `node --test backend-tests/ready.test.cjs`로 실행한다.
일반 브라우저용 회귀 검사는 설치된 Chrome을 검사 도구로 사용해 Go 단독 실행의 비밀번호 설정·로그인·약관을 확인한다. Chrome 설치를 앱의 도구 런타임 준비로 간주하지 않는다.
실제 앱 부팅 실패는 실패로 반환하며 mock 또는 검사 skip으로 바꾸지 않는다.
스크린샷/trace/임시 DB는 로컬 검사 증거로 남으며 커밋하지 않는다.
Windows x64의 앱 전용 도구는 `tools.lock.json`에 버전·공식 출처·배포물 SHA256·라이선스를 고정한다. 빌드는 공식 ZIP을 검증해 `resources/tools`에 만들고, 실행 시 Go는 manifest와 모든 파일·추가 파일·링크를 검증한다. 변경/누락 시 실행을 차단하며 host PATH로 우회하지 않는다. `ARTEX_TOOL_CACHE`는 빌드 캐시를 별도 절대 경로로 지정할 때만 사용한다. 기본 캐시는 LocalAppData의 `ARTEX-development-tools`다. Python/Node/PowerShell/Git 및 각 배포물의 라이선스·제3자 고지를 포함하며 Chrome for Testing의 고지는 포함한 브라우저의 `chrome://credits`와 NOTICE에서 확인할 수 있다.

Go는 별도 Windows AppContainer와 Job Object에서 도구를 실행한다. 작업마다 다른 SID를 사용해 지정된 업무 폴더만 쓰게 하고 네트워크 capability를 부여하지 않는다. 호스트의 인증 환경변수는 상속하지 않는다. 취소/강제 종료에는 자손까지 종료하며 작업이 끝나면 부여한 SID ACL을 회수한다. 일반 네트워크가 차단된 도구를 온라인 스캐너로 사용 가능하다고 표시하지 않는다. 브라우저의 내부 IPC·승인 대상 네트워크 연결은 별도 준비 상태이며 설정 화면에 실제 차단 이유를 표시한다. 다른 OS의 도구 실행 격리는 준비되지 않아 차단한다.

## 백업과 새 폴더 복원

설정의 「백업과 복원」에서 백업을 만들면 Go를 정상 종료하고 동일 실행 파일의 maintenance CLI로 업무·트래픽 SQLite, 증거/본문·설정·JWT 키·스킬·구독 보호 저장을 함께 보존한다. 하루 첫 정상 종료의 자동 백업은 데이터 홈 밖의 `ARTEX-backups/<홈 식별자>`를 사용하며 기존 백업을 삭제하지 않는다. 사용자가 끌 수 있다. 복원은 항상 새 폴더에 만들며 원본을 덮어쓰지 않는다. 「복원한 데이터로 재시작」은 처음 실행한 홈의 `.active-home.json`에 검증한 새 홈을 기록한다. 복원 중 종료 요청은 복원 성공/실패와 자식 종료를 기다린다.

```powershell
$env:ARTEX_HOME = '<현재 앱 데이터 홈의 절대 경로>'
./resources/artex.exe -backup '<존재하지 않는 새 백업 폴더>'
./resources/artex.exe -verify-backup '<백업 폴더>'
./resources/artex.exe -restore '<백업 폴더>' -restore-home '<존재하지 않는 새 데이터 홈>'
```

CLI 백업 전 앱을 종료해야 한다. Go의 데이터 홈 OS 잠금, read-only 무결성/외래키/스키마 검사, 파일 SHA256·증거 참조 검사와 no-replace 게시를 사용한다. 링크·경로 이탈·손상·누락은 오류이며 기존 홈을 지우지 않는다. 백업은 인증 정보를 포함한다. Windows 구독 DPAPI 바이트를 보존하되 다른 Windows 계정으로 옮겨 사용 가능한 인증으로 표현하지 않는다.

## 설치·서명·업데이트

개발 설치 프로그램은 사용자별 설치 폴더, 버전별 실행 파일, 활성 버전 포인터와 기존 사용자 데이터 보존을 검증한다. 제거는 소유한 설치 파일만 대상으로 한다. 동일 설치 폴더의 Setup·제거·Launcher는 같은 Windows mutex를 사용한다. 개발 설치물은 서명된 릴리스나 자동 업데이트로 표시하지 않는다.

`npm run package:signed`는 다음 설정이 없으면 오류로 끝난다. 키는 빌드 PC에서만 읽고 패키지에 넣지 않는다. 공개 배포 주소에는 실제 서명 배포물만 게시해야 하며 이 저장소는 자동 게시·Actions 실행·릴리스 태그를 만들지 않는다.

| 빌드 설정 | 의미 |
| --- | --- |
| `ARTEX_SIGN_CERTIFICATE` | CurrentUser/My의 코드 서명 인증서 thumbprint |
| `ARTEX_SIGN_TIMESTAMP` | HTTPS 서명 타임스탬프 주소 |
| `ARTEX_UPDATE_PRIVATE_KEY` | 최소 RSA 3072비트 PEM 개인키의 절대 경로 |
| `ARTEX_UPDATE_FEED_URL` | 서명된 배포 정보 JSON의 HTTPS 주소 |
| `ARTEX_UPDATE_ARCHIVE_URL` | 해당 통합 앱 ZIP의 HTTPS 주소 |

업데이트는 고정 공개키의 배포 서명, 버전 증가, 크기·SHA256·파일 목록·Windows 코드 서명을 검증한다. signed native Host가 자신의 Windows 신뢰/고정 서명자와 내장 helper SHA256을 검증한 뒤에만 프로세스 범위의 PowerShell 실행 정책을 적용한다. OS 실행 정책이나 TrustedPublisher 저장소는 변경하지 않는다. 설치 준비 ACK와 현재 데이터 백업 이후 종료하며 새 버전의 Go/UI 준비 확인 실패 시 실행 포인터를 되돌린다. 사용자 DB 자동 rollback은 하지 않는다. 기존 백업에서 새 폴더로 복원하는 경로를 사용한다.

```powershell
npm run test:backup
npm run test:distribution
$env:ARTEX_INSTALLER_TEST_BUNDLE = '<최종 Windows 실행 폴더 절대 경로>'
npm run test:installed
```

설치 검사는 기본적으로 별도 임시 설치·userData와 `--no-registration`을 사용한다. 실제 Windows 시작 메뉴·제거 등록을 검증할 때만 `$env:ARTEX_INSTALLER_VERIFY_REGISTRATION = '1'`을 지정한다. 이 검사는 기존 ARTEX 등록/바로가기가 있으면 중단하며, 검사 설치의 등록·업그레이드 버전·바로가기 대상과 제거 후 등록 해제를 확인한다. 중간 실패에도 검사 소유 설치만 제거하고 사용자 DB는 보존한다. 일부 배포 보안 검사는 명시적 로컬 fixture다. 공인 서명·운영 HTTPS 업데이트·macOS/Linux 실제 실행, 물리 IME/OS DPI·모든 UI 상태 수동 검수는 Windows 개발 검사와 구분한다. 현재 통과/미실행 범위는 [유일한 개발 계획](../docs/development-plan.md)을 따른다.
