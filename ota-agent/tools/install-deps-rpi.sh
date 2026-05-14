#!/usr/bin/env bash
# 在 Raspberry Pi OS / Debian 上安装 SDL2 + SDL2_mixer 开发包（构建 CGO 所需）。
set -euo pipefail
sudo apt-get update
sudo apt-get install -y \
  build-essential \
  pkg-config \
  libsdl2-dev \
  libsdl2-mixer-dev

sudo amixer set PCM 255

# Raspberry Pi：在 /boot/firmware/config.txt 末尾追加 USB 电流与 RTC 充电参数（已存在则跳过）
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

export ALSA_CARD=2
export ALSA_PCM_CARD=2
export ALSA_CTL_CARD=2

cat > ~/.asoundrc <<'EOF'
pcm.!default {
  type plug
  slave.pcm "hw:2,0"
}
ctl.!default {
  type hw
  card 2
}
EOF
