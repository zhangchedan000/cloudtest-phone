#!/usr/bin/env bash
# RK3588 板子（魔云腾 / Orange Pi 5 / Radxa Rock 5 等）部署预检 + 接管脚本。
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
else fail "Mali GPU" "/dev/mali0 不在！Mali CSF 驱动没装/没加载，redroid 会退回软件渲染（最大破绽）。装 Mali CSF 内核驱动（cnflysky 用 g18p0）"; fi
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

# 8. 内存（算能开几台）
memg=$(awk '/MemTotal/{printf "%.0f", $2/1024/1024}' /proc/meminfo 2>/dev/null)
[ -n "$memg" ] && note "内存" "${memg}G（约可开 $((memg*10/15)) 台，按每台 1.5G 估）"

echo
echo "===== 预检结果：$ok 项通过 · $warn 项提醒 · $bad 项不达标 ====="
if [ "$bad" -gt 0 ]; then
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
echo
echo "===== 拉取 RK3588 redroid 镜像 ====="
IMG=${PP_REDROID_IMAGE:-cnflysky/redroid-rk3588:lineage-20}
echo "镜像：$IMG（可用环境变量 PP_REDROID_IMAGE 覆盖，比如换成魔云腾的 A16 镜像）"
docker pull "$IMG" || { echo "拉取失败，检查网络/镜像名"; exit 1; }

echo
echo "===== 安装 prism-phone 管家（复用 install-agent.sh） ====="
echo "注意：prism-phone 用 -images 指定 redroid 镜像。板子上请把 agent.env 的 AGENT_ARGS 里"
echo "     -images 指到上面拉的镜像（并去掉 -dev-host），例如："
echo "     -images 13=$IMG"
echo
ROOT=$(cd "$(dirname "$0")/.." && pwd)
if [ -x "$ROOT/host/install-agent.sh" ]; then
  echo "现在运行 install-agent.sh 装管家（它会编译并起服务）..."
  bash "$ROOT/host/install-agent.sh"
else
  echo "没找到 install-agent.sh，手动装：cd $ROOT && bash host/install-agent.sh"
fi

echo
echo "===== 完成 ====="
echo "之后：打开 prism-phone 管理页 → 新建手机（选 Mali-G610 机型）→ 导入真参 → 分配住宅代理 → 开机。"
echo "真实度自检：bash host/check-tells.sh <容器名>（板子上 ro.hardware/GPU/传感器这些应比云上干净很多）。"
