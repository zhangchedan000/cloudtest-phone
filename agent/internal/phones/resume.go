package phones

import (
	"context"
	"os"
)

// 管家重启（自动更新、手动重启、崩溃）时，开机后的后台步骤会跟着中断：
// 删容器痕迹（removeTraces）、开机设置（applySettings：时区 / 语言 / settings / 电池 / 卸 AOSP 测试 App）、
// 挂伪造文件（applyFakes）。重启后没人再做，正在开机的手机就停在「半配置」状态（踩坑 #45）。
// ResumeRunning 在管家启动时把这几步对每台正在运行的手机再做一遍。三步都可以重复做：
//   - removeTraces：rm -f，删过了也成功；
//   - applySettings：属性 / settings 已经是目标值就跳过，不会无故重启安卓框架；
//   - 伪造文件：见 resumeFakes。

// ResumeRunning 对每台容器在跑的手机补做开机后步骤，返回补做了几台。
func (s *Service) ResumeRunning(ctx context.Context) int {
	st := s.store.Snapshot()
	n := 0
	for _, p := range st.Phones {
		if !s.isRunning(ctx, p.ID) {
			continue
		}
		n++
		// 设置做完之前算「开机中」，和 Start 一样，免得设置过程中（语言变了会重启框架）就让人操作。
		s.settings.set(p.ID, SettingsStatus{State: "pending"})
		id := p.ID
		work := func() {
			s.removeTraces(ctx, containerName(id))
			s.ensureWifi(ctx, id)
			// 挂载表改写的登记（/proc/pp_mnt）和本机端口防护，跟 Start 一样补一遍：
			// 平时管家重启这些登记在内核模块里还在，不用补；但自动更新重新编译、重载了 pp_mnt
			// 内核模块后，登记会全丢，只有在这里补登记才能自愈（否则要逐台重启手机，踩坑 #80）。都是幂等的。
			s.applyMounts(ctx, id)
			s.guardLocalPorts(ctx, id)
			s.applySettings(ctx, id)
			s.resumeFakes(ctx, id)
		}
		if s.async {
			go work()
		} else {
			work()
		}
	}
	return n
}

// resumeFakes 伪造文件的补做。伪造目录在开机时以只读方式挂到容器的 /.ppfake，
// applyFakes 挂完会卸掉并删除 /.ppfake。所以：
//   - /.ppfake 还在 → 重启前没挂完，接着挂（applyFakes 会跳过已经挂上的）；
//   - /.ppfake 不在 → 重启前已经挂完了，只按 /proc/mounts 恢复管理页上显示的状态。
func (s *Service) resumeFakes(ctx context.Context, id string) {
	entries, err := os.ReadDir(s.fakeDir(id))
	if err != nil || len(entries) == 0 {
		return
	}
	name := containerName(id)
	if _, err := s.run.Run(ctx, "docker", "exec", name, "ls", fakeMount); err == nil {
		s.applyFakes(ctx, id)
		return
	}
	mounted := mountedTargets(ctx, s.run, name)
	var applied []string
	for _, f := range s.fakeTargets(id) {
		if mounted[f.Target] {
			applied = append(applied, f.Target)
		}
	}
	if len(applied) == 0 {
		s.fakes.set(id, FakeStatus{State: "failed", Error: "管家重启后发现伪造文件没挂上，重启这台手机即可"})
		return
	}
	s.fakes.set(id, FakeStatus{State: "applied", Applied: applied})
}

// Busy 有手机正在做开机后设置或挂伪造文件（自动更新看到 busy 就先不重启管家）。
func (s *Service) Busy() bool {
	for _, p := range s.store.Snapshot().Phones {
		if st := s.settings.get(p.ID); st != nil && st.State == "pending" {
			return true
		}
		if st := s.fakes.get(p.ID); st != nil && st.State == "pending" {
			return true
		}
		if j := s.dataJobs.get(p.ID); j != nil && j.State == "running" {
			return true
		}
	}
	return false
}
