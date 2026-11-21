#!/usr/bin/env bash
# 编译 rootcanal（谷歌开源的蓝牙芯片模拟器，踩坑 #48）装到 /usr/local/bin/pp-rootcanal（在宿主机 / 板子上，root）。
#
# 为什么要自己编：Redroid 自带的蓝牙 HAL 里内嵌的 rootcanal 编译产物是坏的（原版也崩）；官方只给 x86 和 macOS 的现成程序。
# 用法：每台手机开机时管家在手机的网络命名空间里起一个（只给它自己用，手机之间互相看不到），
# 手机里的小助手把蓝牙 HAL 的串口（伪终端）接到它的 HCI 端口（H4 over TCP，127.0.0.1:6402）。
#
# 补丁：环境变量 PP_BT_ADDR=aa:bb:cc:dd:ee:ff 给芯片指定蓝牙地址（每台手机用机型档案里自己的蓝牙 MAC）。
# 编译用 Bazel（bazelisk 自动下对应版本），4 核机器约半小时；只用 2 核、低优先级，不影响正在跑的手机。
set -euo pipefail
[ "$(id -u)" = 0 ] || { echo "请用 root 运行" >&2; exit 1; }
REV=${PP_ROOTCANAL_REV:-ce12bd343fc32e8e67c7764f33ff694e4797973f} # 2026-11-16 main，甲骨文上编过
W=${PP_ROOTCANAL_DIR:-/opt/prism-phone/build-rootcanal}
JOBS=${PP_JOBS:-2}
log() { echo "[rootcanal] $*"; }

arch=$(uname -m)
case "$arch" in aarch64) bz=arm64 ;; x86_64) bz=amd64 ;; *) echo "不支持的架构 $arch" >&2; exit 1 ;; esac
command -v cargo >/dev/null || apt-get install -y rustc cargo git
command -v bazelisk >/dev/null || { curl -fsSL -o /usr/local/bin/bazelisk "https://github.com/bazelbuild/bazelisk/releases/download/v1.25.0/bazelisk-linux-$bz" && chmod +x /usr/local/bin/bazelisk; }
# 官方说明里还要 cargo install pdl-compiler：Bazel 编 rootcanal 本身用不到（它自己拉 pdl 的源码编），在 arm64 上还编不过 → 不装

mkdir -p "$W"; cd "$W"
[ -d rootcanal ] || git clone https://github.com/google/rootcanal.git
cd rootcanal
git fetch -q origin "$REV" 2>/dev/null || true
git checkout -q -f "$REV"
git submodule update --init --recursive -q

log "打补丁：PP_BT_ADDR 指定芯片地址"
python3 - model/setup/test_model.cc <<'PY'
import sys
p = sys.argv[1]
s = open(p).read()
mark = "prism-phone: PP_BT_ADDR"
if mark not in s:
    old = "Address TestModel::GenerateBluetoothAddress(uint32_t device_id) const {\n"
    assert old in s, "rootcanal 源码变了：GenerateBluetoothAddress 找不到"
    s = s.replace(old, old + """  // prism-phone: PP_BT_ADDR 给芯片指定蓝牙地址（每台手机一个 rootcanal、一块芯片，用机型档案里的蓝牙 MAC）
  if (const char* env = std::getenv("PP_BT_ADDR"); env != nullptr && env[0] != '\\0') {
    auto fixed = Address::FromString(env);
    bool used = fixed && std::any_of(phy_devices_.begin(), phy_devices_.end(),
                                     [&](auto& device) { return device.second->GetAddress() == *fixed; });
    if (fixed && !used) {
      return *fixed;
    }
  }
""", 1)
    if "#include <cstdlib>" not in s:
        s = s.replace("#include", "#include <cstdlib>\n#include", 1)
    open(p, "w").write(s)
PY

log "编译（$JOBS 核）"
nice -n 19 bazelisk build --jobs="$JOBS" :rootcanal
install -m 755 bazel-bin/rootcanal /usr/local/bin/pp-rootcanal
log "装好了：/usr/local/bin/pp-rootcanal（$(du -h /usr/local/bin/pp-rootcanal | cut -f1)）"
