#!/system/bin/sh
# 覆盖 redroid 自带的 /vendor/bin/gpu_config.sh，加一个 "mali" 模式：
# 容器用 androidboot.redroid_gpu_mode=mali 启动时，设 ro.hardware.egl=mali，
# 让 Android EGL loader 去加载 /vendor/lib64/egl/libGLES_mali.so（硬件渲染），
# 而不是 redroid 默认的 mesa(host)/swiftshader(guest) 软件渲染。
# host/guest 两个原模式保留，逻辑照搬 redroid 原版。

setup_vulkan() {
    case "$1" in
        panfrost) setprop ro.hardware.vulkan panfrost ;;
        *) echo "vulkan driver not set: $1" ;;
    esac
}

setup_render_node() {
    cd /sys/kernel/debug/dri || return 1
    for d in * ; do
        if [ "$d" -ge "128" ] 2>/dev/null; then
            driver="$(cut -d' ' -f1 "$d/name" 2>/dev/null)"
            node="/dev/dri/renderD$d"
            [ -e "$node" ] && setprop gralloc.gbm.device "$node" && chmod 666 "$node"
        fi
    done
    return 0
}

# ---- Mali 硬件直通（魔云腾 Q14/Q15 的 Mali 栈 graft 到 redroid） ----
gpu_setup_mali() {
    echo "use GPU mali (hardware) mode"
    setprop ro.hardware.egl mali
    setprop ro.hardware.gralloc default
    setprop ro.boot.redroid_fps 60
    for n in /dev/mali0 /dev/mpp_service ; do [ -e "$n" ] && chmod 666 "$n"; done
    [ -d /dev/dri ]      && chmod -R 666 /dev/dri/* 2>/dev/null
    [ -d /dev/dma_heap ] && chmod -R 666 /dev/dma_heap/* 2>/dev/null
}

gpu_setup_host() {
    echo "use GPU host mode"
    setprop ro.hardware.egl mesa
    setprop ro.hardware.gralloc gbm
    setprop ro.boot.redroid_fps 30
}

gpu_setup_guest() {
    echo "use GPU guest mode"
    VENDOR_EGL_DIR=/vendor/lib64/egl
    SYSTEM_EGL_DIR=/system/lib64
    egl=
    if [ -f $VENDOR_EGL_DIR/libEGL_angle.so ] || [ -f $SYSTEM_EGL_DIR/libEGL_angle.so ]; then
        egl=angle
    elif [ -f $VENDOR_EGL_DIR/libEGL_swiftshader.so ] || [ -f $SYSTEM_EGL_DIR/libEGL_swiftshader.so ]; then
        egl=swiftshader
    else
        echo "ERROR no SW egl found!!!"
    fi
    setprop ro.hardware.egl $egl
    setprop ro.hardware.gralloc redroid
    setprop ro.hardware.vulkan pastel
}

gpu_setup() {
    mode=$(getprop ro.boot.redroid_gpu_mode guest)
    if [ "$mode" = "mali" ]; then
        gpu_setup_mali
    elif [ "$mode" = "host" ]; then
        setup_render_node
        gpu_setup_host
    elif [ "$mode" = "guest" ]; then
        gpu_setup_guest
    elif [ "$mode" = "auto" ]; then
        if [ -e /dev/mali0 ]; then gpu_setup_mali; else gpu_setup_guest; fi
    else
        echo "unknown mode: $mode"; gpu_setup_guest
    fi
}

gpu_setup
