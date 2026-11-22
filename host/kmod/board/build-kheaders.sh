#!/bin/sh
# 用【板子内核自带的头文件】编模块（踩坑 #129）。先跑过 build.sh（准备好编译工具链和公开源码树）。
#
# 为什么：魔云腾内核在 Rockchip 公开源码上加了自己的改动（MYT_FS、SELINUX_NS、PER_VMA_LOCK 回移、安卓那套
# ASHMEM/ION…），用公开头文件编，导出符号 CRC 只有 29% 对得上——结构定义不一样，模块装上去会乱写内存。
# 但它开了 CONFIG_IKHEADERS：/sys/kernel/kheaders.tar.xz 就是编这个内核用的头文件（含生成的 autoconf.h、
# asm-offsets.h）。把公开树里的 include/、arch/arm64/include/ 换成它的，结构布局就和板子内核一模一样。
#
# 核对：用它的头文件给 WiFi 驱动要用的几个关键内核函数现算 CRC（genksyms），和板子内核里的比——一致才说明换对了。
# 用法（root，板子上）：sh host/kmod/board/build-kheaders.sh
set -eu
K=${PP_KMOD:-/mmc/pp-build/kmod}
log(){ echo "$(date +%T) $*"; }
[ -s "$K/src/vmlinux.symvers" ] || { echo "先跑 build.sh（要编好的公开源码树）"; exit 1; }
rm -rf "$K/kheaders"; mkdir -p "$K/kheaders"
python3 -c "
import tarfile, sys
with tarfile.open('/sys/kernel/kheaders.tar.xz', 'r:xz') as t:
    t.extractall('$K/kheaders')
" 2>/dev/null
# 版本号从 /proc/version 取：这块板子上 uname -r 被改成了 5.10.198（魔云腾内核的伪装），/proc/version 才是真的 6.1.141
rel=$(cut -d' ' -f3 /proc/version)
grep -q "UTS_RELEASE \"$rel\"" "$K/kheaders/include/generated/utsrelease.h" || { echo "kheaders 不是正在跑的内核（$rel）的"; exit 1; }
log "板子内核头文件：$(du -sh "$K/kheaders" | cut -f1)"

# 核对用的小模块：照头文件里的原型重新「导出」几个内核函数，只为让 genksyms 算出 CRC（不会加载）
mkdir -p "$K/crctest" "$K/hwsim"
cat > "$K/crctest/crctest.c" <<'EOF'
#include <linux/module.h>
#include <linux/skbuff.h>
#include <linux/netdevice.h>
#include <net/genetlink.h>
#include <net/cfg80211.h>
#include <net/mac80211.h>
void skb_trim(struct sk_buff *skb, unsigned int len) {}
EXPORT_SYMBOL(skb_trim);
int netif_rx(struct sk_buff *skb) { return 0; }
EXPORT_SYMBOL(netif_rx);
void *genlmsg_put(struct sk_buff *skb, u32 portid, u32 seq, const struct genl_family *family, int flags, u8 cmd) { return NULL; }
EXPORT_SYMBOL(genlmsg_put);
int ieee80211_register_hw(struct ieee80211_hw *hw) { return 0; }
EXPORT_SYMBOL(ieee80211_register_hw);
struct wiphy *wiphy_new_nm(const struct cfg80211_ops *ops, int sizeof_priv, const char *requested_name) { return NULL; }
EXPORT_SYMBOL(wiphy_new_nm);
struct ieee80211_hw *ieee80211_alloc_hw_nm(size_t priv_data_len, const struct ieee80211_ops *ops, const char *requested_name) { return NULL; }
EXPORT_SYMBOL(ieee80211_alloc_hw_nm);
MODULE_LICENSE("GPL");
EOF
echo 'obj-m := crctest.o' > "$K/crctest/Kbuild"
cp "$K/src/drivers/net/wireless/mac80211_hwsim.c" "$K/src/drivers/net/wireless/mac80211_hwsim.h" "$K/hwsim/"
echo 'obj-m := mac80211_hwsim.o' > "$K/hwsim/Kbuild"

docker run --rm --platform linux/arm64 -v "$K:/k" -w /k/src debian:12-slim bash -ec '
  set -o pipefail
  apt-get update -qq && apt-get install -y -qq --no-install-recommends -o Dir::Cache::Archives=/k/aptcache build-essential bc flex bison libssl-dev libelf-dev kmod python3 >/dev/null
  # 换头文件：公开树的留成 *.public（重复跑时从 public 恢复 config）
  [ -d include.public ] || mv include include.public
  [ -d arch/arm64/include.public ] || mv arch/arm64/include arch/arm64/include.public
  rm -rf include arch/arm64/include
  cp -a /k/kheaders/include include
  cp -a /k/kheaders/arch/arm64/include arch/arm64/include
  cp -a include.public/config include/config          # Makefile 层面的 auto.conf 用我们的；C 代码用板子的 autoconf.h
  cp /k/Module.symvers.board Module.symvers             # 模块里引用内核符号的 CRC 用板子内核真实的
  grep -q "CONFIG_MYT_FS 1" include/generated/autoconf.h
  make -s M=/k/crctest modules 2>&1 | grep -v "^WARNING: modpost.*exported twice" | tail -3 || true
  make -s M=/k/hwsim modules 2>&1 | tail -5
  grep -q "CONFIG_MYT_FS 1" include/generated/autoconf.h || { echo "autoconf.h 被改回公开配置了"; exit 1; }
  modinfo -F vermagic /k/hwsim/mac80211_hwsim.ko
'
log "核对：用板子头文件现算的 CRC vs 板子内核里的"
python3 - "$K" <<'EOF'
import sys
k = sys.argv[1]
def load(p):
    d = {}
    for l in open(p):
        f = l.rstrip("\n").split("\t")
        d[f[1]] = f[0]
    return d
mine = load(f"{k}/crctest/Module.symvers")
board = load(f"{k}/Module.symvers.board")
ok = 0
for s, c in sorted(mine.items()):
    same = board.get(s) == c
    ok += same
    print(f"  {'✓' if same else '✗'} {s}: 板子头文件算出 {c}，板子内核 {board.get(s)}")
print(f"{ok}/{len(mine)} 一致")
sys.exit(0 if ok == len(mine) and mine else 1)
EOF
python3 "$K/compare-crcs.py" "$K/hwsim/mac80211_hwsim.mod.c" "$K/Module.symvers.board"
cp "$K/hwsim/mac80211_hwsim.ko" "$K/mac80211_hwsim.ko"
log "✓ 编好：$K/mac80211_hwsim.ko（按板子真实头文件，引用的内核符号 CRC 全对上）"
