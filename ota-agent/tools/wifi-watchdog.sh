#!/bin/bash
#
# 定时检查 WiFi：STA 未连接则重连；热点模式下恢复热点。
# 依赖 NetworkManager（nmcli）。建议 root。
#
# 参数仅通过命令行传递（含 systemd ExecStart）。STA 与热点复用同一组 --ssid / --psk。
# 切换模式由 ota-agent 在保存配置后执行 install-timer 重写 .service 并 daemon-reload。
#
# 用法：
#   sudo ./wifi-watchdog.sh run --mode sta|hotspot --ssid SSID [--psk PSK] [--iface IFACE]
#   sudo ./wifi-watchdog.sh install-timer [--install-path PATH] --mode sta|hotspot --ssid SSID [--psk PSK] [--iface IFACE]
#   sudo ./wifi-watchdog.sh uninstall-timer [--install-path PATH]
#   sudo ./wifi-watchdog.sh hotspot-on  --ssid SSID [--psk PSK] [--iface IFACE]
#   sudo ./wifi-watchdog.sh hotspot-off [--iface IFACE]
#

set -euo pipefail

DEFAULT_SSID=""
DEFAULT_PSK=""
DEFAULT_INSTALL_PATH="/home/arenatech/client/tools/wifi-watchdog.sh"

NET_MODE=""
SSID="$DEFAULT_SSID"
PSK="$DEFAULT_PSK"
IFACE=""
INSTALL_PATH="$DEFAULT_INSTALL_PATH"

readonly UNIT_BASE="wifi-watchdog"
readonly LOG_TAG="wifi-watchdog"

log() {
  logger -t "$LOG_TAG" -- "$*"
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

usage() {
  cat <<'EOF'
用法:
  wifi-watchdog.sh run --mode sta|hotspot --ssid SSID [--psk PSK] [--iface IFACE]
  wifi-watchdog.sh install-timer --mode sta|hotspot --ssid SSID [--psk PSK] [--install-path PATH] [--iface IFACE]
  wifi-watchdog.sh uninstall-timer [--install-path PATH]
  wifi-watchdog.sh hotspot-on --ssid SSID [--psk PSK] [--iface IFACE]
  wifi-watchdog.sh hotspot-off [--iface IFACE]

说明:
  --psk 允许为空（开放 STA）；热点模式需要非空 --psk（nmcli 热点要求密码）。
EOF
}

escape_systemd_exec_arg() {
  local s=$1 dollar='$'
  if [[ "$s" == *"$dollar"* ]]; then
    s="${s//$dollar/$dollar$dollar}"
  fi
  if [[ "$s" =~ [[:space:]\"\\] ]]; then
    s="${s//\\/\\\\}"
    s="${s//\"/\\\"}"
    printf '"%s"' "$s"
  else
    printf '%s' "$s"
  fi
}

build_watchdog_execstart() {
  local m
  m="$(normalize_mode "$NET_MODE")"
  local out="ExecStart="
  local sp=""
  local -a argv=("$INSTALL_PATH" run --mode "$m" --ssid "$SSID" --psk "$PSK")
  [[ -n "$IFACE" ]] && argv+=(--iface "$IFACE")
  local a
  for a in "${argv[@]}"; do
    out+="${sp}$(escape_systemd_exec_arg "$a")"
    sp=" "
  done
  printf '%s\n' "$out"
}

normalize_mode() {
  local m
  m="$(echo "${1:-sta}" | tr '[:upper:]' '[:lower:]')"
  if [[ "$m" == "hotspot" ]]; then
    echo "hotspot"
  else
    echo "sta"
  fi
}

parse_wifi_flags() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --ssid)
        [[ -n "${2:-}" ]] || die "--ssid 需要值"
        SSID="$2"
        shift 2
        ;;
      --psk)
        [[ $# -ge 2 ]] || die "--psk 需要值（开放网: --psk \"\"）"
        PSK="$2"
        shift 2
        ;;
      --iface)
        [[ -n "${2:-}" ]] || die "--iface 需要值"
        IFACE="$2"
        shift 2
        ;;
      --install-path)
        [[ -n "${2:-}" ]] || die "--install-path 需要值"
        INSTALL_PATH="$2"
        shift 2
        ;;
      --mode)
        [[ -n "${2:-}" ]] || die "--mode 需要值（sta 或 hotspot）"
        NET_MODE="$2"
        shift 2
        ;;
      *)
        die "未知参数: $1"
        ;;
    esac
  done
}

wifi_device() {
  if [[ -n "$IFACE" ]]; then
    echo "$IFACE"
    return
  fi
  nmcli -t -f DEVICE,TYPE device status | awk -F: '$2=="wifi"{print $1; exit}'
}

device_state() {
  local dev="$1"
  nmcli -t -f DEVICE,STATE device status | awk -F: -v d="$dev" '$1==d{print $2; exit}'
}

ensure_radio() {
  local dev="$1"
  local st
  st="$(device_state "$dev")"
  if [[ "$st" == "unavailable" ]]; then
    log "接口 $dev 为 unavailable，尝试打开射频与解除 rfkill"
    rfkill unblock wifi 2>/dev/null || true
    nmcli radio wifi on || true
    sleep 2
  fi
}

wifi_connected() {
  local dev="$1"
  [[ "$(device_state "$dev")" == "connected" ]]
}

connection_exists() {
  local name="$1"
  nmcli connection show "$name" &>/dev/null
}

run_hotspot_watchdog() {
  require_root
  need_cmd nmcli
  [[ -n "$SSID" ]] || die "热点需要 --ssid"
  [[ -n "$PSK" ]] || die "热点需要非空 --psk"
  local dev
  dev="$(wifi_device)"
  [[ -n "$dev" ]] || die "未发现 WiFi 网卡"
  ensure_radio "$dev"
  if wifi_connected "$dev"; then
    log "热点模式：接口已连接（$dev），跳过"
    exit 0
  fi
  log "热点模式：尝试启动热点 SSID=$SSID"
  nmcli radio wifi on || true
  if nmcli device wifi hotspot ifname "$dev" ssid "$SSID" password "$PSK"; then
    log "热点已启动"
    exit 0
  fi
  die "热点启动失败"
}

run_sta_watchdog() {
  require_root
  need_cmd nmcli
  [[ -n "$SSID" ]] || die "STA 需要 --ssid"

  local dev
  dev="$(wifi_device)"
  [[ -n "$dev" ]] || die "未发现 WiFi 网卡"

  if wifi_connected "$dev"; then
    log "STA 模式：WiFi 已连接（$dev），跳过"
    exit 0
  fi

  ensure_radio "$dev"

  if wifi_connected "$dev"; then
    log "STA 模式：射频恢复后已连接（$dev），跳过"
    exit 0
  fi

  log "STA 模式：未连接（$dev），重新扫描并尝试连接 SSID=$SSID"
  nmcli radio wifi on || true
  if nmcli device wifi rescan ifname "$dev" 2>/dev/null; then
    :
  else
    nmcli device wifi rescan 2>/dev/null || true
  fi
  sleep 3

  if connection_exists "$SSID"; then
    if nmcli connection up "$SSID" ifname "$dev" 2>/dev/null; then
      log "已通过已保存的配置连接: $SSID"
      exit 0
    fi
    log "已保存配置 $SSID 激活失败，尝试重新握手连接"
  fi

  if [[ -n "$PSK" ]]; then
    if nmcli device wifi connect "$SSID" password "$PSK" ifname "$dev"; then
      log "已连接到 $SSID"
      exit 0
    fi
  else
    if nmcli device wifi connect "$SSID" ifname "$dev"; then
      log "已连接到开放网络 $SSID"
      exit 0
    fi
  fi

  die "连接 $SSID 失败（请检查密码、信号与 SSID 是否存在）"
}

run_watchdog() {
  local mode
  mode="$(normalize_mode "${NET_MODE:-sta}")"
  log "当前 WiFi 运行模式: $mode"
  if [[ "$mode" == "hotspot" ]]; then
    run_hotspot_watchdog
  else
    run_sta_watchdog
  fi
}

run_watchdog_cmd() {
  parse_wifi_flags "$@"
  if [[ -z "$NET_MODE" ]]; then
    die "run 需要 --mode sta 或 --mode hotspot"
  fi
  run_watchdog
}

hotspot_off() {
  require_root
  need_cmd nmcli
  parse_wifi_flags "$@"
  local dev
  dev="$(wifi_device)"
  [[ -n "$dev" ]] || die "未发现 WiFi 网卡"
  nmcli device disconnect "$dev" 2>/dev/null || true
  nmcli connection down Hotspot 2>/dev/null || true
  log "热点已尝试关闭"
}

hotspot_on_cmd() {
  parse_wifi_flags "$@"
  run_hotspot_watchdog
}

install_timer() {
  require_root
  need_cmd systemctl

  if [[ -z "$NET_MODE" ]]; then
    die "install-timer 需要 --mode sta 或 --mode hotspot"
  fi
  if [[ -z "$SSID" ]]; then
    die "install-timer 需要 --ssid"
  fi
  local hm
  hm="$(normalize_mode "$NET_MODE")"
  if [[ "$hm" == "hotspot" && -z "$PSK" ]]; then
    die "install-timer 热点模式需要非空 --psk"
  fi

  local src
  src="$(readlink -f "$0")"
  [[ -f "$src" ]] || die "无法解析脚本路径: $0"

  mkdir -p "$(dirname "$INSTALL_PATH")"
  if [[ -f "$INSTALL_PATH" ]] && [[ "$src" == "$(readlink -f "$INSTALL_PATH")" ]]; then
    log "脚本已在 $INSTALL_PATH，跳过复制；仅注册 systemd"
  else
    install -m 750 -o root -g root "$src" "$INSTALL_PATH"
    log "已复制脚本至: $INSTALL_PATH"
  fi

  local exec_line
  exec_line="$(build_watchdog_execstart)"

  cat >"/etc/systemd/system/${UNIT_BASE}.service" <<EOF
[Unit]
Description=Ensure WiFi connects to fallback SSID when disconnected
After=network-pre.target NetworkManager.service
Wants=NetworkManager.service

[Service]
Type=oneshot
${exec_line}
Nice=10
EOF

  cat >"/etc/systemd/system/${UNIT_BASE}.timer" <<EOF
[Unit]
Description=Periodic WiFi watchdog ($UNIT_BASE)

[Timer]
Unit=${UNIT_BASE}.service
OnBootSec=3min
OnUnitActiveSec=5min
AccuracySec=1min
Persistent=true

[Install]
WantedBy=timers.target
EOF

  systemctl daemon-reload
  systemctl enable --now "${UNIT_BASE}.timer"
  log "已启用定时器: ${UNIT_BASE}.timer"
  systemctl status "${UNIT_BASE}.timer" --no-pager || true
}

uninstall_timer() {
  require_root
  need_cmd systemctl
  systemctl disable --now "${UNIT_BASE}.timer" 2>/dev/null || true
  rm -f "/etc/systemd/system/${UNIT_BASE}.timer"
  rm -f "/etc/systemd/system/${UNIT_BASE}.service"
  systemctl daemon-reload
  log "已移除 systemd 单元 ${UNIT_BASE}.timer / ${UNIT_BASE}.service"
  if [[ -f "$INSTALL_PATH" ]]; then
    log "安装脚本仍保留: $INSTALL_PATH（需手动删除可 rm）"
  fi
}

main() {
  local cmd
  if [[ $# -ge 1 ]]; then
    if [[ "$1" == -* ]]; then
      die "第一个参数须为子命令（run / install-timer / uninstall-timer / hotspot-on / hotspot-off）"
    fi
    cmd="$1"
    shift
  else
    die "需要子命令: run / install-timer / uninstall-timer / …（见 --help）"
  fi

  case "$cmd" in
    -h | --help | help)
      usage
      ;;
    run)
      run_watchdog_cmd "$@"
      ;;
    install-timer)
      parse_wifi_flags "$@"
      install_timer
      ;;
    uninstall-timer)
      parse_wifi_flags "$@"
      uninstall_timer
      ;;
    hotspot-on)
      hotspot_on_cmd "$@"
      ;;
    hotspot-off)
      hotspot_off "$@"
      ;;
    *)
      die "未知命令: $cmd（可用 run / install-timer / uninstall-timer / hotspot-on / hotspot-off / --help）"
      ;;
  esac
}

main "$@"
