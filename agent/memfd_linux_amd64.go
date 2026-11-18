//go:build linux && amd64

package main

// memfd_create 的系统调用号（amd64，开发机跑 go test 用）。生产是 arm64。
const sysMemfdCreate = 319
