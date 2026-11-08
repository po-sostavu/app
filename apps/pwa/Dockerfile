FROM golang:1.27.3-alpine3.24@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS caddy-build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOMAXPROCS=2
COPY infra/caddy/go.mod infra/caddy/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY infra/caddy/main.go ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -p=2 -mod=readonly -trimpath -ldflags="-s -w" -o /out/caddy .

FROM node:24.19.0-alpine AS dependencies
WORKDIR /app/apps/pwa
RUN npm install --global npm@11.6.2
COPY apps/pwa/package.json apps/pwa/package-lock.json apps/pwa/.npmrc ./
RUN npm ci --no-fund

FROM dependencies AS build
COPY .prettierrc.json .prettierignore /app/
COPY apps/pwa/ ./
ARG VITE_API_BASE_URL=/api
ENV VITE_API_BASE_URL=${VITE_API_BASE_URL}
RUN npm run build

FROM caddy:2.11.9-alpine@sha256:0bbb055bda6793c999bde78b1b5ce3e89f829fb031af2cf50056ea1246559fee AS runtime
COPY --from=caddy-build /out/caddy /usr/bin/caddy
RUN apk add --no-cache 'zlib>=1.4-r0' \
    && addgroup -S -g 10001 pwa \
    && adduser -S -u 10001 -G pwa pwa \
    && chown -R pwa:pwa /data /config
COPY infra/Caddyfile /etc/caddy/Caddyfile
COPY --from=build /app/apps/pwa/dist /srv
USER pwa
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
CMD ["caddy", "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"]
