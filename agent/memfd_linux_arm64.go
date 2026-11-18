//go:build linux && arm64

package main

// memfd_create 的系统调用号（arm64）。startSensors 用它把启动器复制进匿名内存文件，
// 喂数据子进程就不再占着 /pp-init（踩坑 #87）。
const sysMemfdCreate = 279
