#!/bin/sh
# 编译检测器 pp-probe（踩坑 #154）：产物 out/pp-probe.apk。要 JDK 17+；安卓构建工具按固定版本下载到 $PP_SDK（默认 ~/.cache/pp-sdk），校验 sha256。
set -eu
cd "$(dirname "$0")"
SDK=${PP_SDK:-$HOME/.cache/pp-sdk}
mkdir -p "$SDK"
get() { # 文件名 sha256 解压到的目录
  [ -d "$SDK/$3" ] && return 0
  curl -fsSL -o "$SDK/$1" "https://dl.google.com/android/repository/$1"
  echo "$2  $SDK/$1" | sha256sum -c - >/dev/null || { echo "$1 校验不对" >&2; exit 1; }
  mkdir -p "$SDK/$3" && unzip -q -o "$SDK/$1" -d "$SDK/$3" && rm -f "$SDK/$1"
}
get build-tools_r35_linux.zip bd3a4966912eb8b30ed0d00b0cda6b6543b949d5ffe00bea54c04c81e1561d88 bt
get platform-34-ext7_r03.zip 16fdb74c55e59ae3ef52def135aec713508467bd56d7dabcd8c9be31fa8b20f3 plat
BT=$(ls -d "$SDK"/bt/*/ | head -1); JAR=$(find -L "$SDK/plat" -name android.jar | head -1)
rm -rf out && mkdir -p out/classes out/dex
javac -nowarn -source 8 -target 8 -bootclasspath "$JAR" -d out/classes $(find src -name '*.java') 2>&1 | grep -v "^warning\|^Note\|options" || true
"$BT/d8" --min-api 29 --lib "$JAR" --output out/dex $(find out/classes -name '*.class')
"$BT/aapt2" link -o out/unsigned.apk --manifest AndroidManifest.xml -I "$JAR"
(cd out/dex && zip -q ../unsigned.apk classes.dex)
"$BT/zipalign" -f 4 out/unsigned.apk out/aligned.apk
[ -f "$SDK/probe.keystore" ] || keytool -genkeypair -keystore "$SDK/probe.keystore" -storepass pp-probe -keypass pp-probe -alias probe -keyalg RSA -keysize 2048 -validity 10000 -dname "CN=probe" >/dev/null 2>&1
"$BT/apksigner" sign --ks "$SDK/probe.keystore" --ks-pass pass:pp-probe --out out/pp-probe.apk out/aligned.apk
ls -la out/pp-probe.apk
