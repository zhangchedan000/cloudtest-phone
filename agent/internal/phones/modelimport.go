package phones

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ImportResult 导入机型包的结果（给管理页）。
type ImportResult struct {
	Output  []string `json:"output"` // 导入脚本每个包一行：✓ 一致档 / · 伪装档 / ✗ 失败原因
	Builtin int      `json:"builtin"`
	Local   int      `json:"local"`
}

var safePkgName = regexp.MustCompile(`[^\w.() +-]+`)

// ImportModels 导入机型包（我们采集的 / 魔云腾的 zip）到本地机型库，然后重新加载机型库。
// 脚本打包在管家程序里（cfg.Tools：import-model.py、extract-buildprops.py），由板子上的 python3 跑。
func (s *Service) ImportModels(ctx context.Context, pkgs map[string][]byte) (ImportResult, error) {
	if s.cfg.Tools == nil {
		return ImportResult{}, fmt.Errorf("%w：这个管家没带导入脚本", ErrInvalid)
	}
	if _, err := exec.LookPath("python3"); err != nil {
		return ImportResult{}, fmt.Errorf("%w：机器上没有 python3（apk add python3）", ErrInvalid)
	}
	tmp, err := os.MkdirTemp("", "pp-import-")
	if err != nil {
		return ImportResult{}, err
	}
	defer os.RemoveAll(tmp)
	for _, name := range []string{"import-model.py", "extract-buildprops.py"} {
		b, err := fs.ReadFile(s.cfg.Tools, name)
		if err != nil {
			return ImportResult{}, fmt.Errorf("管家里缺导入脚本 %s：%w", name, err)
		}
		if err := os.WriteFile(filepath.Join(tmp, name), b, 0o644); err != nil {
			return ImportResult{}, err
		}
	}
	args := []string{"-I", filepath.Join(tmp, "import-model.py"), "--out", s.LocalModelsDir(),
		"--host-gpu", s.cfg.GPUFamily, "--host-cpus", strconv.Itoa(s.hostCPUs())}
	pkgDir := filepath.Join(tmp, "pkgs")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return ImportResult{}, err
	}
	for name, data := range pkgs {
		base := safePkgName.ReplaceAllString(filepath.Base(name), "_")
		if !strings.HasSuffix(strings.ToLower(base), ".zip") {
			return ImportResult{}, fmt.Errorf("%w：%s 不是 zip", ErrInvalid, name)
		}
		p := filepath.Join(pkgDir, base)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return ImportResult{}, err
		}
		args = append(args, p)
	}
	if err := os.MkdirAll(s.LocalModelsDir(), 0o755); err != nil {
		return ImportResult{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(cctx, "python3", args...).CombinedOutput()
	res := ImportResult{}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			res.Output = append(res.Output, l)
		}
	}
	if err != nil {
		return res, fmt.Errorf("导入脚本出错：%v（%s）", err, strings.Join(res.Output, "；"))
	}
	res.Builtin, res.Local = s.ReloadModels()
	return res, nil
}
