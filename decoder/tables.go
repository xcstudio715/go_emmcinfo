package decoder

// RevTable 为 eMMC 版本号映射（EXT_CSD_REV[192]）。
//
// 来源：JEDEC JESD84 的 EXT_CSD_REV 取值表，经 Samsung eMMC 数据手册
// KLM8G1GETF-B041 Table 5 "Extended CSD revisions" 佐证；
// 其中 0x07→5.0、0x08→5.1 亦与项目 B（newemmc/emmcinfo261006.bat L54）一致。
//
// 0x04 在数据手册中标注为 Obsolete（Revision 1.4），故不臆造其对应 MMC 版本。
// 未列出的键一律由 DecodeRev 归为“未知”。
var RevTable = map[int]string{
	0x00: "eMMC 4.0",
	0x01: "eMMC 4.1",
	0x02: "eMMC 4.2",
	0x03: "eMMC 4.3",
	0x04: "已废弃 (Revision 1.4)",
	0x05: "eMMC 4.41",
	0x06: "eMMC 4.5/4.51",
	0x07: "eMMC 5.0",
	0x08: "eMMC 5.1",
}

// PreEOLTable 为 pre_eol_info 状态映射。
var PreEOLTable = map[int]string{
	0: "Normal / Not Defined (0x00)",
	1: "Normal (0x01)",
	2: "Warning: 已消耗约 80% (0x02)",
	3: "Urgent: 备用块耗尽 (0x03)",
}

// VendorTable 为厂商 ID 表，键 = MID 十六进制低字节。
// 覆盖用户点名 + 项目 B 表 + 公开锚点；仅收录语义明确者。
var VendorTable = map[byte]string{
	0x15: "Samsung",
	0x90: "SK Hynix",
	0x88: "YMTC",
	0xf4: "BIWIN",
	0xd6: "Longsys/FORESEE",
	0x04: "ISOCOM",
	0xdf: "SCY (Shichuangyi)",
	0xea: "SiliconGo (SiliconMotion)",
	0x45: "SanDisk/WD",
	0x11: "Toshiba/Kioxia",
	0x13: "Micron",
	0x2c: "Micron",
	0xfe: "Micron",
	0x70: "Kingston",
	0x02: "Toshiba",
	0x03: "SanDisk",
	0x41: "Kingston (SD)",
	0x1b: "Samsung (SD)",
}

// OEMIDTable 为尽力而为的 OEM/应用 ID 名称表；未收录者以十六进制显示。
var OEMIDTable = map[int]string{
	0x00: "未指定 (0x00)",
}
