#!/bin/sh
# 手机里 App 能读到的「同一台宿主机 / 模拟器」痕迹（踩坑 #134），对所有运行中的手机逐台看、横向比。板子上 root 跑。
#   - /proc/stat 的 cpu 行、btime、ctxt：每台应各不相同、和自己的开机时长对得上
#   - /proc/uptime 空闲时间 ≈ 开机时长 × 核数 × 0.98
#   - /proc/bus/input/devices：只有自己的触摸屏 + 电源键这类，没有 LG 虚拟设备、没有别的手机的触摸屏
#   - /proc/modules：App（uid 10050）读不了
#   - /proc/net/tcp：没有 6401~6404 / 9600 端口、没有 127.0.0.11
#   - 无线网卡名 phy0；/sys/class/net/wlan0 指向的路径里没有 hwsim
#   - uname -r 和 /proc/version 一致
# 用法：sh host/test/proc-tells.sh
set -u
bad=0
for c in $(docker ps --format '{{.Names}}' | grep '^pp-'); do
  pid=$(docker inspect -f '{{.State.Pid}}' "$c")
  # 以 App 身份（uid 10050）在手机的 PID / 时间 / 网络 / 挂载命名空间里读
  app() { nsenter -t "$pid" -a -S 10050 -G 10050 -- /system/bin/sh -c "$1" 2>&1; }
  up=$(app 'cut -d" " -f1 /proc/uptime')
  idle=$(app 'cut -d" " -f2 /proc/uptime')
  cpu=$(app 'head -1 /proc/stat')
  btime=$(app 'grep ^btime /proc/stat | cut -d" " -f2')
  now=$(date +%s)
  touch_n=$(app 'grep -c "^N:" /proc/bus/input/devices')
  lg=$(app 'grep -c "LG " /proc/bus/input/devices')
  names=$(app 'grep "^N:" /proc/bus/input/devices | cut -d= -f2 | tr "\n" " "')
  mods=$(app 'cat /proc/modules 2>&1 | head -1')
  ports=$(app 'cat /proc/net/tcp /proc/net/tcp6 /proc/net/udp /proc/net/udp6 2>/dev/null' | awk '{print $2}' | grep -ciE ':(1901|1902|1903|1904|2580)$|^0B00007F:')
  phy=$(app 'ls /sys/class/ieee80211 2>&1 | tr "\n" " "')
  link=$(app 'readlink /sys/class/net/wlan0')
  urel=$(app 'uname -r'); pver=$(app 'cut -d" " -f3 /proc/version')
  echo "== $c  开机 ${up}s  空闲 ${idle}s"
  echo "   cpu: $cpu"
  echo "   btime=$btime（now-btime=$((now - ${btime:-0}))s）"
  echo "   输入设备 $touch_n 个：$names"
  echo "   /proc/modules（App）：$mods"
  echo "   自己的端口 / Docker DNS 行：$ports"
  echo "   无线网卡：$phy  wlan0 → $link"
  echo "   uname -r=$urel  /proc/version=$pver"
  [ "$lg" = 0 ] || { echo "   ✗ 有 LG 虚拟输入设备"; bad=1; }
  case "$mods" in *ermission*|*denied*) ;; *) echo "   ✗ App 能读 /proc/modules"; bad=1 ;; esac
  [ "$ports" = 0 ] || { echo "   ✗ 本机端口 / Docker DNS 没藏住"; bad=1; }
  case "$phy" in "phy0 ") ;; *) echo "   ✗ 网卡名不是 phy0"; bad=1 ;; esac
  case "$link" in *hwsim*) echo "   ✗ wlan0 路径带 hwsim"; bad=1 ;; esac
  [ "$urel" = "$pver" ] || { echo "   ✗ uname 和 /proc/version 不一致"; bad=1; }
  echo "$cpu" >> /tmp/pp-tells-cpu.$$
done
if [ -f /tmp/pp-tells-cpu.$$ ]; then
  n=$(wc -l < /tmp/pp-tells-cpu.$$); u=$(sort -u /tmp/pp-tells-cpu.$$ | wc -l)
  [ "$n" = "$u" ] || { echo "✗ 有几台的 /proc/stat cpu 行一模一样"; bad=1; }
  rm -f /tmp/pp-tells-cpu.$$
fi
[ $bad = 0 ] && echo "✓ 全部干净" || echo "有问题（见 ✗）"
exit $bad
