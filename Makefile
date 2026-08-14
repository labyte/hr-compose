BINARY  := hr-compose
GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: all build test test-race test-cover vet lint fmt install cross clean

all: build

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

test-cover:
	$(GO) test -cover ./...

vet:
	$(GO) vet ./...

lint:
	golangci-lint run

fmt:
	gofmt -w .

install:
	$(GO) install -ldflags "$(LDFLAGS)" .

# 多平台交叉编译：linux/amd64、linux/arm64 为目标机，darwin/arm64 为本地调试
cross:
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-amd64 .
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-arm64 .
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-arm64 .

clean:
	rm -rf bin
