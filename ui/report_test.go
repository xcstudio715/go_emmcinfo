package ui

import (
	"strings"
	"testing"
	"time"

	"emmc-tool/decoder"
	"emmc-tool/model"
	"emmc-tool/reader"
)

// realRaw 为真机（Android 13 / Samsung eMMC）抓取到的原始字段。
func realRaw() model.RawInfo {
	return model.RawInfo{
		Name:     "RE1BMB",
		CID:      "150100524531424d420726fa9bae442b",
		Rev:      "0x8",
		ManfID:   "0x000015",
		OEMID:    "0x0100",
		HWRev:    "0x0",
		FWRev:    "0x0700000000000000",
		Serial:   "0x26fa9bae",
		Date:     "04/2017",
		LifeTime: "0x01 0x01",
		PreEOL:   "0x01",
	}
}

func TestBuildReportRealDevice(t *testing.T) {
	raw := realRaw()
	in := ReportInput{
		Meta: ReportMeta{
			Platform: platformAndroid,
			Device:   "R58M123456A",
			Source:   "/sys/class/mmc_host/mmc0/mmc0:0001",
			ReadAt:   time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC),
		},
		Raw:    raw,
		Result: decoder.Interpret(raw),
		// mmcblk1 为真机上的 SD 卡，必须被排除（只列本次发现的 mmcblk0 及其分区）。
		Parts: []reader.Partition{
			{Major: 179, Minor: 0, Blocks: 15267840, Name: "mmcblk0"},
			{Major: 179, Minor: 1, Blocks: 1024, Name: "mmcblk0p1"},
			{Major: 179, Minor: 96, Blocks: 30533632, Name: "mmcblk1"},
		},
		BlockName: "mmcblk0",
	}

	rep := BuildReport(in)
	for _, want := range []string{
		"eMMC 读取报告",
		"生成时间: 2026-10-17 12:00:00",
		"平台: Android (ADB)",
		"数据来源: /sys/class/mmc_host/mmc0/mmc0:0001",
		"厂商: Samsung (0x15)",
		"eMMC 版本: eMMC 5.1 (0x8)",
		"产品名 PNM: RE1BMB",
		"制造日期: 04/2017",
		"SLC / Type A: 0%–10% 已用",
		"MLC-TLC / Type B: 0%–10% 已用",
		"预 EOL pre_eol_info",
		"块设备: mmcblk0",
		"整盘容量: 7.28 GiB",
		"mmcblk0p1",
		"== 详细 ==",
	} {
		if !strings.Contains(rep, want) {
			t.Errorf("报告缺少 %q\n---\n%s", want, rep)
		}
	}
	if strings.Contains(rep, "mmcblk1") {
		t.Errorf("报告不应包含非本次设备的 mmcblk1\n---\n%s", rep)
	}
	// 制造日期一律来自 sysfs date 节点，报告里不应再出现 CID 反推的 MDT。
	if strings.Contains(rep, "MDT") {
		t.Errorf("报告不应再展示 CID 反推的 MDT\n---\n%s", rep)
	}
}

func TestBuildReportEmptyIsReadable(t *testing.T) {
	rep := BuildReport(ReportInput{Meta: ReportMeta{ReadAt: time.Unix(0, 0)}})
	for _, want := range []string{"厂商: 不可读", "eMMC 版本: 不可读", "整盘容量: 不可读", "制造日期: 不可读", "分区（来自 /proc/partitions）"} {
		if !strings.Contains(rep, want) {
			t.Errorf("空结果报告缺少 %q\n---\n%s", want, rep)
		}
	}
}
