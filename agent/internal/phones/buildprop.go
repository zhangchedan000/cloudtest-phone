package phones

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
	// dropRedroid 改完以后去掉还带 redroid 字样的行（/vendor/build.prop 里 Redroid 自己的注释和属性，踩坑 #63）
	dropRedroid bool
}

var buildPropFiles = []buildPropFile{
	// dropRedroid 全开：改写后再去掉残留的 redroid / lineage / flysky / UNOFFICIAL 行
	// （lineage-20 镜像的 /system/build.prop 里有一整套 ro.lineage.*、ro.build.host=redroid-builder 等，
	//  这些不在 fingerprintProps 覆盖范围，只能靠去行清掉，踩坑 #104）。
	{"/system/build.prop", 0o600, true, true},
	{"/system/product/etc/build.prop", 0o644, false, true},
	{"/system/system_ext/etc/build.prop", 0o644, false, true},
	// vendor 的 build.prop 0644，App 能直接读：原来满是 redroid（品牌、型号、指纹、ro.hardware）
	{"/vendor/build.prop", 0o644, false, true},
	// odm / vendor_dlkm 分区的 build.prop（lineage-20 有，魔云腾改机 SDK 也处理这俩）：
	// 里面有 ro.odm.build.fingerprint / ro.product.vendor_dlkm.* = redroid，不覆盖会漏（踩坑 #104）。
	// 文件可能不存在（别的镜像没有）：dropRedroid=true 时 docker cp 失败不报错、自动跳过。
	{"/odm/etc/build.prop", 0o644, false, true},
	{"/vendor_dlkm/etc/build.prop", 0o644, false, true},
}

// dropRedroidLines 去掉带容器/改机痕迹的行：redroid / flysky / UNOFFICIAL 字样，
// 以及 lineage 自定义系统才有的属性（ro.lineage.*、ro.modversion、ro.lineagelegal.*）——
// 我们伪装成 Nothing / 小米等真机，这些 lineage 属性本身就是破绽（踩坑 #104）。
// 注意：fingerprintProps 已经把 ro.build.fingerprint / 各分区指纹 / host / user / incremental
// 改写成干净值了（改写在去行之前跑），所以这里去掉的只是没被改写的残留行，不会误删关键属性。
func dropRedroidLines(s string) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(s, "\n") {
		low := strings.ToLower(line)
		key := strings.TrimSpace(strings.SplitN(line, "=", 2)[0])
		drop := strings.Contains(low, "redroid") || strings.Contains(low, "flysky") || strings.Contains(low, "unofficial") ||
			strings.HasPrefix(key, "ro.lineage") || key == "ro.modversion"
		if !drop {
			b.WriteString(line)
		}
	}
	return b.String()
}

// buildPropMu 同一时刻只取一份：几台同镜像的手机一起开机、缓存还没有（比如镜像刚重做过）时，都用同一个临时容器名
// docker create，后来的撞名失败、照镜像原样开机，redroid 原值全漏（2026-11-22 多开实测）。
var buildPropMu sync.Mutex

// imageBuildProps 镜像里原来的 build.prop（按镜像 ID 缓存在 DataDir/cache/buildprop/<ID>/）。
func (s *Service) imageBuildProps(ctx context.Context, image string) (string, error) {
	buildPropMu.Lock()
	defer buildPropMu.Unlock()
	id, err := s.run.Run(ctx, "docker", "image", "inspect", "-f", "{{.Id}}", image)
	if err != nil || id == "" {
		return "", fmt.Errorf("读镜像 ID：%v %s", err, id)
	}
	// buildprop3：buildPropFiles 增加了 odm / vendor_dlkm 分区文件，旧缓存按旧列表存的会漏，换目录名强制重取（踩坑 #104）
	dir := filepath.Join(s.cfg.DataDir, "cache", "buildprop3", strings.TrimPrefix(strings.TrimSpace(id), "sha256:"))
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
		if out, err := s.run.Run(ctx, "docker", "cp", tmp+":"+f.path, filepath.Join(dir, fmt.Sprint(i))); err != nil && !f.dropRedroid {
			return "", fmt.Errorf("取镜像 %s：%v %s", f.path, err, out)
		}
	}
	return dir, os.WriteFile(filepath.Join(dir, "done"), nil, 0o644)
}

// rewriteBuildProp 把原文件里出现的属性改成最终值（final 和带空格的 extra 都算）；
// appendMissing 时 extra 里原文件没有的追加在末尾（只对 /system/build.prop）。
// 注意：同一个属性在后加载的文件里出现会覆盖前面的（system_ext 比 system 晚），所以每个文件里出现的都要改。
func rewriteBuildProp(orig string, final, extra map[string]string, appendMissing bool) string {
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
	if !appendMissing {
		return b.String()
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
		out := filepath.Join(dir, fmt.Sprint(i)+".prop")
		text := rewriteBuildProp(string(orig), final, spaced, f.takeSpaced)
		if f.dropRedroid {
			text = dropRedroidLines(text)
		}
		if err := os.WriteFile(out, []byte(text), f.mode); err != nil {
			return nil, err
		}
		_ = os.Chmod(out, f.mode)
		binds = append(binds, out+":"+f.path+":ro")
	}
	return binds, nil
}
