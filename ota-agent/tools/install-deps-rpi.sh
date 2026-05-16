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