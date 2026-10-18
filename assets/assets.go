// Package assets 承载随二进制一起分发的静态资源（此处为嵌入的中文字体）。
package assets

import _ "embed"

// fontTTF 为子集化后的思源黑体（Noto Sans SC，OFL 许可，可商用）。
//
//go:embed fonts/NotoSansSC-Regular.ttf
var fontTTF []byte

// FontTTF 返回嵌入的中文字体字节。
func FontTTF() []byte { return fontTTF }
