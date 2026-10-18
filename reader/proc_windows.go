//go:build windows

package reader

import (
	"os/exec"
	"syscall"
)

// hideWindow 让子进程（adb.exe 等控制台程序）不分配、不显示控制台窗口。
//
// 根因：-H windowsgui 构建的父进程没有控制台，Windows 会为控制台子进程
// 分配一个新控制台（表现为读取时反复闪 cmd 黑框）。
//
// 两个字段必须**并用**，原因不同：
//   - HideWindow 只设 STARTUPINFO 的 SW_HIDE（隐藏窗口）：控制台**仍然会被分配**，
//     conhost 照旧拉起，仍可能闪一帧后才隐藏。
//   - CreationFlags = CREATE_NO_WINDOW (0x08000000) 才是"**不为子进程分配控制台**"
//     （CreateProcess 语义：进程被创建时不创建控制台，也不继承父进程控制台），
//     从根上不产生窗口。
//
// 不影响输出：exec.Cmd.CombinedOutput 通过匿名管道回传 stdout/stderr，
// 走的是句柄重定向，与子进程是否拥有控制台完全无关，故两种 flag 下输出都完整。
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW：不分配控制台
	}
}
