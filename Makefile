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

# 离线手册 PDF 的版本号：取最近一个 tag 并去掉前导 v，使文件名与 goreleaser 的
# archives（hr-compose_0.6.1_linux_amd64.tar.gz）同一风格。命令行可覆盖：
# CI 用 make docs DOC_VERSION="${GITHUB_REF_NAME#v}"，与 goreleaser 的 {{.Version}} 严格同源。
# 不用 $(VERSION)——它是 git describe --dirty 的结果，可能带 v 前缀与 -dirty 后缀。
DOC_TAG     := $(shell git describe --tags --abbrev=0 2>/dev/null)
DOC_VERSION := $(if $(DOC_TAG),$(patsubst v%,%,$(DOC_TAG)),dev)

# 产物落 build/ 而不是 dist/：release.yml 用 `goreleaser release --clean`，
# dist/ 会在开跑时被整体删除，提前生成的 PDF 会被一起删掉、extra_files 的 glob 就落空。
BUILD_DIR := build
MANUAL    := $(BUILD_DIR)/hr-compose_$(DOC_VERSION)_offline-install.pdf
REPO_URL  := https://github.com/labyte/hr-compose

# PDF 由无头浏览器打印生成：ubuntu-latest 自带 google-chrome，本地按需覆盖
# （make docs PDF_BROWSER=/usr/bin/chromium）。
PDF_BROWSER ?= $(shell command -v google-chrome 2>/dev/null || command -v chromium 2>/dev/null || command -v chromium-browser 2>/dev/null)
# 老版 chromium（<109）不认 --headless=new，需要时整体覆盖本行。
PDF_FLAGS ?= --headless=new --no-sandbox --disable-gpu --disable-dev-shm-usage --no-pdf-header-footer

.PHONY: all build test test-race test-cover vet lint fmt install cross docs clean

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

# 离线手册 PDF：只在发布流水线（tag 触发）里跑，不进 build/cross 依赖链——日常构建
# 不该依赖 pandoc 与 CJK 字体。产物由 .goreleaser.yaml 的 release.extra_files 带走。
docs:
	@command -v pandoc >/dev/null 2>&1 || { echo "错误：缺 pandoc，Debian/Ubuntu 执行 sudo apt-get install -y pandoc fonts-noto-cjk" >&2; exit 1; }
	@test -n "$(PDF_BROWSER)" || { echo "错误：未找到 google-chrome/chromium，PDF 靠无头浏览器打印生成，可 make docs PDF_BROWSER=/path/to/chrome" >&2; exit 1; }
	@mkdir -p $(BUILD_DIR)
	@# 仓库内相对链接在 PDF 里是死链（点开指向 file://），统一改写成 GitHub 上的绝对地址
	sed -E 's@\]\(([^):]+\.(md|png|jpg|svg))(#[^)]*)?\)@]($(REPO_URL)/blob/main/\1\3)@g' offline-install.md > $(BUILD_DIR)/offline-install.md
	pandoc $(BUILD_DIR)/offline-install.md --standalone --embed-resources --toc --toc-depth=2 --metadata pagetitle="hr-compose 离线安装与使用指南 v$(DOC_VERSION)" --css $(CURDIR)/offline-install.css --output $(BUILD_DIR)/offline-install.html
	$(PDF_BROWSER) $(PDF_FLAGS) --print-to-pdf=$(MANUAL) file://$(CURDIR)/$(BUILD_DIR)/offline-install.html
	@test -s $(MANUAL) || { echo "错误：PDF 未生成或为空：$(MANUAL)" >&2; exit 1; }
	@echo "已生成：$(MANUAL)"

clean:
	rm -rf bin $(BUILD_DIR)
