# syntax=docker/dockerfile:1

# 호스트에 Node.js나 Go를 설치하지 않아도 이 포크의 소스로 빌드합니다.
FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build:static

FROM --platform=$BUILDPLATFORM golang:1.26.3-bookworm AS backend
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/web/out ./server/webui/dist
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -tags embedui -ldflags="-s -w -X main.version=${VERSION}" -o /out/artex ./cmd/artex

# 실행 이미지: 기존 도구 구성을 유지합니다.
FROM python:3.12-slim-bookworm AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates ripgrep curl wget vim git jq unzip \
      dnsutils iputils-ping netcat-openbsd inetutils-telnet whois nmap \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*
# 브라우저와 시스템 의존성을 미리 설치하여 최초 실행 때 다운로드하지 않습니다.
RUN npm install -g @playwright/mcp@latest @playwright/cli@latest playwright@latest \
    && playwright-cli --help \
    && playwright install --with-deps chromium \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=backend /out/artex /app/artex
COPY start.sh /app/start.sh
RUN chmod +x /app/artex /app/start.sh
COPY skills/ /app/skills/
# 실행 데이터와 스킬은 Compose의 바인드 마운트로 보존합니다.
VOLUME ["/app/data"]
EXPOSE 8787 8788
ENTRYPOINT ["/app/start.sh"]
CMD ["-addr", ":8787", "-proxy", ":8788"]
