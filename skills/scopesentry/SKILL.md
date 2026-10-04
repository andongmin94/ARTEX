---
name: scopesentry-mcp
description: 이미 배포된 ScopeSentry에 MCP로 연결하여 프로젝트·작업·템플릿·자산·노드를 관리합니다. ScopeSentry 연결, API Key 설정, 승인된 스캔 작업 또는 자산 조회 요청에 사용합니다.
---

# ScopeSentry MCP 사용 안내

이미 배포된 ScopeSentry 인스턴스용 안내입니다. 로컬 소스 없이 Cursor 또는 다른 MCP 클라이언트에서 연결할 수 있습니다. 도구 이름과 매개변수는 서버가 제공하는 실제 스키마를 따르며 설명과 결과 요약은 한국어로 작성하세요. 사용자에게 승인받은 대상과 작업 범위를 벗어나지 마세요.

## 연결 준비

기본 웹 주소는 `http://<host>`, MCP는 `http://<host>/mcp`입니다. 역방향 프록시가 있으면 실제 배포 주소를 사용하세요.

브라우저에서 로그인한 뒤 **API Key 관리**에서 키를 생성하거나 관리자가 제공한 키를 받습니다. `ssk_...` 값은 생성 시 한 번만 표시되므로 안전하게 보관하고 로그나 보고서에 노출하지 마세요.

Cursor의 Settings → MCP에서 서버를 추가합니다.

```json
{
  "mcpServers": {
    "scopesentry": {
      "url": "http://<host>:8082/mcp",
      "headers": {"X-API-Key": "ssk_..."}
    }
  }
}
```

서버가 지원하면 `Authorization: Bearer ssk_...`도 사용할 수 있습니다. 저장 후 클라이언트를 다시 연결하여 `list_projects`, `list_assets` 등의 도구가 표시되는지 확인하세요.

## 도구

| 도구 | 용도 |
| --- | --- |
| list_projects | 태그로 묶은 프로젝트 트리와 프로젝트 ID |
| list_projects_data | 이름 검색과 페이지 조회를 지원하는 프로젝트 목록 |
| get_project / create_project | 프로젝트 상세 / 생성 |
| list_tasks / get_task | 작업 목록 / 상세 |
| list_scan_templates / get_scan_template | 스캔 템플릿 목록 / 상세 |
| list_plugin_modules | 스캔 파이프라인 모듈 이름 |
| list_plugins | 플러그인 hash와 기본 매개변수 |
| create_scan_template / create_scan_task | 템플릿 / 스캔 작업 생성 |
| list_assets / count_assets | 유형별 자산 페이지 조회 / 전체 개수 |
| get_asset_detail | 자산 또는 취약점 상세 |
| add_asset_tag | 자산 태그 추가 |
| list_nodes | 사용 가능한 실행 노드 |

`list_assets`와 `count_assets`의 search/filter 문법은 같습니다. 전체 개수만 필요하면 모든 페이지를 읽지 말고 `count_assets`를 사용하세요.

## 프로젝트별 자산 조회

프로젝트 조건이 있으면 `list_projects` 또는 `list_projects_data`에서 **ObjectID**를 구한 뒤 `filter.project`에 넣습니다. 표시 이름을 ID 대신 전달하지 마세요. 사용자가 프로젝트를 지정하지 않았다면 임의로 제한할 필요는 없습니다.

```json
{
  "asset_type": "asset",
  "search": "domain=^example.com",
  "filter": {"project": ["<projectObjectID>"]},
  "pageIndex": 1,
  "pageSize": 20
}
```

프로젝트 필터를 지원하지 않는 자산 유형도 있으므로 아래 표 또는 실제 도구 스키마를 확인하세요.

## 승인된 스캔 작업 생성

먼저 `list_nodes`에서 온라인 **노드 이름**을 확인하고 `list_scan_templates`에서 **템플릿 ObjectID**를 구하거나 템플릿을 생성합니다. `create_scan_task`에는 name, node가 필수이며 template에 표시 이름이 아닌 ObjectID를 사용합니다.

| targetSource | 대상 출처 | 필요한 매개변수 |
| --- | --- | --- |
| general | 직접 지정 | target |
| project | 프로젝트 자산 | project(ObjectID 배열) |
| asset | 웹 자산 검색 | search, 선택: project/filter/targetNumber |
| RootDomain | 루트 도메인 검색 | search, 선택: project/filter/targetNumber |
| subdomain | 서브도메인 검색 | search, 선택: project/filter/targetNumber |
| UrlScan | URL 결과 검색 | search, 선택: project/filter/targetNumber |
| *Source | 자산 화면의 선택/검색 | targetTp=search이면 search, targetTp=select이면 targetIds |

직접 대상 지정 예:

```json
{
  "name": "승인 대상 서브도메인 조사",
  "node": ["node-1"],
  "template": "<templateObjectID>",
  "targetSource": "general",
  "target": "example.com\nfoo.example.com",
  "project": ["<projectObjectID>"]
}
```

이전 작업 결과를 대상으로 후속 작업을 생성하는 예:

```json
{
  "name": "승인 대상 서비스 점검",
  "node": ["node-1"],
  "template": "<followupTemplateObjectID>",
  "targetSource": "subdomain",
  "search": "task==\"이전 작업 이름\"",
  "project": ["<projectObjectID>"]
}
```

### 루트 도메인 전체 조사: 두 단계

분산 작업은 대상 단위로 배정됩니다. 루트 도메인 하나에 모든 단계를 적용하면 그 도메인을 배정받은 노드가 발견한 서브도메인의 후속 작업까지 처리하여 부하가 쏠릴 수 있습니다.

첫 단계는 `targetSource:general`과 승인된 루트 도메인 목록으로 서브도메인 조사 템플릿을 실행합니다. 원본 구성의 `SubdomainScan`, `SubdomainSecurity` 기능을 사용하는 경우에도 승인 범위를 확인하세요. `get_task`로 완료 여부를 확인합니다.

두 번째 단계는 `targetSource:subdomain`, `search:task=="첫 단계 작업 이름"`으로 결과를 선택합니다. 필요한 프로젝트 필터와 승인된 포트·지문·웹·취약점 조사 템플릿을 지정합니다. 서브도메인마다 개별 대상이 되어 여러 노드에 분산됩니다. 웹 화면에서도 해당 작업의 서브도메인을 선택하여 후속 작업을 만들 수 있습니다.

### 템플릿 생성

`list_plugin_modules`로 모듈 이름을 확인하고 `list_plugins`에서 모듈별 플러그인 hash와 기본 매개변수를 읽습니다. `create_scan_template`의 modules에 **모듈 이름 → 플러그인 hash 배열**을 지정하세요. 이름이나 hash를 추측하지 마세요.

## 자산 검색과 개수

`count_assets`는 같은 asset_type, search, filter를 사용하여 `{ "total": N }`을 반환합니다.

```json
{
  "asset_type": "subdomain",
  "search": "domain==www.example.com",
  "filter": {"project": ["<projectObjectID>"]}
}
```

자산 유형: `asset`, `RootDomain`, `subdomain`, `app`, `mp`, `UrlScan`, `SensitiveResult`, `DirScanResult`, `crawler`, `vulnerability`, `PageMonitoring`, `IPAsset`, `SubdomainTakerResult`.

서버가 제공하는 별칭 예: web→asset, vuln→vulnerability, ip→IPAsset, url→UrlScan. 페이지는 pageIndex/pageSize이며 기본 1/20입니다. sort는 UrlScan/DirScanResult에서 length 정렬에 사용합니다. sid는 SensitiveResult의 규칙 이름입니다.

### search 표현식

이것은 SQL이 아닌 전용 검색식입니다.

| 연산자 | 의미 | 예 |
| --- | --- | --- |
| = | 정규식 기반 부분 일치 | domain=example |
| == | 정확히 일치 | port==443 |
| != | 제외 | port!="80" |
| && | AND | domain==example.com && port==443 |
| \|\| | OR | title=admin \|\| body=login |

알려진 값은 `==`, 접두사는 `^`를 사용하세요. 일반적인 `=` 정규식 부분 검색은 큰 데이터 집합에서 느릴 수 있으므로 지원되는 프로젝트 필터와 함께 사용합니다. 모든 검색식이 인덱스를 사용한다고 가정하지 마세요.

**project는 search 안에 넣지 않고 filter.project로 지정합니다.** 공통 검색 필드는 tag, task(작업 이름), rootDomain입니다.

| 유형 | 주요 검색 필드 |
| --- | --- |
| asset | domain, ip, port, service, app, title, statuscode, icon, banner, type, response body, header |
| RootDomain | domain, icp, company |
| subdomain | domain, icp, type, value |
| app / mp | name, icp, company, category, description, url, apk |
| UrlScan | url, input, source, resultId |
| SensitiveResult | url, sname, body, info, md5 |
| DirScanResult | url, statuscode, redirect, length |
| vulnerability | url, vulname, matched, request, response, level |
| crawler | url, method, body, header |
| PageMonitoring | url, hash, diff, response |
| IPAsset | ip, domain, port, service, webServer, app |
| SubdomainTakerResult | domain, value, type, response |

정확한 필드 지원 여부는 연결된 서버의 도구 설명을 따릅니다.

```text
domain==www.example.com && port==443
domain=^example.com
ip==192.0.2.1
task=="이전 작업 이름"
level==high
statuscode==200
```

### filter: 정확한 값으로 제한

같은 키의 여러 값은 OR, 다른 키끼리는 AND입니다. project는 ObjectID 배열이고 task는 작업 이름입니다. port/service/app/icon/statuscode/status/level/type/color/sname/tags 등의 지원 여부는 유형별로 다릅니다.

| 유형 | 지원하는 filter 키 |
| --- | --- |
| asset | project, port, service, app, icon, statuscode, type, task, tags |
| RootDomain | project, tags |
| subdomain | project, type, task, tags |
| app / mp | project, tags |
| UrlScan / DirScanResult | status, tags |
| SensitiveResult | status, color, sname, tags |
| crawler | project, task, tags |
| vulnerability | project, level, status, task, tags |
| PageMonitoring / SubdomainTakerResult | tags |
| IPAsset | project, port, service, app, tags |

```json
{"project": ["<projectObjectID>"], "port": ["443"]}
```

UrlScan 상태 코드는 filter.status를 사용하고 DirScanResult는 search의 statuscode도 사용할 수 있습니다. SensitiveResult의 규칙 이름은 search의 sname 또는 filter.sname으로 지정합니다. 정렬 예는 `{"length":"ascending"}`이며 UrlScan과 DirScanResult 외 유형은 이를 무시하고 기본 시각순을 사용할 수 있습니다.

## 파이프라인 모듈 이름

`TargetHandler`, `SubdomainScan`, `SubdomainSecurity`, `PortScanPreparation`, `PortScan`, `PortFingerprint`, `AssetMapping`, `AssetHandle`, `URLScan`, `WebCrawler`, `URLSecurity`, `DirScan`, `VulnerabilityScan`, `PassiveScan`.

## 문제 해결

| 현상 | 확인할 항목 |
| --- | --- |
| MCP 도구가 표시되지 않음 | URL, API Key, 서버 실행 상태 |
| 401 / 403 | 키를 새로 발급하거나 올바른 키로 변경 |
| 자산이 검색되지 않음 | filter.project의 ObjectID 확인, search 안의 project 제거 |
| 작업·템플릿 생성 실패 | template에 ObjectID 사용, node에 온라인 노드 이름 사용 |
| 검색이 느림 | 지원되는 프로젝트 필터 추가, 정확한 값은 ==, 접두사는 ^ 사용, pageSize 축소 |

인증 실패를 키 추측이나 반복 로그인으로 해결하지 말고 사용자 또는 관리자에게 올바른 연결 설정을 요청하세요.
