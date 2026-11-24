package phones

import "context"

// Widevine DRM（dobox 的 widevine apex 移植进 /system/apex，踩坑 #155）。
//
// Widevine 的 L3 CDM 把「每台一个、互不相同」的设备 ID 和 keybox 存在 /data/vendor/mediadrm 下
// （每台手机自己的 /data → 天然每台唯一，正好过风控的「跨机关联」）。这个目录本该由 apex 的
// init .rc 用 `mkdir /data/vendor/mediadrm` 建，但在开了强制 SELinux 的正式镜像上，redroid 那个
// 打过补丁的 init 建不出这个目录（dontaudit 静默失败，和 apex 服务 seclabel 不认是同一类 init 老毛病）。
// 目录不在 → CDM 建不了 Level3FileSystem → OEMCrypto 初始化失败 → MediaDrm 读设备 ID 报 cdmError 331。
//
// 所以开机后由管家把这个目录建好。纯文件操作（mkdir/chmod/chown），docker exec 以 root 跑，
// 和 cleanDev 删节点一样不受手机里 SELinux 影响。幂等，建过了也没关系。CDM 的 HAL 跑在 permissive
// 域，往这个目录（docker 建的、标签随便）写 keybox 没问题（实测）。
func (s *Service) ensureMediaDrmDir(ctx context.Context, id, image string) {
	if s.imageLabel(ctx, image, "prism.widevine") != "1" {
		return
	}
	name := containerName(id)
	_, _ = s.run.Run(ctx, "docker", "exec", name, "sh", "-c",
		"mkdir -p /data/vendor/mediadrm && chmod 0770 /data/vendor/mediadrm && chown media:mediadrm /data/vendor/mediadrm")
}
