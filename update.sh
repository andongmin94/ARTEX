#!/usr/bin/env bash
# 원본 이미지를 내려받지 않고 현재 한국어 포크의 소스로 업데이트합니다.
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"
die(){ printf '[오류] %s\n' "$*" >&2; exit 1; }
echo '업데이트 전 DB, data/, skills/, 설정 파일을 백업하세요.'
echo '실행 중인 작업을 중지한 뒤 업데이트하세요. DB 구조는 되돌리지 않습니다.'
read -rp '현재 브랜치의 최신 코드를 가져올까요? (y/n) [y]: ' pull
if [ "${pull:-y}" = y ]; then
  git pull --ff-only || die '코드 동기화 실패. 로컬 변경이나 분기 상태를 해결한 뒤 다시 실행하세요.'
fi
echo '  1) Docker 소스 빌드 및 재시작'
echo '  2) 로컬 소스 빌드'
read -rp '업데이트 방식 [1]: ' mode
case "${mode:-1}" in
  1)
    docker compose version >/dev/null 2>&1 || die 'Docker Compose를 설치하세요.'
    [ -f .env ] || die '먼저 install.sh로 설치하거나 .env를 설정하세요.'
    docker compose up -d --build artex
    echo '한국어판 업데이트 완료: http://localhost:8787'
    ;;
  2)
    command -v go >/dev/null 2>&1 || die 'Go를 설치하세요.'
    command -v npm >/dev/null 2>&1 || die 'Node.js 22와 npm을 설치하세요.'
    (cd web && npm ci && npm run build:static)
    mkdir -p server/webui/dist
    cp -R web/out/. server/webui/dist/
    CGO_ENABLED=0 go build -tags embedui -trimpath -o artex ./cmd/artex
    echo '빌드 완료. 실행 중인 ARTEX를 종료하고 start.sh로 다시 시작하세요.'
    ;;
  *) die '1 또는 2를 선택하세요.' ;;
esac
