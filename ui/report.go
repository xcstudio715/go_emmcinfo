package ui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"emmc-tool/decoder"
	"emmc-tool/model"
	"emmc-tool/reader"
)

// ReportInput 汇总一次读取的全部产物，供界面各标签页与导出报告统一渲染。
// 全部字段均为只读快照，BuildReport / Format* 均为纯函数，便于单测。
type ReportInput struct {
	Meta  ReportMeta
	Raw   model.RawInfo
	Parts []reader.Partition
	Notes []reader.FieldNote

	BlockName string // 块设备名，如 "mmcblk0"
	BlockErr  string // 块设备发现失败原因（成功时为空）

	Result decoder.Result // 由 Raw 解码得到（decoder.Interpret）
}

// ReportMeta 为与设备数据无关的元信息（来源、时间）。
type ReportMeta struct {
	Platform string    // 例 "Android (ADB)" / "本机 (windows sysfs)"
	Device   string    // 设备序列号或 "本机"
	Source   string    // 数据来源路径，如 /sys/class/mmc_host/mmc0/mmc0:0001
	ReadAt   time.Time // 读取时间
}

// reMMCBlock 匹配整盘块设备名（mmcblk0），不含分区（mmcblk0p1）。
var reMMCBlock = regexp.MustCompile(`^mmcblk\d+$`)

// mmcBlockOwner 返回块设备名所属的整盘设备（mmcblk0p1 -> mmcblk0）。
// 不属于任何 mmcblk 时返回空串。
func mmcBlockOwner(name string) string {
	if reMMCBlock.MatchString(name) {
		return name
	}
	if i := strings.Index(name, "p"); i > 0 {
		if head := name[:i]; reMMCBlock.MatchString(head) {
			return head
		}
	}
	return ""
}

// orNA 把空串 / "(不可读)" 统一渲染为"不可读"。
func orNA(s string) string {
	t := strings.TrimSpace(s)
	if t == "" || t == model.Unreadable {
		return "不可读"
	}
	return t
}

// FormatBasic 渲染"基本信息"标签页。
func FormatBasic(in ReportInput) string {
	res := in.Result
	c := res.CID

	vendor := orNA(res.Vendor)
	if v := strings.TrimSpace(res.VendorID); v != "" && v != model.Unreadable {
		vendor = strings.TrimSpace(res.Vendor) + " (" + v + ")"
	}

	psn := "不可读"
	if c.Valid {
		psn = fmt.Sprintf("0x%08x", c.PSN)
	}

	lines := []string{
		"厂商: " + vendor,
		"eMMC 版本: " + orNA(res.EMMCVersion),
		"OEM / 应用 ID: " + orNA(res.OEM),
		"产品名 PNM: " + orNA(c.PNM),
		"产品版本 PRV: " + orNA(c.PRV),
		"序列号 PSN: " + psn,
		"制造日期: " + orNA(in.Raw.Date),
		"设备名 (sysfs name): " + orNA(in.Raw.Name),
	}
	return strings.Join(lines, "\n") + "\n"
}

// FormatHealth 渲染"健康度"标签页（life_time 两段 + pre_eol_info）。
func FormatHealth(in ReportInput) string {
	res := in.Result
	var b strings.Builder

	b.WriteString("寿命 life_time\n")
	if res.LifeTimeSLC == "" {
		b.WriteString("  SLC / Type A: 不可读\n")
		b.WriteString("  MLC-TLC / Type B: 不可读\n")
	} else {
		b.WriteString("  SLC / Type A: " + res.LifeTimeSLC + "\n")
		if res.LifeTimeMLC == "" {
			b.WriteString("  MLC-TLC / Type B: 无第二段（原始值仅一段）\n")
		} else {
			b.WriteString("  MLC-TLC / Type B: " + res.LifeTimeMLC + "\n")
		}
	}
	b.WriteString("  原始值: " + orNA(res.LifeTimeRaw) + "\n\n")

	b.WriteString("预 EOL pre_eol_info\n")
	b.WriteString("  状态: " + orNA(res.PreEOLStatus) + "\n")
	b.WriteString("  原始值: " + orNA(res.PreEOLRaw) + "\n")
	return b.String()
}

// blockCapacity 返回整盘容量（GiB）。
func blockCapacity(in ReportInput) (float64, bool) {
	if in.BlockName == "" {
		return 0, false
	}
	for _, p := range in.Parts {
		if p.Name == in.BlockName {
			return reader.CapacityGiB(p.Blocks), true
		}
	}
	return 0, false
}

// FormatPartitions 渲染"分区容量"标签页。
func FormatPartitions(in ReportInput) string {
	var b strings.Builder
	b.WriteString("块设备: " + orNA(in.BlockName) + "\n")
	if in.BlockErr != "" {
		b.WriteString("块设备发现失败: " + in.BlockErr + "\n")
	}
	if gib, ok := blockCapacity(in); ok {
		b.WriteString("整盘容量: " + reader.FormatGiB(gib) + "\n")
	} else {
		b.WriteString("整盘容量: 不可读\n")
	}

	b.WriteString("\n分区（来自 /proc/partitions）\n")
	if len(in.Parts) == 0 {
		b.WriteString("  不可读\n")
		return b.String()
	}
	shown := 0
	for _, p := range in.Parts {
		// 只列本次发现的那块设备及其分区，避免混入 SD 卡（如 mmcblk1）。
		if in.BlockName != "" && mmcBlockOwner(p.Name) != in.BlockName {
			continue
		}
		shown++
		fmt.Fprintf(&b, "  %-14s %s\n", p.Name, reader.FormatGiB(reader.CapacityGiB(p.Blocks)))
	}
	if shown == 0 {
		b.WriteString("  无 mmcblk 条目\n")
	}
	return b.String()
}

// rawFieldList 按采集顺序列出原始字段。
func rawFieldList(r model.RawInfo) [][2]string {
	return [][2]string{
		{"name", r.Name},
		{"cid", r.CID},
		{"rev", r.Rev},
		{"manfid", r.ManfID},
		{"oemid", r.OEMID},
		{"hwrev", r.HWRev},
		{"fwrev", r.FWRev},
		{"serial", r.Serial},
		{"date", r.Date},
		{"life_time", r.LifeTime},
		{"pre_eol_info", r.PreEOL},
	}
}

// FormatDetail 渲染"详细"标签页（原始字段 + CID 解析 + 不可读原因）。
func FormatDetail(in ReportInput) string {
	var b strings.Builder
	c := in.Result.CID

	b.WriteString("原始字段（reader 采集，未解码）\n")
	for _, kv := range rawFieldList(in.Raw) {
		fmt.Fprintf(&b, "  %s: %s\n", kv[0], orNA(kv[1]))
	}

	b.WriteString("\nCID 解析\n")
	if !c.Valid {
		b.WriteString("  CID 无效或不可读: " + orNA(c.Raw) + "\n")
	} else {
		fmt.Fprintf(&b, "  RAW: %s\n", c.Raw)
		fmt.Fprintf(&b, "  MID: 0x%02x\n", c.MID)
		fmt.Fprintf(&b, "  OID: 0x%02x\n", c.OID)
		b.WriteString("  PNM: " + orNA(c.PNM) + "\n")
		b.WriteString("  PRV: " + orNA(c.PRV) + "\n")
		fmt.Fprintf(&b, "  PSN: 0x%08x\n", c.PSN)
	}

	if len(in.Notes) > 0 {
		b.WriteString("\n不可读字段与原因\n")
		for _, n := range in.Notes {
			fmt.Fprintf(&b, "  %s (%s): %s\n", n.Key, n.Node, n.Reason)
		}
	}
	return b.String()
}

// BuildReport 生成可导出的纯文本报告。
func BuildReport(in ReportInput) string {
	var b strings.Builder
	b.WriteString("eMMC 读取报告\n")
	b.WriteString("生成时间: " + in.Meta.ReadAt.Format("2006-01-02 15:04:05") + "\n")
	b.WriteString("平台: " + orNA(in.Meta.Platform) + "\n")
	b.WriteString("设备: " + orNA(in.Meta.Device) + "\n")
	b.WriteString("数据来源: " + orNA(in.Meta.Source) + "\n")

	b.WriteString("\n== 基本信息 ==\n" + FormatBasic(in))
	b.WriteString("\n== 健康度 ==\n" + FormatHealth(in))
	b.WriteString("\n== 分区容量 ==\n" + FormatPartitions(in))
	b.WriteString("\n== 详细 ==\n" + FormatDetail(in))
	return b.String()
}
