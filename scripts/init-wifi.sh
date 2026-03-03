#!/bin/bash
# WiFi 初始化脚本：根据用户选择创建热点或接入已有 WiFi
#
# 用法：sudo ./init-network.sh

set -e

red='\033[0;31m'
green='\033[0;32m'
yellow='\033[1;33m'
nc='\033[0m'
info()  { echo -e "${green}[INFO]${nc} $1"; }
warn()  { echo -e "${yellow}[WARN]${nc} $1"; }
err()   { echo -e "${red}[ERROR]${nc} $1"; }

[[ $EUID -eq 0 ]] || { err "请使用 sudo 运行本脚本"; exit 1; }

# ========== WiFi 配置 ==========
setup_wifi() {
    echo ""
    info "请选择 WiFi 模式："
    echo "  1) 创建热点 (AP / Hotspot)"
    echo "  2) 接入已有 WiFi (Station)"
    echo "  3) 跳过 WiFi 配置"
    read -p "请输入选项 [1/2/3]: " choice

    case "$choice" in
        1) setup_hotspot ;;
        2) setup_station ;;
        3) info "已跳过 WiFi 配置。" ;;
        *)
            err "无效选项，已跳过 WiFi 配置。"
            ;;
    esac
}

setup_hotspot() {
    info "配置 WiFi 热点..."
    if ! command -v nmcli &>/dev/null; then
        err "需要 NetworkManager (nmcli)，请先安装。"
        return 1
    fi
    read -p "请输入热点名称 (SSID): " ap_ssid
    read -sp "请输入热点密码 (至少 8 位): " ap_pass
    echo ""
    [[ ${#ap_pass} -lt 8 ]] && { err "密码至少 8 位"; return 1; }
    nmcli connection delete "$ap_ssid" 2>/dev/null || true
    if nmcli device wifi hotspot ssid "$ap_ssid" password "$ap_pass" ifname wlan0 2>/dev/null; then
        info "热点已创建: SSID=$ap_ssid"
    else
        nmcli connection add type wifi ifname wlan0 con-name "$ap_ssid" autoconnect yes ssid "$ap_ssid" \
            ipv4.method shared wifi-sec.key-mgmt wpa-psk wifi-sec.psk "$ap_pass" 2>/dev/null
        nmcli connection up "$ap_ssid" && info "热点已创建: SSID=$ap_ssid" || err "创建热点失败。"
    fi
}

setup_station() {
    info "配置接入已有 WiFi..."
    if ! command -v nmcli &>/dev/null; then
        err "需要 NetworkManager (nmcli)，请先安装。"
        return 1
    fi
    echo ""
    nmcli device wifi list 2>/dev/null || true
    echo ""
    read -p "请输入要连接的 WiFi 名称 (SSID): " sta_ssid
    read -sp "请输入 WiFi 密码: " sta_pass
    echo ""
    if nmcli device wifi connect "$sta_ssid" password "$sta_pass" 2>/dev/null; then
        info "已连接到 WiFi: $sta_ssid"
    else
        err "连接失败，请检查 SSID、密码及 wlan0。"
        return 1
    fi
}

# ========== 主流程 ==========
main() {
    echo "=========================================="
    echo "  WiFi 初始化脚本"
    echo "=========================================="
    echo ""
    setup_wifi
    info "脚本执行完毕。"
}

main "$@"
