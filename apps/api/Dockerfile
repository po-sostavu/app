FROM golang:1.27.3-alpine3.24@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS build

WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api && \
    go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

FROM alpine:3.24.4@sha256:3f4d6b32e26703c6082839234f43769d5046b3ac7c910033911d1e71d8cd630a AS runtime

RUN apk add --no-cache ca-certificates 'zlib>=1.4-r0' && \
    addgroup -g 10001 app && \
    adduser -D -H -u 10001 -G app app
USER 10001:10001
WORKDIR /app

FROM runtime AS migrate

COPY --from=build /out/migrate /usr/local/bin/migrate
COPY migrations /migrations
ENTRYPOINT ["/usr/local/bin/migrate", "-path", "/migrations"]
CMD ["up"]

FROM runtime AS api

COPY --from=build /out/api /usr/local/bin/api
ENV HTTP_ADDR=:8081
EXPOSE 8081
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -T 2 -O /dev/null http://127.0.0.1:8081/livez || exit 1
ENTRYPOINT ["/usr/local/bin/api"]
