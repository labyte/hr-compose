#!/bin/sh
# hr-compose 在线安装脚本
#
# 用法：
#   curl -fsSL https://github.com/labyte/hr-compose/releases/latest/download/install.sh | sh
#   HR_COMPOSE_VERSION=v1.0.0 curl -fsSL ... | sh   # 安装指定版本（默认最新版）
#   HR_COMPOSE_INSTALL_DIR=$HOME/.local/bin ...     # 自定义安装目录（默认 /usr/local/bin）
#
# 过程：检测 OS/架构 → 下载对应发行包（tar.gz 或裸二进制）→ 用官方 checksums.txt 校验
# SHA256 → 解压安装。支持 linux / darwin，amd64 / arm64。
set -eu

REPO="labyte/hr-compose"
BASE_URL="${HR_COMPOSE_BASE_URL:-https://github.com/$REPO}"
DEST_DIR="${HR_COMPOSE_INSTALL_DIR:-/usr/local/bin}"
VERSION="${HR_COMPOSE_VERSION:-}"

command -v curl >/dev/null 2>&1 || { echo "错误: 需要 curl 才能在线安装" >&2; exit 1; }

# --- 检测操作系统与架构 ---
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  linux | darwin) ;;
  *) echo "错误: 不支持的平台 $OS（仅支持 linux / darwin）" >&2; exit 1 ;;
esac

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64 | amd64) ARCH="amd64" ;;
  aarch64 | arm64) ARCH="arm64" ;;
  *) echo "错误: 不支持的架构 $ARCH（仅支持 amd64 / arm64）" >&2; exit 1 ;;
esac

# --- 确定 checksums 地址（latest 走稳定文件名重定向，或指定版本） ---
if [ -z "$VERSION" ]; then
  CHECK_URL="$BASE_URL/releases/latest/download/checksums.txt"
else
  case "$VERSION" in v*) ;; *) VERSION="v$VERSION" ;; esac
  CHECK_URL="$BASE_URL/releases/download/$VERSION/checksums.txt"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "==> 获取发行信息: $CHECK_URL"
curl -fsSL "$CHECK_URL" -o "$tmp/checksums.txt"

# 形如: <sha256>  hr-compose_<version>_<os>_<arch>[.tar.gz]
LINE="$(grep "hr-compose_.*_${OS}_${ARCH}" "$tmp/checksums.txt" | head -n1)"
[ -n "$LINE" ] || { echo "错误: 当前版本没有 $OS/$ARCH 的发行包" >&2; exit 1; }

SUM="$(echo "$LINE" | awk '{print $1}')"
ASSET="$(echo "$LINE" | awk '{print $2}')"

# 从资产名解析版本号（兼容裸二进制与 .tar.gz）:
#   hr-compose_1.2.3_linux_amd64[.tar.gz]  ->  1.2.3
VERSION="${ASSET#hr-compose_}"
VERSION="${VERSION%_${OS}_${ARCH}*}"

echo "==> 下载 hr-compose $VERSION ($OS/$ARCH): $ASSET"
curl -fsSL "$BASE_URL/releases/download/v$VERSION/$ASSET" -o "$tmp/$ASSET"

echo "==> 校验 SHA256"
( cd "$tmp" && echo "$SUM  $ASSET" | sha256sum -c - >/dev/null 2>&1 ) || \
( cd "$tmp" && printf '%s  %s\n' "$SUM" "$ASSET" | shasum -a 256 -c - >/dev/null 2>&1 ) || \
  { echo "错误: SHA256 校验失败，安装中止" >&2; exit 1; }

# --- 解压（tar.gz）或直接用裸二进制 ---
cd "$tmp"
case "$ASSET" in
  *.tar.gz | *.tgz)
    tar -xzf "$ASSET"
    ;;
  *)
    chmod +x "$ASSET"
    mv "$ASSET" hr-compose
    ;;
esac

# --- 安装 ---
mkdir -p "$DEST_DIR"
install -m 0755 hr-compose "$DEST_DIR/hr-compose" 2>/dev/null || {
  echo "错误: 无法写入 $DEST_DIR" >&2
  echo "请用 sudo 重试，或设置 HR_COMPOSE_INSTALL_DIR（如 $HOME/.local/bin）" >&2
  exit 1
}

echo "==> 已安装: $DEST_DIR/hr-compose"
echo "==> 运行 hr-compose --help 查看命令帮助"
