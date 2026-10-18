// Package ui 提供 Fyne 桌面界面：主题（内嵌中文字体）、主窗口与文本报告。
package ui

import (
	"image/color"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"

	"emmc-tool/assets"
)

// NotoTheme 是基于 Fyne 默认主题、仅把字体替换为**内嵌中文字体**的主题。
//
// 目的：中文不出现"豆腐块"，且**不使用 FYNE_FONT 环境变量**、不使用 `fyne bundle`。
// Color / Icon / Size 一律代理 theme.DefaultTheme()，只覆盖 Font。
//
// 用法：`a.Settings().SetTheme(ui.NotoTheme{})`。
type NotoTheme struct{}

// notoFontRes 缓存嵌入字体资源：Fyne 会在渲染过程中反复调用 Font()，
// 故用 sync.Once 保证 fyne.NewStaticResource 只构造一次，不重复拷贝 2MB 字节。
var (
	notoFontOnce sync.Once
	notoFontRes  fyne.Resource
)

// notoFont 返回缓存的嵌入字体资源（Noto Sans SC 子集，OFL 许可）。
func notoFont() fyne.Resource {
	notoFontOnce.Do(func() {
		notoFontRes = fyne.NewStaticResource("NotoSansSC-Regular.ttf", assets.FontTTF())
	})
	return notoFontRes
}

// Font 返回内嵌中文字体。所有字重/等宽样式共用同一字体：
// 子集仅含 Regular，不做合成加粗，避免出现缺字回退（豆腐块）。
func (NotoTheme) Font(fyne.TextStyle) fyne.Resource {
	return notoFont()
}

// Color 代理默认主题配色。
func (NotoTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	return theme.DefaultTheme().Color(n, v)
}

// Icon 代理默认主题图标。
func (NotoTheme) Icon(n fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(n)
}

// Size 代理默认主题尺寸。
func (NotoTheme) Size(n fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(n)
}
