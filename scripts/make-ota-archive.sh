#!/usr/bin/env bash
# 生成 ota-agent 可自动解压的压缩包：标准 gzip 压缩的 tar（.tar.gz / .tgz）
# 解压时会把包内「相对路径」展开到配置的 target 目录下，请勿使用 ../ 恶意路径。
#
# 用法:
#   ./scripts/make-ota-archive.sh <打包源目录> [输出路径.tar.gz]
#   ./scripts/make-ota-archive.sh ./my-payload                    # 默认输出到当前目录 my-payload.tar.gz
#   ./scripts/make-ota-archive.sh ./my-payload ./dist/bundle.tar.gz
#
# 可选环境变量 PRINT_SHA256=1 时在末尾打印 sha256，便于填入 OTA 配置的 files[].sha256

set -euo pipefail

SRC="${1:?用法: $0 <打包源目录> [输出.tar.gz]}"
OUT="${2:-}"

if [[ ! -d "$SRC" ]]; then
  echo "错误: 不是目录: $SRC" >&2
  exit 1
fi

SRC_ABS="$(cd "$SRC" && pwd)"
if [[ -z "$OUT" ]]; then
  OUT="$(basename "$SRC_ABS").tar.gz"
fi
OUT_ABS="$(cd "$(dirname "$OUT")" && pwd)/$(basename "$OUT")"

mkdir -p "$(dirname "$OUT_ABS")"
tar -czf "$OUT_ABS" -C "$SRC_ABS" .

echo "已生成: $OUT_ABS"
if [[ "${PRINT_SHA256:-}" == "1" ]]; then
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$OUT_ABS"
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$OUT_ABS"
  else
    echo "未找到 sha256sum/shasum，跳过校验输出" >&2
  fi
fi
