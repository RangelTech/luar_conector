# Hub do conector + binarios do conector para OTA e instalacao (/dl). Spec 11 §26.
FROM golang:1.22-alpine AS build
ARG VERSION=dev
RUN apk add --no-cache python3
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/hub ./cmd/hub
RUN set -e; mkdir -p /out/releases; \
    for t in linux-amd64 linux-arm64 windows-amd64; do \
      os=${t%-*}; arch=${t#*-}; ext=""; [ "$os" = windows ] && ext=".exe"; \
      CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.Version=${VERSION}" -o /out/releases/luar-conector-$t$ext ./cmd/connector; \
    done; \
    cp scripts/install.sh scripts/luar-relay.py /out/releases/; \
    cd /out/releases && python3 /src/scripts/mklatest.py "${VERSION}"

FROM alpine:3.20
RUN adduser -D -u 10001 hub && mkdir -p /data && chown hub /data
COPY --from=build /out/hub /usr/local/bin/hub
COPY --from=build /out/releases /app/releases
USER hub
ENV HUB_DATA=/data HUB_RELEASES=/app/releases HUB_ADDR=:8080 HUB_RELAY_ADDR=:2222
EXPOSE 8080 2222
ENTRYPOINT ["/usr/local/bin/hub"]
