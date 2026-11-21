#!/usr/bin/env bash
# 打给板子用的管家安装包：编译管理页 + ARM64 管家 → tools/dist/prism-phone-agent-arm64.gz 和 .sha256
# （板子 host/board/install.sh 下载后按 .sha256 校验）。
# 用法：先提交代码，再 bash agent/build-dist.sh，然后 git add -f tools/dist/prism-phone-agent-arm64.gz* 再提交一次
# （tools/dist 在 .gitignore 里，所以要 -f；先提交代码再打包，版本号才是干净的提交号、不带 -dirty）。
set -euo pipefail
cd "$(dirname "$0")/.."
(cd web && npm ci --silent && npm run build >/dev/null)
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
bash agent/build.sh "$tmp"
mkdir -p tools/dist
gzip -9 -n -c "$tmp" > tools/dist/prism-phone-agent-arm64.gz
(cd tools/dist && sha256sum prism-phone-agent-arm64.gz > prism-phone-agent-arm64.gz.sha256)
echo "已生成 tools/dist/prism-phone-agent-arm64.gz（$(cat tools/dist/prism-phone-agent-arm64.gz.sha256 | cut -c1-16)…）"
