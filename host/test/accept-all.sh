#!/bin/sh
# 全套验收：14/15/16 普通各 N 台 + 谷歌各 M 台（直连），滚动分批、测完删净。在板子上 root 跑：
#   sh host/test/accept-all.sh            # 默认普通 6 台、谷歌 4 台
#   sh host/test/accept-all.sh 10 8       # 普通 10、谷歌 8
# 每版结果 /mmc/pp-build/accept-v<版本>[g].tsv，总日志 /mmc/pp-build/accept-all.log（每版末尾有汇总）。
set -u
N=${1:-6}; M=${2:-4}
here=$(cd "$(dirname "$0")" && pwd)
L=/mmc/pp-build/accept-all.log; : > "$L"
for spec in "14 $N 0" "15 $N 0" "16 $N 0" "14 $M 1" "15 $M 1" "16 $M 1"; do
  set -- $spec
  echo "===== 安卓$1 $([ "$3" = 1 ] && echo 谷歌版 || echo 普通版) $2 台 =====" | tee -a "$L"
  sh "$here/accept.sh" "$1" "$2" 6 "$3" 2>&1 | tee -a "$L" | grep -E "台数=|唯一|通过|没全过"
done
echo "剩余容器：$(docker ps -aq --filter name=pp- | wc -l)" | tee -a "$L"
