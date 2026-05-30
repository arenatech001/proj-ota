#!/bin/bash
# 编译 ota-agent 并打包：ota-agent 二进制 + ota-server.service + ota-client.service + init-wifi.sh + 部署脚本
# 用法：./scripts/build-ota-agent.sh [GOOS] [GOARCH] [VERSION]
# 示例：./scripts/build-ota-agent.sh linux arm64
# 示例：./scripts/build-ota-agent.sh linux arm64 v1.5

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
AGENT_DIR="$REPO_ROOT/ota-agent"

GOOS="${1:-linux}"
GOARCH="${2:-arm64}"

echo "=========================================="
echo "  OTA-Agent 编译与打包"
echo "  GOOS=$GOOS GOARCH=$GOARCH"
echo "=========================================="

# 1. 编译
echo "编译 agent..."
cd "$AGENT_DIR"
CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -ldflags="-s -w" -o "agent-${GOOS}-${GOARCH}.bin" .
echo "      已生成: $AGENT_DIR/agent-${GOOS}-${GOARCH}.bin"