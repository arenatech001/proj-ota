#!/usr/bin/env bash
# 在 Raspberry Pi OS / Debian 上安装 SDL2 + SDL2_mixer 开发包（构建 CGO 所需）。
sudo dpkg --configure -a
sudo apt-get install -y \
  build-essential \
  pkg-config \
  libsdl2-dev \
  libsdl2-mixer-dev

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