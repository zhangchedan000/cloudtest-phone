// Package selfupdate 管家「在线升级」：从 GitHub 拉最新预编译管家程序，校验后换新重启。
//
// 2026-11-27 用户要求：做一个一键在线升级，以后不用手动上板。
//
// 流程（和 host/install-agent.sh / host/board/install.sh 的「备份→换新→重启→体检→回滚」一致，
// 只是搬进管家自己，板子(OpenRC) 和服务器(systemd) 都能用，不依赖具体脚本）：
//  1. 下载 tools/dist/prism-phone-agent-arm64.gz + .sha256，SHA256 校验；
//  2. 解压成 <当前程序>.new，chmod +x，跑一下 -version 确认能运行（架构对）；
//  3. 和当前版本比，一样就不升；
//  4. 丢一个独立的后台脚本去做：备份当前程序→换成新的→重启服务→体检 /api/health→起不来就换回旧的。
//     脚本脱离管家进程（setsid），这样重启杀掉管家也不影响换新。
package selfupdate

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Base 预编译件在 GitHub raw 上的根地址（板子/服务器都从这里取；install.sh 用的是同一个）。
// 可用环境变量 PP_UPDATE_BASE 覆盖（比如换成自己的镜像站）。
func Base() string {
	if b := strings.TrimRight(os.Getenv("PP_UPDATE_BASE"), "/"); b != "" {
		return b
	}
	return "https://raw.githubusercontent.com/zhangchedan000/cloudtest-phone/main"
}

const (
	gzPath  = "tools/dist/prism-phone-agent-arm64.gz"
	shaPath = "tools/dist/prism-phone-agent-arm64.gz.sha256"
	verPath = "tools/dist/prism-phone-agent-arm64.version"
)

// httpGet 取一个文件（小文件用；带超时）。
func httpGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载 %s 失败：HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 200<<20))
}

// Latest 查 GitHub 上最新管家版本（读 .version 小文件；取不到就报错）。
func Latest(ctx context.Context) (string, error) {
	b, err := httpGet(ctx, Base()+"/"+verPath)
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", fmt.Errorf("最新版本号是空的")
	}
	return v, nil
}

// Status 升级状态：当前版本、最新版本、能不能升。
type Status struct {
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	CanUpdate bool   `json:"canUpdate"`
	Note      string `json:"note,omitempty"`
}

// Check 查当前能不能升级（current 传正在跑的版本号 main.version）。
func Check(ctx context.Context, current string) Status {
	s := Status{Current: current}
	latest, err := Latest(ctx)
	if err != nil {
		s.Note = "连不上更新服务器：" + err.Error()
		return s
	}
	s.Latest = latest
	// 版本号是 git describe（如 a1b2c3d 或 a1b2c3d-dirty）；不一样就认为能升。
	s.CanUpdate = latest != current && current != "" && current != "dev"
	if !s.CanUpdate && latest == current {
		s.Note = "已是最新版本"
	}
	return s
}

// Start 开始升级。current = 正在跑的版本，healthURL = 本机 /api/health（体检用）。
// 返回要升到的新版本号；真正的换新重启在后台独立脚本里做（本函数很快返回）。
func Start(ctx context.Context, current, healthURL string) (newVer string, err error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("找不到当前程序路径：%w", err)
	}
	exe, _ = filepath.EvalSymlinks(exe)

	// 1. 下载 .gz 和 .sha256
	dlCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	gz, err := httpGet(dlCtx, Base()+"/"+gzPath)
	if err != nil {
		return "", err
	}
	shaRaw, err := httpGet(dlCtx, Base()+"/"+shaPath)
	if err != nil {
		return "", err
	}
	want := strings.ToLower(strings.Fields(string(shaRaw))[0])
	sum := sha256.Sum256(gz)
	if got := hex.EncodeToString(sum[:]); got != want {
		return "", fmt.Errorf("下载的程序校验不过（SHA256 对不上，可能没下全），没升级")
	}

	// 2. 解压成 <exe>.new
	newBin := exe + ".new"
	if err := gunzipTo(gz, newBin); err != nil {
		return "", err
	}
	if err := os.Chmod(newBin, 0o755); err != nil {
		_ = os.Remove(newBin)
		return "", err
	}
	// 3. 跑一下 -version 确认能运行（架构对、没下坏）
	out, err := exec.CommandContext(ctx, newBin, "-version").Output()
	if err != nil {
		_ = os.Remove(newBin)
		return "", fmt.Errorf("新程序跑不起来（架构不对或没下全），没升级：%w", err)
	}
	newVer = strings.TrimSpace(string(out))
	if newVer == current {
		_ = os.Remove(newBin)
		return "", fmt.Errorf("已是最新版本（%s）", current)
	}

	// 4. 后台脚本去换新重启（脱离管家进程）
	if err := spawnSwap(exe, newBin, healthURL); err != nil {
		_ = os.Remove(newBin)
		return "", err
	}
	return newVer, nil
}

// gunzipTo 把 gz 内容解压写到 path。
func gunzipTo(gz []byte, path string) error {
	zr, err := gzip.NewReader(strings.NewReader(string(gz)))
	if err != nil {
		return fmt.Errorf("解压失败：%w", err)
	}
	defer zr.Close()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, io.LimitReader(zr, 500<<20)); err != nil {
		f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("解压写入失败：%w", err)
	}
	return f.Close()
}

// swapScript 换新重启脚本：备份→换新→重启服务（自动认 OpenRC/systemd）→体检→起不来换回旧的。
// 参数：$1=当前程序 $2=新程序 $3=健康检查地址 $4=日志文件。
const swapScript = `#!/bin/sh
set -u
EXE="$1"; NEW="$2"; HEALTH="$3"; LOG="$4"
exec >"$LOG" 2>&1
echo "[升级] $(date '+%F %T') 开始：$EXE"
sleep 1
cp -f "$EXE" "$EXE.prev" || { echo "[升级] 备份失败"; exit 1; }
mv -f "$NEW" "$EXE" || { echo "[升级] 换新失败"; exit 1; }
restart() {
  if command -v rc-service >/dev/null 2>&1 && rc-service prism-phone-agent status >/dev/null 2>&1; then
    rc-service prism-phone-agent restart
  elif command -v systemctl >/dev/null 2>&1; then
    systemctl restart prism-phone-agent
  else
    pkill -x prism-phone-agent 2>/dev/null || true   # 没服务管理器：杀掉让守护进程拉起新的
  fi
}
restart
ok=0
i=0
while [ "$i" -lt 40 ]; do
  i=$((i+1)); sleep 1
  code=$(curl -s -m3 -o /dev/null -w '%{http_code}' "$HEALTH" 2>/dev/null || echo 000)
  if [ "$code" = 200 ] || [ "$code" = 503 ]; then ok=1; break; fi
done
if [ "$ok" = 1 ]; then
  echo "[升级] 新版本起来了（$("$EXE" -version 2>/dev/null)）"
else
  echo "[升级] 新版本 40 秒内没起来，换回旧的"
  mv -f "$EXE.prev" "$EXE" && restart
fi
`

// spawnSwap 写出换新脚本，脱离当前进程在后台跑。
func spawnSwap(exe, newBin, healthURL string) error {
	script := filepath.Join(os.TempDir(), "pp-upgrade.sh")
	if err := os.WriteFile(script, []byte(swapScript), 0o755); err != nil {
		return err
	}
	log := filepath.Join(os.TempDir(), "pp-upgrade.log")
	cmd := exec.Command("/bin/sh", script, exe, newBin, healthURL, log)
	cmd.SysProcAttr = detach()
	// 不接管家的 stdin/stdout，自己写日志文件
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	return nil
}

// HealthURL 从监听地址推出本机 /api/health 地址（0.0.0.0 / :port 都换成 127.0.0.1）。
func HealthURL(listen string) string {
	host, port, found := strings.Cut(listen, ":")
	if !found { // 没冒号，兜底
		return "http://127.0.0.1:8099/api/health"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + host + ":" + port + "/api/health"
}
