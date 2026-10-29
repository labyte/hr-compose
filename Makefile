BINARY  := hr-compose
GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

# go install 用模块路径的最后一段当可执行文件名，本模块路径是 hr.compose（无斜杠），
# 装出来会叫 hr.compose，与文档里的命令名 hr-compose 对不上。故先 build 出 bin/$(BINARY)，
# 再按 BINARY 名拷进 go 的 bin 目录（该目录默认已在 PATH 中）。
GOBIN ?= $(shell $(GO) env GOBIN)
ifeq ($(strip $(GOBIN)),)
GOBIN := $(shell $(GO) env GOPATH)/bin
endif

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

install: build
	@mkdir -p $(GOBIN)
	install -m 0755 bin/$(BINARY) $(GOBIN)/$(BINARY)
	@echo "已安装：$(GOBIN)/$(BINARY)"

# 多平台交叉编译：linux/amd64、linux/arm64 为目标机，darwin/arm64 为本地调试
cross:
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-amd64 .
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-arm64 .
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-arm64 .

clean:
	rm -rf bin
