# Mali-G610 硬件加速（redroid + 魔云腾 Mali 栈）

在 RK3588 板子上给官方 redroid 镜像加 Mali-G610 硬件 GPU 加速，办法是从魔云腾
dobox 镜像里扒出配套的 Mali 用户态驱动栈 graft 进去。构建用上一层的
`host/build-mali-image.sh`。

## 原理

- 咱的内核有 Mali 驱动（`bifrost_kbase g18p0`, UK 1.18）和 CSF 固件
  （`/lib/firmware/mali_csffw.bin`，内核态加载，容器不用带）。
- 官方 redroid 默认软件渲染（mesa/swiftshader）。要硬件渲染，得给它一套能和这个
  内核握手的 Mali 用户态驱动（`libGLES_mali.so` + gralloc/mapper/allocator HAL）。
- 魔云腾 dobox 镜像里正好有（它也是 RK3588，同源的 Mali DDK）。镜像有 DRM 不能整体
  用，但文件能提取：Android 在 `super.img`（LP 动态分区），`lpextract.py` 纯 Python
  解析抠出 vendor 分区，debugfs 取文件。
- Mali 版本要和安卓版本配：安卓13 的 libGLES_mali 依赖 A13 的图形 HAL，和 A15 ABI
  冲突。所以**安卓14 用 Q14 的 Mali、安卓15 用 Q15 的 Mali**，别混。

## 为什么默认安卓14，不是15/16

安卓15/16 新增 `CpuPowerStatsCollector`（BPF 读 CPU 电量统计），在这板子的容器里
数组越界，`system_server` 崩溃死循环，开不了机。拿**纯官方 redroid15（不装 Mali）**
对照，一样崩 → 跟 Mali 无关，是 redroid15-A15 在容器/RK3588 的通病。根因：容器里
`aconfigd` 没初始化 aconfig storage（`on post-fs` 的 `exec_start` 静默失败），
`streamlined_battery_stats` flag 回退编译死的默认值 enabled，collector 被启用就崩。
安卓13/14 都没这个 collector，所以不崩。A15/16 要用得先解决 aconfigd 容器适配。

## 文件

| 文件 | 作用 |
|---|---|
| `lpextract.py` | 纯 Python 解析 super.img（LP 动态分区），抠出 vendor/system 等逻辑分区 |
| `graft-deps.py` | 算 Mali 栈的 NEEDED 依赖闭包，把 sphal 看不到的非 VNDK-SP 库从库池补进 vendor/lib64，保证 64 位 |
| `gpu_config.sh` | 覆盖 redroid 自带的，加 `mali` 模式（设 `ro.hardware.egl=mali`） |
| `manifest_mali_a14.xml` | 安卓14 的 allocator@4.0 / mapper@4.0 VINTF 声明 |

## 踩过的坑

- **sphal namespace**：libGLES_mali 在 vendor/sphal namespace 加载，它依赖的 HAL
  接口库、`libdmabufheap`、`libgralloctypes` 等在 system 里但不在 VNDK-SP 白名单，
  dlopen 报 "library not found in namespace sphal"。解决：把这些非 VNDK-SP 依赖复制
  进 `/vendor/lib64`（`graft-deps.py` 自动做）。
- **32/64 位**：`arm.graphics-V3-ndk_platform.so` 在 Q14 的 `odm/lib` 是 32 位 stub、
  `odm/lib64` 才是 64 位。复制错会报 "is 32-bit instead of 64-bit"。`graft-deps.py`
  用 ELF 头判断只取 64 位。
- **mapper VINTF**：mapper@4.0 passthrough 要在 VINTF 声明，否则
  "Gralloc4: mapper 4.x is not supported"，SurfaceFlinger 崩。
- **CSF 固件**：内核态加载（`/lib/firmware/mali_csffw.bin`），容器里不用带。
- **selinux**：graft 的文件没有正确 selinux label，容器用 `androidboot.selinux=permissive`。

## 安卓15 的区别（给以后接手）

Q15 的 Mali 栈用 **AIDL** allocator（`IAllocator/default` version 1，
`allocator-V1-service` + `allocator-V1-arm.so`），不是 A14 的 HIDL allocator@4.0；
mapper 还是 @4.0 passthrough。vintf、rc、依赖闭包都不一样。Q15 的 graft 验证过
Mali-G610 能渲染（`v1.g29p0`），只是卡在上面说的 batterystats（跟 Mali 无关）。
A15 的完整 recipe 当时固化在板子 `/mmc/pp-mali/`（A15）。
