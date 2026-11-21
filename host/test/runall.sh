#!/bin/bash
# 压测主控:等当前测试结束,再按序跑 16普通 + gapps(14/15/16 各8台)。温度/磁盘守护。
# 配合 idtest.sh 用。改 run 行即可定制序列。在板子 /mmc/pp-build 下跑。
set -u
cd /mmc/pp-build
M=/mmc/pp-build/runall.log; : > "$M"
ml(){ echo "$(date +%H:%M:%S) [主控] $*" | tee -a "$M"; }

wait_idle(){
  while [ "$(pgrep -f idtest.sh | wc -l)" -gt 0 ]; do sleep 15; done
}
guard(){
  # 温度>80 等降温; 磁盘<30G 清 docker
  while :; do
    t=$(( $(cat /sys/class/thermal/thermal_zone0/temp)/1000 ))
    d=$(df -BG /mmc | awk 'NR==2{gsub("G","",$4);print $4}')
    [ "$t" -gt 80 ] && { ml "温度${t}℃>80,降温90s"; sleep 90; continue; }
    [ "$d" -lt 30 ] && { ml "磁盘${d}G<30,清docker"; docker image prune -f >/dev/null 2>&1; }
    break
  done
}
run(){ # ver total batch gapps
  guard
  ml "开始 v$1 gapps=$4 (${2}台/批$3)"
  bash idtest.sh "$1" "$2" "$3" "$4" >"idtest-run-v$1g$4.log" 2>&1
  ml "完成 v$1 gapps=$4"
}

ml "===== 主控启动,等当前测试结束 ====="
wait_idle
ml "当前空闲,开始序列"

# 1) 16 普通版 24台
run 16 24 6 0
# 2) gapps:14/15/16 各8台(batch4,gapps更重)
run 14 8 4 1
run 15 8 4 1
run 16 8 4 1

ml "===== 全部序列跑完 ====="
