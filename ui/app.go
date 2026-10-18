package ui

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"emmc-tool/decoder"
	"emmc-tool/reader"
)

// 平台选项。
const (
	platformAuto    = "自动"
	platformAndroid = "Android (ADB)"
	platformLocal   = "本机 (Linux sysfs)"
)

const aboutText = `eMMC 读取工具（Go + Fyne 单文件版）

作者
XChejin


用途
  读取 Android 设备（ADB）或本机（Linux sysfs）eMMC 的
  CID / 版本 / 厂商 / 寿命 / 预 EOL / 分区容量等信息。

数据来源
  Android: /sys/class/mmc_host/<host>/<host>:XXXX/
  本机:    /sys/class/mmc_host/（仅 Linux）

说明
  任意字段不可读都会在"详细"页列出节点路径与原因；
  底部"导出报告"可把当前结果保存为纯文本。
`

// App 承载主窗口与全部交互。
type App struct {
	fyneApp fyne.App
	win     fyne.Window

	platform *widget.RadioGroup
	device   *widget.Select
	status   *widget.Label

	basicTab  *readOnlyView
	healthTab *readOnlyView
	partTab   *readOnlyView
	detailTab *readOnlyView

	readBtn   *widget.Button
	exportBtn *widget.Button

	last    ReportInput
	hasLast bool
	busy    bool
	prog    reader.Progress
}

// NewApp 构造主窗口（不启动事件循环）。
func NewApp(a fyne.App) *App {
	ui := &App{fyneApp: a}
	ui.win = a.NewWindow("eMMC 读取工具")

	ui.status = widget.NewLabel("就绪")
	ui.status.Wrapping = fyne.TextWrapWord

	// 先建好设备下拉再建平台单选：RadioGroup 的 OnChanged 在 SetSelected 时立即触发，
	// 回调会用到 ui.device，此时必须已赋值，否则会 nil 解引用导致启动期 panic。
	ui.device = widget.NewSelect(nil, nil)
	ui.device.PlaceHolder = "自动选择"

	ui.platform = widget.NewRadioGroup([]string{platformAuto, platformAndroid, platformLocal}, func(v string) {
		if v == platformLocal {
			ui.device.Disable()
		} else {
			ui.device.Enable()
		}
	})
	ui.platform.Horizontal = true
	ui.platform.SetSelected(platformAuto)

	refreshBtn := widget.NewButton("刷新设备", ui.RefreshDevices)

	ui.basicTab = newReadOnlyView()
	ui.healthTab = newReadOnlyView()
	ui.partTab = newReadOnlyView()
	ui.detailTab = newReadOnlyView()
	aboutTab := newReadOnlyView()
	aboutTab.SetReadOnlyText(aboutText)

	ui.readBtn = widget.NewButton("读取全部", ui.ReadAll)
	ui.exportBtn = widget.NewButton("导出报告", ui.ExportReport)
	ui.exportBtn.Disable()

	// 操作行放顶部而非底部：部分 DPI 缩放组合下底部 Border 行会被布局挤到可视区域
	// 之外（实测 125% 缩放下"读取全部/导出报告"不可见），顶部行始终可见、可点。
	top := container.NewVBox(
		container.NewHBox(
			widget.NewLabel("平台:"), ui.platform,
			widget.NewLabel("设备:"), ui.device,
			refreshBtn,
		),
		container.NewHBox(ui.readBtn, ui.exportBtn),
		ui.status,
	)

	// 每个标签页的只读文本框套一层滚动容器：MultiLineEntry 的 MinSize 会随内容行数
	// 增长，套滚动后长文本在页内滚动，不会撑大整体布局、挤走操作行。
	tabs := container.NewAppTabs(
		container.NewTabItem("基本信息", container.NewScroll(ui.basicTab.Entry)),
		container.NewTabItem("健康度", container.NewScroll(ui.healthTab.Entry)),
		container.NewTabItem("分区容量", container.NewScroll(ui.partTab.Entry)),
		container.NewTabItem("详细", container.NewScroll(ui.detailTab.Entry)),
		container.NewTabItem("关于", container.NewScroll(aboutTab.Entry)),
	)

	ui.win.SetContent(container.NewBorder(top, nil, nil, nil, tabs))
	ui.win.Resize(fyne.NewSize(960, 720))
	return ui
}

// Run 显示窗口并进入 Fyne 事件循环（阻塞直到窗口关闭）。
func (a *App) Run() {
	a.win.ShowAndRun()
}

// RefreshDevices 异步列出在线 Android 设备并刷新下拉框。
func (a *App) RefreshDevices() {
	adbPath, err := reader.ResolveADB()
	if err != nil {
		a.setStatus("未找到 adb: " + err.Error())
		return
	}
	a.setStatus("正在列出设备…")

	go func() {
		c := reader.NewAdbClient(adbPath, "", nil)
		devs, derr := c.Devices()
		fyne.Do(func() {
			var opts []string
			for _, d := range devs {
				if d.State == "device" {
					opts = append(opts, d.Serial)
				}
			}
			a.device.SetOptions(opts)
			switch {
			case derr != nil:
				a.setStatus("列出设备失败: " + derr.Error())
			case len(opts) == 0:
				a.setStatus("未发现处于 device 状态的设备")
			default:
				a.setStatus(fmt.Sprintf("发现 %d 台在线设备", len(opts)))
			}
		})
	}()
}

// ReadAll 异步执行一次完整读取，完成后在主线程渲染。
//
// 保持原有"后台 goroutine + fyne.Do 回主线程"的异步结构不变，仅新增：
//   - 阶段进度（读取中 x/y 阶段）；
//   - 1 秒心跳的"已用秒数"，避免批量读取期间用户误以为卡死。
func (a *App) ReadAll() {
	if a.busy {
		return
	}
	a.busy = true
	a.readBtn.Disable()
	a.exportBtn.Disable()

	platform := a.platform.Selected
	serial := a.device.Selected

	start := time.Now()
	a.prog = reader.Progress{Stage: 0, Total: androidStages, Name: "准备"}
	a.setStatus(progressText(a.prog, start))

	done := make(chan struct{})
	go func() {
		defer close(done)
		in, err := collect(platform, serial, func(p reader.Progress) {
			fyne.Do(func() {
				a.prog = p
				if a.busy {
					a.setStatus(progressText(a.prog, start))
				}
			})
		})
		fyne.Do(func() {
			a.busy = false
			a.readBtn.Enable()
			if err != nil {
				a.setStatus("读取失败: " + err.Error())
				a.detailTab.SetReadOnlyText("读取失败: " + err.Error())
				return
			}
			a.last = in
			a.hasLast = true
			a.render(in)
			a.exportBtn.Enable()
			a.setStatus(fmt.Sprintf("读取完成（用时 %.1f 秒）", time.Since(start).Seconds()))
		})
	}()

	// 心跳：让用户看到秒数在走，不误判为卡死。
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				fyne.Do(func() {
					if a.busy {
						a.setStatus(progressText(a.prog, start))
					}
				})
			}
		}
	}()
}

// progressText 生成带阶段与已用秒数的进度文案（如 "读取中 3/4 · 读取字段 · 已用 2 秒"）。
func progressText(p reader.Progress, start time.Time) string {
	elapsed := time.Since(start).Seconds()
	if p.Total > 0 && p.Stage > 0 {
		return fmt.Sprintf("读取中 %d/%d · %s · 已用 %.0f 秒", p.Stage, p.Total, p.Name, elapsed)
	}
	return fmt.Sprintf("读取中… 已用 %.0f 秒", elapsed)
}

// ExportReport 弹出保存对话框，把最近一次结果写成纯文本报告。
func (a *App) ExportReport() {
	if !a.hasLast {
		return
	}
	text := BuildReport(a.last)
	d := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		if err != nil {
			a.setStatus("导出失败: " + err.Error())
			return
		}
		if w == nil { // 用户取消
			return
		}
		if _, werr := w.Write([]byte(text)); werr != nil {
			a.setStatus("写入失败: " + werr.Error())
			w.Close()
			return
		}
		if cerr := w.Close(); cerr != nil {
			a.setStatus("关闭文件失败: " + cerr.Error())
			return
		}
		a.setStatus("已导出报告: " + w.URI().Name())
	}, a.win)
	d.SetFileName("emmc-report-" + time.Now().Format("20060102-150405") + ".txt")
	d.Show()
}

// render 把一次读取结果写入各标签页。
func (a *App) render(in ReportInput) {
	a.basicTab.SetReadOnlyText(FormatBasic(in))
	a.healthTab.SetReadOnlyText(FormatHealth(in))
	a.partTab.SetReadOnlyText(FormatPartitions(in))
	a.detailTab.SetReadOnlyText(FormatDetail(in))
}

func (a *App) setStatus(s string) {
	a.status.SetText(s)
}

// collect 按平台选择执行读取。全部为包级纯逻辑，便于将来单测。
func collect(platform, serial string, progress reader.ProgressFunc) (ReportInput, error) {
	switch platform {
	case platformLocal:
		return readLocal(progress)
	case platformAndroid:
		return readAndroid(serial, progress)
	default: // 自动：本机可用则优先本机（仅 Linux），失败再回退 Android。
		if reader.LocalSupported() {
			if in, err := readLocal(progress); err == nil {
				return in, nil
			}
		}
		return readAndroid(serial, progress)
	}
}

// androidStages 为 Android 读取的阶段总数（devices / 发现 / 字段+block+分区表）。
const androidStages = 3

// readAndroid 经 ADB 发现 mmc 设备并读取全部字段、块设备与分区表。
//
// adb 进程启动次数目标 ≤3：devices(1，单次读取内缓存) + 发现(1) + 字段+block+分区表(1)。
// 批量失败时降级为逐字段 ReadAll + DiscoverBlock + ShellCat(/proc/partitions) 回退路径。
func readAndroid(serial string, progress reader.ProgressFunc) (ReportInput, error) {
	if progress != nil {
		progress(reader.Progress{Stage: 1, Total: androidStages, Name: "列出设备"})
	}
	adbPath, err := reader.ResolveADB()
	if err != nil {
		return ReportInput{}, err
	}
	c := reader.NewAdbClient(adbPath, serial, nil)

	devs, derr := c.Devices()
	if derr != nil && len(devs) == 0 {
		return ReportInput{}, fmt.Errorf("执行 adb devices 失败: %w", derr)
	}
	d, err := reader.SelectDevice(devs, serial)
	if err != nil {
		return ReportInput{}, err
	}
	c.Serial = d.Serial

	if progress != nil {
		progress(reader.Progress{Stage: 2, Total: androidStages, Name: "发现 mmc 设备"})
	}
	mmc, err := reader.DiscoverMMC(c)
	if err != nil {
		return ReportInput{}, err
	}

	if progress != nil {
		progress(reader.Progress{Stage: 3, Total: androidStages, Name: "读取字段与分区表"})
	}
	batch, berr := reader.ReadAllBatched(c, mmc.DeviceDir)
	if berr != nil {
		// 批量路径失败：降级为逐字段读取（旧路径），保证仍能出结果。
		raw, notes := reader.ReadAll(c, mmc.DeviceDir)
		block, blockErr := reader.DiscoverBlock(c, mmc)
		batch = reader.BatchResult{Raw: raw, Notes: notes, BlockName: block, BlockErr: blockErr}
		if out, perr := c.ShellCat("/proc/partitions"); perr == nil {
			batch.PartitionOut = out
		} else {
			reason, _ := reader.UnreadableReason(out, perr)
			batch.PartitionNote = &reader.FieldNote{Key: "partitions", Node: "/proc/partitions", Reason: reason}
		}
	}

	in := ReportInput{
		Meta: ReportMeta{
			Platform: platformAndroid,
			Device:   d.Serial,
			Source:   mmc.DeviceDir,
			ReadAt:   time.Now(),
		},
		Raw:       batch.Raw,
		Result:    decoder.Interpret(batch.Raw),
		Notes:     batch.Notes,
		BlockName: batch.BlockName,
	}
	if batch.BlockErr != nil {
		in.BlockErr = batch.BlockErr.Error()
	}
	if batch.PartitionNote != nil {
		in.Notes = append(in.Notes, *batch.PartitionNote)
	} else {
		in.Parts = reader.ParsePartitions(batch.PartitionOut)
	}
	return in, nil
}

// readLocal 读取本机 sysfs（仅 Linux）。
func readLocal(progress reader.ProgressFunc) (ReportInput, error) {
	if progress != nil {
		progress(reader.Progress{Stage: 1, Total: 1, Name: "读取本机 sysfs"})
	}
	raw, notes, err := reader.LocalRead()
	if err != nil {
		return ReportInput{}, err
	}
	in := ReportInput{
		Meta: ReportMeta{
			Platform: "本机 (" + runtime.GOOS + " sysfs)",
			Device:   "本机",
			Source:   "/sys/class/mmc_host/",
			ReadAt:   time.Now(),
		},
		Raw:    raw,
		Result: decoder.Interpret(raw),
		Notes:  notes,
	}
	if b, rerr := os.ReadFile("/proc/partitions"); rerr == nil {
		in.Parts = reader.ParsePartitions(string(b))
		in.BlockName = smallestMMCBlock(in.Parts)
	} else {
		in.BlockErr = rerr.Error()
	}
	return in, nil
}

// smallestMMCBlock 返回整盘块设备名（如 mmcblk0，不含 mmcblk0p1）。
func smallestMMCBlock(parts []reader.Partition) string {
	best, bestN := "", -1
	for _, p := range parts {
		if !reMMCBlock.MatchString(p.Name) {
			continue
		}
		n := 0
		fmt.Sscanf(strings.TrimPrefix(p.Name, "mmcblk"), "%d", &n)
		if bestN < 0 || n < bestN {
			best, bestN = p.Name, n
		}
	}
	return best
}

// readOnlyView 是**视觉正常、可选中复制、但用户无法修改**的多行文本框。
//
// 不使用 Entry.Disable()（禁用后无法选中复制）：改为拦截 OnChanged，
// 当内容被用户改动时立即还原为最后一次程序设置的值。
type readOnlyView struct {
	*widget.Entry
	guard bool
	last  string
}

func newReadOnlyView() *readOnlyView {
	e := widget.NewMultiLineEntry()
	e.Wrapping = fyne.TextWrapWord
	v := &readOnlyView{Entry: e}
	e.OnChanged = func(string) {
		if v.guard {
			return
		}
		v.guard = true
		v.Entry.SetText(v.last)
		v.guard = false
	}
	return v
}

// SetReadOnlyText 以程序方式更新内容（不触发还原逻辑）。
func (v *readOnlyView) SetReadOnlyText(s string) {
	v.last = s
	v.guard = true
	v.Entry.SetText(s)
	v.guard = false
}
