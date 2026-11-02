BINARY  := hr-compose
GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

# 不用 go install：模块路径是 hr.compose（无斜杠），go install 装出来的可执行文件会叫
# hr.compose，与文档里的命令名 hr-compose 对不上。故一律先 build 出 bin/$(BINARY) 再拷贝。

# 安装目录：默认 /usr/local/bin —— 它同时在普通用户 PATH 和 sudo 的 secure_path 里，
# `sudo hr-compose up` 才找得到命令；$GOPATH/bin 与 $HOME/.local/bin 都不在 secure_path 中，
# 装到那里普通 shell 能用、sudo 下就是 "command not found"。
# 装到用户目录（免 sudo，但 sudo 下不可用）：make install PREFIX=$HOME/.local
PREFIX  ?= /usr/local
DESTDIR ?=
BINDIR  := $(DESTDIR)$(PREFIX)/bin

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

# 安装（只拷贝、不重建）：用法 make build && sudo make install。
# 不依赖 build 是刻意的——sudo 下 PATH 被 secure_path 重置，通常找不到 go；即便找得到，
# 以 root 跑 go build 还会往用户的 module cache 写 root 属主文件，害后续非 root 构建报权限错。
install:
	@test -f bin/$(BINARY) || { echo "错误：未找到 bin/$(BINARY)，请先执行 make build" >&2; exit 1; }
	@mkdir -p $(BINDIR)
	install -m 0755 bin/$(BINARY) $(BINDIR)/$(BINARY)
	@echo "已安装：$(BINDIR)/$(BINARY)"
	@$(BINDIR)/$(BINARY) --version 2>/dev/null || true

# 多平台交叉编译：linux/amd64、linux/arm64 为目标机，darwin/arm64 为本地调试
cross:
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-amd64 .
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-arm64 .
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-arm64 .

clean:
	rm -rf bin
