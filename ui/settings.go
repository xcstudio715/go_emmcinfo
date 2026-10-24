package ui

import (
	"os"
	"strconv"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"emmc-tool/decoder"
	"emmc-tool/reader"
)

// ---------------------------------------------------------------------------
// Preferences 键（经 Fyne Preferences 持久化；持久化生效依赖 main.go 的
// app.NewWithID("io.github.xchejin.go-emmcinfo")，无唯一 ID 时纯内存不落盘）
// ---------------------------------------------------------------------------

const (
	prefRootEnabled       = "root_enabled"        // root 模式开关状态（跨进程恢复）
	prefRootWarnDismissed = "root_warn_dismissed" // root 确认弹窗「不再提示」
	prefLifetimeDismissed = "lifetime_unsupported_dismissed"
)

// rootDescText 为设置页「读取权限」的三行说明，也用作 root 确认弹窗正文。
const rootDescText = "root 模式通过 su 以最高权限直读 eMMC 寿命节点（life_time / pre_eol_info）。\n" +
	"设备未 root 或授权被拒时自动回退普通读取，受保护节点将显示为不可读。\n" +
	"仅在 Android (ADB) 平台读取时生效，可随时在此关闭。"

// ---------------------------------------------------------------------------
// root 失败缓存：serial → 最近一次 root 不可用的原因。
// 已知不可用的设备再勾选开关时直接给原因回弹，不重试 su（避免设备端反复弹授权）；
// 命中缓存的那次手动切换会**清一次**该 serial 的缓存——再下一次勾选将重新探测，
// 给"刚在设备端完成授权"的场景重试机会。空 serial（未选择设备）同样入缓存，
// 保证无设备时勾选行为一致（不反复弹确认框+探测死 adb）。
// ---------------------------------------------------------------------------

var (
	rootFailMu    sync.Mutex
	rootFailCache = map[string]string{}
)

func cachedRootFail(serial string) (string, bool) {
	rootFailMu.Lock()
	defer rootFailMu.Unlock()
	r, ok := rootFailCache[serial]
	return r, ok
}

func cacheRootFail(serial, reason string) {
	rootFailMu.Lock()
	defer rootFailMu.Unlock()
	rootFailCache[serial] = reason
}

func clearRootFail(serial string) {
	rootFailMu.Lock()
	defer rootFailMu.Unlock()
	delete(rootFailCache, serial)
}

// ---------------------------------------------------------------------------
// SDK 缓存：serial → ro.build.version.sdk（避免每次读取重复 getprop）。
// 负值表示未知（查询失败），LifetimeSupport 对负值不参与判定。
// ---------------------------------------------------------------------------

var (
	sdkMu    sync.Mutex
	sdkCache = map[string]int{}
)

// ---------------------------------------------------------------------------
// 设置页
// ---------------------------------------------------------------------------

// buildSettingsTab 构造第 6 个标签页「设置」：
// 小标题「读取权限」+ root 模式开关 + 三行说明 + 分隔线 + 「重置所有提示」按钮。
func (a *App) buildSettingsTab() fyne.CanvasObject {
	title := widget.NewLabelWithStyle("读取权限", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	a.rootCheck = widget.NewCheck("root 模式", a.onRootCheckChanged)
	// 按 Preferences 恢复（默认 false）；程序化恢复走守卫，不触发手动切换状态机。
	if a.fyneApp.Preferences().Bool(prefRootEnabled) {
		a.rootGuard = true
		a.rootCheck.SetChecked(true)
		a.rootGuard = false
		a.useRoot = true
	}

	desc := widget.NewLabel(rootDescText)
	desc.Wrapping = fyne.TextWrapWord

	resetBtn := widget.NewButton("重置所有提示", a.resetAllNotices)

	return container.NewVBox(title, a.rootCheck, desc, widget.NewSeparator(), resetBtn)
}

// onRootCheckChanged 为 root 开关的手动切换状态机。
//
//   - 开→关：直接关闭（UseRoot=false、写 root_enabled=false），不弹窗；
//   - 关→开：该 serial 已知不可用（失败缓存命中）→ 直接回弹并给原因、不重试 su，
//     且该次手动切换清一次缓存（下次勾选重新探测）；root_warn_dismissed=true →
//     跳过弹窗直接探测；否则先回弹、再弹确认框（正文同三行说明 + 复选框
//     「不再提示」+「启用」/「取消」），点「启用」→ 异步 CheckRoot，成功则置位
//     并写 root_enabled=true，失败则缓存原因并提示；取消则保持关闭、不写回。
func (a *App) onRootCheckChanged(checked bool) {
	if a.rootGuard {
		return
	}
	serial := a.device.Selected

	if !checked {
		a.useRoot = false
		a.fyneApp.Preferences().SetBool(prefRootEnabled, false)
		return
	}

	if reason, bad := cachedRootFail(serial); bad {
		clearRootFail(serial) // 命中即清一次：下次勾选重新探测（覆盖"刚授权完"场景）
		a.bounceRootCheck()
		a.setStatus("root 模式不可用: " + reason)
		return
	}

	if !a.fyneApp.Preferences().Bool(prefRootWarnDismissed) {
		// 先回弹：点「启用」探测成功后再置位；取消则自然保持关闭、不写回。
		a.bounceRootCheck()
		a.showConfirm("启用 root 模式？", rootDescText, prefRootWarnDismissed, a.probeRoot)
		return
	}
	a.probeRoot()
}

// probeRoot 异步探测 root 可用性（不阻塞 UI）：
// 成功 → 勾选置位 + 写 root_enabled=true + UseRoot=true + 状态栏「root 模式已启用」；
// 失败 → 缓存原因 + 回弹 + 状态栏「root 模式不可用: <原因>」，勾选不写回。
func (a *App) probeRoot() {
	serial := a.device.Selected
	adbPath, err := reader.ResolveADB()
	if err != nil {
		reason := "未找到 adb: " + err.Error()
		cacheRootFail(serial, reason)
		a.bounceRootCheck()
		a.setStatus("root 模式不可用: " + reason)
		return
	}
	a.setStatus("正在探测 root…")
	go func() {
		c := reader.NewAdbClient(adbPath, serial, nil)
		ok, cerr := reader.CheckRoot(c)
		fyne.Do(func() {
			if ok {
				a.useRoot = true
				a.fyneApp.Preferences().SetBool(prefRootEnabled, true)
				a.rootGuard = true
				a.rootCheck.SetChecked(true)
				a.rootGuard = false
				a.setStatus("root 模式已启用")
				return
			}
			reason := "未知原因"
			if cerr != nil {
				reason = cerr.Error()
			}
			cacheRootFail(serial, reason)
			a.bounceRootCheck()
			a.setStatus("root 模式不可用: " + reason)
		})
	}()
}

// bounceRootCheck 把开关回弹为关（程序化置位走守卫，不触发手动状态机）。
func (a *App) bounceRootCheck() {
	a.rootGuard = true
	a.rootCheck.SetChecked(false)
	a.rootGuard = false
}

// resetAllNotices 清空全部「不再提示」标记：下次触发 root 确认与寿命提示时弹窗重现。
func (a *App) resetAllNotices() {
	p := a.fyneApp.Preferences()
	p.SetBool(prefRootWarnDismissed, false)
	p.SetBool(prefLifetimeDismissed, false)
	a.setStatus("已重置所有提示")
}

// showConfirm 确认型弹窗（root 开关用）：标题 + 正文 + 「不再提示」复选框，
// 按钮「启用」/「取消」。勾选「不再提示」且点「启用」才写 prefKey=true，再执行 onConfirm；
// 点「取消」或直接关窗不写任何状态。
func (a *App) showConfirm(title, body, prefKey string, onConfirm func()) {
	bodyLabel := widget.NewLabel(body)
	bodyLabel.Wrapping = fyne.TextWrapWord
	dontAsk := widget.NewCheck("不再提示", nil)
	content := container.NewVBox(bodyLabel, dontAsk)
	d := dialog.NewCustomConfirm(title, "启用", "取消", content, func(ok bool) {
		if !ok {
			return
		}
		if dontAsk.Checked {
			a.fyneApp.Preferences().SetBool(prefKey, true)
		}
		if onConfirm != nil {
			onConfirm()
		}
	}, a.win)
	d.Show()
}

// showInfo 通知型弹窗（寿命提示用）：标题 + 正文 + 「不再提示」复选框，
// **只有一个按钮「知道了」**。勾选「不再提示」后关闭（点「知道了」）才写 prefKey=true；
// 未勾选或未勾选直接 Esc 关窗不写。
func (a *App) showInfo(title, body, prefKey string) {
	bodyLabel := widget.NewLabel(body)
	bodyLabel.Wrapping = fyne.TextWrapWord
	dontAsk := widget.NewCheck("不再提示", nil)
	content := container.NewVBox(bodyLabel, dontAsk)
	d := dialog.NewCustom(title, "知道了", content, a.win)
	d.SetOnClosed(func() {
		if dontAsk.Checked {
			a.fyneApp.Preferences().SetBool(prefKey, true)
		}
	})
	d.Show()
}

// ---------------------------------------------------------------------------
// 寿命支持提示
// ---------------------------------------------------------------------------

// maybeNoticeLifetime 在一次 Android 读取完成后判定是否弹「寿命字段不可用」提示。
//
//   - 仅对 Android 平台的读取结果生效；
//   - lifetime_unsupported_dismissed=true 时不再弹；
//   - SDK 按 serial 缓存，未缓存时异步 getprop 查询——查询与弹窗都不得阻塞
//     "读取完成"状态栏文案的即时展示（本函数在状态栏更新之后调用，
//     查询在后台 goroutine 完成，结果经 fyne.Do 回 UI 线程）。
//   - 判定输入可被调试钩子覆盖：EMMC_FAKE_SDK / EMMC_FAKE_REV
//     （未设置或解析失败一律视为未设置，行为与生产一致）。
func (a *App) maybeNoticeLifetime(in ReportInput) {
	if in.Meta.Platform != platformAndroid {
		return
	}
	if a.fyneApp.Preferences().Bool(prefLifetimeDismissed) {
		return
	}
	sdk, revRaw := -1, in.Raw.Rev
	if v, ok := fakeEnvSDK(); ok {
		sdk = v
	} else if serial := in.Meta.Device; serial != "" {
		sdkMu.Lock()
		cached, have := sdkCache[serial]
		sdkMu.Unlock()
		if have {
			sdk = cached
		} else {
			go a.querySDKAndNotice(serial, in.Raw.Rev)
			return
		}
	}
	if v, ok := fakeEnvREV(); ok {
		revRaw = v
	}
	a.noticeLifetimeIfNeeded(sdk, revRaw)
}

// querySDKAndNotice 异步查询 ro.build.version.sdk 并续接寿命判定（后台 goroutine）。
func (a *App) querySDKAndNotice(serial, revRaw string) {
	sdk := -1
	if adbPath, err := reader.ResolveADB(); err == nil {
		c := reader.NewAdbClient(adbPath, serial, nil)
		if out, qerr := c.ShellScript("getprop ro.build.version.sdk"); qerr == nil {
			if n, perr := strconv.Atoi(strings.TrimSpace(out)); perr == nil {
				sdk = n
			}
		}
	}
	fyne.Do(func() {
		sdkMu.Lock()
		sdkCache[serial] = sdk
		sdkMu.Unlock()
		if a.fyneApp.Preferences().Bool(prefLifetimeDismissed) {
			return
		}
		a.noticeLifetimeIfNeeded(sdk, revRaw)
	})
}

// noticeLifetimeIfNeeded 依据 LifetimeSupport 结果弹提示（须在 UI 线程调用）。
func (a *App) noticeLifetimeIfNeeded(sdk int, revRaw string) {
	supported, reason := decoder.LifetimeSupport(sdk, revRaw)
	if supported {
		return
	}
	a.showInfo("寿命字段不可用", lifetimeBody(reason), prefLifetimeDismissed)
}

// lifetimeBody 按原因关键词分流三种弹窗正文（仅 Android / 仅 eMMC / 两者）。
func lifetimeBody(reason string) string {
	hasAndroid := strings.Contains(reason, "Android")
	hasEMMC := strings.Contains(reason, "eMMC")
	switch {
	case hasAndroid && hasEMMC:
		return "该设备的 Android 版本与 eMMC 版本均过低：系统未暴露寿命节点，" +
			"且该 eMMC 未定义寿命字段（life_time / pre_eol_info），健康度页将显示不可读。"
	case hasAndroid:
		return "该设备的 Android 版本过低：系统未向应用暴露寿命节点" +
			"（life_time / pre_eol_info），健康度页将显示不可读。"
	default:
		return "该设备的 eMMC 版本过低（低于 eMMC 5.0）：未定义寿命字段" +
			"（life_time / pre_eol_info），健康度页将显示不可读。"
	}
}

// ---------------------------------------------------------------------------
// 调试钩子：EMMC_FAKE_SDK / EMMC_FAKE_REV 覆盖寿命判定输入。
// 未设置或解析失败一律视为未设置，行为与生产一致。
// ---------------------------------------------------------------------------

// fakeEnvSDK 读取 EMMC_FAKE_SDK（十进制整数，如 "25"）。
func fakeEnvSDK() (int, bool) {
	s := strings.TrimSpace(os.Getenv("EMMC_FAKE_SDK"))
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// fakeEnvREV 读取 EMMC_FAKE_REV（十六进制 rev 原文，如 "0x6"）；
// 非法（无法按十六进制单字节归一化）视为未设置。
func fakeEnvREV() (string, bool) {
	s := strings.TrimSpace(os.Getenv("EMMC_FAKE_REV"))
	if s == "" {
		return "", false
	}
	if _, ok := decoder.NormalizeHexByte(s); !ok {
		return "", false
	}
	return s, true
}
