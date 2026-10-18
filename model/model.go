// Package model 定义 reader 与 decoder 共享的纯数据契约。
// reader 与 decoder 都依赖本包，二者互不依赖，避免 import cycle。
package model

// Unreadable 为节点存在但不可读时 reader 写入字段的占位符。
// decoder 见到该值会显式显示"不可读"，不做解码。
const Unreadable = "(不可读)"

// RawInfo 为 reader 从设备采集到的原始字段（未经任何解码）。
// 空字符串表示未采集到；Unreadable 表示节点存在但不可读。
type RawInfo struct {
	Name     string
	CID      string
	Rev      string
	ManfID   string
	OEMID    string
	HWRev    string
	FWRev    string
	Serial   string
	Date     string
	LifeTime string
	PreEOL   string
}

// CIDResult 为 Card Identification 寄存器的解析结果。
//
// 注：**不解析 CID b[14] 的制造日期（MDT）**。该字节的年份基准缺少规范依据
// （JEDEC 的 1997/2000 起算规则都解不出真机的 04/2017），属于从 sysfs date
// 反推的拟合常数，实现与测试同源、无法证伪，故整体删除。制造日期一律以
// sysfs 的 date 节点为准（见 reader.RawInfo.Date）。
type CIDResult struct {
	Raw   string // 原始 32 位十六进制串
	Valid bool   // 长度/格式合法时为 true
	MID   byte   // 厂商 ID
	OID   byte   // OEM / 应用 ID
	PNM   string // 6 字节 ASCII 产品名
	PRV   string // 产品版本，形如 "0.7"
	PSN   uint32 // 产品序列号
}
