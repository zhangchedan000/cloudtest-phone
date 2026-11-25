// SPDX-License-Identifier: Apache-2.0
/*
 * pp_vkshim：伪装档机型的 Vulkan 转接库（2026-11-25，超越魔云腾——它的 Vulkan 没伪装）。
 *
 * 安卓 Vulkan 加载器按 ro.hardware.vulkan 找 /vendor/lib64/hw/vulkan.<名>.so（hwvulkan HAL 模块，导出符号 "HMI"）。
 * 伪装档机型把这个名字指向本库；它 dlopen 板子真正的 Mali 驱动 vulkan.mali.so，HAL 三件套
 * （EnumerateInstanceExtensionProperties / CreateInstance / GetInstanceProcAddr）原样转过去，只把
 * GetInstanceProcAddr 包一层：对 vkGetPhysicalDeviceProperties / vkGetPhysicalDeviceProperties2[KHR] 返回包装函数——
 * 先调真驱动拿真实 VkPhysicalDeviceProperties，再把 deviceName / vendorID / deviceID / driverVersion 改成机型包里
 * 录的真机值。渲染、扩展、能力全部走真 Mali 驱动，只改这几个字符串/数字。
 *
 * 每台手机的真机值由管家写进 cfg 区（找 "PPVKCFG1" 标记，见下布局；开关 0 时完全透传真驱动）。
 * 不用 NDK：自己声明用到的 libc / libdl 函数，链接时用同名空壳库（见 build.sh）。
 * VkPhysicalDeviceProperties 字段偏移是 Vulkan 稳定 ABI：apiVersion@0 driverVersion@4 vendorID@8 deviceID@12
 * deviceType@16 deviceName[256]@20。VkPhysicalDeviceProperties2：sType@0 + pNext@8 + properties@16。
 */
typedef unsigned int u32;

extern void *dlopen(const char *path, int flags);
extern void *dlsym(void *handle, const char *name);
extern unsigned long strlen(const char *s);
extern int strcmp(const char *a, const char *b);
extern void *memcpy(void *d, const void *s, unsigned long n);

#define RTLD_NOW 2
// 真 Mali 驱动挂在这个隐藏点文件上（管家挂、小助手打标签）：镜像自带的 vulkan.mali.so 被小助手删掉藏痕迹，
// 点文件不被「vulkan.*.so」删除规则匹配、App 也列不到 /vendor/lib64/hw。
#define REAL_DRIVER "/vendor/lib64/hw/.vkreal.so"
#define HARDWARE_MODULE_TAG 0x48574D54u /* 'HWMT' */

typedef struct hw_module_methods_t {
	int (*open)(const struct hw_module_t *m, const char *id, struct hw_device_t **dev);
} hw_module_methods_t;

typedef struct hw_module_t {
	u32 tag;
	unsigned short module_api_version;
	unsigned short hal_api_version;
	const char *id;
	const char *name;
	const char *author;
	struct hw_module_methods_t *methods;
	void *dso;
	unsigned long reserved[32 - 7];
} hw_module_t;

typedef struct hw_device_t {
	u32 tag;
	u32 version;
	struct hw_module_t *module;
	unsigned long reserved[12];
	int (*close)(struct hw_device_t *device);
} hw_device_t;

typedef void (*PFN_vkVoidFunction)(void);
typedef PFN_vkVoidFunction (*PFN_GetInstanceProcAddr)(void *instance, const char *name);

typedef struct hwvulkan_device_t {
	struct hw_device_t common;
	void *EnumerateInstanceExtensionProperties;
	void *CreateInstance;
	PFN_GetInstanceProcAddr GetInstanceProcAddr;
} hwvulkan_device_t;

typedef struct hwvulkan_module_t {
	struct hw_module_t common;
} hwvulkan_module_t;

/* 配置区（管家按 "PPVKCFG1" 标记写）：
 * [0..7]="PPVKCFG1" [8]=enable [9..264]=deviceName\0 [280..283]=vendorID [284..287]=deviceID [288..291]=driverVersion（小端，0=不改） */
__attribute__((used, section(".ppvkcfg"))) static volatile char g_cfg[320] = "PPVKCFG1";
#define CFG_ENABLE 8
#define CFG_NAME 9
#define CFG_VENDOR 280
#define CFG_DEVICE 284
#define CFG_DRIVER 288

static u32 rd32(int off) {
	const unsigned char *p = (const unsigned char *)g_cfg + off;
	return (u32)p[0] | ((u32)p[1] << 8) | ((u32)p[2] << 16) | ((u32)p[3] << 24);
}

typedef void (*PFN_GPDP)(void *phys, void *props);
static PFN_GPDP real_gpdp, real_gpdp2;
static PFN_GetInstanceProcAddr real_gipa;
static void *g_real;

static void patch_props(char *props) {
	if (g_cfg[CFG_ENABLE] != 1)
		return;
	const char *name = (const char *)g_cfg + CFG_NAME;
	if (name[0]) {
		unsigned long n = strlen(name);
		if (n > 255)
			n = 255;
		memcpy(props + 20, name, n);
		props[20 + n] = 0;
	}
	u32 vendor = rd32(CFG_VENDOR), device = rd32(CFG_DEVICE), driver = rd32(CFG_DRIVER);
	if (vendor)
		memcpy(props + 8, &vendor, 4);
	if (device)
		memcpy(props + 12, &device, 4);
	if (driver)
		memcpy(props + 4, &driver, 4);
}

static void my_gpdp(void *phys, void *props) {
	real_gpdp(phys, props);
	patch_props((char *)props);
}

static void my_gpdp2(void *phys, void *props2) {
	real_gpdp2(phys, props2);
	patch_props((char *)props2 + 16);
}

static PFN_vkVoidFunction my_gipa(void *instance, const char *name) {
	if (name && real_gipa) {
		if (!strcmp(name, "vkGetPhysicalDeviceProperties")) {
			if (!real_gpdp)
				real_gpdp = (PFN_GPDP)real_gipa(instance, name);
			if (real_gpdp)
				return (PFN_vkVoidFunction)my_gpdp;
		}
		if (!strcmp(name, "vkGetPhysicalDeviceProperties2") || !strcmp(name, "vkGetPhysicalDeviceProperties2KHR")) {
			if (!real_gpdp2)
				real_gpdp2 = (PFN_GPDP)real_gipa(instance, name);
			if (real_gpdp2)
				return (PFN_vkVoidFunction)my_gpdp2;
		}
	}
	return real_gipa ? real_gipa(instance, name) : (PFN_vkVoidFunction)0;
}

static hwvulkan_device_t g_dev;

static int my_open(const struct hw_module_t *m, const char *id, struct hw_device_t **dev) {
	(void)m;
	if (!g_real)
		g_real = dlopen(REAL_DRIVER, RTLD_NOW); // 真 vulkan.mali.so 桩（依赖 libGLES_mali.so）
	// HMI（hwvulkan HAL 模块符号）其实在 libGLES_mali.so 里。先从桩的句柄按依赖找；找不到就直接从 libGLES_mali.so 取。
	hwvulkan_module_t *rm = g_real ? (hwvulkan_module_t *)dlsym(g_real, "HMI") : 0;
	if (!rm) {
		void *gl = dlopen("/vendor/lib64/egl/libGLES_mali.so", RTLD_NOW);
		if (gl)
			rm = (hwvulkan_module_t *)dlsym(gl, "HMI");
	}
	if (!rm || !rm->common.methods || !rm->common.methods->open)
		return -1;
	hw_device_t *rdev = 0;
	int rc = rm->common.methods->open(&rm->common, id, &rdev);
	if (rc != 0 || !rdev)
		return rc ? rc : -1;
	hwvulkan_device_t *rhv = (hwvulkan_device_t *)rdev;
	g_dev = *rhv;
	real_gipa = rhv->GetInstanceProcAddr;
	g_dev.GetInstanceProcAddr = my_gipa;
	*dev = (hw_device_t *)&g_dev;
	return 0;
}

static struct hw_module_methods_t g_methods = {my_open};

__attribute__((visibility("default"))) hwvulkan_module_t HMI = {
	{
		HARDWARE_MODULE_TAG,
		1, /* module_api_version */
		0, /* hal_api_version */
		"vulkan",
		"pp vulkan shim",
		"pp",
		&g_methods,
		0,
		{0},
	},
};
