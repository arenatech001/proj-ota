#!/usr/bin/env bash
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

# gpiochip / spidev / tty：非 root 运行 client 需要组权限
ensure_device_access_groups() {
  local user="${SUDO_USER:-${USER:-arenatech}}"
  if [[ "$user" == "root" ]]; then
    user="arenatech"
  fi
  sudo groupadd -f gpio
  sudo groupadd -f spi
  sudo groupadd -f dialout

  local rule="/etc/udev/rules.d/99-arenatech-device-access.rules"
  sudo tee "$rule" >/dev/null <<'EOF'
# Arenatech: allow gpio / spi / uart for non-root client
SUBSYSTEM=="gpio", KERNEL=="gpiochip*", GROUP="gpio", MODE="0660"
KERNEL=="gpiochip*", GROUP="gpio", MODE="0660"
SUBSYSTEM=="spidev", GROUP="spi", MODE="0660"
KERNEL=="spidev*", GROUP="spi", MODE="0660"
KERNEL=="ttyAMA*|ttyS*|ttyUSB*|ttyACM*", GROUP="dialout", MODE="0660"
EOF

  if id "$user" >/dev/null 2>&1; then
    sudo usermod -aG gpio,spi,dialout "$user"
    echo "已将用户 $user 加入组: gpio spi dialout"
  else
    echo "WARN: 用户 $user 不存在，跳过 usermod（请手动: sudo usermod -aG gpio,spi,dialout <user>）" >&2
  fi

  if command -v udevadm >/dev/null 2>&1; then
    sudo udevadm control --reload-rules 2>/dev/null || true
    sudo udevadm trigger --action=add --subsystem-match=gpio 2>/dev/null || true
    sudo udevadm trigger --action=add --subsystem-match=spidev 2>/dev/null || true
  fi
  echo "udev 规则已写入: $rule（需重新登录或 reboot 后组权限生效）"
}
ensure_device_access_groups