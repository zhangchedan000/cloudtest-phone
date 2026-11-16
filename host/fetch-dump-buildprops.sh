#!/bin/bash
# 用法：bash host/fetch-dump-buildprops.sh <owner/repo> <输出目录>（然后 python3 host/extract-buildprops.py <输出目录> ...）
# 浅克隆公开的固件转储库（不下载大文件），只取各分区 build.prop，压平成 a.b.build.prop 给 extract-buildprops.py 用
set -e
repo=$1; out=$2; tmp=$(mktemp -d)
git clone -q --filter=blob:none --no-checkout --depth 1 "https://github.com/$repo" "$tmp/r"
cd "$tmp/r"
files=$(git ls-tree -r --name-only HEAD | grep -E "(^|/)([A-Za-z0-9_]+_)?build\.prop$|^odm/etc/[^/]+\.prop$|^vendor/[^/]+_build\.prop$" | grep -vE "^(vendor_boot|boot|recovery|init_boot)/|/(lib|app|priv-app|overlay)/")
git checkout -q HEAD -- $files README.md 2>/dev/null || git checkout -q HEAD -- $files
mkdir -p "$out"
for f in $files; do cp "$f" "$out/$(echo "$f" | tr '/' '.')"; done
head -1 README.md 2>/dev/null || true
ls "$out"
rm -rf "$tmp"
