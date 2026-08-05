#!/usr/bin/env bash
# 蓝牙游戏手柄：列出已配对、扫描可配对、配对、取消配对（bluetoothctl / BlueZ）。
# 配对逻辑参考 proj-a01/cmd/gamepad-tools/scripts/connect-bt.sh。
# 需 root；由 ota-agent 管理页经 sudoers 调用。
# 机器可读结果仅写 stdout（JSON）；日志写 stderr。
set -euo pipefail

CMD_TIMEOUT="${CMD_TIMEOUT:-8}"
PAIR_TIMEOUT="${PAIR_TIMEOUT:-30}"
CONNECT_TIMEOUT="${CONNECT_TIMEOUT:-15}"
VISIBLE_SCAN_SEC="${VISIBLE_SCAN_SEC:-25}"
# 扫描结果名称过滤（不区分大小写）；可用环境变量覆盖，如 NAME_FILTER= 显示全部
NAME_FILTER="${NAME_FILTER:-GameSir}"

log() { printf '%s %s\n' "$(date -Is)" "$*" >&2; }
die() { log "ERROR: $*"; exit 1; }

require_root() {
  [[ "$(id -u)" -eq 0 ]] || die "请使用 root 运行（sudo）"
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "缺少命令: $1（请安装 bluez）"
}

valid_mac() {
  [[ "$1" =~ ^([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}$ ]]
}

normalize_mac() {
  echo "$1" | tr '[:lower:]' '[:upper:]'
}

json_escape() {
  local s="$1"
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  s="${s//$'\n'/\\n}"
  s="${s//$'\r'/}"
  printf '%s' "$s"
}

# 带超时的 bluetoothctl，避免某条命令卡住整脚本。
# 注意：不要调用 default-agent（会常驻阻塞）。
bt() {
  if command -v timeout >/dev/null 2>&1; then
    timeout --signal=KILL "$CMD_TIMEOUT" bluetoothctl "$@" 2>/dev/null
  else
    bluetoothctl "$@" 2>/dev/null
  fi
}

bt_loud() {
  if command -v timeout >/dev/null 2>&1; then
    timeout --signal=KILL "$CMD_TIMEOUT" bluetoothctl "$@"
  else
    bluetoothctl "$@"
  fi
}

ensure_bluetooth_service() {
  if command -v systemctl >/dev/null 2>&1; then
    if ! systemctl is-active --quiet bluetooth 2>/dev/null; then
      log "bluetooth 服务未运行，尝试启动…"
      systemctl start bluetooth 2>/dev/null || true
      sleep 1
    fi
  fi
}

ensure_adapter() {
  ensure_bluetooth_service
  log "开启蓝牙适配器（跳过 default-agent，避免卡住）"
  bt power on >/dev/null || true
  # NoInputNoOutput：手柄配对无需 PIN 输入
  bt agent NoInputNoOutput >/dev/null || bt agent on >/dev/null || true
  bt pairable on >/dev/null || true
}

device_known() {
  local mac="$1"
  bluetoothctl devices 2>/dev/null | grep -qi "$mac"
}

# 确保 MAC 在 bluez 缓存中可见（remove 后必须先 scan，否则会 not available）。
ensure_device_visible() {
  local mac="$1"
  local wait_sec="${2:-$VISIBLE_SCAN_SEC}"
  local force_scan="${3:-0}"

  if [[ "$force_scan" -ne 1 ]] && device_known "$mac"; then
    log "设备已在缓存中: $mac"
    return 0
  fi

  log "扫描 ${wait_sec}s 以发现 $mac（请保持手柄配对模式 / 灯快闪）"
  if command -v timeout >/dev/null 2>&1; then
    timeout --signal=INT "$wait_sec" bluetoothctl --timeout "$wait_sec" scan on 2>&1 \
      | grep -E "NEW|CHG.*${mac}|Discovery|Failed|Device ${mac}" >&2 || true
  else
    bluetoothctl scan on >/dev/null 2>&1 &
    local scan_pid=$!
    sleep "$wait_sec"
    kill -INT "$scan_pid" 2>/dev/null || true
    wait "$scan_pid" 2>/dev/null || true
  fi
  bluetoothctl scan off >/dev/null 2>&1 || true

  if device_known "$mac"; then
    log "已发现: $mac"
    bluetoothctl devices 2>/dev/null | grep -i "$mac" >&2 || true
    return 0
  fi

  log "当前 devices:"
  bluetoothctl devices >&2 || true
  return 1
}

# 解析 "Device AA:BB:CC:DD:EE:FF Name..." 行 → JSON 对象数组
devices_lines_to_json() {
  local first=1
  printf '['
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "$line" ]] && continue
    if [[ "$line" =~ ^Device[[:space:]]+([0-9A-Fa-f:]{17})([[:space:]]+(.*))?$ ]]; then
      local addr
      addr="$(normalize_mac "${BASH_REMATCH[1]}")"
      local name="${BASH_REMATCH[3]:-}"
      name="$(echo -n "$name" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
      [[ -z "$name" ]] && name="$addr"
      if [[ $first -eq 0 ]]; then
        printf ','
      fi
      first=0
      printf '{"address":"%s","name":"%s"}' "$(json_escape "$addr")" "$(json_escape "$name")"
    fi
  done
  printf ']\n'
}

run_scan_for() {
  local sec="$1"
  log "扫描 ${sec}s（请将手柄置于配对模式）…"
  if command -v timeout >/dev/null 2>&1; then
    timeout --signal=INT "$((sec + 2))" bluetoothctl --timeout "$sec" scan on >/dev/null 2>&1 \
      || timeout --signal=INT "$sec" bluetoothctl scan on >/dev/null 2>&1 \
      || true
  else
    bluetoothctl scan on >/dev/null 2>&1 &
    local scan_pid=$!
    sleep "$sec"
    kill "$scan_pid" 2>/dev/null || true
    wait "$scan_pid" 2>/dev/null || true
  fi
  bt scan off >/dev/null || true
}

cmd_list_paired() {
  ensure_adapter
  local out=""
  if out="$(bt devices Paired)"; then
    :
  elif out="$(bt paired-devices)"; then
    :
  else
    out=""
  fi
  printf '%s\n' "$out" | devices_lines_to_json
}

cmd_scan() {
  local sec="${1:-10}"
  if ! [[ "$sec" =~ ^[0-9]+$ ]] || [[ "$sec" -lt 3 ]] || [[ "$sec" -gt 60 ]]; then
    die "扫描秒数须为 3–60 的整数，当前: $sec"
  fi
  ensure_adapter
  run_scan_for "$sec"

  local paired_addrs=""
  local pout=""
  if pout="$(bt devices Paired)"; then
    :
  elif pout="$(bt paired-devices)"; then
    :
  else
    pout=""
  fi
  while IFS= read -r line || [[ -n "$line" ]]; do
    if [[ "$line" =~ Device[[:space:]]+([0-9A-Fa-f:]{17}) ]]; then
      paired_addrs="${paired_addrs} $(normalize_mac "${BASH_REMATCH[1]}")"
    fi
  done <<< "$pout"

  local all
  all="$(bt devices || true)"
  local first=1
  local matched=0 skipped=0
  printf '['
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "$line" ]] && continue
    if [[ "$line" =~ ^Device[[:space:]]+([0-9A-Fa-f:]{17})([[:space:]]+(.*))?$ ]]; then
      local addr
      addr="$(normalize_mac "${BASH_REMATCH[1]}")"
      if [[ " ${paired_addrs} " == *" ${addr} "* ]]; then
        continue
      fi
      local name="${BASH_REMATCH[3]:-}"
      name="$(echo -n "$name" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
      [[ -z "$name" ]] && name="$addr"
      # 名称过滤（默认 GameSir）；NAME_FILTER 为空则不过滤
      if [[ -n "$NAME_FILTER" ]] && ! echo "$name" | grep -qiF -- "$NAME_FILTER"; then
        skipped=$((skipped + 1))
        continue
      fi
      if [[ $first -eq 0 ]]; then
        printf ','
      fi
      first=0
      matched=$((matched + 1))
      printf '{"address":"%s","name":"%s"}' "$(json_escape "$addr")" "$(json_escape "$name")"
    fi
  done <<< "$all"
  printf ']\n'
  # 日志必须在 JSON 之后且仅 stderr；agent 用 CombinedOutput，解析时只取第一个 JSON
  if [[ -n "$NAME_FILTER" ]]; then
    log "名称过滤「${NAME_FILTER}」: 匹配 ${matched} 台，跳过 ${skipped} 台"
  fi
}

cmd_pair() {
  local mac="${1:-}"
  valid_mac "$mac" || die "无效 MAC 地址: $mac"
  mac="$(normalize_mac "$mac")"
  ensure_adapter
  log "配对目标: $mac（请保持手柄配对模式）"

  # remove 后缓存清空，必须重新 scan，否则 Device not available
  log "移除旧配对（若存在）…"
  bluetoothctl remove "$mac" >/dev/null 2>&1 || true

  ensure_device_visible "$mac" "$VISIBLE_SCAN_SEC" 1 || \
    die "扫描 ${VISIBLE_SCAN_SEC}s 后仍找不到 $mac：请确认手柄配对模式、电量、靠近主机"

  log "pair…"
  local old_to="$CMD_TIMEOUT"
  CMD_TIMEOUT="$PAIR_TIMEOUT"
  if ! bt_loud pair "$mac"; then
    if bluetoothctl info "$mac" 2>/dev/null | grep -qi 'Paired: yes'; then
      log "pair 返回非零但已 Paired: yes，继续"
    else
      log "WARN: pair 未成功（可能已配对，继续 trust/connect）"
    fi
  fi
  CMD_TIMEOUT="$old_to"

  log "trust…"
  CMD_TIMEOUT="$PAIR_TIMEOUT"
  bt_loud trust "$mac" || log "WARN: trust 失败（可忽略）"
  CMD_TIMEOUT="$old_to"

  log "connect…"
  local i connected=0
  CMD_TIMEOUT="$CONNECT_TIMEOUT"
  for i in $(seq 1 10); do
    if bt_loud connect "$mac"; then
      connected=1
      break
    fi
    log "重试 connect ($i/10)…"
    if [[ "$i" -eq 3 ]] || [[ "$i" -eq 7 ]]; then
      ensure_device_visible "$mac" 10 1 || true
    fi
    sleep 1
  done
  CMD_TIMEOUT="$old_to"

  if bluetoothctl info "$mac" 2>/dev/null | grep -qi 'Connected: yes'; then
    log "已连接: $mac"
  elif [[ "$connected" -eq 1 ]]; then
    log "WARN: connect 已返回成功，但 info 未确认 Connected: yes"
  else
    log "WARN: 未确认 Connected: yes（部分手柄配对后需再按配对键）；当前 info:"
    bt_loud info "$mac" >&2 || true
    # 仍返回 ok：配对/信任可能已完成，连接可稍后重试
  fi

  printf '{"ok":true,"address":"%s","action":"pair"}\n' "$(json_escape "$mac")"
}

cmd_remove() {
  local mac="${1:-}"
  valid_mac "$mac" || die "无效 MAC 地址: $mac"
  mac="$(normalize_mac "$mac")"
  ensure_adapter
  log "取消配对 $mac …"
  bluetoothctl disconnect "$mac" >/dev/null 2>&1 || true
  if ! bluetoothctl remove "$mac"; then
    die "remove 失败: $mac"
  fi
  printf '{"ok":true,"address":"%s","action":"remove"}\n' "$(json_escape "$mac")"
}

usage() {
  cat >&2 <<'EOF'
Usage:
  bluetooth-gamepad.sh list-paired
  bluetooth-gamepad.sh scan [seconds]
  bluetooth-gamepad.sh pair <MAC>
  bluetooth-gamepad.sh remove <MAC>

环境变量:
  NAME_FILTER   扫描名称子串过滤（默认 GameSir，不区分大小写；置空则不过滤）

配对流程（参考 connect-bt.sh）:
  remove → 强制 scan 发现设备 → pair → trust → connect（可重试）
  不使用 default-agent（会阻塞）。
EOF
}

require_root
need_cmd bluetoothctl

cmd="${1:-}"
shift || true
case "$cmd" in
  list-paired) cmd_list_paired ;;
  scan)        cmd_scan "${1:-10}" ;;
  pair)        cmd_pair "${1:-}" ;;
  remove)      cmd_remove "${1:-}" ;;
  -h|--help|help|"") usage; exit 1 ;;
  *) die "未知命令: $cmd" ;;
esac
