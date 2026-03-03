#!/bin/bash
# 编译 ota-agent 并打包：ota-agent 二进制 + ota-server.service + ota-client.service + init-wifi.sh + 部署脚本
# 用法：./scripts/build-ota-agent.sh [GOOS] [GOARCH] [VERSION]
# 示例：./scripts/build-ota-agent.sh linux arm64
# 示例：./scripts/build-ota-agent.sh linux arm64 v1.5

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
AGENT_DIR="$REPO_ROOT/ota-agent"
OUT_DIR="$REPO_ROOT/dist"
STAGE_DIR="$OUT_DIR/ota-agent-package"

# 默认编译目标与版本（可被参数覆盖，与 build-ota-agent.ps1 一致）
GOOS="${1:-linux}"
GOARCH="${2:-arm64}"
VERSION="${3:-v1.5}"

BINARY_NAME="ota-agent"
if [[ "$GOOS" == "windows" ]]; then
    BINARY_NAME="ota-agent.exe"
fi

echo "=========================================="
echo "  OTA-Agent 编译与打包"
echo "  GOOS=$GOOS GOARCH=$GOARCH VERSION=$VERSION"
echo "=========================================="

# 1. 编译
echo "[1/4] 编译 ota-agent..."
cd "$AGENT_DIR"
CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -ldflags="-s -w" -o "$BINARY_NAME" .
echo "      已生成: $AGENT_DIR/$BINARY_NAME"

# 2. 准备打包目录
echo "[2/4] 准备打包文件..."
rm -rf "$STAGE_DIR"
mkdir -p "$STAGE_DIR"
cp "$AGENT_DIR/$BINARY_NAME" "$STAGE_DIR/"
cp "$REPO_ROOT/scripts/$VERSION/ota-server.service" "$STAGE_DIR/"
cp "$REPO_ROOT/scripts/$VERSION/ota-client.service" "$STAGE_DIR/"
cp "$REPO_ROOT/scripts/init-wifi.sh" "$STAGE_DIR/"
cp "$REPO_ROOT/scripts/deploy-ota-client.sh" "$STAGE_DIR/"
cp "$REPO_ROOT/scripts/deploy-ota-server.sh" "$STAGE_DIR/"
chmod +x "$STAGE_DIR/init-wifi.sh" "$STAGE_DIR/deploy-ota-server.sh" "$STAGE_DIR/deploy-ota-client.sh"

# 3. 打压缩包
ARCHIVE_NAME="ota-agent-${GOOS}-${GOARCH}-$(date +%Y%m%d-%H%M%S).tar.gz"
ARCHIVE_PATH="$OUT_DIR/$ARCHIVE_NAME"
echo "[3/4] 打包: $ARCHIVE_NAME ..."
mkdir -p "$OUT_DIR"
tar -czf "$ARCHIVE_PATH" -C "$STAGE_DIR" \
    "$BINARY_NAME" \
    init-wifi.sh \
    ota-server.service \
    ota-client.service \
    deploy-ota-server.sh \
    deploy-ota-client.sh

# 4. 清理临时目录
echo "[4/4] 清理临时目录..."
rm -rf "$STAGE_DIR"

echo ""
echo "完成。压缩包: $ARCHIVE_PATH"
echo "内含: ota-agent, ota-server.service, ota-client.service, init-wifi.sh, deploy-ota-server.sh, deploy-ota-client.sh"
