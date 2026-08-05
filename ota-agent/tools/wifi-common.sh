#!/bin/bash
# WiFi 公共函数（由 wifi-apply.sh / wifi-watchdog.sh source，勿直接执行）。
# shellcheck shell=bash

readonly HOTSPOT_PSK="Arenatech0502"
readonly STA_FAIL_TIMEOUT_SEC="${STA_FAIL_TIMEOUT_SEC:-180}"

LOG_TAG="${LOG_TAG:-wifi}"

log() {
  logger -t "$LOG_TAG" -- "$*" 2>/dev/null || true
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

# 热点 AP 的 SSID 固定为当前主机名（短名）；与 agent.yaml 的 wifi_ssid 无关。
hotspot_ssid() {
  local h
  h="$(hostname -s 2>/dev/null || true)"
  [[ -z "$h" ]] && h="$(hostname 2>/dev/null || echo "ota-device")"
  h="${h%%.*}"
  printf '%s' "$h"
}

wifi_device() {
  if [[ -n "${IFACE:-}" ]]; then
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
  local dev="$1" ap_ssid ap_psk out
  ap_ssid="$(hotspot_ssid)"
  ap_psk="$HOTSPOT_PSK"
  log "启动热点 SSID=$ap_ssid（主机名）"
  nmcli radio wifi on || true
  nmcli device disconnect "$dev" 2>/dev/null || true
  nmcli connection down Hotspot 2>/dev/null || true
  sleep 2
  ensure_radio "$dev"
  if ! out=$(nmcli device wifi hotspot ifname "$dev" ssid "$ap_ssid" password "$ap_psk" band bg 2>&1); then
    log "nmcli hotspot 失败: $out"
    return 1
  fi
  log "热点已就绪: $out"
  return 0
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
  log "STA apply：连接 $SSID 失败（临时热点由 wifi-watchdog 定时任务处理）"
  return 1
}

apply_hotspot_now() {
  local dev ap_ssid
  dev="$(wifi_device)"
  [[ -n "$dev" ]] || die "未发现 WiFi 网卡"
  ap_ssid="$(hotspot_ssid)"
  ensure_radio "$dev"
  log "热点 apply：SSID=$ap_ssid"
  hotspot_start_device "$dev" || die "热点启动失败"
  log "热点 apply：已启动"
}
