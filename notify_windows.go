//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// messageBoxW 调用 user32.dll 的 MessageBoxW 弹出模态提示框。
//
// -H windowsgui 构建没有控制台，启动期 panic 若只落盘日志，用户会误以为程序损坏，
// 故此处显式弹窗告知日志位置。返回前一直阻塞，直到用户点"确定"。
func messageBoxW(title, text string) {
	const mbIconError = 0x00000010 // MB_ICONERROR
	proc := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	proc.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), uintptr(mbIconError))
}

// notifyPanic 在 Windows 上弹窗提示启动失败。
func notifyPanic(msg string) {
	messageBoxW("eMMC 读取工具 启动失败", msg)
}
