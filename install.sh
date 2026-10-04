#!/usr/bin/env bash
# 한국어 포크 설치: 로컬 소스 Docker 빌드 또는 프런트엔드를 포함한 직접 빌드.
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"
umask 077
info(){ printf '[*] %s\n' "$*"; }
die(){ printf '[오류] %s\n' "$*" >&2; exit 1; }
ask(){ local value; read -rp "$1 [$2]: " value; printf '%s' "${value:-$2}"; }
require(){ command -v "$1" >/dev/null 2>&1 || die "$1 설치 후 다시 실행하세요."; }
ensure_docker(){ require docker; docker compose version >/dev/null 2>&1 || die 'Docker Compose를 설치하세요.'; }
build_local(){
  require go; require npm
  (cd web && npm ci && npm run build:static)
  mkdir -p server/webui/dist
  cp -R web/out/. server/webui/dist/
  CGO_ENABLED=0 go build -tags embedui -trimpath -o artex ./cmd/artex
}
echo 'ARTEX 한국어판 설치'
echo '  1) Docker로 한국어 소스 빌드 및 실행'
echo '  2) 로컬 빌드 및 실행(Node.js 22, Go, PostgreSQL 필요)'
case "$(ask '설치 방식' 1)" in
  1)
    ensure_docker
    if [ ! -f .env ]; then
      password="$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"
      printf 'POSTGRES_PASSWORD=%s\nANTHROPIC_API_KEY=\nOPENAI_API_KEY=\n' "$password" > .env
      info '로컬 테스트용 DB 비밀번호를 .env에 생성했습니다. 모델 Key는 웹 화면에서 설정하세요.'
    fi
    info '이 포크의 소스로 한국어 이미지를 빌드합니다.'
    docker compose up -d --build
    info '접속: http://localhost:8787 / 로그: docker compose logs -f artex'
    ;;
  2)
    require go; require npm; require node
    if [ ! -f config.json ]; then
      export DB_HOST DB_PORT DB_USER DB_PASS DB_NAME DB_SSL
      DB_HOST="$(ask 'PostgreSQL 주소' 127.0.0.1)"
      DB_PORT="$(ask '포트' 5432)"
      DB_USER="$(ask '계정' artex)"
      read -rsp 'PostgreSQL 비밀번호: ' DB_PASS; printf '\n'
      DB_NAME="$(ask '데이터베이스 이름' artex)"
      DB_SSL="$(ask 'SSL 모드(disable/require)' disable)"
      node - <<'JS'
const fs = require('node:fs');
const env = process.env;
const port = Number(env.DB_PORT);
if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('포트가 유효하지 않습니다');
fs.writeFileSync('config.json', JSON.stringify({database:{host:env.DB_HOST,port,user:env.DB_USER,password:env.DB_PASS,dbname:env.DB_NAME,sslmode:env.DB_SSL}}, null, 2)+'\n', {mode:0o600});
JS
      unset DB_PASS
    fi
    info '프런트엔드와 백엔드를 함께 빌드합니다.'
    build_local
    info '실행합니다. 종료하려면 Ctrl+C를 누르세요.'
    ./start.sh -addr 127.0.0.1:8787 -proxy 127.0.0.1:8788
    ;;
  *) die '1 또는 2를 선택하세요.' ;;
esac
