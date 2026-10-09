# UI 설계 기준 — neobrutal-ui

결정일: 2026-10-05. 사용자 지정 기준이며 선택적 아이디어가 아니다.
진행 상태와 다음 작업은 `development-plan.md`에서만 관리한다.
이 문서를 추가한 시점에 ARTEX 화면이 이미 바뀐 것으로 해석하지 않는다.

## 원본과 검토 범위

- 저장소: https://github.com/andongmin94/neobrutal-ui
- 최초 검토 ref: `main`, SHA `b4da2463fe710a77bf464c65125a1a7f40424722`.
- `README.md`: Base UI + Tailwind v4, 소스 소유 방식의 shadcn registry, 기본 Mono 테마 및 템플릿 사용 범위.
- `registry/src/data/theme.ts`: 색상 역할, 섀도/모서리/글자 굵기 및 눌림 거리 토큰.
- `registry/src/data/colors.ts`: Mono·Mono Warm과 17개 컬러 프리셋의 라이트/다크 배경·액션·차트 팔레트.
- `registry/src/components/ui/button.tsx`, `button-variants.ts`: 실제 Base UI 컨트롤과 hover/active/disabled/focus/motion-reduce 동작.
- 전체 화면 구성은 원본의 Dashboard/CMS 데모와 `registry/src/blocks`를 추가 확인한다. 이번 검토에서는 데모를 브라우저로 실행하거나 시각 검수하지 않았다.

UI 구현을 시작할 때 원본 최신 ref와 위 기준의 차이를 확인한다.
채택한 버전과 파일을 이 문서에 갱신하되, 빌드/실행 때 원격 main의 CSS나 JS를 자동 주입하지 않는다.
설치형 ARTEX에 필요한 소스만 포함한다. 원본 문서 사이트, 예제 데이터, 템플릿의 가짜 저장 로직을 제품 기능으로 가져오지 않는다.
원본 코드를 가져오는 단계에서 MIT 저작권/허가문을 보존하고 ARTEX의 기존 라이선스·저작자 표기도 유지한다.

## 채택할 시각·상호작용 규칙

원본 토큰의 출발점은 **Mono 색상 테마, 2px 컨트롤 테두리, 4px/4px 무블러 섀도, 5px 기본 모서리, 본문 500/제목 700**이다.
2026-10-09 사용자 요청으로 기본 Mono를 포함한 원본의 19개 프리셋을 화면 설정에 제공한다. 배경용 액션색과 링크·코드 강조의 텍스트색을 구분해 밝은 Yellow/Amber/Lime에서도 읽을 수 있게 한다.
글꼴은 사용자 요청에 따라 **Pretendard Variable 하나**를 로컬 제공한다. Mono는 색상 테마 이름이며 고정폭 글꼴을 뜻하지 않는다.
버튼 원본은 140ms 전환으로 hover 때 섀도를 줄이고 active 때 눌림 위치로 이동한다.
이를 공통 토큰/컴포넌트에서 정의하고 화면마다 다른 임의 값으로 흉내 내지 않는다.

ARTEX 적용 시에는 다음을 함께 지킨다.

- 배경/본문은 차분하게 유지하고 강한 색은 주요 액션·상태·심각도에 의미 있게 사용한다. 색만으로 상태를 표현하지 않는다.
- 카드, 폼, 탭, 표 도구막대, 사이드바의 간격/정렬/선 두께/선택 상태를 통일한다. 표의 모든 셀과 중첩 컨테이너에 굵은 테두리·섀도를 중복 적용하지 않는다.
- 표/로그/요청·응답/코드처럼 밀도가 높은 영역도 Pretendard를 사용하며 행 높이, 구분선, 들여쓰기와 스크롤을 설계한다. 디자인 때문에 정보나 기능을 삭제하지 않는다.
- 한글/영문/숫자는 동일한 역할의 글꼴 크기·굵기를 사용한다. 한국어 글리프가 포함된 Pretendard를 로컬 제공하고 실제 로딩과 굵기를 확인한다. 글꼴 선택 설정은 제공하지 않는다.
- 클릭 가능한 사이드바·탭·정렬·접기 항목에는 알맞은 커서와 hover/pressed/focus를 제공한다. 비활성 컨트롤과 단순 정보 카드는 눌리는 것처럼 표현하지 않는다.
- hover 이동 때문에 인접 컨트롤·팝오버가 겹치거나 표의 열 정렬이 변하지 않게 한다. `prefers-reduced-motion`과 키보드 포커스를 유지한다.
- 라이트/다크에서 입력, 오류, 메뉴, 다이얼로그, 비활성 상태까지 검수한다. 기존 muted 등의 의미가 원본 토큰과 다를 수 있으므로 전역 CSS만 덮어쓰지 않는다.

## 현재 ARTEX 코드와의 접점

`web/src/components/ui/button.tsx`는 현재 native button + Radix Slot의 `asChild` API를 사용한다.
참고 원본의 Button은 Base UI `render`/상태 props 방식이므로 **파일을 무작정 overwrite하면 안 된다.**
원본을 채택하는 컴포넌트는 기존 호출부, 링크 조합, ref, disabled, 폼 제출과 포커스를 함께 검토하고 한 단위로 교체한다.
구 API를 위해 새 호환 래퍼를 추가하지 않는다. 단순 스타일 변경으로 충분한 컴포넌트까지 근거 없이 primitive를 전면 교체하지 않는다.
이미 ARTEX에 있는 Base UI/React/Tailwind/CVA 등 의존성을 먼저 사용하고, 데모 사이트의 개발 의존성을 통째로 추가하지 않는다.

순서는 `토큰/타이포그래피 → 공통 컨트롤/오버레이 → 앱 셸/탐색 → 실제 업무 화면`이다.
UI 단위마다 기존 API를 호출하는 사용 경로를 유지한다. 샘플 화면만 만들어 실제 페이지와 분리하지 않는다.
DB 이식과 스타일 교체는 별도 검증 단위로 유지해 오류 원인을 구분한다.

## 반드시 적용·검수할 범위

| 영역 | 실제 검수 항목 |
| --- | --- |
| 진입/앱 상태 | Electron 자동 진입, Go 단독 브라우저 setup/로그인, 백엔드·인증 시작/실패/재시도, 도구 준비·누락, 업데이트/종료 안내 |
| 공통 셸 | 사이드바/접기/선택, 상단, 탭, 검색, 필터, 빈 상태, 로딩, 오류, toast |
| 업무 화면 | 대시보드, 대화, 작업 목록·상세·생성·보관, 자산·범위·동기화 |
| 증거 화면 | 취약점·재검증·보고서, 트래픽 목록/본문, 도구 실행, LLM 기록, 작업 공간 |
| 설정/오버레이 | 모델·에이전트·MCP·스킬·도구·알림·승인/차단, dialog/sheet/popover/menu/tooltip |

위 표는 UI 검수 범위이지 별도 체크리스트나 진행표가 아니다.
실제 라우트를 조사해 각 항목을 대응시키고, 동작하는 페이지와 동일 상태의 스크린샷을 함께 남긴다.

## 완료 조건

최소 1280px·1440px 창, Windows 125%·150% 배율, 라이트/다크에서 잘림·겹침·불필요한 가로 스크롤을 검사한다.
키보드만으로 탐색·다이얼로그 열기/닫기·폼 오류 복구가 가능해야 한다.
한글 IME 조합 중 입력/Enter 제출, 긴 한국어 라벨, 긴 URL·코드, 빈/로딩/오류 상태를 검사한다.
스크린샷은 컴포넌트 쇼케이스가 아니라 실제 앱 진입 → 설정 → 작업 → 증거 → 재실행 흐름에서 얻는다. Electron의 진입은 비밀번호 없는 앱 세션 인증이며 Go 단독 브라우저 접속은 setup/로그인을 검사한다.
원본 느낌과 공통 규칙을 비교해 확인하고, 빌드/타입 검사 성공만으로 시각 품질을 완료 처리하지 않는다.

## 2026-10-07 적용 근거

원본 최신 main을 재확인했고 SHA b4da2463fe710a77bf464c65125a1a7f40424722가 동일했다.
Mono 라이트/다크 역할 색상, 2px 테두리·4px 하드 섀도·5px 모서리·140ms 눌림을 공통 CSS와 실제 UI 컨트롤에 적용했다.
기존 Radix/native primitive의 API·포커스·폼·오버레이 동작은 유지하며 원본 Button variants의 시각 규칙을 적용했다.
외부 런타임 CSS/JS는 주입하지 않는다. 불필요한 shadcn CLI는 제거하고 필요한 MIT CSS만 소스와 저작권을 보존한다.

2026-10-07 사용자 요청으로 웹 UI와 Electron 시작·실패 화면의 글꼴을 로컬 Pretendard Variable 하나로 통일한다. 코드·로그의 별도 서체와 글꼴 선택 설정도 제거한다.
원본 파일은 `web/src/lib/fonts/files/PretendardVariable.woff2`이며 `OFL-Pretendard.txt`의 저작권과 SIL Open Font License를 앱에 포함한다. 실행 중 원격 글꼴을 요청하지 않는다.

글꼴 출처는 [orioncactus/pretendard 공식 v1.3.9](https://github.com/orioncactus/pretendard/releases/tag/v1.3.9), 확인 ref는 `5c41199ea0024a9e0b2cb31735265056e5472d76`이다. 원본 경로는 `packages/pretendard/dist/web/variable/woff2/PretendardVariable.woff2`이며 파일을 변형하지 않고 포함한다. 원저자 Kil Hyung-jin, Reserved Font Name Pretendard와 SIL Open Font License 1.1 전문을 보존한다.

| 포함 파일 | 크기 | 확인한 SHA256 |
| --- | --- | --- |
| `PretendardVariable.woff2` | 2,057,688바이트 | `9599f12fd42fc0bce1cd50b47a0c022e108d7aa64dd0d1bb0ed44f3282d900b4` |
| `OFL-Pretendard.txt` | 4,418바이트 | `d31ddd9f2bed32fd7e302a205cf2380ba0de6529152d239ef99cfb6f261bfc04` |

한국어 참조 token·IME 입력, 사이드바 선택 경로·aria-current, 실제 설정의 토스트 테마도 함께 연결했다.
Playwright는 실제 Electron/Go에서 주요 21개 화면을 라이트/다크·1280/1440으로 열고 캡처한다.
125/150% Electron zoom 검사는 실제 Windows 디스플레이 배율 검사와 다르다. DOM composition 검사도 OS 네이티브 한글 IME의 완전한 대체가 아니다.
최종 실행 결과·업무 fixture 검증과 미실행 범위는 development-plan.md만 현재 목록으로 관리한다.

## 2026-10-09 프리셋과 석탄색 개선

최신 원본 main의 SHA는 `b4da2463fe710a77bf464c65125a1a7f40424722`로 동일하다. `registry/src/data/colors.ts`의 19개 프리셋을 로컬 CSS와 선택 목록에 포함한다. Mono, Mono Warm, Red, Orange, Amber, Yellow, Lime, Green, Emerald, Teal, Cyan, Sky, Blue, Indigo, Violet, Purple, Fuchsia, Pink, Rose를 제공하며 기존 쿠키·부트·화면 상태 경로로 선택을 저장한다.

사용자 요청에 따라 Mono 다크의 배경을 원본 `#18191c`에서 `#27282b`로 밝힌다. 카드 `#303239`, 사이드바 `#2b2d32`, 그림자 `#17191d`로 표면을 구분한다. Mono Warm도 동일한 석탄색 계층을 따뜻한 중립색에 적용한다. 이는 원본값 복구가 아니라 ARTEX에서 밝기를 개선한 부분이다.

컬러 프리셋의 액션·배경·차트 팔레트는 원본을 사용하되 카드·표·로그의 보조색은 업무 화면의 역할에 맞춘다. 링크·HTTP 코드·강조 텍스트에는 `primary-text`와 전용 syntax 색상을 사용해 밝은 액션/차트색이 글자의 대비를 낮추지 않게 한다. 다크 Fuchsia의 액션 글자는 흰색으로 구분한다. 공식 ChatGPT 연결 버튼의 지정 색상·로고는 유지한다. Pretendard Variable과 기존 눌림·포커스·축소 모션은 그대로 사용한다.

## ChatGPT 구독 연결 버튼 자산

OpenAI의 [Sign in with ChatGPT UI 지침](https://developers.openai.com/siwc/token-sharing-open-source/ui-ux-guidelines)에 따라 `Continue with ChatGPT` 버튼과 최초 연결 안내를 제공한다.
OpenAI의 공식 로고 두 개를 변형 없이 로컬 파일로 포함한다. 이 자산을 neobrutal-ui의 MIT 자산으로 재표기하지 않는다.

- `web/public/icons/chatgpt-logo-white.svg`: [OpenAI 공식 white 원본](https://developers.openai.com/assets/siwc/sign-in-buttons/chatgpt-logo-white.svg).
- `web/public/icons/chatgpt-logo-black.svg`: [OpenAI 공식 black 원본](https://developers.openai.com/assets/siwc/sign-in-buttons/chatgpt-logo-black.svg).

라이트 화면의 검정 버튼에는 white, 다크 화면의 흰 버튼에는 black을 사용한다. 연결·동의·오류·모델 선택 화면은 기존 한국어·Pretendard와 공통 컨트롤을 사용한다.
