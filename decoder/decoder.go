// Package decoder 提供 eMMC 字段的纯函数解码：输入字符串 → 结构化结果。
//
// 只依赖 emmc-tool/model，**不 import reader / ui / fyne / os/exec**，可独立单测。
// 这是本工程对 Python 版 §6.1（life_time/pre_eol 十六进制解码失效）与
// §6.2（厂商表键空间混用）两处缺陷的修复主战场。
package decoder

import (
	"fmt"
	"strconv"
	"strings"

	"emmc-tool/model"
)

// Result 为一次解码的完整产物。
type Result struct {
	Vendor       string          // 厂商名
	VendorID     string          // 归一化后的厂商号，如 "0x15"
	EMMCVersion  string          // rev→版本，如 "eMMC 5.1 (0x8)"
	LifeTimeSLC  string          // life_time 第 1 段（SLC / Type A）
	LifeTimeMLC  string          // life_time 第 2 段（MLC-TLC / Type B）；无第 2 段则为空
	LifeTimeRaw  string          // life_time 原始串
	PreEOLStatus string          // pre_eol_info 解码
	PreEOLRaw    string          // pre_eol_info 原始串
	OEM          string          // OEM / 应用 ID 解码
	CID          model.CIDResult // CID 解析结果
}

// ---------------------------------------------------------------------------
// 归一化：厂商 ID（显式来源，不按字符串长度猜进制）
// ---------------------------------------------------------------------------

// MIDFromHex 解析**十六进制来源**的厂商 ID（CID 前 2 位、sysfs manfid 节点）。
//
// 规则：去空白 → 去可选 0x/0X 前缀 → 校验全为十六进制字符 → 取末 2 个十六进制字符
// → 作为低字节返回。失败（含非 hex 字符 / 空）返回 (0, false)。
//
// 例："0x000015"→0x15，"0x15"→0x15，"15"→0x15，"0x88"→0x88，"88"→0x88，"0x90"→0x90。
//
// 调用方通常经 NormalizeMID(s, SourceCID|SourceSysfsManfid) 间接使用，以确保来源语义显式。
func MIDFromHex(s string) (byte, bool) {
	t := stripHexPrefix(s)
	if !isHex(t) {
		return 0, false
	}
	if len(t) > 2 {
		t = t[len(t)-2:]
	}
	v, err := strconv.ParseUint(t, 16, 8)
	if err != nil {
		return 0, false
	}
	return byte(v), true
}

// MIDFromDecimal 解析**十进制来源**的厂商 ID。
//
// 规则：去空白 → 按十进制解析 → 取低字节。失败返回 (0, false)。
//
// 例："136"→0x88，"144"→0x90，"21"→0x15。
//
// 内核 mmc sysfs 的 manfid 节点以 "0x%06x" 输出、CID 为十六进制，二者**均为十六进制来源**；
// 内置读取路径不使用，仅用于显式声明十进制来源（经 NormalizeMID(s, SourceDecimal)）。
func MIDFromDecimal(s string) (byte, bool) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, false
	}
	v, err := strconv.ParseUint(t, 10, 64)
	if err != nil {
		return 0, false
	}
	return byte(v), true
}

// Source 指明 MID 字符串的**来源与进制语义**，从而避免"按长度/字符集猜进制"的静默误判。
type Source int

const (
	// SourceCID：CID 的前 2 个字符 —— 十六进制。
	SourceCID Source = iota
	// SourceSysfsManfid：/sys/.../manfid 节点 —— 内核以 "0x%06x" 输出，十六进制。
	SourceSysfsManfid
	// SourceDecimal：调用方**显式声明**为十进制来源。
	//
	// 触发条件：仅当调用方确知该值是十进制时使用（如外部工具/厂商 SDK 返回值）。
	// 内置读取器（ADB/本机 sysfs、CID）一律为十六进制，故 Interpret 不会走到这里。
	SourceDecimal
)

// String 便于日志与测试输出。
func (s Source) String() string {
	switch s {
	case SourceCID:
		return "CID"
	case SourceSysfsManfid:
		return "SysfsManfid"
	case SourceDecimal:
		return "Decimal"
	default:
		return "Unknown"
	}
}

// NormalizeMID 依据**显式来源**把 MID 串解析为低字节，不做任何进制猜测：
// SourceCID / SourceSysfsManfid → 按十六进制；SourceDecimal → 按十进制。
//
// 这是对"按长度猜进制"缺陷的修复：NormalizeMID("15", SourceSysfsManfid) = 0x15，
// 而 NormalizeMID("15", SourceDecimal) = 0x0f；同一形态输入在两条语义下取值不同，
// 由调用方显式声明，不存在静默误判。
func NormalizeMID(s string, src Source) (byte, bool) {
	switch src {
	case SourceCID, SourceSysfsManfid:
		return MIDFromHex(s)
	case SourceDecimal:
		return MIDFromDecimal(s)
	default:
		return 0, false
	}
}

// decodeVendor 按显式来源解析 MID 并查厂商表，返回 (厂商名, "0xNN")。
// 空/不可读 → ("", "")；解析失败 → ("未知制造商 (无法解析: …)", "")。
func decodeVendor(s string, src Source) (name, id string) {
	t := strings.TrimSpace(s)
	if t == "" || t == model.Unreadable {
		return "", ""
	}
	b, ok := NormalizeMID(t, src)
	if !ok {
		return "未知制造商 (无法解析: " + t + ")", ""
	}
	return vendorName(b), fmt.Sprintf("0x%02x", b)
}

// DecodeManufacturer 解码 **sysfs manfid 节点**（十六进制来源）。
// 空串或不可读 → ""（交由调用方回退到 CID）；解析失败 → "未知制造商 (无法解析: …)"。
func DecodeManufacturer(manfid string) string {
	name, _ := decodeVendor(manfid, SourceSysfsManfid)
	return name
}

// DecodeManufacturerFromDecimal 解码**显式十进制来源**的 manfid。
// 内置读取路径不使用，仅用于显式声明十进制来源（内核 sysfs 与 CID 均为十六进制）。
func DecodeManufacturerFromDecimal(manfid string) string {
	name, _ := decodeVendor(manfid, SourceDecimal)
	return name
}

// DecodeManufacturerFromCID 从 CID 前 2 个字符（十六进制来源）取 MID → 查厂商表。
// 与 sysfs manfid 路径**等价**（见报告 §7 真机确证）。
// 返回 (厂商名, "0xNN")；CID 非法时返回 ("", "")。
func DecodeManufacturerFromCID(cid string) (vendor, mid string) {
	r := DecodeCID(cid)
	if !r.Valid {
		return "", ""
	}
	return vendorName(r.MID), fmt.Sprintf("0x%02x", r.MID)
}

// vendorName 查厂商表；未命中返回"未知制造商 (0xNN)"。
func vendorName(mid byte) string {
	if name, ok := VendorTable[mid]; ok {
		return name
	}
	return fmt.Sprintf("未知制造商 (0x%02x)", mid)
}

// ---------------------------------------------------------------------------
// 归一化：单字节十六进制（§6.1 修复核心）
// ---------------------------------------------------------------------------

// NormalizeHexByte 把 sysfs 的十六进制单字节写法归一化为十进制整数。
//
// 兼容项目 B 的 0x01|01|1 三种写法（bat L57/L60）：去空白 → 去 0x/0X 前缀 →
// 校验全为十六进制字符 → 按 16 进制解析。失败返回 (0, false)。
//
// 例："0x01"→1，"01"→1，"1"→1，"0x0a"→10，"00"→0。
func NormalizeHexByte(s string) (int, bool) {
	t := stripHexPrefix(s)
	if !isHex(t) {
		return 0, false
	}
	v, err := strconv.ParseUint(t, 16, 16)
	if err != nil {
		return 0, false
	}
	return int(v), true
}

// ---------------------------------------------------------------------------
// 单字段解码
// ---------------------------------------------------------------------------

// DecodePreEOL 解码 pre_eol_info：先归一化（兼容 0x01|01|1）再查 PreEOLTable。
// 空串 → ""；不可读 → "不可读"；未命中/无法解析 → "未知 (原始值)"。
func DecodePreEOL(raw string) string {
	t := strings.TrimSpace(raw)
	if t == "" {
		return ""
	}
	if t == model.Unreadable {
		return "不可读"
	}
	n, ok := NormalizeHexByte(t)
	if !ok {
		return "未知 (" + t + ")"
	}
	if name, ok := PreEOLTable[n]; ok {
		return name
	}
	return fmt.Sprintf("未知 (%s)", t)
}

// DecodeLifeTime 解码 life_time。真机返回形如 "0x01 0x01" 的**两段**（SLC / MLC-TLC），
// 故**先按空白切分**再分别归一化解码（§6.1 关键修复点）；单值输入只填 SLC。
//
// 返回 (SLC 段, MLC 段, ok)。空串/不可读 → ok=false；不可读时 SLC 段为 "不可读"。
func DecodeLifeTime(raw string) (slc, mlc string, ok bool) {
	t := strings.TrimSpace(raw)
	if t == "" {
		return "", "", false
	}
	if t == model.Unreadable {
		return "不可读", "", false
	}
	fields := strings.Fields(t)
	if len(fields) == 0 {
		return "", "", false
	}
	slc = decodeLifeValue(fields[0])
	if len(fields) >= 2 {
		mlc = decodeLifeValue(fields[1])
	}
	return slc, mlc, true
}

// decodeLifeValue 将单段寿命值（可为 "0x01"/"01"/"1"）解码为磨损区间说明。
func decodeLifeValue(field string) string {
	n, ok := NormalizeHexByte(field)
	if !ok {
		return "未知 (" + strings.TrimSpace(field) + ")"
	}
	switch {
	case n == 0:
		return "未定义 (0x00)"
	case n >= 1 && n <= 10:
		return fmt.Sprintf("%d%%–%d%% 已用", (n-1)*10, n*10)
	case n == 11:
		return "已超出最大寿命 (0x0b)"
	default:
		return fmt.Sprintf("未知 (0x%02x)", n)
	}
}

// DecodeRev 解码 EXT_CSD_REV：归一化后查 RevTable，输出形如 "eMMC 5.1 (0x8)"。
// 空串 → ""；不可读 → "不可读"；未命中 → "未知 (0xN)"。
func DecodeRev(rev string) string {
	t := strings.TrimSpace(rev)
	if t == "" {
		return ""
	}
	if t == model.Unreadable {
		return "不可读"
	}
	n, ok := NormalizeHexByte(t)
	if !ok {
		return "未知 (" + t + ")"
	}
	if name, ok := RevTable[n]; ok {
		return fmt.Sprintf("%s (0x%x)", name, n)
	}
	return fmt.Sprintf("未知 (0x%x)", n)
}

// ---------------------------------------------------------------------------
// CID 解析
// ---------------------------------------------------------------------------

// DecodeCID 解析 16 字节（32 个十六进制字符）CID。
//
// 字节布局（0-based，MSB 先）：
//
//	b[0]    MID  厂商 ID
//	b[1]    reserved
//	b[2]    OID  OEM / 应用 ID
//	b[3:9]  PNM  产品名（6 字节 ASCII）
//	b[9]    PRV  产品版本，"主.次"
//	b[10:14] PSN 产品序列号（32-bit 大端）
//	b[14]   制造日期 MDT —— **不解析**（年份基准无规范依据，见 model.CIDResult 注释），
//	        制造日期一律以 sysfs 的 date 节点为准
//	b[15]   CRC7 + 结束位
//
// 长度/字符校验失败时返回 Valid=false 的零值。
func DecodeCID(cid string) model.CIDResult {
	res := model.CIDResult{Raw: strings.TrimSpace(cid)}
	t := stripHexPrefix(cid)
	if len(t) != 32 || !isHex(t) {
		return res
	}
	b := make([]byte, 16)
	for i := 0; i < 16; i++ {
		v, err := strconv.ParseUint(t[i*2:i*2+2], 16, 8)
		if err != nil {
			return res
		}
		b[i] = byte(v)
	}
	res.Valid = true
	res.MID = b[0]
	res.OID = b[2]
	res.PNM = productName(b[3:9])
	res.PRV = fmt.Sprintf("%d.%d", b[9]>>4, b[9]&0x0f)
	res.PSN = uint32(b[10])<<24 | uint32(b[11])<<16 | uint32(b[12])<<8 | uint32(b[13])
	return res
}

// productName 把 PNM 的 6 个字节渲染为可打印 ASCII 串（丢弃非可打印字节并去空白）。
func productName(b []byte) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c >= 0x20 && c <= 0x7e {
			out = append(out, c)
		}
	}
	return strings.TrimSpace(string(out))
}

// ---------------------------------------------------------------------------
// 汇总
// ---------------------------------------------------------------------------

// Interpret 一次性产出全部解码结果。
//
// 厂商优先取 sysfs manfid（若为空或不可读则回退 CID 的 MID）；
// life_time 两段分别解码，pre_eol / rev 归一化后查表。
func Interpret(raw model.RawInfo) Result {
	res := Result{
		LifeTimeRaw: strings.TrimSpace(raw.LifeTime),
		PreEOLRaw:   strings.TrimSpace(raw.PreEOL),
		CID:         DecodeCID(raw.CID),
	}

	res.LifeTimeSLC, res.LifeTimeMLC, _ = DecodeLifeTime(raw.LifeTime)
	res.PreEOLStatus = DecodePreEOL(raw.PreEOL)
	res.EMMCVersion = DecodeRev(raw.Rev)

	// 厂商：sysfs manfid（十六进制来源，内核以 0x%06x 输出）优先；
	// 缺失 / 不可读 / 无法解析时回退 CID 前 2 字符（同为十六进制）。
	if name, id := decodeVendor(raw.ManfID, SourceSysfsManfid); name != "" {
		res.Vendor, res.VendorID = name, id
	}
	if (res.Vendor == "" || strings.HasPrefix(res.Vendor, "未知")) && res.CID.Valid {
		res.Vendor = vendorName(res.CID.MID)
		res.VendorID = fmt.Sprintf("0x%02x", res.CID.MID)
	}

	// OEM：sysfs oemid 优先，缺失/不可读时回退 CID 的 OID。
	if o := strings.TrimSpace(raw.OEMID); o != "" && o != model.Unreadable {
		if n, ok := NormalizeHexByte(o); ok {
			if name, ok := OEMIDTable[n]; ok {
				res.OEM = name
			} else {
				res.OEM = fmt.Sprintf("OEM ID (0x%02x)", n)
			}
		} else {
			res.OEM = o
		}
	} else if res.CID.Valid {
		oid := int(res.CID.OID)
		if name, ok := OEMIDTable[oid]; ok {
			res.OEM = name
		} else {
			res.OEM = fmt.Sprintf("OEM ID (0x%02x)", res.CID.OID)
		}
	}

	return res
}

// ---------------------------------------------------------------------------
// 内部工具
// ---------------------------------------------------------------------------

// stripHexPrefix 去空白并去可选 0x/0X 前缀。
func stripHexPrefix(s string) string {
	t := strings.TrimSpace(s)
	t = strings.TrimPrefix(t, "0x")
	t = strings.TrimPrefix(t, "0X")
	return strings.TrimSpace(t)
}

// isHex 判断非空串是否全部为十六进制字符。
func isHex(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return false
	}
	return true
}
