# syntax=docker/dockerfile:1
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/hello-guest ./cmd/hello-guest

FROM build AS bin-build
ARG GOOS
ARG GOARCH
ARG VERSION=dev
RUN mkdir -p /out && CGO_ENABLED=0 GOOS=$GOOS GOARCH=$GOARCH go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/hello-guest ./cmd/hello-guest

FROM scratch AS bin
COPY --from=bin-build /out/hello-guest /

FROM build AS release-build
ARG VERSION=dev
RUN <<EOF
set -e
mkdir -p /out
cd /src
for t in "linux amd64 linux-amd64" "linux arm64 linux-arm64" "darwin amd64 macos-amd64" "darwin arm64 macos-arm64" "windows amd64 windows-amd64.exe"; do
  set -- $t
  CGO_ENABLED=0 GOOS=$1 GOARCH=$2 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o "/out/hello-guest-$3" ./cmd/hello-guest
done
cd /out && sha256sum hello-guest-* > SHA256SUMS
EOF

FROM scratch AS release
COPY --from=release-build /out /

FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/hello-guest /usr/local/bin/hello-guest
EXPOSE 80
VOLUME /app/data
ENTRYPOINT ["hello-guest", "--data", "/app/data"]
