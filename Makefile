VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS = -s -w -X main.version=$(VERSION)
OUT = dist
HOSTOS = $(shell uname -s | tr '[:upper:]' '[:lower:]')
HOSTARCH = $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')

.PHONY: all build build-docker test docker-image release-docker clean

all: build

build:
	mkdir -p $(OUT)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(OUT)/hello-guest ./cmd/hello-guest

build-docker:
	mkdir -p $(OUT)
	docker build --target bin --build-arg GOOS=$(HOSTOS) --build-arg GOARCH=$(HOSTARCH) --build-arg VERSION=$(VERSION) -o $(OUT) .

test:
	docker run --rm -v $(CURDIR):/src -v gomodcache:/go/pkg/mod -v gobuildcache:/tmp/gocache -w /src -e GOCACHE=/tmp/gocache golang:1.22-alpine go test ./...

docker-image:
	docker build -t hello-guest:$(VERSION) .

release-docker:
	rm -rf $(OUT)
	mkdir -p $(OUT)
	docker build --target release --build-arg VERSION=$(VERSION) -o $(OUT) .

clean:
	rm -rf $(OUT)
