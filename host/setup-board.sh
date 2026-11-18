#!/usr/bin/env bash
# RK3588 板子（魔云腾 Q1 / C1 / R1S、Orange Pi 5、Radxa Rock 5 等）部署预检 + 接管脚本。
# 魔云腾：直接用官方 Ubuntu 22.04 固件，不刷第三方系统（资料和适配清单见 docs/调研/魔云腾适配.md）。
#
# 用途：买了 RK3588 云手机盒子 / 开发板，刷了开源 Ubuntu/Armbian 固件后，在上面跑
#       cnflysky/redroid-rk3588（真 Mali-G610 GPU + GApps + 假WiFi）+ 我们的 prism-phone 管家。
#       这样硬件一次性买断、不用交 MYT OS 每台月费，软件全自己可控。
#
# 它只做两件事：
#   1) 预检（只读）：确认这块板子满足跑 redroid 的硬性条件（内核/Mali 驱动/binderfs/PSI/Docker）。
#   2) 预检全过后，可选地拉取 redroid 镜像、调用 install-agent.sh 让 prism-phone 接管。
#
# ⚠️ 本脚本的硬件检查项按 cnflysky/redroid-rk3588 的公开要求写，尚未在真板子上验证过
#    （开发是在无 GPU 的云主机上做的）。到真板子上以预检报告为准，缺啥补啥；不会乱改系统。
#
# 用法（root，在板子上）：
#   bash host/setup-board.sh            # 只预检，报告环境够不够
#   bash host/setup-board.sh --install  # 预检通过后，拉镜像 + 装 prism-phone 管家
set -u

ok=0; warn=0; bad=0
P(){ printf '  %-30s %s\n' "$1" "$2"; }
good(){ P "$1" "✅ $2"; ok=$((ok+1)); }
note(){ P "$1" "⚠️  $2"; warn=$((warn+1)); }
fail(){ P "$1" "❌ $2"; bad=$((bad+1)); }

[ "$(id -u)" = 0 ] || { echo "请用 root 运行"; exit 1; }
install=0; [ "${1:-}" = "--install" ] && install=1

echo "===== RK3588 板子环境预检 ====="

# 1. SoC 是不是 RK3588 系
soc=$(tr -d '\0' < /proc/device-tree/compatible 2>/dev/null)
if echo "$soc" | grep -qiE 'rk3588'; then good "SoC" "RK3588 系（$soc）"
else note "SoC" "没认出 RK3588（compatible=$soc）。不是 RK3588 的话 Mali-G610 对不上我们的机型库"; fi

# 2. 架构
arch=$(uname -m)
[ "$arch" = "aarch64" ] && good "架构" "$arch (ARM64)" || fail "架构" "$arch 不是 ARM64"

# 3. 内核版本（cnflysky 要 5.10 或 6.1）
kr=$(uname -r)
case "$kr" in 5.10.*|6.1.*) good "内核" "$kr（redroid 支持的版本）" ;; *) note "内核" "$kr —— cnflysky 推荐 5.10 或 6.1，其它版本可能缺 GPU/binder 支持" ;; esac

# 4. 真 Mali GPU 驱动（最关键——这是 beat 软件渲染的根本）
if [ -e /dev/mali0 ]; then good "Mali GPU" "/dev/mali0 在（真显卡驱动已加载）"
else fail "Mali GPU" "/dev/mali0 不在！Mali CSF 驱动没装/没加载，redroid 会退回软件渲染（最大差异项）。装 Mali CSF 内核驱动（cnflysky 用 g18p0）"; fi
# Mali 固件
if ls /lib/firmware/mali_csffw.bin >/dev/null 2>&1; then good "Mali 固件" "mali_csffw.bin 在 /lib/firmware"
else note "Mali 固件" "/lib/firmware/mali_csffw.bin 没找到（有的驱动要它，没有可能 GPU 起不来；可从 redroid 容器里拷）"; fi

# 5. binderfs（redroid 要；新版用 memfd 不需要 ashmem）
if grep -q binder /proc/filesystems 2>/dev/null || [ -d /dev/binderfs ]; then good "binderfs" "内核支持 binder"
else fail "binderfs" "内核没有 binderfs（CONFIG_ANDROID_BINDERFS=y）。redroid 起不来"; fi

# 6. PSI（CONFIG_PSI=y）
if [ -e /proc/pressure/memory ]; then good "PSI" "/proc/pressure 在（CONFIG_PSI=y）"
else note "PSI" "没有 /proc/pressure（CONFIG_PSI）。部分 redroid 功能/lmkd 可能受影响"; fi

# 7. Docker
if command -v docker >/dev/null && docker info >/dev/null 2>&1; then good "Docker" "已装并在运行"
else fail "Docker" "没装或没运行。装 docker-ce 或 docker.io"; fi

# 8. 内存（算能开几台）：每台按机型上限 8GB，板子上默认 2.5 倍超卖（手机平时只用 1~2GB）
memg=$(awk '/MemTotal/{printf "%.0f", $2/1024/1024}' /proc/meminfo 2>/dev/null)
[ -n "$memg" ] && note "内存" "${memg}G：按 8GB 机型、超卖 2.5 倍，约可同时开 $(( (memg-3)*25/80 )) 台（官方 Q1 说开 8 台稳）"

# 9. 我们自己的功能要的内核项（缺了对应功能自动退回，不影响开机）
[ -e /proc/self/ns/time ] && good "时间命名空间" "有（开机时长从 0 开始）" || note "时间命名空间" "没有 CONFIG_TIME_NS：手机里开机时长会是板子的开机时长"
if modprobe -n mac80211_hwsim >/dev/null 2>&1 || [ -d /sys/module/mac80211_hwsim ]; then good "虚拟 WiFi" "mac80211_hwsim 模块有"
else note "虚拟 WiFi" "没有 mac80211_hwsim 模块：手机照旧用有线网（要 WiFi 得用同版本内核源码单独编这个模块）"; fi
[ -e /dev/uinput ] && good "虚拟触摸屏" "/dev/uinput 在" || note "虚拟触摸屏" "没有 /dev/uinput（CONFIG_INPUT_UINPUT）：点击滑动退回 input 命令"

# 10. 魔云腾官方系统的痕迹
if docker ps -a --format '{{.Image}}' 2>/dev/null | grep -q dobox; then note "魔云腾云机" "板子上有官方的 dobox 云机容器（不影响我们，它们占内存；不用的话在官方客户端里删掉）"; fi
if ss -ltn 2>/dev/null | grep -qE ':2375\b'; then fail "Docker 2375 端口" "开着而且没有密码：同一个网络里谁都能控制所有手机。--install 时会挡掉（只许本机访问）"
else good "Docker 2375 端口" "没对外开"; fi

echo
echo "===== 预检结果：$ok 项通过 · $warn 项提醒 · $bad 项不达标 ====="
# 2375 端口开着算 ❌，但 --install 自己会挡掉，不拦安装
bad_hard=$bad; ss -ltn 2>/dev/null | grep -qE ':2375\b' && bad_hard=$((bad-1))
if [ "$bad_hard" -gt 0 ]; then
  echo "有 ❌ 项，先按提示补齐（通常是 Mali 驱动 / binderfs / Docker）。参考 github.com/CNflysky/redroid-rk3588 的 envcheck.sh 和 README。"
  [ "$install" = 1 ] && echo "环境没齐，不执行安装。"
  exit 1
fi
echo "硬性条件都满足，可以跑 redroid + prism-phone。"

if [ "$install" != 1 ]; then
  echo
  echo "下一步（确认无误后跑）：  bash host/setup-board.sh --install"
  exit 0
fi

# ---------- 预检通过，开始接管 ----------
ROOT=$(cd "$(dirname "$0")/.." && pwd)

echo
echo "===== 挡掉没密码的 Docker 2375 端口（只许本机访问，重启后也生效） ====="
cat > /etc/systemd/system/prism-block-docker-tcp.service <<'UNIT'
[Unit]
Description=prism-phone：Docker 远程接口（2375/2376）只许本机访问
After=network-pre.target docker.service
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/sbin/nft -f /etc/prism-phone/block-docker-tcp.nft
[Install]
WantedBy=multi-user.target
UNIT
mkdir -p /etc/prism-phone
cat > /etc/prism-phone/block-docker-tcp.nft <<'NFT'
table inet ppdockerguard
delete table inet ppdockerguard
table inet ppdockerguard {
  chain input {
    type filter hook input priority -10; policy accept;
    iifname "lo" accept
    tcp dport { 2375, 2376 } drop
  }
}
NFT
systemctl daemon-reload && systemctl enable --now prism-block-docker-tcp.service && echo "已挡掉（魔云腾官方客户端从此连不上这台，要用官方客户端时：systemctl disable --now prism-block-docker-tcp）"

echo
echo "===== 宿主机准备（lxcfs、WiFi 工具、内核设置守护等，复用 setup-host.sh） ====="
bash "$ROOT/host/setup-host.sh" || echo "setup-host.sh 有报错，看上面；不影响继续"

echo
echo "===== 拉取 RK3588 能用 Mali 显卡的安卓镜像 ====="
IMG=${PP_REDROID_IMAGE:-cnflysky/redroid-rk3588:lineage-20}
VER=${PP_ANDROID_VERSION:-13}
echo "镜像：$IMG（安卓 $VER；可用环境变量 PP_REDROID_IMAGE / PP_ANDROID_VERSION 换）"
docker pull "$IMG" || { echo "拉取失败，检查网络/镜像名"; exit 1; }
echo "叠上我们的 WiFi / 传感器 / 触摸层（host/build-wifi-image.sh）"
if bash "$ROOT/host/build-wifi-image.sh" "$IMG" "$VER"; then IMG="$IMG-wifi"; else echo "叠层失败：先用原镜像（没有虚拟 WiFi / 传感器 / 触摸），把上面的报错发给开发"; fi

echo
echo "===== 写板子用的管家参数 ====="
ENVF=/etc/prism-phone/agent.env
[ -f "$ENVF" ] && cp "$ENVF" "$ENVF.bak-$(date +%Y%m%d-%H%M%S)"
TOKEN=$(sed -n 's/^PP_TOKEN=//p' "$ENVF" 2>/dev/null); [ -n "$TOKEN" ] || TOKEN=$(head -c 24 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 30)
cat > "$ENVF" <<ENV
# 管家程序参数（setup-board.sh 写的；改完执行 systemctl restart prism-phone-agent）
# 板子：真 Mali-G610 显卡（gpu-mode host）、内存超卖 2.5 倍、不开「直连（仅开发）」
AGENT_ARGS=-listen 127.0.0.1:8099 -data /opt/prism-phone/data -host-name $(hostname) -gpu-family Mali-G610 -gpu-mode host -mem-overcommit 2.5 -mihomo-api 127.0.0.1:9090 -images $VER=$IMG
PP_TOKEN=$TOKEN
ENV
chmod 600 "$ENVF"
echo "已写 $ENVF（旧的备份在同目录 .bak-*）。管理页口令：在这个文件里 PP_TOKEN= 后面"

echo
echo "===== 安装 prism-phone 管家（复用 install-agent.sh） ====="
bash "$ROOT/host/install-agent.sh"

echo
echo "===== 完成 ====="
echo "之后：打开 prism-phone 管理页 → 新建手机（选 Mali-G610 机型）→ 分配住宅代理 → 开机。"
echo "真实度自检：bash host/check-tells.sh <手机ID>（显卡、uname、SELinux 这些应比云上干净很多）。"
echo "到货后的核实结果写进 docs/调研/魔云腾适配.md。"
