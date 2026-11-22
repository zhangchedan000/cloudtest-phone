//go:build !(linux && arm64)

package timens

import "errors"

var helper []byte

func execHelper([]string) error { return errors.New("这个平台没有 pp-timens") }
