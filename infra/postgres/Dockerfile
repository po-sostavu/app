FROM golang:1.27.3-alpine3.24@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS gosu-build

ENV CGO_ENABLED=0 GOTOOLCHAIN=local
ADD --checksum=sha256:207a2045e890d3ca3326027c5f585733085960b401a49dbc00e3bb628c054cf8 \
    https://github.com/tianon/gosu/archive/refs/tags/1.20.tar.gz /tmp/gosu.tar.gz
WORKDIR /src
RUN tar -xzf /tmp/gosu.tar.gz --strip-components=1
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod edit -require=golang.org/x/sys@v0.44.0 && \
    go mod tidy && \
    go build -mod=readonly -trimpath -ldflags="-s -w -X main.Version=1.20" -o /out/gosu .

FROM postgres:18.6-alpine3.24@sha256:123b17998ccaf47e53b502e8c6a1e79afd8f579c6f3eec83c563da026f957395 AS runtime

RUN apk add --no-cache 'zlib>=1.4-r0'
COPY --from=gosu-build /out/gosu /usr/local/bin/gosu
RUN gosu nobody true
