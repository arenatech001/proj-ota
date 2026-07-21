#!/usr/bin/env bash
# PI2（Armbian）系统初始化：设备节点权限 + armbianEnv.txt（串口控制台、spidev1.0）
set -euo pipefail

sudo groupadd -f gpio
sudo groupadd -f spi
sudo groupadd -f dialout

sudo tee /etc/udev/rules.d/99-arenatech-device-access.rules >/dev/null <<'EOF'
SUBSYSTEM=="gpio", KERNEL=="gpiochip*", GROUP="gpio", MODE="0660"
KERNEL=="gpiochip*", GROUP="gpio", MODE="0660"
SUBSYSTEM=="spidev", GROUP="spi", MODE="0660"
KERNEL=="spidev*", GROUP="spi", MODE="0660"
KERNEL=="ttyAMA*|ttyS*|ttyUSB*|ttyACM*", GROUP="dialout", MODE="0660"
EOF

sudo usermod -aG gpio,spi,dialout arenatech
sudo udevadm control --reload-rules
sudo udevadm trigger

# /boot/armbianEnv.txt：串口控制台 + SPI1 CS0 overlay
ARMBIAN_ENV="/boot/armbianEnv.txt"
ensure_armbian_env() {
  if [[ ! -f "$ARMBIAN_ENV" ]]; then
    echo "WARN: $ARMBIAN_ENV 不存在，跳过 Armbian 环境配置" >&2
    return 0
  fi
  # console=display -> console=serial（无该行则追加）
  if sudo grep -qE '^console=' "$ARMBIAN_ENV" 2>/dev/null; then
    sudo sed -i 's/^console=.*/console=serial/' "$ARMBIAN_ENV"
  else
    printf '\nconsole=serial\n' | sudo tee -a "$ARMBIAN_ENV" >/dev/null
  fi
  # overlays=hdmi -> overlays=spidev1_0（无该行则追加）
  if sudo grep -qE '^overlays=' "$ARMBIAN_ENV" 2>/dev/null; then
    sudo sed -i 's/^overlays=.*/overlays=spidev1_0/' "$ARMBIAN_ENV"
  else
    printf 'overlays=spidev1_0\n' | sudo tee -a "$ARMBIAN_ENV" >/dev/null
  fi
  echo "已更新 $ARMBIAN_ENV:"
  sudo grep -E '^(console|overlays)=' "$ARMBIAN_ENV" || true
  echo "注意: armbianEnv 修改需 reboot 后生效"
}
ensure_armbian_env

echo "完成。组权限需重新登录或 reboot 后生效。"
