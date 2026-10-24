//go:build !windows

package main

import (
	"fmt"
	"os"
)

// notifyPanic 在非 Windows 平台把提示写到 stderr（无 GUI 弹窗）。
func notifyPanic(msg string) {
	fmt.Fprintln(os.Stderr, "eMMC 读取工具 启动失败: "+msg)
}
