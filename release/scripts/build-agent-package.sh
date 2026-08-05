#!/usr/bin/env bash
# 组装 release/agent 发布目录：仅拷贝文件、dos2unix 脚本、设置可执行权限。
# 不编译、不改配置内容、不拷贝任何日志文件。
#
# 用法（在仓库根目录或任意目录）：
#   bash release/scripts/build-agent-package.sh
#
# 依赖：事先已构建
#   - proj-a01/dist/client/bin/client-arm64.bin
#   - proj-a01/dist/server/bin/server-arm64.bin
#   - proj-ota/ota-agent/agent（linux/arm64 ota-agent 二进制，命名为 agent）

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"	

PROJ_A01="${REPO_ROOT}/proj-a01"
PROJ_OTA_AGENT="${REPO_ROOT}/proj-ota/ota-agent"
OUT="${REPO_ROOT}/proj-ota/release/agent"

CLIENT_DIST="${PROJ_A01}/dist/client"
SERVER_DIST="${PROJ_A01}/dist/server"
AGENT_YAML_TEMPLATE="${OUT}/config/agent.yaml"

die() {
	echo "ERROR: $*" >&2
	exit 1
}

require_file() {
	[[ -f "$1" ]] || die "缺少文件: $1"
}

require_dir() {
	[[ -d "$1" ]] || die "缺少目录: $1"
}

dos2unix_file() {
	local f
	for f in "$@"; do
		[[ -f "$f" ]] || continue
		if command -v dos2unix >/dev/null 2>&1; then
			dos2unix "$f" >/dev/null 2>&1 || true
		else
			sed -i 's/\r$//' "$f" 2>/dev/null || {
				local tmp
				tmp="$(mktemp)"
				tr -d '\r' <"$f" >"$tmp"
				mv "$tmp" "$f"
			}
		fi
	done
}

echo "==> 发布目录: ${OUT}"

require_file "${CLIENT_DIST}/bin/client-arm64.bin"
require_file "${CLIENT_DIST}/config/client.yaml"
require_file "${SERVER_DIST}/bin/server-arm64.bin"
require_file "${SERVER_DIST}/config/server.yaml"
require_file "${SERVER_DIST}/config/battle_config.json"
require_file "${PROJ_OTA_AGENT}/agent.yaml"
require_file "${PROJ_OTA_AGENT}/agent-arm64.bin"
require_file "${PROJ_OTA_AGENT}/tools/wifi-common.sh"
require_file "${PROJ_OTA_AGENT}/tools/wifi-watchdog.sh"
require_file "${PROJ_OTA_AGENT}/tools/wifi-apply.sh"
require_file "${PROJ_OTA_AGENT}/tools/install-deps-rpi.sh"
require_file "${PROJ_OTA_AGENT}/tools/init-eth0.sh"
require_file "${PROJ_OTA_AGENT}/tools/init-system-pi2.sh"
require_file "${PROJ_OTA_AGENT}/tools/bluetooth-gamepad.sh"

mkdir -p "${OUT}/bin" "${OUT}/config" "${OUT}/tools" "${OUT}/logs"

echo "==> 拷贝 ota-agent"
install -m 755 "${PROJ_OTA_AGENT}/agent-arm64.bin" "${OUT}/bin/agent-arm64.bin"

echo "==> 拷贝 agent.yaml（模板: release/agent/config/agent.yaml）"
install -m 644 "${PROJ_OTA_AGENT}/agent.yaml" "${OUT}/config/agent.yaml"

echo "==> 拷贝 client / server 二进制与配置"
install -m 755 "${CLIENT_DIST}/bin/client-arm64.bin" "${OUT}/bin/client-arm64.bin"
install -m 644 "${CLIENT_DIST}/config/client.yaml" "${OUT}/config/client.yaml"
install -m 755 "${SERVER_DIST}/bin/server-arm64.bin" "${OUT}/bin/server-arm64.bin"
install -m 644 "${SERVER_DIST}/config/server.yaml" "${OUT}/config/server.yaml"
install -m 644 "${SERVER_DIST}/config/battle_config.json" "${OUT}/config/battle_config.json"

echo "==> 拷贝 client/assets（跳过 logs 与 *.log）"
if [[ -d "${CLIENT_DIST}/assets" ]]; then
	rm -rf "${OUT}/assets"
	cp -a "${CLIENT_DIST}/assets" "${OUT}/assets"
else
	echo "WARN: 未找到 ${CLIENT_DIST}/assets，跳过"
fi

echo "==> 拷贝 tools"
install -m 644 "${PROJ_OTA_AGENT}/tools/wifi-common.sh" "${OUT}/tools/wifi-common.sh"
install -m 755 "${PROJ_OTA_AGENT}/tools/wifi-watchdog.sh" "${OUT}/tools/wifi-watchdog.sh"
install -m 755 "${PROJ_OTA_AGENT}/tools/wifi-apply.sh" "${OUT}/tools/wifi-apply.sh"
install -m 755 "${PROJ_OTA_AGENT}/tools/install-deps-rpi.sh" "${OUT}/tools/install-deps-rpi.sh"
install -m 755 "${PROJ_OTA_AGENT}/tools/init-eth0.sh" "${OUT}/tools/init-eth0.sh"
install -m 755 "${PROJ_OTA_AGENT}/tools/init-system-pi2.sh" "${OUT}/tools/init-system-pi2.sh"
install -m 755 "${PROJ_OTA_AGENT}/tools/bluetooth-gamepad.sh" "${OUT}/tools/bluetooth-gamepad.sh"

echo "==> dos2unix 脚本"
dos2unix_file \
	"${OUT}/tools/wifi-common.sh" \
	"${OUT}/tools/wifi-watchdog.sh" \
	"${OUT}/tools/wifi-apply.sh" \
	"${OUT}/tools/install-deps-rpi.sh" \
	"${OUT}/tools/init-eth0.sh" \
	"${OUT}/tools/init-system-pi2.sh" \
	"${OUT}/tools/bluetooth-gamepad.sh"

echo "==> 设置可执行权限"
chmod 755 "${OUT}/bin/agent-arm64.bin" "${OUT}/bin/client-arm64.bin" "${OUT}/bin/server-arm64.bin"
chmod 755 "${OUT}/tools/"*.sh

echo "==> 清理发布目录中的日志文件（不打包日志）"
find "${OUT}" \( -type d -name logs \) ! -path "${OUT}/logs" -exec rm -rf {} + 2>/dev/null || true
find "${OUT}" -type f -name '*.log' -delete 2>/dev/null || true
# 保留空 logs/ 供运行时写入
mkdir -p "${OUT}/logs"
touch "${OUT}/version"

echo "==> 完成: ${OUT}"
echo "    可选打 zip: (cd release && zip -r agent_$(date +%Y%m%d).zip agent)"
