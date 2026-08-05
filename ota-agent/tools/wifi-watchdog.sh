#!/bin/bash
#
# 定时看门狗：读取 agent.yaml，检查 STA 是否已连上目标 WiFi；
# 超时未连上则临时开热点（SSID=主机名），不修改 agent.yaml，下次启动仍优先 STA。
# 不负责 Web「立即改网」——请用 wifi-apply.sh。
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=wifi-common.sh
source "${SCRIPT_DIR}/wifi-common.sh"

LOG_TAG="wifi-watchdog"
CONFIG_PATH=""
INSTALL_PATH=""

readonly UNIT_BASE="wifi-watchdog"
readonly TIMER_ON_BOOT_SEC=60
readonly TIMER_INTERVAL_SEC=300

usage() {
  cat <<'EOF'
用法:
  wifi-watchdog.sh run --config PATH
  wifi-watchdog.sh install --config PATH [--install-path PATH]
  wifi-watchdog.sh uninstall

说明:
  run（定时器调用）:
    - wifi_mode=sta：若未连上 wifi_ssid，则在约 3 分钟内重试；仍失败则临时开热点（不改 yaml）
    - wifi_mode=hotspot：确保热点在跑（SSID=主机名）
  install：仅注册 systemd 定时器（开机 60s、之后每 5 分钟 run），不立即改网。
  立即改网请用: wifi-apply.sh apply --config PATH
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
        # 兼容旧调用：install 已不再 apply，忽略
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

sta_fallback_to_hotspot() {
  local dev="$1" sta_target="$SSID" ap_ssid
  ap_ssid="$(hotspot_ssid)"
  log "STA：${STA_FAIL_TIMEOUT_SEC}s 内未连上 ${sta_target}，开启临时热点 SSID=$ap_ssid（不改 agent.yaml，重启仍优先 STA）"
  hotspot_start_device "$dev" || die "临时热点启动失败"
  log "临时热点已开启 SSID=$ap_ssid"
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
  log "STA：${STA_FAIL_TIMEOUT_SEC}s 内尝试连接 $SSID，失败则临时热点"
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
  local dev ap_ssid
  dev="$(wifi_device)"
  [[ -n "$dev" ]] || die "未发现 WiFi 网卡"
  ap_ssid="$(hotspot_ssid)"
  ensure_radio "$dev"
  if wifi_connected_to_ssid "$dev" "$ap_ssid"; then
    log "热点已运行 SSID=$ap_ssid，跳过"
    return 0
  fi
  hotspot_start_device "$dev" || die "热点启动失败"
  log "热点已启动 SSID=$ap_ssid"
}

run_watchdog() {
  require_root
  need_cmd nmcli
  resolve_config_path
  load_network_config "$CONFIG_PATH"
  if [[ "$NET_MODE" == "hotspot" ]]; then
    log "WiFi run 配置=$CONFIG_PATH 模式=hotspot 热点SSID=$(hotspot_ssid)"
    run_hotspot_watchdog
  else
    log "WiFi run 配置=$CONFIG_PATH 模式=sta SSID=${SSID:-—}"
    run_sta_watchdog
  fi
}

install_service() {
  require_root
  need_cmd systemctl nmcli

  resolve_config_path
  resolve_install_path
  load_network_config "$CONFIG_PATH"

  if [[ "$NET_MODE" == "sta" && -z "$SSID" ]]; then
    die "STA 模式需要 network.wifi_ssid（可先在管理页保存 WiFi，或 yaml 中填写后再 install）"
  fi

  local src
  src="$(readlink -f "$0" 2>/dev/null || realpath "$0" 2>/dev/null || echo "$0")"
  [[ -f "$src" ]] || die "无法解析脚本路径"

  if [[ "$src" != "$INSTALL_PATH" ]]; then
    mkdir -p "$(dirname "$INSTALL_PATH")"
    install -m 750 -o root -g root "$src" "$INSTALL_PATH"
    # 同目录的 wifi-common.sh 一并安装
    local common_src common_dst
    common_src="$(dirname "$src")/wifi-common.sh"
    common_dst="$(dirname "$INSTALL_PATH")/wifi-common.sh"
    if [[ -f "$common_src" ]]; then
      install -m 640 -o root -g root "$common_src" "$common_dst"
    fi
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
  log "已启用 ${UNIT_BASE}.timer（仅定时检查；立即改网请用 wifi-apply.sh）配置=$CONFIG_PATH 模式=$NET_MODE"
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
