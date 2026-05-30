#!/bin/bash
#
# WiFi 看门狗（NetworkManager / nmcli）。建议 root。
# ota-agent 保存网络配置时调用 install-timer：写 systemd、清除已保存 WiFi 配置、立即尝试连网。
# 热点：SSID=主机短名，密码 Arenatech0502（脚本内固定）。
# STA：仅在 run（含 systemd 定时器）中，120s 内连不上目标 SSID 则临时开热点（不改 systemd/agent 配置，重启仍优先 STA）。

set -euo pipefail

DEFAULT_INSTALL_PATH="/home/arenatech/client/tools/wifi-watchdog.sh"

NET_MODE=""
SSID=""
PSK=""
IFACE=""
SKIP_APPLY=0
INSTALL_PATH="$DEFAULT_INSTALL_PATH"

readonly UNIT_BASE="wifi-watchdog"
readonly LOG_TAG="wifi-watchdog"
readonly HOTSPOT_PSK="Arenatech0502"
readonly STA_FAIL_TIMEOUT_SEC=120
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
  wifi-watchdog.sh run --mode sta|hotspot [--ssid SSID] [--psk PSK] [--iface IFACE]
  wifi-watchdog.sh install-timer --mode sta|hotspot [--ssid SSID] [--psk PSK]
      [--install-path PATH] [--iface IFACE] [--no-apply]
  wifi-watchdog.sh uninstall-timer [--install-path PATH]

说明:
  STA 须传 --ssid（--psk 可为空表示开放网）。热点无需传 SSID/密码（使用主机名 + Arenatech0502）。
  install-timer 会先删除本机所有已保存 WiFi 配置，再注册定时器并立即尝试连网（可用 --no-apply 跳过连网）。
  STA 仅在 run 中：120s 连不上则临时开热点（SSID=主机名，密码 Arenatech0502）；不写入配置，重启仍按 install-timer 中的 STA 重试。
EOF
}

normalize_mode() {
  local m
  m="$(echo "${1:-sta}" | tr '[:upper:]' '[:lower:]')"
  [[ "$m" == "hotspot" ]] && echo "hotspot" || echo "sta"
}

hostname_short() {
  local h
  h="$(hostname -s 2>/dev/null || true)"
  [[ -z "$h" ]] && h="$(hostname 2>/dev/null || echo "ota-device")"
  h="${h%%.*}"
  printf '%s' "$h"
}

apply_hotspot_defaults() {
  SSID="$(hostname_short)"
  PSK="$HOTSPOT_PSK"
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

resolve_mode_config() {
  local mode
  mode="$(normalize_mode "${NET_MODE:-sta}")"
  NET_MODE="$mode"
  if [[ "$mode" == "hotspot" ]]; then
    apply_hotspot_defaults
  fi
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
  local m out sp
  m="$(normalize_mode "$NET_MODE")"
  out="ExecStart="
  sp=""
  local -a argv=("$INSTALL_PATH" run --mode "$m")
  if [[ "$m" == "sta" ]]; then
    argv+=(--ssid "$SSID" --psk "$PSK")
  fi
  [[ -n "$IFACE" ]] && argv+=(--iface "$IFACE")
  local a
  for a in "${argv[@]}"; do
    out+="${sp}$(escape_systemd_exec_arg "$a")"
    sp=" "
  done
  printf '%s\n' "$out"
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

connection_delete() {
  local name="$1"
  [[ -n "$name" ]] || return 0
  if connection_exists "$name"; then
    log "删除 NM 连接: $name"
    nmcli connection delete "$name" 2>/dev/null || true
  fi
}

# install-timer 专用：删除 NetworkManager 中所有已保存的 WiFi（802-11-wireless）连接。
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
  local dev="$1"
  apply_hotspot_defaults
  log "STA run：${STA_FAIL_TIMEOUT_SEC}s 内未连上目标 WiFi，开启临时热点 SSID=$SSID 密码=${HOTSPOT_PSK}（不修改 systemd/agent，重启仍优先 STA）"
  nmcli device disconnect "$dev" 2>/dev/null || true
  nmcli connection down Hotspot 2>/dev/null || true
  sleep 2
  ensure_radio "$dev"
  hotspot_start_device "$dev" || die "临时热点启动失败"
  log "STA run：临时热点已开启 SSID=$SSID"
  exit 0
}

# run 专用：在 STA_FAIL_TIMEOUT_SEC 内轮询连接，超时则回退热点（主机名 + Arenatech0502）。
run_sta_watchdog() {
  local dev deadline
  dev="$(wifi_device)"
  [[ -n "$dev" ]] || die "未发现 WiFi 网卡"
  [[ -n "$SSID" ]] || die "STA 需要 SSID"

  if wifi_connected_to_ssid "$dev" "$SSID"; then
    log "STA run：已连接目标 $SSID，跳过"
    return 0
  fi

  ensure_radio "$dev"
  if wifi_connected_to_ssid "$dev" "$SSID"; then
    log "STA run：已连接目标 $SSID，跳过"
    return 0
  fi

  log "STA run：${STA_FAIL_TIMEOUT_SEC}s 内尝试连接 $SSID，失败则回退热点"
  deadline=$(( $(date +%s) + STA_FAIL_TIMEOUT_SEC ))
  while (( $(date +%s) < deadline )); do
    if wifi_connected_to_ssid "$dev" "$SSID"; then
      log "STA run：已连接 $SSID"
      return 0
    fi
    if sta_try_connect_once "$dev"; then
      log "STA run：已连接 $SSID"
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
  log "热点已启动"
}

# install-timer 专用：快速连网一次，不回退热点（回退仅由 run 负责）。
apply_sta_now() {
  local dev
  dev="$(wifi_device)"
  [[ -n "$dev" ]] || die "未发现 WiFi 网卡"
  [[ -n "$SSID" ]] || die "STA 需要 SSID"
  log "STA apply：断开 $dev 后尝试连接 $SSID"
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
  die "STA apply：连接 $SSID 失败（回退热点由定时 run 处理）"
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

apply_wifi_now() {
  resolve_mode_config
  log "install-timer apply：模式=$NET_MODE SSID=${SSID:-—}"
  if [[ "$NET_MODE" == "hotspot" ]]; then
    apply_hotspot_now
  else
    apply_sta_now
  fi
}

run_watchdog() {
  resolve_mode_config
  log "WiFi run 模式=$NET_MODE SSID=${SSID:-—}"
  if [[ "$NET_MODE" == "hotspot" ]]; then
    run_hotspot_watchdog
  else
    run_sta_watchdog
  fi
}

run_watchdog_cmd() {
  parse_wifi_flags "$@"
  [[ -n "$NET_MODE" ]] || die "run 需要 --mode"
  run_watchdog
}

install_timer() {
  require_root
  need_cmd systemctl nmcli

  [[ -n "$NET_MODE" ]] || die "install-timer 需要 --mode"
  resolve_mode_config

  local hm
  hm="$(normalize_mode "$NET_MODE")"
  if [[ "$hm" == "sta" ]]; then
    [[ -n "$SSID" ]] || die "install-timer STA 需要 --ssid"
  fi

  delete_all_saved_wifi_connections

  local src
  src="$(readlink -f "$0")"
  [[ -f "$src" ]] || die "无法解析脚本路径"

  mkdir -p "$(dirname "$INSTALL_PATH")"
  if [[ ! -f "$INSTALL_PATH" ]] || [[ "$src" != "$(readlink -f "$INSTALL_PATH" 2>/dev/null || true)" ]]; then
    install -m 750 -o root -g root "$src" "$INSTALL_PATH"
    log "已安装脚本: $INSTALL_PATH"
  fi

  local exec_line
  exec_line="$(build_watchdog_execstart)"

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
  log "已启用 ${UNIT_BASE}.timer（模式=$hm SSID=${SSID:-hostname}）"

  if [[ "$SKIP_APPLY" != "1" ]]; then
    apply_wifi_now
  fi
}

uninstall_timer() {
  require_root
  need_cmd systemctl
  systemctl disable --now "${UNIT_BASE}.timer" 2>/dev/null || true
  rm -f "/etc/systemd/system/${UNIT_BASE}.timer" "/etc/systemd/system/${UNIT_BASE}.service"
  systemctl daemon-reload
  log "已移除 ${UNIT_BASE} systemd 单元"
}

main() {
  local cmd
  [[ $# -ge 1 ]] || die "需要子命令（install-timer / run / uninstall-timer / …）"
  [[ "$1" != -* ]] || die "第一个参数须为子命令"
  cmd="$1"
  shift

  case "$cmd" in
    -h | --help | help) usage ;;
    run) run_watchdog_cmd "$@" ;;
    install-timer)
      parse_wifi_flags "$@"
      install_timer
      ;;
    uninstall-timer)
      parse_wifi_flags "$@"
      uninstall_timer
      ;;
    *)
      die "未知命令: $cmd"
      ;;
  esac
}

main "$@"
