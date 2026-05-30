#!/usr/bin/env bash
# 在 Raspberry Pi OS / Debian 上安装 SDL2 + SDL2_mixer 开发包（构建 CGO 所需）。
set -euo pipefail
sudo apt-get update
sudo apt-get install -y \
  build-essential \
  pkg-config \
  libsdl2-dev \
  libsdl2-mixer-dev

# Raspberry Pi：在 /boot/firmware/config.txt 中追加或调整 USB 电流、RTC 充电、SPI、UART0（已存在则跳过，SPI 若为 off 则改为 on）
RPI_FIRMWARE_CONFIG="/boot/firmware/config.txt"
append_config_txt_line_if_missing() {
  local line="$1"
  if [[ ! -f "$RPI_FIRMWARE_CONFIG" ]]; then
    echo "WARN: $RPI_FIRMWARE_CONFIG 不存在，跳过 config.txt 修改" >&2
    return 0
  fi
  if sudo grep -qFx "$line" "$RPI_FIRMWARE_CONFIG" 2>/dev/null; then
    return 0
  fi
  printf '%s\n' "$line" | sudo tee -a "$RPI_FIRMWARE_CONFIG" >/dev/null
}
append_config_txt_line_if_missing "usb_max_current_enable=1"
append_config_txt_line_if_missing "dtparam=rtc_bbat_vchg=3900000"

# SPI：若存在 dtparam=spi=off 则改为 on；否则在没有 spi 行时追加 dtparam=spi=on
ensure_spi_on_in_config_txt() {
  if [[ ! -f "$RPI_FIRMWARE_CONFIG" ]]; then
    echo "WARN: $RPI_FIRMWARE_CONFIG 不存在，跳过 SPI 配置" >&2
    return 0
  fi
  if sudo grep -qFx "dtparam=spi=off" "$RPI_FIRMWARE_CONFIG" 2>/dev/null; then
    sudo sed -i 's/^dtparam=spi=off$/dtparam=spi=on/' "$RPI_FIRMWARE_CONFIG"
  fi
  append_config_txt_line_if_missing "dtparam=spi=on"
}
ensure_spi_on_in_config_txt

# 串口 UART0（与 raspi-config 开启串口硬件等效）
append_config_txt_line_if_missing "dtparam=uart0=on"

# 自动检测 ALSA 播放声卡：优先 USB，其次非 HDMI；可用环境变量 ALSA_CARD 强制指定。
detect_alsa_playback_card() {
  if [[ -n "${ALSA_CARD:-}" ]]; then
    echo "$ALSA_CARD"
    return 0
  fi

  local cards_file="/proc/asound/cards"
  if [[ ! -r "$cards_file" ]]; then
    echo "WARN: 无法读取 $cards_file，回退 card 0" >&2
    echo "0"
    return 0
  fi

  local usb_card="" first_non_hdmi=""
  while IFS= read -r line; do
    if [[ "$line" =~ ^[[:space:]]*([0-9]+)[[:space:]]+\[([^]]+)\][[:space:]]*:[[:space:]]*(.+)$ ]]; then
      local num="${BASH_REMATCH[1]}"
      local id="${BASH_REMATCH[2]}"
      local desc="${BASH_REMATCH[3]}"
      local combined="${id} ${desc}"
      if [[ "$combined" =~ [Uu][Ss][Bb] ]]; then
        usb_card="$num"
        break
      fi
      if [[ ! "$combined" =~ vc4hdmi|hdmi|HDMI ]]; then
        if [[ -z "$first_non_hdmi" ]]; then
          first_non_hdmi="$num"
        fi
      fi
    fi
  done < "$cards_file"

  if [[ -n "$usb_card" ]]; then
    echo "$usb_card"
  elif [[ -n "$first_non_hdmi" ]]; then
    echo "$first_non_hdmi"
  else
    echo "WARN: 未找到 USB/非 HDMI 声卡，回退 card 0（可用 aplay -l 查看后设置 ALSA_CARD=N 重跑）" >&2
    echo "0"
  fi
}

write_asoundrc() {
  local card="$1"
  local asoundrc="${HOME}/.asoundrc"
  cat > "$asoundrc" <<EOF
pcm.!default {
  type plug
  slave.pcm "hw:${card},0"
}
ctl.!default {
  type hw
  card ${card}
}
EOF
  echo "已写入 ${asoundrc}，默认 ALSA 播放设备: hw:${card},0"
}

ALSA_CARD="$(detect_alsa_playback_card)"
export ALSA_CARD ALSA_PCM_CARD="$ALSA_CARD" ALSA_CTL_CARD="$ALSA_CARD"
write_asoundrc "$ALSA_CARD"
echo "ALSA 环境变量: ALSA_CARD=${ALSA_CARD}（systemd 服务若需显式指定，可在 [Service] 中加 Environment=ALSA_CARD=${ALSA_CARD}）"