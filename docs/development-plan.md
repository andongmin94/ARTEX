# 개발 계획 — 유일한 현재 작업 목록

목표: **기존 ARTEX + Electron + Go + SQLite**, Docker/PG 없는 로컬 데스크톱 앱.
UI 기준: **[andongmin94/neobrutal-ui](https://github.com/andongmin94/neobrutal-ui)**. 기존 기능·한국어·화면 흐름을 유지하고 시각·상호작용을 통일한다.
작업 기준: **main 단일 브랜치**. 최종 갱신: 2026-10-11.

## 현재 단위 — 2026-10-11 브라우저·UI/접근성·장기 안정성과 파일 보호

미커밋 변경 없는 로컬/원격 main `5449b5aed707e7a59ae3df8dcd612bfb8005cdfd`에서 시작했다. 사용자가 남은 1·2·3번을 모두 진행하고 **4번 서명/운영 자동 업데이트와 5번 macOS/Linux 지원은 제외**하도록 지정했다. 이번 단위는 Windows 브라우저 연결, 화면/접근성, 장기 부하와 파일 보호를 다룬다. 이전 성공과 새 구현/검증을 구분하며, 실행되지 않은 물리 IME/OS 배율·디스크 장애를 제어 검사로 완료 처리하지 않는다.

- [x] Windows 브라우저의 내부 sandbox·작업 격리·승인 대상 통신과 실제9개 도구 호출 연결
- [x] 전체 UI 린트 오류/접근성 문제 수정과 실제 화면·상태·키보드 회귀
- [x] 실제150% Windows 모니터의 기본 zoom/DPR1.5 LLM 화면 확인, 한글 IME 시도 결과와 미검증 범위 기록
- [x] 삭제·보관·복원의 관리 폴더 이동 중 경로 교체 보호와 실제 Windows 경쟁 검사
- [x] 업무8개 lane의120초 부하·쓰기/권한 오류·데이터 보존과 복구 검사
- [x] 최종 전체 Go 검사·계획 갱신. main의 원격 SHA는 완료 보고에서 확인
- [x] 최종 앱/포터블 회귀·ZIP 내용 검증

브라우저는 같은 Electron의 숨긴 sandbox 창과 실행별 메모리 partition을 사용한다. Go가 실제 renderer AppContainer 토큰과 권한·제한 SID를 확인하고 기존 에이전트/MCP 승인과 호출 lease를 연결한다. HTTP/HTTPS·Worker·redirect는 Go의 작업 범위·전역/작업 차단·DNS 전체 주소·내부 listener 금지 검사를 거치며 트래픽 수집을 켜면 같은 응답을 기존 저장소에 기록한다. Chromium SID는 고정이며 작업마다 다르다고 표현하지 않는다. 작업 공간/홈 ACL을 renderer에 추가하지 않으며 공개 Electron 폴더에만 AAP RX를 준비한다. 기존 Chrome for Testing 번들은 제거했다. 정상 초기화의 전체 도구 상태는 이제 `ready=true`이며 누락·변조·격리 준비 실패는 계속 차단한다. 브라우저 MCP의 초기 비활성과 호출 승인 정책은 유지한다.

관리 폴더 이동은 열린 source/parent handle과 no-replace native rename을 사용한다. 보관 입력·출력, traffic/evidence 본문 및 GC도 열린 Root를 사용한다. 본문은 임시 파일에 기록·동기화하고 기존 이름을 교체하지 않는 링크로 게시한 뒤 길이·SHA를 다시 확인한다. 기존 손상 파일·하드 링크를 덮어쓰거나 삭제하지 않고 실패를 반환한다. 물리 전원 장애의 디렉터리 동기화까지 보장한다고 주장하지 않는다.

| 검사 | 결과와 실제 범위 |
| --- | --- |
| 전체 Go | 지정Go1.26.3·modernc1.52.0·norma0.4.3·CGO=0, 실제 도구 root/manifest digest와 Electron 실행 파일을 지정했다. `./...`에서 server 외22개 검사 패키지 통과 후 오래된 browser fixture의 기대를 수정해 server 전체85.145초 재검사 통과. 최종23개 검사 패키지·최상위931개/하위 포함1518개 통과·미해결 실패0·test skip2개. skip은 외부 모델 설정이 필요한 `TestLiveContextReview`와 Windows가 staging rename을 거부한 native 경쟁 검사이며 `report`는 test 파일이 없다. 단일 최초 전체 명령의 종료1을 최종 종료0으로 바꿔 기록하지 않는다. |
| 브라우저 native/race | 실제 Windows Go/modernc/Electron broker의9개 MCP 도구, renderer capability0·Untrusted/NULL restricted SID, 두 작업의 쿠키/localStorage·파일·프로세스 접근 거부, 실제PNG, lease 만료·취소·정리 통과. 최종 브라우저 집중 race31개(하위 포함)와 제어 채널2개 통과·skip0. 추가 C:/D: 분리·동일 볼륨 별칭 경로 회귀13개와 기존 보호2개 race 통과. WS/WebRTC의 STUN UDP/TURN TCP 수신0·ICE후보0 확인. 실제 HTTP403은 정상 응답, 미승인 redirect/내부 주소는 오류다. |
| 경로·본문 보호 | 실제 Windows300회 관리 parent rename 경쟁 거부·50회 이동, held-parent junction 변경 뒤 native rename 거부·외부 파일 보존, 보관 traffic/evidence Root 교체 거부 통과. 실제 활성 evidence child-junction의 기록/읽기/GC 거부·외부 bytes/mtime·DB rows0 확인. 기존 본문/manifest 하드 링크·손상 본문 보존·복구·동시 게시 통과. 최종 evidence race12.965초, traffic 전체 race41.958초, relay 전체 race1.878초 통과. 내부 staging 교체 검사는 Windows의 열린 파일 rename 거부로 skip1이며 가능한 모든 경쟁창을 검증했다고 표현하지 않는다. |
| 장기/권한 |120초8개 lane cycles=`[2203 2872 2800 2604 114 444 1680 1681]`, actual worker 재개→부분 모델 SSE→pause/cancel→drain2회·재시작 행/본문/integrity/FK 보존 통과. Engine2회는 초기에 실행하며120초 연속 Engine 부하가 아니다. 최종 경계 수정 후 기본3초 혼합 검사와 FindingTraffic/export/archive/retry 집중 검사12.451초 통과. 실제 NTFS workspace/관리 이동 ACL 쓰기 거부·파일 보존·권한 복구 통과. DB ACL은 modernc 초기화 전 writable-file preflight 거부이며 중간 DB ACL 장애와 구분한다. |
| 웹 | TypeScript·한국어·입력 정적 검사·unit8개·정적 export31페이지 통과. Biome146파일 오류0·기존 warning131/info721·접근성 오류0. 공식 ChatGPT SVG 두 개는 byte 그대로 유지하며 두 자산의 title 경고만 한정한다. |
| 실제 UI | 기존 Electron/Chrome6개 회귀 통과2.8분, 새 UI 상태 검사 최종10.0초 통과. 실제21페이지×2모드×2폭의 접근성 트리에서 이름 없는 컨트롤0, 작업 상세10탭·모델 저장/재시작·백업/복원·단일 인스턴스·시작 실패/재시도·DOM composition/확대 검증. MCP 로딩/500오류/키보드 재시도·폼 검증·두 모드 Checkbox 색/Switch thumb·Radio·Sheet Tab/Escape 포커스 복귀 통과. 대표 MCP/내보내기 캡처를 직접 확인했다. |
| 물리 UI | 실제150% 모니터의 기본zoom·DPR1.5 LLM 화면과 overflow0 확인. 열린 Sheet 전체 물리 배율·125% 물리 모니터·네이티브 한글 조합은 미검증. 지원되지 않은 Hangul 키 입력과 RightAlt의 메뉴 동작을 성공으로 기록하지 않는다. |
| Node/앱/포터블 | backend·backup·distribution 기존 Node regression106개35.5초 통과. 최종 Go embedui 빌드/vet 통과. 새 포터블 실제부팅·재시작·모델/도구 준비·작업 파일 CRUD/경계/보존2개55.5초 통과. 설치/서명/운영 업데이트를 이번 결과로 처리하지 않는다. |
| ZIP | `ARTEX-2.2.0-Windows-x64-portable-browser.zip`,3174파일·이름/크기/중복 검사와 Electron/Go/broker/main/manifest/CSP/fonts/라이선스/포트 정의 등12개 내용 해시 일치.395,323,178바이트·SHA256 `55B882CA70006090B25FADC3DFA439A8C629BAF296BB10A6A28362CEC656658A`.5개 manifest component와 별도Chrome/미완성stage 없음 확인. 기존 실행 폴더·ZIP은 보존했다. |

초기 native race의 zero restricted SID buffer checkptr 실패와 큰 evaluate 반환값을 수정하고 재검증했다. 다른 드라이브 경로를 중첩으로 오판하던 오류도 보완했다. 초기 relay prefetched tunnel fixture가 요청 전에 응답을 내보낸 가정은 실제 dial stream 검사로 수정했다. UI 선택색 검사는 CSS transition 종료를 기다리도록 바로잡고 제품을 우회하지 않았다. 공식 도구 ZIP의 최초 PowerShell 추출 timeout은 실패 stage를 보존한 뒤 동일 고정 배포물을 재준비해 통과했다. ZIP 검사의 빈 directory entry를 파일로 세던 오류를 수정했고 생성한 ZIP을 교체하지 않고 다시 검증했다. 초기 실패 로그/trace와 최종 로그·SQLite·PNG는 저장소 밖 로컬에만 보존한다.

변경 범위는 Electron browser/backend/main·고정 도구 구성과 실제 UI/포터블 검사, Go browserrelay/toolruntime/server의 호출 승인·범위·본문·native 관리 경로, evidence/traffic 저장·보관·회귀 검사, 공통 Radix 컨트롤과 화면 이름/label/로딩/오류/키보드, 제품/구조/개발/UI 안내와 이 계획이다. 원본v0.3.15 이후 한글화 이력은 변경하지 않는다. Actions·릴리스 태그·운영 게시·원격 artifact 업로드는 하지 않는다. 원격 반영 SHA는 완료 보고와 이 단위 Git 커밋에서 확인한다.

**다음 시작점은 남은 실기 검수**다. 네이티브 한글 IME·물리125%와 열린 오버레이의150%·모든 데이터/오류/승인 상태 수동 검수, 물리 디스크 고장/전원 장애·전체 OS ACL 장애를 완료 처리하지 않는다. 서명·운영 업데이트(4)와 macOS/Linux 실제 지원(5)은 사용자 요청으로 이 단위에서 제외했다.

## 이전 단위 — 2026-10-10 작업 파일 경계·시작 안정성·신뢰성·UI 검수

미커밋 변경이 없는 원격/로컬 main `b6c3781dd6a2a69880e371ab3a82c5a4752fd9ae`에서 시작했다. 사용자 요청으로 남은 계획을 진행하며 작업 파일과 앱 관리 파일의 경계, 간헐적인 금지 포트 선택, 실제 SQLite 부하·저장 오류와 Windows 설치 등록을 확인한다. 원본 v0.3.15와 한글화 시작 이후의 이력 순서는 변경하지 않는다.

- [x] 작업 파일 API의 루트를 `data/workspace`로 분리하고 실제 에이전트·첨부·셸·MCP 호출부 연결
- [x] 직접 경로 호출·Windows junction·하드 링크·장치/ADS 경로·보관 우회 차단, 기존 파일 보존 검증
- [x] Go 바인딩·Electron ready·패키징에 동일한 Chromium 금지 포트 정의 연결, 실패·재시도·정리 검증
- [x] 실제 modernc의 다중 기록/읽기 부하·용량 한도·읽기 전용 오류와 재열기·복구 검증
- [x] 공식 Windows 도구의 일반/PTY 브라우저 시작 우회 차단 및 집중 `-race` 검사
- [x] 전체 Go·웹·Electron 회귀, 최종 포터블의 작업 파일 CRUD·재시작·ZIP 내용 검증
- [x] 실제 시작 메뉴·사용자 제거 등록과 설치·업그레이드·제거 결과 확인
- [x] MCP 카드에서 이름/전송 방식과 토글/삭제를 두 행으로 분리해 두 창 크기·두 모드의 버튼 잘림 수정
- [x] 확대된 모델 설정창의 안정된 상태·실제 클릭과 완전한 Electron 캡처 확인
- [x] 자산 커버리지 그래프의 과대 확대·긴 서비스 레이블 잘림 보정·최종 패키지 재검증

작업 공간은 수명 주기 동안 열린 `os.Root`를 유지한다. 목록·읽기·다운로드·쓰기·업로드·폴더 생성·삭제가 이 루트를 사용하고 읽기는 열린 파일의 하드 링크 수를 검사한다. 쓰기는 새 파일을 생성·동기화한 뒤 루트 내부 rename으로 게시한다. 업무 DB/WAL/SHM·설정·증거·트래픽·대화 기록·보관 파일은 루트 밖에 둔다. 첨부·작업·대화·editor/stdio MCP 산출물도 새 루트에 연결했다. 이전 `data/tasks` 등은 자동 이동/삭제하지 않으며 이전 파일이 남아 있는 보관은 명시적으로 중단하고 삭제는 보존 안내를 반환한다.

보관의 payload 순회/읽기도 열린 루트를 사용해 앱 파일을 가리키는 하드 링크를 내보내지 않는다. 작업 삭제·보관·복원의 정적인 junction 부모를 거부한다. 이 세 경로의 관리 폴더 간 이동은 검증 후 일반 rename을 사용하므로, 호스트가 동시에 경로를 바꾸는 모든 경쟁 상황까지 원자적으로 보호했다고 주장하지 않는다.

이 PC의 동적 TCP 범위 1024–15000과 브라우저 금지 목록이 겹쳤다. 번들 Electron 44.6.0의 Chromium 152.0.7977.130 [원본 정의](https://chromium.googlesource.com/chromium/src/+/152.0.7977.130/net/base/port_util.cc)를 고정 JSON 하나로 공유한다. 금지 후보 소켓을 유지하면서 최대82회 안에 안전한 바인딩을 확보하고, 같은 열린 소켓으로 Serve를 시작한다. 명시한 금지 포트와 금지 ready 응답은 오류이며 Chromium 제한을 해제하는 옵션은 추가하지 않는다.

| 검사 | 결과와 실제 범위 |
| --- | --- |
| 전체 Go | Go 1.26.3·modernc 1.52.0·CGO=0의 `go test -json -p 1 -count=1 -timeout 180s ./...` 종료0, 261.857초. 테스트가 있는22패키지, 최상위886·하위 포함1,423개 통과·실패0. 외부 모델을 요구하는 `TestLiveContextReview`만 skip1. `go vet ./...` 통과. |
| 파일 경계 | 실제 HTTP의7개 파일 API·절대/상위/앱 관리/장치/ADS 경로, Windows 실제 junction/하드 링크, 한글 CRUD·첨부/agent 경로, 보관 roundtrip·journal·rollback·삭제 보존 검사 통과. 최종 포터블의 같은7개 API×12개 거부 경로·DB 직접 읽기404·junction/하드 링크 숨김·업로드/다운로드/저장/삭제·재시작 보존도 통과3.2초. |
| 시작 포트 | 공유 정의·제한 후보 유지·소켓 닫기 오류·상한 실패·명시 포트·ready 거부/중복 응답의 집중 Go 및 Node 검사 통과. 실제 Electron의6000 요청은 `ERR_UNSAFE_PORT(-312)`, 새 Go의 안전한2482 ready/health·부모 EOF 종료0, 명시10080은 ready 없이 종료1을 확인했다. 기존 실제 앱의8788 충돌→실패→충돌 해제→재시도도 통과. |
| 실제 SQLite | 같은 파일의 독립2풀·12writer×40회×4업무 메서드=1,920회 커밋과4reader의 실제 관찰·재열기·무결성/FK 통과. 사용량/LLM 원문480개씩, 설정 묶음 일관성, 프롬프트480개 버전 보존을 확인했다. 실제 `SQLITE_FULL(13)`의 설정/버전/큰 원문 rollback과 한도 해제 후 복구, `SQLITE_READONLY(8)`의 기존 설정 보존·해제 후 저장도 통과. [SQLite 용량 한도](https://sqlite.org/pragma.html#pragma_max_page_count)·`query_only` 제어이며 물리 디스크 고장/ACL 전체 검사와 구분한다. |
| Windows race/도구 | 임시 [공식 w64devkit v2.10.0](https://github.com/skeeto/w64devkit/releases/tag/v2.10.0)의 GCC16.2.0으로 같은 Go/modernc의 집중 `-race` 최상위28·하위4개 통과, 실패/skip/race 경고0. compiler ZIP의 공식 asset digest와 다운로드 SHA256 `18d0a4c71a166f8401ab6305781bec5882b40b5e06ba9807c61cb5f3b3c6325e`가 일치한다. 양성 대조군의 실제 race 경고·예상 종료66도 확인했다. 전역 PATH·OS 설치·제품 의존성은 변경하지 않았다. 공식 도구의 별도 native 상위14·하위18개도 통과35.229초. 일반/PTY의 browser 직접 시작은 프로세스 생성 전에 거부한다. 전체 패키지 race 검사는 아니다. |
| Node/웹/앱 | backend·backup·distribution Node106개 통과108.532초·실패/skip0. web TypeScript·한국어 런타임·입력10조합/4editor·unit8개 통과. 기존 실제 Electron/Chrome6개 통과1.8분: 백업 IPC/새 홈 복원·업무/증거/보관/재시작·21페이지×2모드×2폭·10개 작업 탭·DOM composition/확대·인증·격리·시작 실패/재시도. 일부 모델/OAuth/오류 화면은 명시적 로컬 fixture이며 외부 계정/대상 호출과 구분한다. |
| UI 안정 상태 | MCP·그래프 수정 뒤 대표 업무/화면/재시작/격리1개를 보완해 최종 통과1.3분. 카드 토글/삭제의4조합 경계와 모델 Sheet의 opacity1·모션 종료·이름/형식/닫기 viewport·내부 가로폭을 확인했다. 재검증 기록·실제 생성된 보고서와 그래프의 canvas 그리기/표본 픽셀3회 안정 대기 후 캡처한다. 기본21페이지×2모드의42개1280 PNG 등은 독립 시각 검토했고 MCP 버튼 잘림을 수정했다. 그래프도 최종 PNG에서 IP·긴 서비스 이름이 모두 보이고 가장자리/범례와 겹치지 않음을 확인했다. 모든 데이터/오류/승인/접근성 상태 검수는 아니다. |
| 빌드/포터블 | 파일 경계 변경을 빌드한 뒤 시각 검토에서 MCP 버튼과 그래프 레이블 잘림을 발견해 카드 헤더·작은 그래프 맞춤을 수정했다. 다시31페이지 정적 export·TypeScript·Go embedui·CSP·글꼴·공식 도구·라이선스와 최종 `ARTEX-win32-x64-checked` 생성 통과. 최종 포터블2개 통과11.2초: 실제 Go/SQLite/UI·비밀번호/스킬·종료/재실행7.6초와 작업 파일 경계/CRUD·재시작3.2초. OS 설치 등록 검사는 이 두 UI 배치 변경 전 같은 백엔드/설치 코드의 패키지에서 수행했다. |
| 실제 OS 설치 등록 | `ARTEX_INSTALLER_VERIFY_REGISTRATION=1`의 실제 native Setup→Electron/Go/SQLite 부팅→동일 코드 메타데이터2.2.1 업그레이드→제거1개 통과310.691초. 새 임시 설치의 HKCU 제거 등록·버전·실제 시작 메뉴 바로가기 대상, 로그인 JWT·사용자 DB/스킬 보존을 확인했다. 완료 후 등록/바로가기 없음도 별도 확인했다. 검사 전 기존 등록이 없음을 확인하고 검사 소유 설치만 제거했다. 공인 서명·운영 자동 업데이트는 미검증이다. |
| 다른 OS 교차 빌드 | 같은 Go1.26.3·CGO0에서 Linux amd64/macOS arm64의 server 검사 실행 파일·cmd/artex embedui·toolruntime 검사 실행 파일6개 모두 종료0. 빌드 메타데이터와 modernc1.52.0 유지 확인. 해당 OS의 실제 실행·UI·설치 성공을 의미하지 않는다. |
| 정적 검사 한계 | 변경 Go28개 gofmt·Go vet·JS 구문·diff·문서5개의 코드 블록/로컬 링크10개 검사를 통과했다. MCP 한 파일의 Biome은 변경 전후 모두 오류12·경고2·정보10으로 실패한다. 같은 프로젝트 문맥의 HEAD 복사본과 비교했으며 라벨/정적 click 이벤트 등 기존 위반을 이번 헤더 배치 수정의 완료로 처리하지 않는다. 전체 lint·모든 UI 상태/접근성 수동 검수는 미완료다. |
| ZIP | 최종 `ARTEX-2.2.0-Windows-x64-portable-checked.zip`의3,482개 파일 이름·개수·크기·중복 없음 및 Electron/Go/main/CSP/MIT/시작 HTML/공유 포트 JSON7개 내용 해시 일치. 605,476,758바이트, SHA256 `634546D7CB11AB10DDCBA877C88894783F71CBF36E3424A0182439131D0773CD`. ZIP 루트의 `ARTEX.exe`로 실행하며 중간 파일 경계/UI ZIP과 기존 설치/포터블 출력은 보존했다. |

브라우저 격리 연결은 여전히 미완료다. 기존 비대화형 windowstation과 SID 전용 desktop에 capability0 AppContainer 진입은 성공했지만 Chromium이 내부에서 수행하는 windowstation 생성은 실제 AccessDenied로 실패했다. 전역 station/desktop ACL·네트워크 capability·내부 sandbox를 약화하지 않았고 승인 대상 중계와 브라우저 MCP를 활성화하지 않았다. 전체 도구 `ready=false`, 브라우저 `blocked`를 유지한다. 실험 프로세스는 정리했고 probe·실패 증거는 저장소 밖 로컬에 보존한다.

확대 Sheet의 초기 반투명 캡처는 애니메이션 중 상태였으며 대기 후에도 잘려 보이는 이미지는 이 Windows 표시 환경의 Playwright 캡처 좌표 문제였다. 별도 실제 앱 진단의1.25/1.5 모두 패널·이름·형식·닫기 rect와 중앙 hit-test/닫기 클릭은 정상이다. 일반/`fullPage` PNG는 서로 같고 실제 viewport보다 작았지만, clip 없는 CDP 캡처와 Electron `capturePage`는 크기/내용 SHA256까지 같으며 전체 폼이 보인다. 확대2장만 Electron의 완전한 PNG로 저장해 독립 시각 검토했다. 제품 Sheet 폭을 임의로 바꾸거나 이 검사를 물리 OS DPI·IME 전체 검수로 표현하지 않는다.

초기 집중 검사의 기존 workdir 기대·장치 이름 검증과 검사용 활성 WAL 변경 문제를 바로잡았다. 최종 Go 검사는 백엔드 소스 동결 후 통과했고 두 UI 배치 보정 뒤 대표 Electron·최종 패키지 검사를 다시 통과했다. 포터블의 비동기 도구 검증이 끝나기 전에5초 대기가 실패해40초의 상태 대기로 고친 뒤 같은 제품 패키지가 통과했다. 화면 검사 보완 중 실행 grep·괄호 구문과 빈 보고서라는 fixture 가정의 실패를 실제 생성된 보고서로 바로잡았다. ZIP 검사 도구의 native 종료 코드 부재와 잘못된 CSP 경로는 별도 내용 검증으로 바로잡았다. 확대 캡처 진단의 종료 후 process 조회 오류도 기록을 보존하고 정상 종료/정리까지 다시 확인했다. 초기 실패 로그/PNG/trace와 최종 결과를 모두 보존하며 실패를 skip이나 제품 우회로 바꾸지 않는다.

최종 검사 종료 후 검사 소유 앱/도구 프로세스0·8788 listener0·검사 설치의 HKCU 제거 등록과 시작 메뉴 바로가기 없음도 확인했다. 기존 사용자 파일과 이전 패키지 출력은 보존했다.

변경 범위는 `server`의 파일 루트/첨부/업무 폴더/보관·삭제 및 회귀 검사, `cmd/artex/http_lifecycle*`, 새 `internal/browserports`, `internal/toolruntime/process*`, Electron backend/패키징·실제 포터블/설치/화면 검사, 실제 SQLite 신뢰성 검사와 MCP 카드 헤더·자산 커버리지 그래프·README 두 개·architecture·UI 기준·이 계획이다. 사용자 데이터·구 파일은 삭제/이전하지 않는다. Actions·태그·릴리스·원격 artifact 업로드는 하지 않는다. 원격 반영 SHA는 완료 보고와 이 단위 Git 커밋에서 확인한다.

다음 구현 시작점은 **내부 sandbox와 작업별 AppContainer를 함께 유지하는 Windows 브라우저 IPC 연결·승인 대상 네트워크 중계**다. 공인 인증서/운영 업데이트 주소가 없고 macOS/Linux 실행 환경도 없어 공인 서명·운영 자동 업데이트·다른 OS 실제 지원은 미검증으로 남긴다. 물리 IME/OS DPI·모든 화면 상태 수동 검수·물리 디스크 장애와 관리 폴더 간 이동의 동시 경로 교체는 아래 미완료 범위다.

## 이전 단위 — 2026-10-09 Mono 버튼의 원본 외곽 복구

원격 main `e4110dc214dbe4973f74cd334036e1315734e7e0`와 미커밋 변경 없는 로컬에서 시작했다. 사용자가 원본 neobrutal-ui Mono 버튼과 ARTEX를 함께 보여준 캡처의 색 차이를 수정한다. 원본 최신 main은 `b4da2463fe710a77bf464c65125a1a7f40424722`로 동일하며 `colors.ts`, `theme.ts`, `button-variants.ts` 및 원본 페이지의 실제 Button 호출부를 대조했다.

- [x] 원본 Mono/Warm의 액션·글자·검정 테두리/그림자와 실제 ARTEX 호출부의 차이 확인
- [x] 공통 외곽의 검정 복구, Mono/Warm 모드별 회색 재정의 제거, 다크 액션 글자 복구
- [x] Electron 시작/오류 화면의 라이트 버튼 외곽·글자 정리
- [x] 정적 UI/Go 빌드·CSS/한국어 검사
- [x] 실제 포터블의 색상/눌림/키보드/대표 화면 검사
- [x] 새 포터블 ZIP 구성·핵심 내용 검증

캡처의 작업 상세 `재개`와 `테스트 자산 추가`는 모두 `default + sm` 공통 Button을 사용하며 별도 색상 override가 없다. 기존 Mono 라이트는 채움·테두리·그림자를 모두 `#27282b`로 적용해 외곽이 묻혔다. 원본은 같은 석탄색 채움에 2px `#000` 테두리와 4px `#000` 그림자를 사용한다. 공통 `--border=#000`, `--shadow-color=var(--border)`로 맞추고 Mono/Warm의 덮어쓰기를 제거했다. Mono/Warm 다크 액션 글자도 원본의 검정으로 맞췄다. 기존 석탄색 화면·카드와 밝은 다크 입력/포커스, 컬러 대비 보정은 유지한다.

| 검사 | 결과와 범위 |
| --- | --- |
| 소스/빌드 | 원본 최신 SHA 및 Mono/Warm 양모드의 기본 액션·2px 검정 경계·4px 그림자 소스 대조와 독립 검토 통과. CSS Biome 종료0·한국어 런타임 검사 통과. 정적 export31페이지·TypeScript·Go 1.26.3 embedui·CSP/글꼴/도구/라이선스 빌드와 새 Windows 실행 패키지 생성 통과. |
| 실제 전후/팔레트 | 변경 전 포터블의 Mono/Warm×2모드4조합을 새 임시 홈의 실제 중지된 작업 상세에서 확인했다. 변경 후 같은 두 버튼의 원본 채움/글자·2px 검정 선·4px 검정 그림자 일치,19프리셋×2모드38개 실제 선택/쿠키·버튼 대비4.5:1 이상을 확인했다. 최저4.5375:1(다크 Fuchsia), Mono 라이트13.51:1·다크16.94:1이다. 외부 모델/대상 호출은 없으며 재개 버튼을 클릭해 실행하지 않았다. |
| 상태/키보드/저장 | 안전한 자산 추가 버튼의 일반4px 그림자·hover3px 이동/1px 그림자·pressed4px 이동/그림자0,140ms 전환·키보드 포커스·축소 모션0.01ms를 확인했다. 자산 추가/모델 생성 열기·Escape 취소 후 저장 미변경, 기존 기본 버튼 모서리5px/소형4px를 확인했다. Mono Warm 다크의 실제 새로고침 유지 통과; 별도 종료/재실행 저장 검사는 이번에 반복하지 않았다. |
| 시각/시작 실패 | 전후24PNG 중 변경 후20PNG(대표 팔레트8·자산/모델창2·상세4/설정4·시작 실패/재시도2)를 보존했다. 실제 viewport1298/1458×1008의4상세에서 가로 넘침0·page error0. Mono 라이트/다크 대표를 독립 시각 검토했고 카드/표/팝오버의 공통 검정 외곽도 확인했다. 실제 로컬8788 충돌로 시작 실패 화면의 검정 버튼/패널을 확인하고 충돌 해제 후 재시도 ready를 검증했다. 실제 Windows OS DPI·IME 전체 검수와 구분한다. |
| ZIP | `ARTEX-2.2.0-Windows-x64-portable-mono.zip`의3,481개 파일 이름·개수·크기 및 Electron/Go/main/CSP/MIT/시작 HTML6개 내용 해시 일치. 605,550,384바이트, SHA256 `DD05F187BDD29DF74BF4AE0353006A61C339502D0F776572D0AE918D9CEE2E91`. 기존 설치/포터블 출력은 보존했다. |

초기 검사 도구의 인증 준비 대기·상세 화면 설정 동선·기본 Mono 쿠키 부재·투명 그림자 직렬화·소형 버튼 모서리·축소 모션·새로고침 후 기본 탭 기대를 실제 앱 동작에 맞춰 보완했다. 같은 제품 소스/패키지의 최종 집중 검사는 통과했으며 초기 실패 JSON/PNG도 별도 보존했다. 검사 앱/Go 프로세스가 남지 않았음을 확인했다.

변경 파일은 `web/src/styles/neobrutal.css`, `desktop/src/startup.html`, UI 기준과 이 계획이다. 새 ZIP 내부 `ARTEX-win32-x64-mono/ARTEX.exe`를 실행한다. API·DB·승인/실행 정책·버튼 호출 계약·크기·기존 눌림 코드는 변경하지 않았다. 사용자의 앱 데이터 대신 새 임시 홈으로 검사한다. 전체 Go·웹 unit/입력·기존 전체 업무 회귀·설치/업데이트·다른 OS·외부 모델/대상·물리 IME/OS DPI 검사는 이번 색상 단위에서 반복하지 않았다. Actions·태그·릴리스·원격 artifact 업로드는 하지 않는다. 원격 반영 SHA는 완료 보고와 이 단위의 Git 커밋에서 확인한다.

다음 시작점은 앞서 발견한 **작업 파일과 앱 관리 DB/WAL 파일의 API 경계 보호**다. 동적 loopback 금지 포트의 시작 안정성, 브라우저 IPC/승인 대상 네트워크 중계와 외부 환경 검증도 아래 미완료 목록으로 유지한다.

## 이전 단위 — 2026-10-09 단일 동기화 소스·탭·작업 파일 목록 정리

원격 main `a958d37b0666f0de42a992836d9c17382e9abb5b`와 미커밋 변경 없는 로컬에서 시작했다. 첨부된 자산 동기화·LLM·작업 공간의 불필요한 단일 선택과 겹친 프레임을 정리한다. neobrutal-ui 원본 ref는 `b4da2463fe710a77bf464c65125a1a7f40424722`와 같으며 원본 Table의 자체 테두리와 Tabs의 상태 속성을 확인했다. ARTEX의 기존 Radix를 유지하고 실제 속성에 맞게 적용한다.

- [x] ScopeSentry 단일 탭 제거, 연결 상태·설정과 실제 프로젝트/작업 선택을 직접 연결
- [x] LLM 밑줄형 탭 및 공통 Radix 선택 상태·방향 수정, 화면 설정의 임시 선택 스타일 제거
- [x] 작업 공간의 제목·기능 버튼·현재 폴더·항목 수 정리, 표의 이중 테두리·빈 여백 제거
- [x] 타입·한국어·정적 UI/Go 빌드와 기존 실제 Electron/Chrome 회귀 검사
- [x] 새 포터블의 세 화면 조작·상태·라이트/다크 배치 검사
- [x] 좁은 폭의 LLM 위치 안내 문구 수정 후 최종 포터블 집중 재검사
- [x] ScopeSentry 안내문의 기존 낮은 대비 수정 후 최종 포터블 집중 검사
- [x] 새 포터블 ZIP 구성·핵심 파일 내용 해시 검증, 기존 출력 보존

ScopeSentry는 소스 선택 대신 연결 카드에서 주소·API 키와 상태를 표시한다. 최초 확인 중·실패·미생성을 구분하고, API 키를 비워 두면 저장된 키를 유지한다. 프로젝트/작업 기준의 실제 선택·검색·자산 유형·페이지 이동·동기화 결과는 유지한다. LLM의 두 실제 탭은 밑줄로 선택을 표시한다. 공통 Tabs는 Radix의 `data-state`·`data-orientation` 및 orientation prop으로 선택·방향을 처리하며 기존 기본형 호출부도 같은 선택 스타일을 사용한다. 작업 공간은 Table 하나의 프레임에 파일을 표시하고 파일명 줄임·열 정렬·입력 및 아이콘 버튼 이름을 제공한다.

| 검사 | 결과와 범위 |
| --- | --- |
| web/빌드 | TypeScript·한국어 런타임 문구 검사 통과. 변경 호출부4파일의 Biome(포맷 검사 제외) 종료0: 기존 LLM suppression 경고1개·작업 공간 클래스 정렬 정보6개. Sync 단독 Biome 통과. LLM 일반 Biome의 기존 전체 파일 포맷 차이는 그대로 남아 있으며 전체 lint 통과로 표현하지 않는다. 정적 export31페이지·Go 1.26.3 embedui·CSP/글꼴/도구/라이선스 빌드와 Windows 실행 패키지 생성 통과. |
| 기존 실제 회귀 | 기존6개 첫 실행은5통과·1실패(1.2분). 자동 진입·업무/모델·증거·재시작·화면·격리 검사1개가 시스템 설정 확인 중 다른 페이지로 이동한 상태에서 실패했으며 원인은 확정하지 않았다. 소스 수정 없이 해당1개를 순차 재실행해 통과(1.0분). 백업/복원 IPC·ChatGPT 로컬 fixture2개·프록시 충돌/재시도·일반 Chrome 인증 검사도 통과했다. 첫 실패와 재실행 증거를 함께 로컬 보존한다. |
| 실제 포터블 조작 | 새 임시 홈의 실제 Go로 폴더 생성·업로드·파일 열기/편집 저장·디스크 내용·다운로드 완료/내용·현재 폴더 이동·새로고침·검사 파일/폴더만 삭제를 확인했다. 실제 LLM의 두 밑줄 탭·ArrowRight/Left+Enter·생성 열기/Escape 취소와 프로필 미변경, 실제 Go의 ScopeSentry 초기 미설정 시드를 확인했다. |
| ScopeSentry UI 상태 | 미생성/생성 요청·loading/error/retry·빈 API 키 요청·프로젝트/작업 검색/선택·동기화 payload는 명시적 `page.route` UI fixture로 통과했다. 외부 MCP 연결·실데이터 동기화 검사가 아니다. 초기 다운로드 이벤트 대기와 미생성 시드 가정이라는 검사 도구 오류를 Electron 완료/파일 내용·실제 Go 응답으로 고친 뒤 동일 패키지가 통과했다. 초기 실패 증거도 별도 보존했다. |
| 시각 | 작업 공간·LLM 모델/재시도·Scope 프로젝트/작업5상태×2모드×2요청폭의20PNG에서 가로 넘침0, 실제 viewport1298/1458×1008. 작업 공간·동기화 표 자체2px/부모0px의 단일 테두리, 기존19프리셋·테마/배치 기본형 탭과 계정 메뉴 부재를 확인했다. 물리 OS DPI 검수와 구분한다. 시각 확인 중 좁은 폭의 기존 LLM '오른쪽 위 생성' 안내가 실제 버튼 위치와 달라 위치 지시만 제거했고 최종 UI/Go를 다시 빌드했다. |
| 최종 포터블/대비 | 안내 문구 수정 후 집중 검사 통과·4PNG를 보존했다. 독립 시각 검토에서 Scope 미설정/비활성 안내의 기존 고정 주황색 대비3.20:1을 발견해 두 곳만 테마 보조색으로 바꿨다. 다시 빌드한 최종 `ARTEX-win32-x64-screen`의 실제 부팅·LLM 안내/밑줄/키보드/생성 취소·작업 공간 단일 프레임·Scope 초기 미설정/단일 소스 탭 부재를 새 임시 홈에서 확인했다. Scope 두 안내×2모드의 실제 computed 대비는 라이트6.32:1·다크6.99:1이며 미설정은 실제 Go, 설정됨/비활성은 명시적 UI fixture다. 실제 viewport1298×1008의 세 페이지 가로 넘침0·page error0,8PNG를 별도 보존했다. 앞선 전체 회귀·20PNG·파일 CRUD·Scope UI fixture 검사는 문구/안내색 수정 전 같은 기능/배치의 패키지에서 수행한 검사로 구분한다. |
| ZIP | 최종 `ARTEX-2.2.0-Windows-x64-portable-screen.zip`의3,481개 파일 이름·개수·크기 및 Electron/Go/main/CSP/MIT5개 내용 해시 일치. 605,564,560바이트, SHA256 `CE29DD84DB3936F2140FA2BB2FCB0AE807A95918D80C29CB8ED8D913A2594A99`. 중간 UI 패키지도 별도 검증 후 보존했고 기존 설치/포터블 출력은 덮어쓰지 않았다. |

변경 파일은 `function/sync/page.tsx`, `function/workspace/page.tsx`, `system/llm/page.tsx`, 공통 `tabs.tsx`, `layout-controls.tsx`, UI 기준과 이 계획이다. 새 ZIP 내부 `ARTEX-win32-x64-screen/ARTEX.exe`를 실행한다. Go 업무/API·DB·사용자 데이터 경로·설치/업데이트는 변경하지 않았다. 전체 Go·웹 unit/입력·설치/업데이트·다른 OS·외부 ScopeSentry·실계정 OAuth/추론·물리 IME/DPI 검사는 이번 UI 단위에서 반복하지 않았다. Actions·태그·릴리스·원격 artifact 업로드는 하지 않는다. 원격 반영 SHA는 완료 보고와 이 단위의 Git 커밋에서 확인한다.

코드 확인으로 작업 공간 API가 앱 데이터 루트와 같은 경계를 사용하며 `artex.sqlite`·WAL/SHM 등 관리 파일도 목록·다운로드·수정·삭제 대상이 되는 기존 문제를 확인했다. 이번 UI 정리를 이 저장 경계 문제의 해결로 처리하지 않는다. 실제 검사는 새 임시 홈과 검사 파일만 사용하며 사용자의 DB·설정은 조작하지 않는다.

- [x] 2026-10-10: 작업 파일과 앱 관리 파일의 API 경계 분리·직접 경로·junction/하드 링크 검증 완료. 위 현재 단위의 범위와 제한을 따른다.
- [x] 2026-10-10: 동적 loopback 브라우저 금지 포트 배제·실제 ready/실패/재시도 검증 완료.

브라우저 IPC/승인 대상 네트워크 중계와 외부 환경 검증은 아래 미완료 목록으로 유지한다.

## 이전 단위 — 2026-10-09 프리셋 선택판·불필요한 계정 메뉴 정리

원격 main `b897d524f44f3d8670bfcb34dff17ec13ae776e8`와 미커밋 변경 없는 로컬에서 시작했다. 추가 사용자 요청으로 19개 프리셋을 한눈에 비교하는 선택판으로 바꾸고, Electron의 우측 A·하단 ARTEX 계정 메뉴를 제거한다. 원본 최신 ref는 기존 `b4da2463fe710a77bf464c65125a1a7f40424722`와 같으며 프리셋 이름·스와치·컬러 배경/액션/차트 276개 값의 불일치는 0개다. 원본 Styling 선택 컨트롤 소스도 확인했으며 원본 사이트의 실제 브라우저 시각 검수를 수행한 것으로 표현하지 않는다.

- [x] 19개 색상칩·이름·선택 체크를 격자로 표시하고 테마/화면 배치 탭 연결
- [x] 기능 없는 상·하단 계정 전환/프로필과 표시 전용 코드 제거, 단독 브라우저의 실제 인증 액션 직접 연결
- [x] 정적 UI·Go embedui 빌드와 실제 Electron/Chrome 회귀·최종 포터블의 선택/저장/재실행·시각 확인
- [x] 새 포터블 ZIP 구성·핵심 파일 해시 검증, 기존 출력 보존

`users=[ARTEX 1명]`의 우측 메뉴는 고정 표시만 전환했고, desktop 하단 메뉴도 같은 이름만 다시 보여줘 실제 액션이 없었다. 두 메뉴와 미사용 사용자 표시 훅·JWT 메타데이터 해석을 제거했다. Go 단독 브라우저의 비밀번호 변경·로그아웃은 사이드바 하단에 기능 이름으로 직접 제공하며 실제 인증 API/토큰 저장·제거는 유지한다. 앱 이름은 사이드바 머리의 탐색 링크에 남긴다.

| 검사 | 결과와 범위 |
| --- | --- |
| web/빌드 | TypeScript·한국어 런타임 문구 검사 통과. 변경 웹5파일 Biome 종료0(기존 img/cookie 경고3개), JS 문법·diff 검사 통과. 정적 export31페이지·Go 1.26.3 embedui·CSP/도구/라이선스 빌드 통과. |
| 기존 실제 회귀 | `desktop.spec.cjs`2개와 `standalone.spec.cjs`1개 통과1.3분·실패/skip0. Electron 자동 진입·업무/모델/증거/재시작·21페이지×2모드×2폭·격리, 계정 메뉴 부재, 프록시 충돌 실패/재시도, 일반 Chrome setup·비밀번호 변경 대화상자·직접 로그아웃·다시 로그인·Go 종료를 검사했다. 이번 브라우저 검사는 새 비밀번호 저장까지 반복한 검사가 아니다. |
| 실제 최종 포터블 | 19개×2모드38조합의 실제 라디오 선택·선택 유지·팔레트/쿠키/글꼴/9쌍 대비4.5:1 이상 확인. 새로고침·종료/재실행의 Rose 다크 유지, Mono 복귀·시스템 모드·기본값 복원 통과. 팔레트 CSS와 사용자 데이터는 변경하지 않았다. |
| 설정/시각 | 실제 패널에서19개 이름·색상칩·체크·선택 탭 강조, 탭 화살표 이동·프리셋 화살표+Space 선택·Escape/포커스 복귀, 화면 배치4항목 변경·복원을 확인했다. Electron의 세 창 크기와 측정된 최소 viewport1018×748에서 가로 넘침/라벨 잘림 없이19개가 모두 보였다. 대표 팔레트10PNG·선택판 라이트/다크·정리한 셸을 로컬 보존하고 선택판/셸3PNG를 독립 검토했다. 물리 OS DPI/IME 전체 검수와 구분한다. |
| ZIP | `ARTEX-2.2.0-Windows-x64-portable-ui.zip`의3,481개 파일 이름·개수·크기 및 Electron/Go/main/CSP/MIT5개 내용 해시 일치. 605,538,695바이트, SHA256 `57D0C7DF672347AE54AFF1012AF0B3D7024CEEE58E562C9EC99BF8CCFA34A444`. 기존 설치/포터블 출력은 보존했다. |

기존 Tabs의 선택 속성과 Radix의 실제 속성이 달라 이번 호출부에서 정확한 `data-state` 선택 스타일·세로 배치를 적용했다. 별도 로컬 검사의 초기 비동기 포커스/선택 상태와 현재 선택 재클릭의 빈값 기대를 보완한 뒤 최종 검사를 통과했다. 제품 오류를 skip·우회로 바꾸지 않았다. 현재 변경 범위의 최종 실패 검사는 없다. 앞선 전체 lint의 `todo-popover.tsx` 기존 위반과 간헐적 시작 포트 문제는 해결한 것으로 처리하지 않는다.

변경 파일은 `layout-controls.tsx`, 셸의 `main-content.tsx`/`app-sidebar.tsx`, 새 `standalone-auth-actions.tsx`, `auth.ts`, 삭제한 계정 메뉴2개/표시 훅, 기존 실제 회귀2개와 README 두 개·UI 기준·이 계획이다. 새 ZIP 내부 `ARTEX-win32-x64-ui/ARTEX.exe`를 실행한다. 전체 Go·웹 unit/입력·설치/업데이트·다른 OS·실계정 OAuth/추론·물리 IME/DPI 검사는 이번 UI 단위에서 반복하지 않았다. Actions·태그·릴리스·원격 artifact 업로드는 하지 않는다. 원격 반영 SHA는 완료 보고와 이 단위의 Git 커밋에서 확인한다.

다음 시작점은 앞선 **동적 loopback 포트의 브라우저 금지 포트 선택을 재현·배제하는 시작 안정성**이다. 브라우저 IPC/승인 대상 네트워크 중계와 외부 환경 검증은 아래 미완료 목록으로 유지한다.

## 이전 단위 — 2026-10-09 테마 프리셋·Mono 석탄색 개선

미커밋 변경이 없는 main `460ff0d11a573e9f94da4bf3bed0ff54cf2ea64e`에서 시작했다. 사용자 요청으로 neobrutal-ui의 프리셋을 선택할 수 있게 연결하고 Mono 다크의 검게 보이는 배경·카드·사이드바·그림자를 밝힌다. 글꼴은 기존 로컬 Pretendard Variable을 유지한다.

- [x] 원본 최신 SHA `b4da2463fe710a77bf464c65125a1a7f40424722`와 `colors.ts`의 19개 공식 팔레트·MIT 표기 확인
- [x] 19개 프리셋 선택·라이트/다크 스와치·기존 저장/부트 연결, 링크·코드 강조의 별도 대비 토큰 적용
- [x] 최종 정적 UI·Go embedui 빌드와 실제 Electron의 38개 팔레트 전환·대비·재실행 보존 확인
- [x] 변경된 Windows 포터블 ZIP 생성·구성 검증

루트 페이지의 서버 redirect가 정적 export에서 오류 HTML을 만들면서 처음 앱에 진입할 때 head의 테마 부트가 실행되지 않는 기존 문제를 재현했다. 저장 쿠키 Red와 화면 Mono가 어긋났고 작업 페이지를 직접 새로고침하면 복구됐다. 루트를 정상 HTML의 client redirect로 교체해 기존 부트 하나가 실행되게 했으며, 중복 부트·CSP 우회는 추가하지 않았다.

| 검사 | 결과와 범위 |
| --- | --- |
| web | 최종 TypeScript·한국어·입력 검사와 기존 unit8개 통과. 정적 export31페이지와 Go 1.26.3 embedui 빌드 통과. |
| 실제 Windows 포터블 | 19개 프리셋 × 라이트/다크38조합을 실제 선택 UI로 전환. 배경/액션 팔레트·텍스트 utility·쿠키·폰트 로딩·가로 넘침 확인. 본문/버튼/링크/보조문구/강조/사이드바/HTTP 코드3색의 대비9쌍이 모두4.5:1 이상이며 최저4.54:1(다크 Fuchsia 버튼)이다. OS 네이티브 전체 접근성 검수와 구분한다. |
| 저장/부트 | 실제 새로고침·종료/재실행의 Rose 다크 보존, Mono 복귀, 시스템 라이트/다크 반영, 기본값 복원 통과. 실제 로컬 Pretendard Variable1개 로딩 확인. |
| 기존 Electron 회귀 | `desktop.spec.cjs` 실제 Go/SQLite2개 통과1.2분. 기존 업무·모델·증거 흐름, 21개 기본 페이지 ×2모드×2폭, 키보드·DOM 조합/확대·축소 모션, 시작 실패/재시도 포함. |
| 시각/ZIP | 전환 애니메이션 완료 상태의 대표5프리셋 ×2모드10PNG를 로컬 보존. Mono/Amber/Fuchsia 독립 시각 검토. 새 ZIP3,481개 파일의 이름·개수·크기와 Electron/Go/main/CSP/MIT5개 파일의 내용 해시 일치. 기존 포터블·설치 출력은 보존했다. |
| 정적 검사 | 선택·루트·HTTP 코드5파일 Biome 종료0(기존 경고/정보 있음), CSS/HTTP formatter 및 diff 검사 통과. 강조색 호출부의 전체 lint는 기존 `todo-popover.tsx:45`의 `noFloatingPromises` 위반으로 실패하며 HEAD에서도 동일함을 확인했다. |

검사 스크립트의 radio 역할, 숨긴 창의 캡처, CSS 색상의1바이트 양자화 차이를 보완한 뒤 실제 팔레트 검사를 통과했다. 캡처 재확인 중 한 번 재시작에서 기존 동적 포트의 `ERR_UNSAFE_PORT` 시작 실패가 발생했다. 오류 화면을 보존했고 이후 동일 패키지의38조합·재시작 검사는 통과했다. 실패를 숨기거나 브라우저 포트 정책을 약화하지 않았다.

변경 파일은 테마 목록·CSS·강조색 호출부·루트 진입과 제품/UI/개발 안내다. 새 산출물은 `desktop/dist/ARTEX-2.2.0-Windows-x64-portable-presets.zip`이며 내부 `ARTEX-win32-x64-presets/ARTEX.exe`를 실행한다. 사용자 데이터 위치는 기존 방식과 같다. Go 업무 소스·런타임 격리·설치/업데이트는 변경하지 않았으며 이번에 전체 Go·설치 업그레이드 검사를 반복하지 않았다. Actions·태그·릴리스·원격 artifact 업로드는 하지 않는다.

- [x] 2026-10-10: 동적 loopback 브라우저 금지 포트 배제·실제 ready/실패/재시도 검증 완료.

앞선 Windows 도구·백업·설치 구현은 아래에 보존한다. 브라우저 IPC/승인 대상 네트워크 중계, 다른 OS 실제 실행, 공인 서명·운영 업데이트, 물리 IME/OS DPI·전체 상태 수동 검수는 여전히 미완료다. 이번 색상 개선으로 이 범위를 완료 처리하지 않는다.

## 이전 단위 — 2026-10-09 Windows 도구·백업·설치 연결

main `e72ddd27a74f84f11473898f1b2ad00cc0e50715`의 미커밋 변경이 없는 상태에서 시작했다. 사용자 요청으로 남은 구현을 연결한다. 기존 한글화 시작 커밋과 원본 v0.3.15의 부모 관계는 바꾸지 않는다.

- [x] 공식 고정 버전 Windows x64 도구 묶음, manifest·전체 파일 해시·추가 파일·링크 검증과 host PATH 우회 차단
- [x] AppContainer 작업 폴더·네트워크 차단과 Job Object 자손 정리, PowerShell·ConPTY·Python·Node·Git·stdio MCP의 실제 Go 호출부 연결
- [x] 정상 종료/강제 종료 WAL의 실제 modernc 백업·무결성·증거 참조 검사, 새 폴더 복원과 원본 보존
- [x] 최종 Electron의 백업/복원·중복 종료·유지 작업 실패·새 홈 재시작 및 기존 업무/UI 회귀
- [x] 최종 Windows 실행 패키지의 임시 설치·업그레이드·제거·실제 Go/UI 준비·기존 데이터 보존
- [ ] 공인 Windows 코드 서명·운영 HTTPS 업데이트, macOS/Linux 실제 실행, 물리 IME/OS DPI와 전체 상태 수동 검수

PowerShell 7.6.6·Python 3.14.8·Node.js 24.21.0·MinGit 2.56.0.2와 Chrome for Testing 153.0.8010.12를 공식 ZIP의 고정 SHA256으로 준비한다. 실제 셸·PTY·스크립트·MCP·Git은 지정 업무 폴더의 AppContainer 안에서 동작하며 일반 TCP와 호스트 인증 환경변수는 차단한다. 다른 작업/에이전트 세션의 터미널 접근은 거부한다. 정상 에이전트 반환에도 owner cleanup이 남은 프로세스를 종료한다.
장시간 셸 작업은 명시적인 background 요청으로 시작한다. foreground timeout은 작업을 중지하며 자동으로 background로 바꾸지 않는다. 현재 SDK의 세션 알림은 `shell_read`/`shell_list`로 확인한다. stdio MCP는 앱 manifest key 또는 검증된 실행 파일 절대 경로만 받으며 host `npx` 자동 설치를 제거했다. 도구 목록 마지막 페이지의 cursor 생략과 stdin 쓰기/직렬화 대기 중 호출 취소를 검사했다. 현 SDK가 전달하지 못하는 image/audio/resource 콘텐츠는 text를 보존하고 명시적 오류로 반환한다.

**전체 도구 준비 상태는 아직 `ready=false`다.** 셸/PTY/Python/Node/CLI는 `verified` + `available`, 브라우저는 파일만 `verified`이며 실행은 `blocked`다. Chromium의 실제 Windows AppContainer 시작에서 crashpad 네임드 파이프 접근 거부를 확인했다. 승인 대상 네트워크 중계도 준비되지 않아 브라우저 MCP를 활성화하지 않는다. 미지원 OS·누락·변조는 차단하며 호스트 실행을 fallback으로 추가하지 않는다.
대안인 공식 Headless Shell 153.0.8010.12도 파일 해시를 대조한 뒤 별도 임시 AppContainer에서 검사했다. 버전 조회는 성공했으나 내부 sandbox를 유지한 DOM 실행은 `0x80000003`으로 실패했다. 공식 Chromium의 [Windows sandbox 초기화](https://raw.githubusercontent.com/chromium/chromium/153.0.8010.12/sandbox/policy/sandbox.cc)와 [alternate windowstation 코드](https://raw.githubusercontent.com/chromium/chromium/153.0.8010.12/sandbox/win/src/window.cc)는 관련 경로를 보여주지만 정확한 crash stack은 확보하지 않았다. Firefox의 자손 IPC도 실패했으며 종료0에 PNG가 없으면 실패로 처리했다. SID 전용 private windowstation의 일반 권한 생성도 거부됐다. `--no-sandbox`의 진단 성공을 제품에 적용하거나 기존 WinSta0 ACL·네트워크 capability를 바꾸지 않았다. 다음 브라우저 연결에는 내부 sandbox와 AppContainer의 desktop·IPC를 함께 유지하는 실행 경로가 필요하다.
기존 Electron 44.6.0도 별도 임시 worker로 복제해 같은 capability0 AppContainer/Job과 `--enable-sandbox`에서 검사했다. JS 시작 전에 `0x80000003`으로 실패했다. 같은 스크립트의 일반 호스트 대조는 DOM·렌더러 Node 접근 부재·PNG 생성에 성공했으나 제품 fallback으로 사용하지 않았다. 종료 후 probe의 Go/Electron 프로세스가 남지 않았으며 공유 소스/자원과 실제 앱의 프록시 포트는 변경하지 않았다.

백업은 Go를 정상 중지하고 데이터 홈 OS 잠금을 확보한 뒤 실제 SQLite 백업 기능과 설정·JWT 키·스킬·증거/본문·구독 보호 저장을 함께 검증한다. 자동 백업은 하루 첫 정상 종료에 데이터 홈 밖에 새 폴더를 만든다. 복원은 새 홈만 만들고 기존 홈을 덮어쓰지 않는다. Go의 손상·경로 이탈·링크·증거 누락 거부, 동일 parsed manifest 사용, 보호 ACL 및 no-replace 게시를 검사했다. UI 복원 중 종료와 백업 프로세스 timeout은 자식 종료를 기다린다.

설치·업데이트는 버전별 폴더와 실제 ready 확인, shared mutex, 고정 RSA 배포 서명·파일 목록·SHA256·Authenticode를 사용한다. signed native Host는 자신의 Windows 신뢰/고정 서명자와 내장 script SHA를 확인한 뒤 프로세스 범위의 PowerShell 정책을 적용한다. 설치 준비 ACK 뒤 데이터 백업이 성공해야 게시 permit을 남기고 종료한다. 백업 실패/취소에는 permit 회수와 native Host의 Job 자손 종료를 확인한다. ready 실패 버전의 재설치는 소유한 비활성 버전을 설치 루트 밖의 고유 sibling에 보존한다. 제거 후 같은 경로 재설치도 검사한다. 개발 설치물은 자동 업데이트 미구성이며 정식 배포가 아니다. 공인 인증서·운영 배포 주소·다른 OS 환경은 현재 준비돼 있지 않다.

### 실제 검사와 범위

| 검사 | 결과와 범위 |
| --- | --- |
| 전체 native Go | Go 1.26.3·modernc 1.52.0·norma 0.4.3·CGO=0의 `go test -json -p 1 -count=1 -timeout 180s ./...` 종료0, 149.568초. 테스트가 있는 21개 패키지, 최상위861·하위 포함1,378개 통과·실패0·외부 모델 설정을 요구하는 `TestLiveContextReview`만 skip1. `report`는 테스트 파일이 없다. 이후 MCP 취소/지원 콘텐츠 보완의 공식 Node 집중3·하위2 및 `go vet ./server`를 별도 통과했다. |
| Windows 도구 | 최종 공식 묶음 native 상위13·하위18, server 집중6·하위2, agent owner cleanup1 통과·실패/skip0. 실제 PowerShell·Unicode ConPTY·Python·Node·Git·stdio MCP, 외부 파일/TCP/환경변수 거부, 취소/Go 강제 종료의 detached 자손 정리와 Windows 8.3/긴 경로12개 관계 거부·정상 alias의 같은 격리 ID 공유·junction 거부를 확인했다. 영향 패키지 vet도 통과했다. Linux/macOS 3개 패키지 cross-build는 코드 빌드이며 다른 OS의 실행 검사가 아니다. |
| 백업·복원 | Go backup 상위10·하위8, cmd7, Node4 통과. 실제 modernc 정상/강제 종료 WAL의 새 백업·복원·비밀번호/JWT/증거/스킬 보존, 누락·손상·경로 이탈·링크·쓰기 실패·동시 목적지·manifest 교체 거부를 검사했다. Node timeout/UTF-8 제어 fixture는 실제 DB 검사와 구분한다. |
| 웹·정적 검사 | 타입·한국어·입력·unit8, 새 설정 카드/desktop 타입 Biome, `go vet ./...` 및 이후 영향 패키지 vet 통과. Go32개 gofmt·JS18개/PowerShell3개 문법·diff, 문서4개 코드 블록/로컬 링크10개 검사 통과. 의존성/lockfile/수동 전용 workflow를 유지했다. |
| 실제 앱/UI | 백업 Electron1개 15.0초 및 나머지 Electron/Chrome5개 1.4분 통과·실패0·skip0. 실제 백업 IPC·새 홈 재실행·중복 종료 대기·복원 실패 후 Go 종료, 기존 업무/증거/보관/인증/재시작·21페이지×2모드×2폭과 IPC 격리를 확인했다. `app.relaunch` 호출은 제어하고 새 프로세스를 실제 실행했다. 구독 화면/모델/일부 장애는 명시적 로컬 fixture이며 이번에 실계정 OAuth·외부 추론을 반복하지 않았다. |
| 빌드·실행 패키지 | 31페이지 export·최종 embedui Go·CSP·글꼴/스킬/공식 도구/라이선스·Windows 실행 패키지·서명되지 않은 개발 Setup 빌드 통과. 최종 `test:package` 1개 8.4초 통과·실패0·skip0. 실제 SQLite/UI 부팅·재시작·비밀번호/스킬 보존, 사용 가능 도구5개·차단 브라우저1개와 Go health/Electron 패키지 버전 일치를 확인했다. Go 버전은 빌드 시 desktop/package.json에서 주입하며 새 릴리스 태그를 만들지 않는다. |
| 배포 집중 검사 | 최종 Node9개 28.4초 통과·실패0·skip0. 실제 native Setup/Host·mutex·게시 취소·준비 실패 rollback·동일 버전 재시도·제거 후 같은 경로 재설치, 8.3/긴 소유권 경로와 실제 실행 중 프로세스의 설치/제거 거부를 확인했다. 일부 서명/취소 검사는 명시적 로컬 키/제어 fixture이며 공인 서명·운영 HTTPS 업데이트 성공 증거가 아니다. |
| 실제 설치 앱 | 최종 `test:installed` 1개 266.2초 통과·실패0·skip0. 임시 `--no-registration` native Setup 설치→실제 Electron·Go 2.2.0·SQLite와 업무 화면→기존 비밀번호의 실제 로그인 JWT/스킬 보존→업그레이드 healthy 확인→제거 후 DB/스킬 보존을 확인했다. 2.2.1은 동일 Go/앱 코드의 package.json 메타데이터만 바꾼 fixture이며 새 코드 릴리스나 운영 자동 업데이트 검사가 아니다. 실제 시작 메뉴/사용자 제거 등록은 변경하지 않았다. |

초기 전체 Go의 localization 검사는 동시 작성 중 Go 파일의 구문 오류로 실패했다. 소스 동결 후 위 전체 검사를 통과했다. 백업 Electron fixture의 main-process 모듈 접근과 npm의 검사 인수 전달을 수정했다. 첫 실제 설치 검사에서는 Windows 8.3/긴 경로의 문자열 비교가 정상 설치를 거부해 ready 뒤 실패 화면으로 이동하는 제품 결함을 발견했다. 설치 소유권·helper root·실행 중 프로세스와 도구 폴더 관계를 canonical 경로로 검사하도록 보완하고 기존 reparse 차단을 유지했다. 실패 로그와 최종 JSON·PNG·trace·임시 DB는 로컬에 보존한다. 이전 빌드/패키지는 별도 로컬 캐시에 보존했다.

race(C 컴파일러 없음), 공인 코드 서명·운영 HTTPS 업데이트, 실제 시작 메뉴/제거 등록의 OS 통합, macOS/Linux 실제 실행, 물리 IME/OS DPI와 모든 데이터/오류/승인 상태 수동 검수는 미실행이다. 다음 구현 시작점은 **Chromium의 Windows 격리 실행과 승인 대상 네트워크 중계를 유지하는 브라우저 연결**이다. 위 외부 환경 검증과 구분한다.
Actions 실행/재실행·원격 artifact 업로드·태그·배포는 하지 않는다. 원격 반영 SHA는 완료 보고와 이 단위의 Git 커밋에서 확인한다.
변경 범위는 `internal/toolruntime`, `internal/backup`, `server/runtime_*`와 도구 조립/사용자 도구, `cmd/artex`·`db/snapshot.go`, Electron의 실행/백업/업데이트·패키징/설치 스크립트·관련 검사, 설정 카드/desktop 타입, README 두 개·아키텍처·이 계획이다.

## 이전 단위 — 2026-10-09 인증·SQLite·Electron 회귀 검증

Electron 자동 진입과 일반 브라우저 비밀번호 흐름의 인증 보호를 확인하고, SQLite 및 실제 데스크톱 회귀 검사를 수행했다.
아래 결과는 해당 날짜에 실제 수행한 검사 기록이며 전체 제품 출시나 미완료 도구·설치 기능의 완료를 뜻하지 않는다.

- [x] 초기화·변경·기존 비밀번호·Electron 자동 진입의 인증 계약 연결
- [x] 새 비밀번호의 8 Unicode 문자 이상·72 UTF-8 바이트 이하 검증을 초기화/변경 API에 연결
- [x] 인증 저장소 조회 오류의 503, 원자 초기화·변경·기존 비밀번호·Electron 자동 진입 회귀 검사
- [x] setup 상태 조회 실패 시 폼 차단과 재시도 복구, 지정 Go/SQLite·웹·실제 앱 검증 결과 기록

이미 구현된 SQLite의 `SetSettingIfAbsent`·비밀번호 변경 CAS·연결 풀 4개와 공통 인증 상태 화면을 유지한다.
Electron 앱 세션 자동 진입·ChatGPT 구독 연결·한국어·Pretendard·neobrutal-ui와 사용자 데이터 경로를 보존한다.

관련 파일: `server/{auth.go,auth_test.go,auth_sqlite_test.go}`, `db/chatgpt_profiles_test.go`, `desktop/tests/standalone.spec.cjs`, 이 계획.
실제 저장소 읽기 오류는 503으로 응답하고 기존 빈 비밀번호 해시의 손상 상태는 500으로 차단한다. 새 길이 정책은 저장할 새 비밀번호에만 적용한다.

### 인증·데스크톱 회귀 검증

| 검사 | 실제 결과와 범위 |
| --- | --- |
| 지정 Go/SQLite 인증 | 공식 ZIP SHA256을 대조한 Go 1.26.3 Windows amd64·modernc 1.52.0·CGO=0. `go test -count=1 -timeout 120s -run '^(TestValidatePassword\|TestAuth)' ./server` 통과5.163초. 잘못된 초기화의 hash/JWT 부재·잘못된 변경의 기존 hash 보존·ASCII/한글/이모지/72바이트 경계·기존 비밀번호 로그인·동시 초기화/변경·읽기 장애·Electron 앱 세션 경계를 확인했다. |
| 전체 native Go | 최종 `go test -json -p 1 -count=1 -timeout 180s ./...` 종료0. 테스트가 있는 19개 패키지, 최상위832개 통과·하위 포함1,337개 통과·실패0·skip1. skip은 외부 모델 설정을 명시적으로 요구하는 `TestLiveContextReview`이며 실제 모델/대상 호출은 하지 않았다. `report`는 테스트 파일이 없다. |
| 정적 검사 | gofmt·`go vet ./...`·fixture 수정 후 `go vet ./db`·diff 검사 통과. web 타입·한국어·입력 검사·unit8개·standalone JS 문법 검사 통과. 기존 의존성/lockfile/라이선스와 수동 전용 workflow를 유지했다. |
| Windows 빌드/수명 주기 | 31페이지 정적 export·실제 Go/embedui·CSP·글꼴/스킬/라이선스·서명되지 않은 Windows 실행 패키지 빌드 통과. 새 한글 데이터 홈의 실제 SQLite 초기화→ready PID/loopback URL→HTTP health→부모 EOF→종료0 검사 통과. |
| 실제 Electron/Chrome | `npm run test:electron` 5개 통과52.7초·실패0·skip0. setup의 503 응답은 명시적 UI fixture이며 폼/비밀번호 입력 부재와 한국어 오류/재시도를 확인했다. fixture 해제 후 실제 Go의 상태200/폼 복구→설정/로그인/종료도 확인했다. 기존 Electron 자동 진입·업무/증거/보관/재시작·화면·격리와 구독 연결 시작/취소·명시적 연결 UI fixture를 함께 검사했다. 실제 계정 OAuth 완료 검사가 아니다. |
| 실제 Windows 실행 패키지 | `npm run test:package` 1개 통과5.2초·실패0·skip0. 실제 Go/SQLite/UI 부팅·재시작·사용자 스킬/DB 보존·Pretendard·API 경계를 확인했다. 설치·서명·릴리스 검사가 아니다. |

첫 전체 Go 검사에서는 기존 구독 스키마 업그레이드 fixture가 Windows CRLF 파일에서 열 정의를 제거하지 못해 실패했다.
fixture의 정확한 열 정의 매칭에서 줄바꿈 조건만 제거한 뒤 전체 검사를 다시 통과했다. 실제 스키마·업그레이드·사용자 데이터 처리 코드는 바꾸지 않았다.
Electron 검사 명령의 최초 작업 디렉터리 오류로 npm 실행이 한 번 중단됐으며 올바른 `desktop` 디렉터리에서 최종 검사를 통과했다.
처음의 실패 로그와 최종 Go JSON·Playwright PNG/trace·신규 임시 SQLite는 로컬 검증 자료로 보존하고 원격에 업로드하지 않는다. lifecycle 스크립트의 신규 임시 홈은 해당 검사 종료 시 정리했다.
race는 C 컴파일러가 없어 미실행이다. 다른 OS 실제 실행·물리 IME/OS DPI·실계정 OAuth/추론·M4 도구/백업/설치/서명/업데이트는 이번 단위에서 실행하거나 완료 처리하지 않았다.

제품의 다음 구현 시작점은 기존 M4 앱 전용 도구 런타임이다.
Actions 실행/재실행·원격 artifact 업로드·태그·배포는 하지 않는다. 원격 반영 SHA는 완료 보고와 이 단위의 Git 커밋에서 확인한다.

## 이전 단위 — 2026-10-07 공개 문서의 비공개 대상 정보 제거

원격 main과 로컬 미커밋 변경을 확인한 뒤 시작했다. 사용자 요청에 따라 비공개 검토 대상의 식별 정보·소스 구성·경로·환경 설정·분석 결과·시험 기록을 공개 문서와 main 이력에서 제거했다. 사용법은 가상 프로젝트 예시로 안내하며 제품 자체의 구현·검증 결과와 구분한다.

- [x] README의 실제 대상 예시·환경·분석 기록 제거 및 일반 사용법 유지
- [x] 이 계획의 실제 대상 시험·분석 기록과 로컬 증거 참조 제거
- [x] 공개 문서·예시·커밋에 비공개 대상 정보를 기록하지 않는 작업 기준 추가
- [x] 사용자 명시 승인으로 공개 main 이력의 비공개 대상 문서·커밋 메시지 제거 및 코드 보존 검증

변경 파일은 `AGENTS.md`, `README.md`, 이 계획이다. 비공개 검토 산출물과 기존 시험 DB·설정은 저장소 밖의 로컬 자료로 보존한다. Git 추적 파일 699개의 대상 식별 정보 검색은 0건이며 가상 예시·Markdown 로컬 링크 8개·코드 블록·diff 검사는 통과했다. 문서 검사 실패는 없다. 앱 구현 변경이 없어 Go/웹/Electron 빌드·회귀는 재실행하지 않았다.
이력 강제 갱신 금지의 이번 예외를 사용자에게 명시 승인받고 원격의 예상 SHA를 지정해 main을 갱신했다. 기존 488개 커밋 중 앞선 482개는 SHA 그대로이며, 뒤의 6개는 문서 두 파일과 메시지 세 개만 정리했다. 각 커밋의 나머지 파일·작성 정보·부모 연결을 보존했고 마지막 파일 트리는 기존 문서 정리본과 동일하다. 새 main 이력의 식별 정보·메시지·diff 검사와 최종 트리·연결성 검사는 통과했다. 원본 이력 백업과 감사 자료는 저장소 밖에 보존한다. 다른 복제본·호스팅 캐시의 삭제까지 확인한 결과는 아니다.
다음 구현 시작점은 M4 앱 전용 도구 런타임이다. 원격 반영 SHA는 완료 보고와 이 단위 Git 커밋에서 확인한다. Actions·원격 artifact·태그·배포는 없다.

## 이전 단위 — 2026-10-07 실제 ChatGPT 구독 검증

미커밋 변경이 없는 main에서 시작했다.
사용자 요청에 따라 실제 계정의 공식 OAuth·구독 동의부터 모델 추론·소스 읽기까지 검증했다.
ARTEX가 소유한 로그인과 보호 저장을 사용하며 다른 앱의 인증 파일이나 API 키는 사용하지 않았다.

- [x] 실제 로그인·구독 동의·공개 모델 목록 조회와 GPT-6-Astra 활성화
- [x] 같은 데이터 홈 재시작 후 연결·권한·활성 프로필 유지
- [x] 실제 추론 오류 재현·수정과 `OK` 응답 확인
- [x] 실제 MainAgent의 복사본 Read 3건·경로별 승인·결과 대조·한국어 분석 답변 저장
- [x] 관련 Go 검사·Windows 패키지 빌드/회귀와 README·계획 갱신

처음 실제 추론은 HTTP 200의 정상 SSE 본문인데 `Content-Type` 헤더가 없어 거부됐다. 거부 전에 본문을 읽지 않아 기존 Capture가 비어 있던 문제도 확인했다. 헤더가 생략되면 동일 SSE 파서로 처리하고 반드시 `response.completed`와 완료 상태를 확인한다. 명시된 잘못된 미디어 타입은 최대 64KiB의 진단만 기존 Capture에 남기고 실패한다. 원문·헤더 매개변수·OAuth 토큰은 공개 오류에 넣지 않는다. JSON 응답 수용·다른 엔드포인트·유료 API 전환은 추가하지 않았다.

| 검사 | 실제 결과와 범위 |
| --- | --- |
| 실제 OAuth/모델/추론 | 공식 로그인·구독 동의 후 연결/권한 확인, 계정 모델 4개 조회, GPT-6-Astra 활성화 통과. 재시작 2회 연결/프로필 유지. 수정 후 실제 연결 테스트가 3,500ms에 `OK` 반환. Chrome의 callback 완료 페이지는 ERR_BLOCKED_BY_CLIENT를 표시했지만 앱의 callback 처리와 연결은 성공했다. 브라우저의 차단 설정은 변경하지 않았다. |
| 실제 파일 도구/저장 | MainAgent의 실제 Read 호출·경로별 승인·도구 반환 내용 대조·입력 해시 보존·최종 답변 및 활동/사용량 저장 확인. 구체적인 입력 소스와 검토 결과는 공개하지 않는다. 기존 승인·차단 정책과 작업 상태를 유지했다. |
| Go 집중/전체 agent | 지정 Go 1.26.3·CGO=0, ChatGPT 집중 검사 7개/하위 17개 통과0.562초. 정상 헤더 생략·본문·usage·도구 ID/인수, JSON/HTML/빈 본문/중단/잘못된 이벤트 거부, 명시 비SSE 진단 크기·비밀 비노출 회귀 포함. `./agent` 전체 통과5.060초, `go vet ./agent`·gofmt·diff 검사 통과. |
| Windows 실행 패키지 | 31페이지 정적 export·embedui Go·서명되지 않은 패키지 빌드 통과. `npm run test:package` 1개 통과3.8초(전체4.2초)·실패0·skip0. 실제 Go/SQLite/UI 부팅·종료·재시작·자동 진입·사용자 스킬/DB·Pretendard·API 경계 회귀 확인. 이전 출력·실패 진단·실제 계정 증거는 로컬에 보존했다. |

변경 파일은 `agent/chatgpt_provider.go`, `agent/chatgpt_provider_test.go`, README 두 개와 이 계획이다. 실제 계정·토큰·비공개 소스·분석 결과·진단 본문·스크린샷·시험 DB는 저장소 밖에 보존하며 커밋/원격 업로드하지 않는다.
전체 Go/전체 Electron 회귀는 이전 단위 결과이며 이번에 반복하지 않았다. race(C 컴파일러 없음)·다른 OS 실행·실계정 갱신/원격 해제/한도 소진·물리 IME/OS DPI는 미실행이다.
다음 시작점은 기존 M4 앱 전용 도구 런타임이다. OS/네트워크 격리·설치/서명/업데이트는 별도 미완료다.
원격 반영 SHA는 완료 보고와 이 단위의 Git 커밋에서 확인한다. Actions 실행/재실행·원격 artifact·태그·배포는 없다.

## 이전 단위 — 2026-10-07 ChatGPT 구독 연결

아래 기동·미검증·다음 시작점은 이 구현 단위 종료 당시의 기록이다. 후속 실제 계정 검증 결과와 현재 시작점은 상단의 현재 단위를 따른다.

사용자 요청으로 ChatGPT 구독 OAuth 연결을 Go 업무 에이전트에 추가한다.
미커밋 변경이 없는 main에서 시작했다.
공식 Sign in with ChatGPT의 공개 인증·모델·Responses 계약을 확인했다.
다른 앱의 로그인 파일을 가져오지 않고, ARTEX가 직접 PKCE 로그인/동의와 보호된 로컬 토큰 저장·갱신을 소유한다.
API 프로토콜과 인증 방식을 분리하며, 업무 SQLite 버전 2는 기존 버전 1에 `auth_method` 열 하나를 동일 트랜잭션에서 추가한다.
기존 프로필·관계·사용자 데이터와 API 키 방식은 보존한다. PG 데이터 이전이나 DB 이중 지원은 추가하지 않는다.

- [x] Go OAuth/보호 저장·갱신·공개 모델 목록 구현과 실제 로컬 검증
- [x] 구독 Responses의 도구 호출·완료/실패 처리, 업무 호출부 연결과 검사
- [x] 구독 연결 UI·제한된 Electron 외부 로그인 열기와 Playwright 검사
- [x] Windows 앱 빌드·실제 브라우저 로그인 열기, README와 검사 결과 기록
- [x] 사용자 계정의 공식 로그인·구독 동의·실제 모델 목록/추론·파일 도구 호출 확인 — 후속 구독 검증 단위 참조

ARTEX를 별도 데이터 홈에서 실제 실행하고 같은 홈을 다시 열었다. 비밀번호 없는 자동 진입·기존 작업 보존·SQLite 버전 1에서 2로 갱신을 확인했다. 당시 구독 연결은 기본 브라우저 열기와 승인 대기까지 확인했으며 후속 실제 계정 검증 결과는 상단의 구독 검증 기록을 따른다.

도구 호출 검토 중 MainAgent/Planner/목표 분해가 업무 Guard를 전달하지 않던 경로를 연결했다. 실제 도구 실행 전 허용·차단·수동 승인과 작업/역할별 감사 기록을 검사했다. 구독 연결 해제나 권한 상실 시 명시적으로 선택한 구독 프로필이 유료 API 경로로 자동 전환되지 않는다. 병렬 도구 인수와 호출 ID는 전체 검증 후 실행 이벤트로 전달한다.

### 구독 연결 단위의 로컬 검증

| 검사 | 실제 결과와 범위 |
| --- | --- |
| Go 전체·수정 후 검사 | `go test -count=1 -timeout 180s ./...`의 검사 대상 19개 패키지 통과. 이후 추가한 구독 fallback·병렬 도구 검사와 MainAgent/Planner/Goals Guard의 허용·차단·승인 9개 경우 통과. 마지막 서버 전체 검사 38.136초 통과. DB 전체 검사 29.052초 통과. 지정 Go 1.26.3/modernc를 사용했다. |
| OAuth/비밀 저장 | 실제 로컬 HTTP/JWKS fixture의 PKCE·상태·nonce·서명·audience·만료·동의·갱신 회전·취소·로그아웃 및 Windows DPAPI/ACL·변조·자식 프로세스 파일 잠금 검사 통과. 공개 DTO/기록에 토큰을 내보내지 않는다. 공식 OpenAI 서버의 계정 로그인·구독 추론 성공 증거가 아니다. |
| SQLite | 실제 modernc 버전 1 DB의 API 키·관계·설정 보존/버전 2 재개방과 구독 프로필 검증 통과. 별도 제어 드라이버의 ALTER/version 오류 rollback 검사도 통과하며 실제 modernc 검사와 구분한다. 실제 앱의 기존 홈에서 버전 2와 기존 작업 보존을 재확인했다. |
| 정적 검사/빌드 | 타입·한국어·입력·관련 lint·JS 문법·Go vet·gofmt·diff 검사 통과. 31페이지 정적 UI·embedui Go·Windows 실행 패키지 빌드 통과. 새로운 의존성은 없다. 기존 전체 formatter의 CRLF 진단을 없애기 위한 무관한 서식 변경은 하지 않았다. |
| 실제 Electron·일반 Chrome | 최종 `npm run test:electron` 5개 통과55.1초·실패0·skip0. 실제 Go 로그인 시작/취소·JWT/앱 세션·callback/IPC 거부 검사, 명시적 UI fixture의 동의/첫 안내/모델 순서/활성화/로그아웃, 기존 업무/화면/격리와 일반 Chrome 비밀번호 흐름을 확인했다. 계정 연결 화면 fixture를 실제 OAuth 완료로 표현하지 않는다. |
| 실제 실행 패키지 | `npm run test:package` 1개 통과6.3초·실패0·skip0. 실제 Go/SQLite/UI 부팅·재시작·자동 진입·Pretendard·스킬/DB 보존·API 경계 확인. 이전 패키지는 별도 dist 경로에 보존했다. 서명·설치·릴리스 검사가 아니다. |

초기 검사에서 기존 최소 DB fixture의 누락 열, 인증 cookie를 무시한 테스트 전제, 보조 Electron 창의 preload 경로, DB 없는 서버 fixture 접근을 수정한 뒤 위 최종 검사를 통과했다. 실패 흔적과 새 스크린샷·임시 DB·trace는 로컬에 보존한다.
Linux/macOS용 인증 패키지 cross-build는 통과했지만 다른 OS 실제 실행은 하지 않았다. C 컴파일러가 없어 race 검사는 미실행이다. 물리 IME/Windows 디스플레이 배율·실제 OAuth 동의/구독 사용량은 미검증이다.
다음 시작점은 열어 둔 ARTEX의 사용자 로그인·동의 완료 후 모델 활성화와 복사본 파일의 실제 승인/읽기/결과 확인이다. 제품 구현의 다음 단위는 기존 M4 앱 전용 도구 런타임이며 OS/네트워크 격리·설치/서명/업데이트는 완료 처리하지 않는다.
원격 반영 SHA는 완료 보고와 이 단위의 Git 커밋에서 확인한다. Actions 실행/재실행·자동 트리거·원격 artifact·태그·배포는 없다.

## 이전 단위 — 2026-10-07 사용법 안내

README에 실행·모델 활성화·작업 생성·소스 경로/첨부·도구/승인 기록·발견 검토 순서를 구체화했다. Go의 네이티브 파일 도구와 미준비 외부 명령/MCP를 구분하고, 경로 입력은 자동 가져오기·색인이 아니며 실행 환경 미준비는 읽기 전용 모드가 아님을 기록했다.
변경 파일은 README와 이 계획이며 문서의 UI/모델/파일 도구 계약을 현재 ARTEX·지정 SDK 소스와 대조했다. Markdown 로컬 링크·코드 블록·diff 검사는 통과했다. 구현 변경이 없어 앱 빌드·단위/Playwright 검사는 재실행하지 않았다. 비공개 대상별 조사·시험 기록은 공개 문서에 포함하지 않는다.

## 이전 단위 — 2026-10-07 Pretendard 단일 글꼴

사용자 요청으로 한국어·영문·숫자·코드·로그·그래프와 Electron 시작/실패 화면을 로컬 Pretendard Variable 하나로 통일했다.
미커밋 변경이 없는 main/원격 `6d76903330b63f08c9cf0421c74c127a437759af`에서 시작했다.
Noto 파일/라이선스·Geist 의존성·폰트 registry/선택 UI/상태/쿠키 적용 경로를 제거했다. 기존 사용자 설정을 삭제하거나 이행하지 않는다.
`font-mono` 호출을 `font-sans`로 교체하고 code/pre/kbd/samp는 동일 글꼴을 상속한다. G6 canvas는 글꼴 로딩 후 실제 body family로 렌더링한다.
Mono 색상/테두리/섀도와 기존 비밀번호 없는 진입·업무 API는 유지한다. 알림 HTML의 개별 OS 글꼴 지정도 Pretendard로 교체했다.

공식 v1.3.9의 글꼴과 OFL을 원격 Git blob/로컬 SHA256으로 대조했다. 출처·크기·해시·원저작자와 라이선스는 `docs/ui-design.md`에 기록한다.
정적 UI와 시작 화면 각각에서 같은 원본 WOFF2 하나를 제공하며 실행 중 CDN이나 다른 글꼴을 요청하지 않는다.
변경 파일은 웹 글꼴/설정/로더·39개 화면/컴포넌트의 글꼴 클래스·G6 연결·package/lock,
`desktop/{scripts/build.cjs,src/main.cjs,src/startup.html,tests/desktop.spec.cjs,package-tests/portable.spec.cjs}`·`notify/html.go`·OFL 원문 보존용 `.gitattributes`·PRODUCT/README 두 개/UI 기준/이 계획이다.
기존 생성 패키지와 검사 증거는 별도 경로에 보존하고, 새 생성 자원에는 사용하지 않는 글꼴을 포함하지 않는다.
다음 구현 시작점은 M4의 앱 전용 도구 런타임이다. 설치/서명/업데이트·다른 OS 실제 실행·물리 IME/OS DPI 검증은 별도 미완료다.

### 단일 글꼴 단위의 로컬 검증

| 검사 | 실제 결과와 범위 |
| --- | --- |
| 원본/생성 자원 | 공식 Git blob·SHA256 일치. OFL 원문의 기존 줄 끝 공백과 LF도 `.gitattributes`로 보존한다. 최종 `web/out` 글꼴은 Pretendard WOFF2 1개이며 시작 화면/패키지 글꼴과 SHA256이 동일하다. Noto 자원·구 라이선스·Geist package/lock/설치 항목과 구 글꼴 선택 경로가 없다. |
| 정적 검사/빌드 | 타입·한국어·입력·관련 7개 구조 파일 lint·JS 문법·gofmt·diff 검사 통과. 최종 31페이지 정적 export, 지정 Go 1.26.3 embedui와 Windows 실행 패키지 빌드 통과. 전체 Biome formatter 검사는 기존 CRLF 진단으로 미통과이며 관련 없는 자동 서식 변경은 하지 않았다. |
| 실제 Electron + 일반 Chrome | `npm run test:electron` 3개 통과32.0초·실패0·skip0. 로딩된 font face 1개, 본문/컨트롤/코드의 동일 font family, CDP의 한글 실제 렌더링 글꼴이 custom Pretendard임을 확인했다. 글꼴 선택 UI 부재·시작/실패 화면과 진단 pre의 실제 Pretendard 로딩, 기존 자동 진입/업무/보관/재시작/격리와 21페이지×2모드×2폭·상세10탭·입력 조합·확대·모션 회귀도 통과했다. |
| 실제 실행 패키지 | `npm run test:package` 1개 통과3.1초·실패0·skip0. 패키지의 폰트 파일/라이선스 1종, 실제 Pretendard 로딩·자동 진입·재시작·스킬/SQLite 보존·API 경계 확인. |
| 알림 HTML | 실제 `go test -count=1 -timeout 120s ./notify` 통과0.5초. 메일 발송이나 외부 이메일 클라이언트 렌더링 검사는 하지 않았다. |

위 최종 기능/글꼴/빌드 검사에는 실패/skip이 없다. 전체 Go/서버·기존 web unit·race·다른 OS 실행 검사는 반복하지 않았으며 이전 단위 결과와 구분한다.
스크린샷·임시 DB·진단·trace는 로컬에만 보존한다. Actions 실행/재실행·자동 트리거·원격 artifact·태그·배포는 없다.
원격 반영 SHA는 완료 보고와 이 단위의 Git 커밋에서 확인한다.

## 이전 단위 — 2026-10-07 비밀번호 없는 Electron 진입

사용자 요청으로 Electron의 비밀번호 설정/로그인 화면을 앱 세션 인증으로 교체했다.
미커밋 변경이 없는 main/원격 `6c8183f8cf6a4e25697a7718e1b816882c290445`에서 시작했다.
Go가 정확한 앱 세션 키·바인딩 Host·Origin을 검증한 요청에만 기존 형식의 JWT를 발급한다.
업무 화면은 JWT 발급/저장 뒤 표시하며, 연결 실패는 한국어 오류와 재시도로 남긴다.
새 데이터 홈과 기존 비밀번호 데이터 모두 바로 열리고 기존 비밀번호/작업/증거/스킬은 변경하지 않는다.
Go 단독 브라우저 접속은 비밀번호 설정·로그인·이용 안내를 유지한다. 승인·차단·대상 범위와 일반 API JWT 검증도 유지한다.
두 프로필 메뉴에서 Electron의 비밀번호 변경/로그아웃을 숨겼고 mock 실행 모드와 실행 안내를 함께 갱신했다.

변경 파일: `server/{auth.go,desktop_session.go,server.go,auth_desktop_test.go}`, 웹의 공통 인증 준비/상태/모드 저장과 API/types/mock·layout·login/setup·두 프로필 메뉴,
`desktop/tests/{desktop.spec.cjs,standalone.spec.cjs}`·`package-tests/portable.spec.cjs`, README 두 개·architecture·ui-design·이 계획.
이 단위의 검증 결과는 아래에 기록한다. 다음 구현 시작점은 기존 M4의 앱 전용 도구 런타임이며 정식 릴리스 상태는 아니다.

### 자동 진입 단위의 로컬 검증

| 검사 | 실제 결과와 범위 |
| --- | --- |
| 지정 Go/modernc | 인증·앱 세션 집중 검사와 `go test -count=1 -timeout 180s ./server` 통과(전체 server 39.7초). 새 홈/정상·빈·잘못된 기존 비밀번호 보존, 실제 저장소 오류 전파, 앱 세션·Host/Origin·JWT와 standalone 차단 검사. |
| 정적 검사/빌드 | gofmt·`go vet ./server`·diff 검사 통과. web 타입·한국어·입력 검사·unit8개·신규 인증 파일 Biome 통과. 최종 소스로 31개 페이지 정적 export와 실제 Windows Go/embedui·CSP·라이선스·실행 패키지 빌드 통과. |
| 실제 Electron + 일반 Chrome | 최종 `npm run test:electron` 3개 통과43.4초·실패0·skip0. 자동 진입·두 프로필 메뉴·잘못된 토큰 교체·인증 실패 재시도·JWT 없는 API 거부·업무/증거/보관/복원/기존 데이터 재시작·21개 화면·격리·프록시 충돌 재시도, Go 단독 setup/비밀번호/약관/로그인/정상 종료 포함. 설치된 Chrome은 검사 도구이며 앱 런타임 준비 성공이 아니다. |
| 실제 Windows 실행 패키지 | 최종 `npm run test:package` 1개 통과3.2초·실패0·skip0. `app.isPackaged`의 새 홈/기존 비밀번호 자동 진입, SQLite·사용자 스킬 보존·API 경계·Go 종료·라이선스·미준비 상태 검사. |
| 로컬 증거 | 최종 결과106 PNG와 standalone trace·프로세스 기록·임시 SQLite를 로컬에 보존. 이전 패키지/검사 결과와 실패 trace도 별도 경로에 보존하며 커밋/원격 업로드하지 않는다. |

초기 패키지 생성은 기존 출력 보호로 중단됐다. 기존 폴더를 보존한 뒤 재빌드했다.
새 Playwright 검사의 헤더 메뉴 선택자·중복 trace 시작·Next route announcer와 오류 alert 선택 충돌을 수정한 뒤 최종 검사가 모두 통과했다. 실제 실패를 skip으로 바꾸지 않았다.
이번 단위에서 전체 `./...`·Linux/macOS 교차 빌드는 반복하지 않았으며 아래 이전 단위 결과와 구분한다. race(C 컴파일러 없음)·다른 OS 실제 실행·물리 IME/OS DPI·M4 도구/백업/설치/서명/업데이트는 미실행이다.
Actions 실행/재실행·새 자동 트리거·원격 artifact 업로드·태그·배포는 하지 않는다. 원격 반영 SHA는 완료 보고와 이 단위의 Git 커밋에서 확인한다.

## 이전 단위 — 2026-10-07 실제 SQLite/Electron 연결

이전 브랜치 통합은 main `93f2179928bf735e7ba3570d6a0662dabe7863ad`에 완료됐다. 과거의 미완성 전환 별도 브랜치 유지 지침은 종료한다.
통합 전 main·업무 전환·foundation의 이력은 해당 커밋의 부모와 이전 기록에 보존돼 있다.
이번 작업은 미커밋 변경이 없는 최신 main에서 시작했다. 옛 ZIP/패치나 잔존 work 브랜치를 작업 기준으로 선택하지 않는다.

Go 1.26.3과 modernc 1.52.0을 확보하고 실제 업무 SQLite 전환을 앱에 연결했다.
NewManager는 data/artex.sqlite를 사용하고 server.New는 필수 초기화 오류를 반환하며 자원을 정리한다.
작업·자산·회사 범위·탐색·취약점·재검증·증거·보관·알림·부가 질문의 실제 호출 SQL을 이식했다.
PG 드라이버/DSN/보정 DDL/테스트 fixture·skip, Go 자체 업데이트, Docker·시작 스크립트는 제거했다.
Electron은 정적 한국어 UI/Go를 재사용하고 앱 세션·Host/Origin·JWT·CSP·렌더러 격리·부모 EOF·장애 UI를 연결했다.
neobrutal-ui Mono와 로컬 Noto Sans KR/OFL을 적용했다.

**개발 검증 상태이며 정식 릴리스가 아니다.** 도구 런타임 번들/OS 격리·자동 일관 백업·설치/서명/업데이트와 macOS/Linux 실제 지원은 미완료다.
없는 도구를 준비 완료로 표시하거나 host PATH로 우회 실행하지 않는다.

이번 시작에서 원격 heads는 main만 남았음을 실제 확인했다. 시작 main/원격 SHA는 93f2179928bf735e7ba3570d6a0662dabe7863ad이며 미커밋 변경은 없었다.
사용자 DB/증거/설정/볼륨, 릴리스 태그, artifact/cache를 정리 대상으로 삼지 않는다.

## 이전 M2.2i — 작업 모델 목록과 전환 저장

- `db/task_context.go`의 실제 목록 교체/할당량 소진 전환에서 PG 행 잠금과 now()를 제거했다. 기존 OpenImmediate의 짧은 쓰기 트랜잭션 안에서 검증/관계 변경/현재 모델/리비전을 함께 저장한다.
- 명시적 `?N` 매개변수를 사용한다. 일괄 조회의 PG 배열은 int64 목록을 JSON TEXT로 바인딩한 json_each 조회로 교체했다. 세 일괄 조회는 유지하며 N+1 조회나 이중 저장을 추가하지 않았다.
- 모델 전환 도중 SELECT/UPDATE/COMMIT이 실패하면 반환하는 전환 구조체를 비운다. 커밋되지 않은 Advanced/NextProfileID를 성공한 전환으로 반환하지 않는다.
- 이전 리비전의 늦은 오류는 새 목록을 바꾸지 않는다. 현재 커서 뒤의 ready 모델만 고르고, 소진 후 앞의 모델을 되살리지 않는 동작을 유지한다. 삭제된 작업의 늦은 전환과 음수 활성 모델 입력을 거부한다.
- 같은 파일의 의도 차단/재개 시간 SQL과 RowsAffected 오류 전파를 이식했다. 기존 대상/승인/차단 정책이나 도구 실행 본문은 변경하지 않는다.
- PG 프로세스/행 잠금 관찰에만 의존하던 `task_context_lock_test.go`를 제거하고 SQLite 경합/원자성 검사로 대체했다. 기존 모델 참조의 SQLite 테스트도 유지한다.

위 단위의 실제 Go/modernc 검사는 이번에 통과했다. 설정/도구/모델 참조/재검증 시드/LLM DDL 제거/모델 목록 저장을 다시 만들지 않는다.
다음 시작점은 아래 M4의 관리된 도구 런타임 배포와 OS 격리다. SQLite/Electron 회귀 검사를 먼저 유지한다.

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
- [x] **M2.2 실행 가능한 기반:** 전체 스키마/시드/쓰기/취소/닫기를 실제 NewManager/New에 연결, native 시작/오류 전파 검사.
- [x] **M2.2a 연결/트래픽:** 이전 경로/PRAGMA/WAL/취소/재열기/FTS 검증. OpenImmediate 전체 통합은 별도.
- [x] **M2.3a 설정/인증 저장:** 실제 메서드/HTTP fixture의 재열기/동시 최초 설정/fail-closed. 전체 부팅 아님.
- [x] **M2.2b/M2.3b 프롬프트·모델:** 실제 드라이버 원자 저장/조회·수정 오류 검사.
- [x] **M2.2c 부팅/설정:** manager 오류/정리, 묶음 저장, 실제 server.New 생성/부팅 검사.
- [x] **M2.2d 스키마:** 58개 테이블/시드/식별자/버전/IMMEDIATE 실제 Open·동시 초기화·취소.
- [x] **M2.2e 도구/MCP:** 관계 CRUD/집계/원자성/조건부 MCP 저장의 native 검사. 외부 도구 배포는 M4.
- [x] **M2.2f 모델 참조/대화:** 모델 삭제/에이전트·대화 연결/대화 저장의 native 검사.
- [x] **M2.2g 재검증 시드:** FK 순서/단일 트랜잭션/사용자 편집·삭제 보존, 실제 시작 검사. reporter도 원자화.
- [x] **M2.2h LLM 기록/사용량:** 실제 opener/Recorder·검색·UTC 집계·원장 보존 검사.
- [x] **M2.2i 작업 모델 목록:** 기존 실제 SQLite 6개와 오류 주입 제어 검사를 지정 Go에서 실행.
- [x] **M2.3 설정·인증 통합:** 실제 Electron 앱 세션 자동 진입/모델 저장·재실행, Go 단독 브라우저 setup/로그인, JWT 오류/정리/앱 세션 정책.
- [x] **M2.4 자산·범위:** 관계/DSL/net/netip, IPv4·IPv6 경계/기업 귀속/중복과 실제 다중 풀 경합 검사.
- [x] **M2.5 작업·탐색:** native 작업/의도/대화/사실/취약점/승인/재검증/사용량/기록/취소 검사.
- [x] **M2.6 증거·보관:** 형식4의 열·키·27개 테이블/행 수/JSON/stream 검증, 큰 ID와 공유 자산 병합, 오류 주입·rollback·재시도 및 실제 Electron 증거 보관/복원/재실행 통과.
- [x] **M2.7 통합:** PG 없이 로컬 모델 fixture → 작업/기록/330KB 트래픽/취약점 증거 → 보관/복원 → 종료/자동 진입/재실행 보존. PG fixture/skip/driver/config/서비스/smoke/헬퍼/구 배포 도구 제거.

DB 선택 스위치/SQL 번역기/PG fallback/구 데이터 자동 이전기/임시 호환 계층은 추가하지 않는다.

## M3. Electron 실제 UI + neobrutal-ui

UI 지침과 최신 지정 원본을 읽었고 원본 SHA b4da2463fe710a77bf464c65125a1a7f40424722가 동일함을 확인했다.

- [x] 공통 Mono 색상 토큰/Pretendard 단일 로컬 글꼴/컨트롤/표/오버레이와 호출부 연결, MIT/OFL/기존 저작권 보존.
- [x] 셸/전체 업무 화면의 기본21개 페이지×2모드×2폭·접근 가능한 컨트롤 이름 검사. 모든 데이터·오류·승인 상태의 수동 검수와 구분한다.
- [x] Electron 메인/렌더러 분리, lockfile/Go/정적 UI, 단일 인스턴스/userData/ready/장애/정상 종료.
- [x] 앱 세션/Origin/Host/IPC/CSP/탐색·부모 EOF와 Windows Go 강제 kill·관리된 도구의 detached 자손 정리 검사. 다른 OS의 자손 정리는 별도 미검증.
- [x] 서명되지 않은 Windows 실행 패키지의 실제 resources/userData/Go/SQLite 부팅·재실행·사용자 스킬 보존·라이선스 검사.
- [x] 실제 Electron의 21개 페이지 × 라이트/다크 × 1280/1440px, 작업 상세 10개 탭, 취약점 증거, 키보드·DOM composition·125/150% Electron 확대·축소 모션 검사.
- [ ] 물리 Windows IME·OS DPI·모든 데이터/오류/승인/오버레이 상태의 수동 시각·접근성 검수. DOM composition/Electron 확대 검사와 구분한다.

빈 창/일부 쇼케이스/mock 캡처로 전체 제품 또는 UI 완료를 표시하지 않는다.

## M4/M5. 도구·신뢰성·배포

- [x] Windows x64 셸/PTY/Python/Node/Git/stdio MCP, 공식 경로·버전·출처·전체 해시·라이선스·누락/변조/미지원 준비 상태 차단.
- [x] Windows 브라우저 내부 IPC·Go 승인 대상 네트워크 중계·실제9개 MCP 도구 연결. 정상 초기화에서 전체 도구 `ready=true`, 실패/변조는 차단한다.
- [x] Windows AppContainer의 작업 공간/네트워크/인증 환경변수 차단, Job 자손 정리·kill·손상·쓰기 실패와 일관된 DB+증거 백업/새 폴더 복원.
- [x] 실제 SQLite 독립2풀의12writer/4reader·1,920회 커밋·재열기 및 용량 한도/읽기 전용 오류의 rollback·복구. Windows 집중 race28개+하위4개 통과.
- [x] 작업 파일의 독립 루트·실제7개 API·첨부/도구 연결, Windows junction/하드 링크와 보관 유출 거부·구 파일 보존.
- [x] 공유 Chromium 금지 포트 정의의 동적 소켓 선택·명시 포트/ready 거부·실제 앱 실패/재시도.
- [x] 실제120초8개 업무 lane·초기Engine 실행/취소2회·재시작 보존, Windows 관리 폴더 이동/보관 Root 경로 교체와 workspace/이동 ACL 거부·복구.
- [ ] 물리 디스크 고장/전원 장애·전체 OS ACL 장애·120초 연속 Engine 실행·모든 물리 UI 상태 검수. 제어 검사/초기화 preflight/교차 컴파일과 구분한다.
- [ ] macOS/Linux 도구/PTY/네트워크/자손 격리의 실제 실행. 교차 컴파일을 실행 검증으로 처리하지 않는다.
- [x] Go 자체 업데이트/시작 스크립트/Docker/PG 안내·의존성·구 배포 workflow 제거. 사용자 데이터 보존. Electron 자동 업데이트 배포는 별도 미완료.
- [x] README와 개발 안내를 현재 Electron/SQLite 빌드·검사·데이터 경로·미완료 상태로 교체.
- [x] 최종 Windows 임시 설치·업그레이드·제거와 실제 Go/UI 준비·기존 데이터 보존. 2.2.1은 동일 코드의 메타데이터 검사 fixture.
- [x] 실제 Windows 시작 메뉴 바로가기·HKCU 제거 등록, 업그레이드 후 대상/버전과 제거 후 해제·데이터 보존. 기존 ARTEX 등록이 없던 PC의 임시 설치로 확인.
- [ ] 공인 Windows 코드 서명·운영 HTTPS 업데이트. 이번 사용자 요청에서 제외(4).
- [ ] macOS/Linux 실제 지원 검증. 이번 사용자 요청에서 제외(5).

## 이전 단위의 로컬 검증 — 2026-10-07 SQLite/Electron 연결

소스 기준은 main `93f2179928bf735e7ba3570d6a0662dabe7863ad`와 이번 변경이다. 지정 Go 1.26.3 Windows amd64 배포물을 공식 SHA256과 대조했고 modernc 1.52.0·norma 0.4.3을 유지했다.

| 검사 | 실제 결과와 범위 |
| --- | --- |
| 전체 native Go | 최종 구현 동결 후 `go test -json -count=1 -timeout 180s ./...` 종료0. 18개 테스트 패키지, 최상위788개 통과·2개 skip·실패0. 하위 테스트 포함1,196개 통과. |
| 추가 archive/증거/연결 회귀 | 최종 archive 무결성 수정 후 `./db ./evidence ./internal/sqlitedb` 모두 통과. malformed manifest/JSON, 늦은 stream 행 수 오류 rollback, 2^53 초과 ID, 공유 자산 관계 병합 포함. |
| Go 정적 검사/빌드 | `go vet ./...` 통과. 실제 Windows embedui 빌드와 backend lifecycle smoke 통과. Linux amd64/macOS arm64는 교차 빌드만 통과, OS 실행 검증 아님. |
| Python SQL | Python 3.12.14/SQLite 3.53.1에서 6개 검사 스크립트 총87개 통과. 실제 modernc 검사와 별도. |
| web | TypeScript·한국어 텍스트·입력 정적 검사·unit8개·정적 export31페이지·추가 상태 UI Biome 통과. web/desktop npm audit 취약점0. |
| 실제 Electron Playwright | Electron 44.6.0/Playwright 1.63.0, 초기 탐색 오류 수정 후 2개 통과41.5초·실패0·skip0. 로컬 HTTP/모델 fixture, 업무 흐름·보관 복원·재시작·격리·단일 인스턴스·프록시 충돌 후 실패/재시도 포함. |
| 실제 Windows 실행 패키지 | 최신 main.cjs를 포함한 `app.isPackaged` 경로의 Playwright 1개 통과5.7초·실패0·skip0. setup/저장/종료/로그인·사용자 편집·실제 Go 종료·라이선스 확인. |
| 패키지 신규 홈 연속 부팅 | 서로 다른 새 데이터 홈 3회 모두 실제 setup 도달·종료0·HTTP 종료·Go PID 소멸. 두 번째 실행에서 실제 ERR_ABORTED(-3)를 다시 발생시켜 정상 이동 처리까지 확인. |
| 로컬 시각 증거 | 총103 PNG: Electron99, 시작 실패2, 패키지2. trace·임시 SQLite와 함께 로컬 검사 결과에만 보존하며 커밋/원격 업로드하지 않는다. |

전체 Go의 skip은 외부 비공개 모델 설정이 필요한 `TestLiveContextReview`와 이 Windows 계정의 symlink 생성 권한이 없는 `TestTaskArchivePackageSkipsSymlink`다. `report`는 테스트 파일이 없는 패키지다. PG 미설정으로 건너뛰는 검사는 없다.
Python 실행 파일은 `ARTEX_TEST_PYTHON`으로 실제 경로를 지정해 확인했다. Windows App Execution Alias만으로 도구 준비 성공을 인정하지 않는다.
race는 Windows C 컴파일러가 없어 미실행이다. Actions 실행/재실행·새 자동 트리거·원격 artifact 업로드·태그·배포는 하지 않았다.

최신 실행 파일 재검사에서 최초 HTTP 로드와 정적 UI의 root→작업→로그인/설정 이동이 겹치며 Electron이 종료되는 문제를 재현했다. 실제 같은 백엔드·동일 origin·메인 프레임의 후속 이동이 확인된 ERR_ABORTED만 정상 이동으로 처리했다. 다른 로드/초기화 오류는 실패로 남긴다. Playwright에는 프로세스 종료 코드·stdout/stderr·창 종료·탐색·마지막 상태 기록을 추가했고, 닫힌 창을 재시도 대기로 숨기지 않는다. 실패 실행의 trace와 임시 DB는 로컬에 보존했다.

업무 DB의 보관 복원과 증거 메타데이터는 같은 IMMEDIATE 트랜잭션에서 rollback한다. 별도 트래픽 저장소/본문 파일은 먼저 준비하므로 실패 시 재시도/GC용 자료가 남을 수 있다. 모든 저장소·파일을 아우르는 하나의 원자적 트랜잭션으로 표현하지 않는다.
go-mitmproxy의 public API에 없는 내부 attacker goroutine 종료는 Go 프로세스 종료에 의존한다. DNS 작업 종료 대기는 최대12초이며 Electron은 종료 대기8초 뒤 프로세스를 종료한다. 자손 도구의 강제 종료/OS 격리는 미검증이다.

다음 시작점은 **M4의 앱 전용 도구 런타임**이다. Windows 우선으로 도구의 버전·출처·해시·라이선스를 고정하고 실제 경로/실행·프로세스/네트워크 격리를 검증한 뒤 현재 미준비 상태와 실행 차단을 해제한다. 일관된 DB+증거 백업/복구와 설치·서명·Electron 업데이트 검증을 이어간다.

## 이전 로컬 검증 — M2.2i

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
