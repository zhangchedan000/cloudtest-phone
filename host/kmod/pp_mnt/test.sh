#!/usr/bin/env bash
# pp_mnt 改写规则的用户态测试（不用装模块）：bash host/kmod/pp_mnt/test.sh
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
gcc -std=gnu11 -Wall -Wextra -Werror -Wno-unused-function -O1 -fsanitize=address,undefined -o "$W/t" "$here/test_rewrite.c"
"$W/t"
