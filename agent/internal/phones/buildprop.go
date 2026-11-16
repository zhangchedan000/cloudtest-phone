package phones

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 开机前改写的 build.prop 文件（踩坑 #55）：
//   - 启动参数遇空格就截断（#43），ro.build.description（"missi_phone_global-user 14 UP1A... release-keys"）、
//     市场名（"Xiaomi 13T"）这类带空格的属性传不进去，以前一直是 redroid 原值或缺失。
//   - Redroid 镜像的 /product/etc/build.prop、/system_ext/etc/build.prop 是 0644，任何 App 都能直接读，
//     里面写的还是 redroid14_arm64_only 这些原值——属性（getprop）改了，文件内容没改，一对就露馅。
// 做法：按镜像原文件，把里面的属性改成这台手机的最终值（启动参数 + 带空格的那些），带空格的属性追加到
// /system/build.prop；生成的文件开机前只读挂到原位置（容器建好、安卓 init 读 build.prop 之前）。
// 文件权限保持和原文件一样（/system/build.prop 0600、product / system_ext 0644）。
// 代价：/proc/mounts 里多几行挂载记录（挂载列表本来就暴露了容器，见 check-tells）。

// buildPropFile 一个要改写的 build.prop：容器里的路径、权限、带空格的属性追加到这里吗。
type buildPropFile struct {
	path       string
	mode       os.FileMode
	takeSpaced bool
}

var buildPropFiles = []buildPropFile{
	{"/system/build.prop", 0o600, true},
	{"/system/product/etc/build.prop", 0o644, false},
	{"/system/system_ext/etc/build.prop", 0o644, false},
}

// imageBuildProps 镜像里原来的 build.prop（按镜像 ID 缓存在 DataDir/cache/buildprop/<ID>/）。
func (s *Service) imageBuildProps(ctx context.Context, image string) (string, error) {
	id, err := s.run.Run(ctx, "docker", "image", "inspect", "-f", "{{.Id}}", image)
	if err != nil || id == "" {
		return "", fmt.Errorf("读镜像 ID：%v %s", err, id)
	}
	dir := filepath.Join(s.cfg.DataDir, "cache", "buildprop", strings.TrimPrefix(strings.TrimSpace(id), "sha256:"))
	if _, err := os.Stat(filepath.Join(dir, "done")); err == nil {
		return dir, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp := "pp-buildprop-" + strings.TrimPrefix(strings.TrimSpace(id), "sha256:")[:12]
	_, _ = s.run.Run(ctx, "docker", "rm", "-f", tmp)
	if out, err := s.run.Run(ctx, "docker", "create", "--name", tmp, image); err != nil {
		return "", fmt.Errorf("取镜像 build.prop：%v %s", err, out)
	}
	defer s.run.Run(context.Background(), "docker", "rm", "-f", tmp)
	for i, f := range buildPropFiles {
		if out, err := s.run.Run(ctx, "docker", "cp", tmp+":"+f.path, filepath.Join(dir, fmt.Sprint(i))); err != nil {
			return "", fmt.Errorf("取镜像 %s：%v %s", f.path, err, out)
		}
	}
	return dir, os.WriteFile(filepath.Join(dir, "done"), nil, 0o644)
}

// rewriteBuildProp 把原文件里出现的属性改成最终值；extra（带空格的）里原文件没有的追加在末尾。
func rewriteBuildProp(orig string, final, extra map[string]string) string {
	var b strings.Builder
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimRight(orig, "\n"), "\n") {
		k, _, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if ok && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			if v, has := extra[k]; has {
				line, seen[k] = k+"="+v, true
			} else if v, has := final[k]; has {
				line = k + "=" + v
			}
		}
		b.WriteString(line + "\n")
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(k + "=" + extra[k] + "\n")
	}
	return b.String()
}

// prepareBuildProps 生成这台手机的 build.prop 文件，返回要加的挂载（宿主机文件:容器路径:ro）。
// 失败不挡开机（只是这几个文件还是镜像原样），错误返回给调用方记日志。
func (s *Service) prepareBuildProps(ctx context.Context, id, image string, args []string, spaced map[string]string) ([]string, error) {
	src, err := s.imageBuildProps(ctx, image)
	if err != nil {
		return nil, err
	}
	final := map[string]string{}
	for _, a := range args {
		if k, v, ok := strings.Cut(a, "="); ok && strings.HasPrefix(k, "ro.") {
			final[k] = v
		}
	}
	dir := filepath.Join(s.phoneDir(id), "buildprop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var binds []string
	for i, f := range buildPropFiles {
		orig, err := os.ReadFile(filepath.Join(src, fmt.Sprint(i)))
		if err != nil {
			continue
		}
		extra := map[string]string{}
		if f.takeSpaced {
			extra = spaced
		}
		out := filepath.Join(dir, fmt.Sprint(i)+".prop")
		if err := os.WriteFile(out, []byte(rewriteBuildProp(string(orig), final, extra)), f.mode); err != nil {
			return nil, err
		}
		_ = os.Chmod(out, f.mode)
		binds = append(binds, out+":"+f.path+":ro")
	}
	return binds, nil
}
