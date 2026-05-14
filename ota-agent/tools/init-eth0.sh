#!/bin/bash
#
# 初始化 eth0：创建 NM 连接 eth0-static（固定地址），不设为默认路由（ipv4.never-default yes）。
# 需 root；建议在设备上由 ota-agent 管理页触发。
#

set -euo pipefail

readonly CON_NAME="eth0-static"
readonly IFACE="eth0"
readonly IP4="192.168.123.100/24"
readonly GW4="192.168.123.1"
readonly DNS='8.8.8.8 8.8.4.4'

log() {
  printf '%s %s\n' "$(date -Is)" "$*" >&2
}

die() {
  log "ERROR: $*"
  exit 1
}

require_root() {
  [[ "$(id -u)" -eq 0 ]] || die "请使用 root 运行（sudo）"
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "缺少命令: $1"
}

require_root
need_cmd nmcli

if ! [[ -e "/sys/class/net/$IFACE" ]]; then
  die "未找到网卡 $IFACE（请确认接口名）"
fi

if nmcli connection show "$CON_NAME" &>/dev/null; then
  log "删除已存在的连接: $CON_NAME"
  nmcli connection delete "$CON_NAME"
fi

log "创建连接 $CON_NAME（$IFACE, $IP4, gw $GW4）"
nmcli connection add con-name "$CON_NAME" ifname "$IFACE" type ethernet ip4 "$IP4" gw4 "$GW4"
nmcli connection modify "$CON_NAME" ipv4.dns "$DNS"
nmcli connection modify "$CON_NAME" ipv4.method manual
# 禁止该连接提供默认路由（等价于「去掉 eth0 上的默认路由」）
nmcli connection modify "$CON_NAME" ipv4.never-default yes
nmcli connection up "$CON_NAME"

log "完成: $CON_NAME 已激活（ipv4.never-default=yes，不会作为系统默认出口）"
