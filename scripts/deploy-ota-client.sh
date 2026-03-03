#!/bin/bash
# 部署 ota-agent：创建目录、复制二进制、安装并启动 systemd 服务
# 在解压后的目录下执行：sudo ./deploy-agent.sh

set -e

BASE_DIR="/home/arenatech/client"
BIN_DIR="$BASE_DIR/bin"
TOOLS_DIR="$BASE_DIR/tools"
LOGS_DIR="$BASE_DIR/logs"
CONFIG_DIR="$BASE_DIR/config"
SERVICE_NAME="ota-client.service"

# 脚本所在目录（即解压目录，含 ota-agent、agent.service 等）
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

red='\033[0;31m'
green='\033[0;32m'
nc='\033[0m'
info() { echo -e "${green}[INFO]${nc} $1"; }
err()  { echo -e "${red}[ERROR]${nc} $1"; }

[[ $EUID -eq 0 ]] || { err "请使用 sudo 运行本脚本"; exit 1; }

echo "=========================================="
echo "  OTA-Agent 部署"
echo "=========================================="

# 第一步：创建目录
info "[1/3] 创建目录..."
mkdir -p "$BIN_DIR" "$TOOLS_DIR" "$LOGS_DIR" "$CONFIG_DIR"
info "      $BIN_DIR"
info "      $TOOLS_DIR"
info "      $LOGS_DIR"
info "      $CONFIG_DIR"

# 第二步：复制 ota-agent 到 $BIN_DIR
info "[2/3] 复制 ota-agent 到 $BIN_DIR ..."
if [[ -f "$SCRIPT_DIR/ota-agent" ]]; then
    cp "$SCRIPT_DIR/ota-agent" "$BIN_DIR/"
    chmod +x "$BIN_DIR/ota-agent"
    info "      已复制 ota-agent"
else
    err "未找到 $SCRIPT_DIR/ota-agent，请在本脚本所在目录（解压目录）下执行。"
    exit 1
fi

# 第三步：安装 ota-client.service 并启动服务
info "[3/3] 安装 systemd 服务并启动..."
if [[ ! -f "$SCRIPT_DIR/$SERVICE_NAME" ]]; then
    err "未找到 $SCRIPT_DIR/$SERVICE_NAME"
    exit 1
fi
cp "$SCRIPT_DIR/$SERVICE_NAME" /etc/systemd/system/
systemctl daemon-reload
systemctl enable $SERVICE_NAME
systemctl start $SERVICE_NAME
info "      已安装并启动 $SERVICE_NAME"

echo ""
info "部署完成。"
echo "  查看状态: systemctl status $SERVICE_NAME"
echo "  查看日志: journalctl -u $SERVICE_NAME -f"
