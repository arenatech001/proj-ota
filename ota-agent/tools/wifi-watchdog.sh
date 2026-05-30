#!/bin/bash
#
# WiFi 看门狗（NetworkManager / nmcli）。建议 root。
# 从 agent.yaml 的 network 段读取 wifi_mode / wifi_ssid / wifi_psk / wifi_iface。
# STA：启动后尝试连接；3 分钟内连不上则临时开热点（SSID=主机名，密码 AtAdmin0502），不修改 agent.yaml，重启后仍按配置优先 STA。
# 热点：按配置或默认（主机名 + AtAdmin0502）启动 AP。

set -euo pipefail

CONFIG_PATH=""
INSTALL_PATH=""
SKIP_APPLY=0

readonly UNIT_BASE="wifi-watchdog"
readonly LOG_TAG="wifi-watchdog"
readonly HOTSPOT_PSK="AtAdmin0502"
readonly STA_FAIL_TIMEOUT_SEC=180
readonly TIMER_ON_BOOT_SEC=60
readonly TIMER_INTERVAL_SEC=300

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
  wifi-watchdog.sh run --config PATH
  wifi-watchdog.sh install --config PATH [--install-path PATH] [--no-apply]
  wifi-watchdog.sh uninstall

说明:
  配置来自 agent.yaml 的 network.wifi_mode / wifi_ssid / wifi_psk / wifi_iface。
  STA 模式：run 中 3 分钟内连不上目标 WiFi 则临时开热点（不改 agent.yaml，重启仍优先 STA）。
  install 会注册 systemd 定时器（开机 60s、之后每 5 分钟 run 一次）。
EOF
}

parse_common_flags() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --config)
        [[ -n "${2:-}" ]] || die "--config 需要值"
        CONFIG_PATH="$2"
        shift 2
        ;;
      --install-path)
        [[ -n "${2:-}" ]] || die "--install-path 需要值"
        INSTALL_PATH="$2"
        shift 2
        ;;
      --no-apply)
        SKIP_APPLY=1
        shift
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

resolve_install_path() {
  if [[ -z "$INSTALL_PATH" ]]; then
    INSTALL_PATH="$(readlink -f "$0" 2>/dev/null || realpath "$0" 2>/dev/null || echo "$0")"
  fi
}

# 读取 agent.yaml network 段字段（简单 YAML，不含嵌套）。
yaml_network_field() {
  local cfg="$1" key="$2"
  awk -v key="$key" '
    /^network:[[:space:]]*$/ { in_net=1; next }
    in_net && /^[^[:space:]]/ { in_net=0 }
    in_net && $1 == key":" {
      sub(/^[^:]+:[[:space:]]*/, "")
      gsub(/^["'\''"]|["'\''"]$/, "")
      print
      exit
    }
  ' "$cfg"
}

load_network_config() {
  local cfg="$1"
  NET_MODE="$(yaml_network_field "$cfg" "wifi_mode")"
  SSID="$(yaml_network_field "$cfg" "wifi_ssid")"
  PSK="$(yaml_network_field "$cfg" "wifi_psk")"
  IFACE="$(yaml_network_field "$cfg" "wifi_iface")"
  NET_MODE="$(echo "${NET_MODE:-sta}" | tr '[:upper:]' '[:lower:]')"
  [[ "$NET_MODE" == "hotspot" ]] || NET_MODE="sta"
}

hostname_short() {
  local h
  h="$(hostname -s 2>/dev/null || true)"
  [[ -z "$h" ]] && h="$(hostname 2>/dev/null || echo "ota-device")"
  h="${h%%.*}"
  printf '%s' "$h"
}

apply_hotspot_defaults() {
  [[ -z "$SSID" ]] && SSID="$(hostname_short)"
  [[ -z "$PSK" ]] && PSK="$HOTSPOT_PSK"
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
  local dev="$1" st
  st="$(device_state "$dev")"
  if [[ "$st" == "unavailable" ]]; then
    log "接口 $dev unavailable，打开射频"
    rfkill unblock wifi 2>/dev/null || true
    nmcli radio wifi on || true
    sleep 2
  fi
}

wifi_connected() {
  [[ "$(device_state "$1")" == "connected" ]]
}

wifi_active_ssid() {
  local dev="$1" ssid conn
  ssid="$(nmcli -t -f ACTIVE,SSID dev wifi list ifname "$dev" 2>/dev/null | awk -F: '$1=="yes"{print $2; exit}')"
  if [[ -n "$ssid" ]]; then
    printf '%s' "$ssid"
    return 0
  fi
  conn="$(nmcli -g GENERAL.CONNECTION device show "$dev" 2>/dev/null || true)"
  if [[ -n "$conn" && "$conn" != "--" ]]; then
    ssid="$(nmcli -g 802-11-wireless.ssid connection show "$conn" 2>/dev/null || true)"
    [[ -n "$ssid" ]] && printf '%s' "$ssid" && return 0
  fi
  return 1
}

wifi_connected_to_ssid() {
  local dev="$1" want="$2" cur
  wifi_connected "$dev" || return 1
  cur="$(wifi_active_ssid "$dev" 2>/dev/null || true)"
  [[ -n "$cur" && "$cur" == "$want" ]]
}

connection_exists() {
  nmcli connection show "$1" &>/dev/null
}

delete_all_saved_wifi_connections() {
  local id
  log "清除本机所有已保存的 WiFi 连接配置"
  while IFS= read -r id; do
    [[ -z "$id" ]] && continue
    log "删除 WiFi 连接: $id"
    nmcli connection delete "$id" 2>/dev/null || true
  done < <(
    nmcli -t -f UUID,TYPE connection show 2>/dev/null | awk -F: '
      $2 == "802-11-wireless" || $2 == "wifi" { print $1 }
    '
  )
}

hotspot_start_device() {
  local dev="$1"
  apply_hotspot_defaults
  log "启动热点 SSID=$SSID"
  nmcli radio wifi on || true
  nmcli device wifi hotspot ifname "$dev" ssid "$SSID" password "$PSK"
}

sta_try_connect_once() {
  local dev="$1"
  log "尝试连接 STA SSID=$SSID"
  nmcli radio wifi on || true
  nmcli device wifi rescan ifname "$dev" 2>/dev/null || nmcli device wifi rescan 2>/dev/null || true
  sleep 3
  if connection_exists "$SSID"; then
    nmcli connection up "$SSID" ifname "$dev" 2>/dev/null && return 0
  fi
  if [[ -n "$PSK" ]]; then
    nmcli device wifi connect "$SSID" password "$PSK" ifname "$dev" && return 0
  else
    nmcli device wifi connect "$SSID" ifname "$dev" && return 0
  fi
  return 1
}

sta_fallback_to_hotspot() {
  local dev="$1" target="$SSID"
  apply_hotspot_defaults
  log "STA：${STA_FAIL_TIMEOUT_SEC}s 内未连上 ${target}，开启临时热点 SSID=$SSID 密码=$PSK（不改 agent.yaml，重启仍优先 STA）"
  nmcli device disconnect "$dev" 2>/dev/null || true
  nmcli connection down Hotspot 2>/dev/null || true
  sleep 2
  ensure_radio "$dev"
  hotspot_start_device "$dev" || die "临时热点启动失败"
  log "临时热点已开启 SSID=$SSID"
  exit 0
}

run_sta_watchdog() {
  local dev deadline
  dev="$(wifi_device)"
  [[ -n "$dev" ]] || die "未发现 WiFi 网卡"
  [[ -n "$SSID" ]] || die "STA 需要 network.wifi_ssid"

  if wifi_connected_to_ssid "$dev" "$SSID"; then
    log "STA：已连接 $SSID，跳过"
    return 0
  fi

  ensure_radio "$dev"
  log "STA：${STA_FAIL_TIMEOUT_SEC}s 内尝试连接 $SSID，失败则回退热点"
  deadline=$(( $(date +%s) + STA_FAIL_TIMEOUT_SEC ))
  while (( $(date +%s) < deadline )); do
    if wifi_connected_to_ssid "$dev" "$SSID"; then
      log "STA：已连接 $SSID"
      return 0
    fi
    if sta_try_connect_once "$dev"; then
      log "STA：已连接 $SSID"
      return 0
    fi
    sleep 10
  done

  sta_fallback_to_hotspot "$dev"
}

run_hotspot_watchdog() {
  local dev
  dev="$(wifi_device)"
  [[ -n "$dev" ]] || die "未发现 WiFi 网卡"
  apply_hotspot_defaults
  ensure_radio "$dev"
  if wifi_connected_to_ssid "$dev" "$SSID"; then
    log "热点已运行 SSID=$SSID，跳过"
    return 0
  fi
  hotspot_start_device "$dev" || die "热点启动失败"
  log "热点已启动 SSID=$SSID"
}

apply_sta_now() {
  local dev
  dev="$(wifi_device)"
  [[ -n "$dev" ]] || die "未发现 WiFi 网卡"
  [[ -n "$SSID" ]] || die "STA 需要 network.wifi_ssid"
  log "STA apply：尝试连接 $SSID"
  nmcli device disconnect "$dev" 2>/dev/null || true
  sleep 2
  ensure_radio "$dev"
  if wifi_connected_to_ssid "$dev" "$SSID"; then
    log "STA apply：已连接 $SSID"
    return 0
  fi
  if sta_try_connect_once "$dev"; then
    log "STA apply：已连接 $SSID"
    return 0
  fi
  log "STA apply：连接 $SSID 失败（回退热点由定时 run 处理）"
  return 1
}

apply_hotspot_now() {
  local dev
  dev="$(wifi_device)"
  [[ -n "$dev" ]] || die "未发现 WiFi 网卡"
  apply_hotspot_defaults
  ensure_radio "$dev"
  log "热点 apply：SSID=$SSID"
  nmcli device disconnect "$dev" 2>/dev/null || true
  nmcli connection down Hotspot 2>/dev/null || true
  sleep 2
  ensure_radio "$dev"
  hotspot_start_device "$dev" || die "热点启动失败"
  log "热点 apply：已启动"
}

run_watchdog() {
  resolve_config_path
  load_network_config "$CONFIG_PATH"
  log "WiFi run 配置=$CONFIG_PATH 模式=$NET_MODE SSID=${SSID:-—}"
  if [[ "$NET_MODE" == "hotspot" ]]; then
    run_hotspot_watchdog
  else
    run_sta_watchdog
  fi
}

apply_wifi_now() {
  resolve_config_path
  load_network_config "$CONFIG_PATH"
  log "apply：模式=$NET_MODE SSID=${SSID:-—}"
  if [[ "$NET_MODE" == "hotspot" ]]; then
    apply_hotspot_now
  else
    apply_sta_now || true
  fi
}

install_service() {
  require_root
  need_cmd systemctl nmcli

  resolve_config_path
  resolve_install_path
  load_network_config "$CONFIG_PATH"

  if [[ "$NET_MODE" == "sta" && -z "$SSID" ]]; then
    die "STA 模式需要 network.wifi_ssid"
  fi

  local src
  src="$(readlink -f "$0" 2>/dev/null || realpath "$0" 2>/dev/null || echo "$0")"
  [[ -f "$src" ]] || die "无法解析脚本路径"

  if [[ "$src" != "$INSTALL_PATH" ]]; then
    mkdir -p "$(dirname "$INSTALL_PATH")"
    install -m 750 -o root -g root "$src" "$INSTALL_PATH"
    log "已安装脚本: $INSTALL_PATH"
  fi

  local exec_line="ExecStart="
  exec_line+="$(escape_systemd_exec_arg "$INSTALL_PATH")"
  exec_line+=" "
  exec_line+=$(escape_systemd_exec_arg "run")
  exec_line+=" "
  exec_line+=$(escape_systemd_exec_arg "--config")
  exec_line+=" "
  exec_line+=$(escape_systemd_exec_arg "$CONFIG_PATH")

  cat >"/etc/systemd/system/${UNIT_BASE}.service" <<EOF
[Unit]
Description=WiFi watchdog (ota-agent)
After=network-pre.target NetworkManager.service
Wants=NetworkManager.service

[Service]
Type=oneshot
${exec_line}
Nice=10
EOF

  cat >"/etc/systemd/system/${UNIT_BASE}.timer" <<EOF
[Unit]
Description=Periodic WiFi watchdog

[Timer]
Unit=${UNIT_BASE}.service
OnBootSec=${TIMER_ON_BOOT_SEC}
OnUnitActiveSec=${TIMER_INTERVAL_SEC}
AccuracySec=30
Persistent=true

[Install]
WantedBy=timers.target
EOF

  systemctl daemon-reload
  systemctl enable --now "${UNIT_BASE}.timer"
  log "已启用 ${UNIT_BASE}.timer（配置=$CONFIG_PATH 模式=$NET_MODE）"

  if [[ "$SKIP_APPLY" != "1" ]]; then
    if [[ "$NET_MODE" == "sta" ]]; then
      delete_all_saved_wifi_connections
    fi
    apply_wifi_now
  fi
}

uninstall_service() {
  require_root
  need_cmd systemctl
  systemctl disable --now "${UNIT_BASE}.timer" 2>/dev/null || true
  rm -f "/etc/systemd/system/${UNIT_BASE}.timer" "/etc/systemd/system/${UNIT_BASE}.service"
  systemctl daemon-reload
  log "已移除 ${UNIT_BASE} systemd 单元"
}

main() {
  local cmd
  [[ $# -ge 1 ]] || die "需要子命令（run / install / uninstall）"
  [[ "$1" != -* ]] || die "第一个参数须为子命令"
  cmd="$1"
  shift

  case "$cmd" in
    -h | --help | help) usage ;;
    run)
      parse_common_flags "$@"
      run_watchdog
      ;;
    install | install-timer)
      parse_common_flags "$@"
      install_service
      ;;
    uninstall | uninstall-timer)
      parse_common_flags "$@"
      uninstall_service
      ;;
    *)
      die "未知命令: $cmd"
      ;;
  esac
}

main "$@"
