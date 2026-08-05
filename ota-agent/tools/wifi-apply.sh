#!/bin/bash
#
# Web / 管理端：按 agent.yaml 立即应用 WiFi（STA 连接或开热点）。
# 不注册定时器；临时热点回退由 wifi-watchdog.sh 负责。
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=wifi-common.sh
source "${SCRIPT_DIR}/wifi-common.sh"

LOG_TAG="wifi-apply"
CONFIG_PATH=""

usage() {
  cat <<'EOF'
用法:
  wifi-apply.sh apply --config PATH

说明:
  读取 agent.yaml 的 network.wifi_mode / wifi_ssid / wifi_psk / wifi_iface，立即应用：
  - sta：清除已保存 WiFi 配置后尝试连接目标 SSID（失败不改 yaml，不在此开热点）
  - hotspot：以主机名为 SSID、密码 Arenatech0502 开热点
  需 root；供 ota-agent 管理页调用。
EOF
}

parse_flags() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --config)
        [[ -n "${2:-}" ]] || die "--config 需要值"
        CONFIG_PATH="$2"
        shift 2
        ;;
      -h | --help | help)
        usage
        exit 0
        ;;
      *)
        die "未知参数: $1"
        ;;
    esac
  done
}

resolve_config_path() {
  [[ -n "$CONFIG_PATH" ]] || die "需要 --config"
  CONFIG_PATH="$(readlink -f "$CONFIG_PATH" 2>/dev/null || realpath "$CONFIG_PATH" 2>/dev/null || echo "$CONFIG_PATH")"
  [[ -f "$CONFIG_PATH" ]] || die "配置文件不存在: $CONFIG_PATH"
}

cmd_apply() {
  require_root
  need_cmd nmcli
  resolve_config_path
  load_network_config "$CONFIG_PATH"

  if [[ "$NET_MODE" == "hotspot" ]]; then
    log "apply：模式=hotspot 热点SSID=$(hotspot_ssid)（主机名）配置=$CONFIG_PATH"
    apply_hotspot_now
  else
    [[ -n "$SSID" ]] || die "STA 模式需要 network.wifi_ssid"
    log "apply：模式=sta SSID=$SSID 配置=$CONFIG_PATH"
    delete_all_saved_wifi_connections
    apply_sta_now || true
  fi
}

main() {
  local cmd
  [[ $# -ge 1 ]] || die "需要子命令 apply"
  cmd="$1"
  shift
  case "$cmd" in
    -h | --help | help) usage ;;
    apply)
      parse_flags "$@"
      cmd_apply
      ;;
    *)
      die "未知命令: $cmd（仅支持 apply）"
      ;;
  esac
}

main "$@"
