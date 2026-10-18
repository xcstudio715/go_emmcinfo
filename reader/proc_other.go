//go:build !windows

package reader

import "os/exec"

// hideWindow 在非 Windows 平台为空实现（不存在为子进程新建控制台窗口的问题）。
func hideWindow(cmd *exec.Cmd) {}
