# shellcheck shell=sh
# 构建脚本下载件的 sha256 固定值：下回来的东西和这里对不上就不用（防上游被换包、下载半截、中间人）。
# 用法（构建脚本里）：. "$here/pinned.sh"；下载到 xxx.part 后  pp_verify xxx.part <文件名>  || die …
# 上游正常换了版本（比如 lpunpack master 有新提交）时，确认新文件没问题后把这里的值改掉。
pp_sha() {
  case "$1" in
    arm64-v8a-33_r02.zip) echo 55ba9a20d473dd5351c77342016956342c40375f3e073b65a43dc9db13ccc5c6 ;;
    arm64-v8a-34_r04.zip) echo 1447958a4c6747c44390ac5f5f4c894be6d1dfce93868a0385a95c5f0ae4c339 ;;
    arm64-v8a-35_r02.zip) echo 1e6ae4fca6449c07278a333311f0ef29c5101d1e3e18ba48fbe9bd2dc5006926 ;;
    arm64-v8a-36_r02.zip) echo c6194eb8ace128b174ea86b5fbc0c78204147123b3b5ce6d0e3d615a06ad31d1 ;;
    build-tools_r34-linux.zip) echo e858c4b60069d0431051b225d384413b1643e1289b00a4825aed347f25bd510f ;;
    build-tools_r35_linux.zip) echo bd3a4966912eb8b30ed0d00b0cda6b6543b949d5ffe00bea54c04c81e1561d88 ;;
    platform.pk8) echo 1ad8ef556870edb70f69a9d3c112544c07de5162ba440d84d33f8bb0c5962875 ;;
    platform.x509.pem) echo 9837de028f460c35cc8d3fa45f14eecce30f6fbfe4b93d399aef1acb80c20d14 ;;
    lpunpack.py) echo 600d1cab2fc7de5127fb287f4e2791718db54efb1102d0f365db0d3ec1ccf62e ;;
    *) echo "" ;;
  esac
}
# pp_verify <文件> <名字>：对得上返回 0；对不上把文件改名成 .bad 留着查，返回 1
pp_verify() {
  want=$(pp_sha "$2"); [ -n "$want" ] || { echo "pinned.sh 里没有 $2 的 sha256" >&2; return 1; }
  got=$(sha256sum "$1" 2>/dev/null | cut -d' ' -f1)
  [ "$got" = "$want" ] && return 0
  echo "✗ $2 校验不对：要 $want，下到的是 ${got:-（读不到）}，已改名 $1.bad" >&2
  mv -f "$1" "$1.bad" 2>/dev/null
  return 1
}
