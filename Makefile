VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS = -s -w -X main.version=$(VERSION)
OUT = dist

.PHONY: all build test docker clean release

all: build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/hello-guest ./cmd/hello-guest

test:
	go test ./...

docker:
	docker build -t hello-guest:$(VERSION) .

release:
	mkdir -p $(OUT)
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(OUT)/hello-guest-linux-amd64      ./cmd/hello-guest
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(OUT)/hello-guest-linux-arm64      ./cmd/hello-guest
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(OUT)/hello-guest-macos-arm64      ./cmd/hello-guest
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(OUT)/hello-guest-macos-amd64      ./cmd/hello-guest
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(OUT)/hello-guest-windows-amd64.exe ./cmd/hello-guest
	cd $(OUT) && shasum -a 256 hello-guest-* > SHA256SUMS

clean:
	rm -rf bin $(OUT)
