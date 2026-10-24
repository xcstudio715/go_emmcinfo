// emmc-tool：Go + Fyne 单文件 eMMC 读取工具。
//
// 中文显示方案：内嵌思源黑体子集 + 自定义 fyne.Theme（ui.NotoTheme），
// **不依赖 FYNE_FONT 环境变量**、不使用 `fyne bundle`。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"

	"fyne.io/fyne/v2/app"

	"emmc-tool/ui"
)

// logPanic 把启动期/运行期 panic 落盘到 %TEMP%\emmc-tool.log，并弹窗告知、以非零码退出。
//
// -H windowsgui 构建没有控制台，任何启动期 panic 对用户完全静默；只写日志会让用户
// 误以为程序损坏，故再弹一次 MessageBox，最后 os.Exit(1) 明确失败退出。
func logPanic() {
	if r := recover(); r != nil {
		logPath := filepath.Join(os.TempDir(), "emmc-tool.log")
		msg := fmt.Sprintf("panic: %v\n\n%s", r, debug.Stack())
		_ = os.WriteFile(logPath, []byte(msg), 0o644)

		notifyPanic("启动失败，详情见 " + logPath)
		os.Exit(1)
	}
}

func main() {
	defer logPanic()

	// 必须用 NewWithID：无唯一 ID 的 app.New() 的 Preferences 为纯内存实现，
	// 不落盘（%APPDATA%\fyne\<ID>\preferences.json），且每次访问打 "unique ID" 告警。
	a := app.NewWithID("io.github.xchejin.go-emmcinfo")
	a.Settings().SetTheme(ui.NotoTheme{})
	ui.NewApp(a).Run()
}
