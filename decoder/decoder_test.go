package decoder

import (
	"reflect"
	"strings"
	"testing"

	"emmc-tool/model"
)

// 真机样例（报告 §6.1/§7，Android 13 / Samsung eMMC RE1BMB，未 root）。
const (
	realCID      = "150100524531424d420726fa9bae442b"
	realLifeTime = "0x01 0x01"
	realPreEOL   = "0x01"
	realRev      = "0x8"
	realManfID   = "0x000015"
)

func TestMIDFromHex(t *testing.T) {
	tests := []struct {
		in   string
		want byte
		ok   bool
	}{
		{"0x000015", 0x15, true},
		{"0x15", 0x15, true},
		{"15", 0x15, true},
		{"0x88", 0x88, true},
		{"88", 0x88, true},
		{"0x90", 0x90, true},
		{"0X90", 0x90, true},
		{"0xf4", 0xf4, true},
		{"d6", 0xd6, true},
		{"  0xea  ", 0xea, true},
		{"", 0, false},
		{"0x", 0, false},
		{"0xZZ", 0, false},
		{"gg", 0, false},
	}
	for _, tc := range tests {
		got, ok := MIDFromHex(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("MIDFromHex(%q) = (0x%02x,%v), want (0x%02x,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestMIDFromDecimal(t *testing.T) {
	tests := []struct {
		in   string
		want byte
		ok   bool
	}{
		{"136", 0x88, true}, // YMTC
		{"144", 0x90, true}, // Hynix
		{"21", 0x15, true},  // Samsung
		{"244", 0xf4, true}, // BIWIN
		{"  21 ", 0x15, true},
		{"", 0, false},
		{"abc", 0, false},
		{"0x15", 0, false}, // 带前缀不是纯十进制
	}
	for _, tc := range tests {
		got, ok := MIDFromDecimal(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("MIDFromDecimal(%q) = (0x%02x,%v), want (0x%02x,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// NormalizeMID：同一形态输入按**显式来源**走不同进制，杜绝静默误判（修订指令 #2）。
func TestNormalizeMID(t *testing.T) {
	tests := []struct {
		in   string
		src  Source
		want byte
		ok   bool
	}{
		{"15", SourceSysfsManfid, 0x15, true}, // 十六进制语义 → 0x15 (Samsung)
		{"15", SourceCID, 0x15, true},         // 十六进制语义 → 0x15
		{"15", SourceDecimal, 15, true},       // 十进制语义 → 0x0f（与上面不同，但无歧义）
		{"0x15", SourceSysfsManfid, 0x15, true},
		{"0x000015", SourceSysfsManfid, 0x15, true},
		{"136", SourceDecimal, 0x88, true}, // YMTC
		{"144", SourceDecimal, 0x90, true}, // Hynix
		{"21", SourceDecimal, 0x15, true},  // Samsung
		{"", SourceSysfsManfid, 0, false},
		{"0xZZ", SourceSysfsManfid, 0, false},
		{"abc", SourceDecimal, 0, false},
		{"15", Source(99), 0, false}, // 非法来源 → 显式失败，不猜
	}
	for _, tc := range tests {
		got, ok := NormalizeMID(tc.in, tc.src)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("NormalizeMID(%q,%s) = (0x%02x,%v), want (0x%02x,%v)", tc.in, tc.src, got, ok, tc.want, tc.ok)
		}
	}
}

// 厂商解码：hex 来源（sysfs manfid / CID）——走 DecodeManufacturer。
func TestDecodeManufacturerHexSource(t *testing.T) {
	tests := []struct {
		in      string
		wantSub string
	}{
		{"0x000015", "Samsung"},
		{"0x88", "YMTC"},
		{"0x90", "Hynix"},
		{"0x000090", "Hynix"},
		{"0xf4", "BIWIN"},
		{"0xd6", "Longsys"},
		{"0x04", "ISOCOM"},
		{"0xdf", "SCY"},
		{"0xea", "SiliconGo"},
		{"0x45", "SanDisk"},
		{"0x11", "Toshiba"},
	}
	for _, tc := range tests {
		got := DecodeManufacturer(tc.in)
		if !strings.Contains(got, tc.wantSub) {
			t.Errorf("DecodeManufacturer(%q) = %q, want contains %q", tc.in, got, tc.wantSub)
		}
	}
}

// 厂商解码：decimal 来源——显式走 DecodeManufacturerFromDecimal / MIDFromDecimal。
func TestDecodeManufacturerDecimalSource(t *testing.T) {
	tests := []struct {
		in      string
		wantSub string
	}{
		{"136", "YMTC"},   // 136 == 0x88
		{"144", "Hynix"},  // 144 == 0x90
		{"21", "Samsung"}, // 21  == 0x15
		{"244", "BIWIN"},  // 244 == 0xf4
	}
	for _, tc := range tests {
		got := DecodeManufacturerFromDecimal(tc.in)
		if !strings.Contains(got, tc.wantSub) {
			t.Errorf("DecodeManufacturerFromDecimal(%q) = %q, want contains %q", tc.in, got, tc.wantSub)
		}
		// 直接对底层函数再校验一次字节映射。
		b, ok := MIDFromDecimal(tc.in)
		if !ok {
			t.Fatalf("MIDFromDecimal(%q) 解析失败", tc.in)
		}
		if !strings.Contains(vendorName(b), tc.wantSub) {
			t.Errorf("vendorName(MIDFromDecimal(%q)) = %q, want contains %q", tc.in, vendorName(b), tc.wantSub)
		}
	}
}

// 厂商解码：CID 来源（真机 CID → MID 0x15 / Samsung）。
func TestDecodeManufacturerFromCID(t *testing.T) {
	vendor, mid := DecodeManufacturerFromCID(realCID)
	if mid != "0x15" {
		t.Errorf("CID MID = %q, want 0x15", mid)
	}
	if !strings.Contains(vendor, "Samsung") {
		t.Errorf("CID vendor = %q, want contains Samsung", vendor)
	}

	if v, m := DecodeManufacturerFromCID("zzzz"); v != "" || m != "" {
		t.Errorf("非法 CID 应返回空: got (%q,%q)", v, m)
	}
}

func TestDecodeCID(t *testing.T) {
	r := DecodeCID(realCID)
	if !r.Valid {
		t.Fatalf("DecodeCID(%q) Valid=false", realCID)
	}
	if r.MID != 0x15 {
		t.Errorf("MID = 0x%02x, want 0x15", r.MID)
	}
	if r.PNM != "RE1BMB" {
		t.Errorf("PNM = %q, want RE1BMB", r.PNM)
	}
	if r.PRV != "0.7" {
		t.Errorf("PRV = %q, want 0.7", r.PRV)
	}
	if r.PSN != 0x26fa9bae {
		t.Errorf("PSN = 0x%08x, want 0x26fa9bae", r.PSN)
	}

	if bad := DecodeCID("1"); bad.Valid {
		t.Error("长度不足的 CID 应 Valid=false")
	}
	if bad := DecodeCID(""); bad.Valid {
		t.Error("空 CID 应 Valid=false")
	}
	if bad := DecodeCID("zz" + realCID[2:]); bad.Valid {
		t.Error("含非 hex 字符的 CID 应 Valid=false")
	}
}

// CID 不解析 b[14] 的制造日期（MDT）：该字节的年份基准无规范依据，
// 制造日期一律以 sysfs 的 date 节点为准。此处锁定"结构里没有 MDT 字段"这一契约。
func TestCIDHasNoMDTField(t *testing.T) {
	r := DecodeCID(realCID)
	if !r.Valid {
		t.Fatalf("DecodeCID(%q) Valid=false", realCID)
	}
	rv := reflect.ValueOf(r)
	for i := 0; i < rv.NumField(); i++ {
		name := rv.Type().Field(i).Name
		if strings.Contains(strings.ToUpper(name), "MDT") {
			t.Errorf("CIDResult 不应再包含 MDT 相关字段，却找到 %s", name)
		}
	}
}

func TestDecodePreEOL(t *testing.T) {
	tests := []struct {
		in      string
		wantSub string
	}{
		{"0x00", "Not Defined"},
		{"0", "Not Defined"},
		{"00", "Not Defined"},
		{"0x01", "Normal (0x01)"},
		{"1", "Normal (0x01)"},
		{"01", "Normal (0x01)"},
		{"0x02", "Warning"},
		{"0x03", "Urgent"},
		{"0x09", "未知"},
	}
	for _, tc := range tests {
		got := DecodePreEOL(tc.in)
		if !strings.Contains(got, tc.wantSub) {
			t.Errorf("DecodePreEOL(%q) = %q, want contains %q", tc.in, got, tc.wantSub)
		}
	}
	// 空输入必须返回空串（精确比较；Contains(x, "") 恒真，不能用作断言）。
	if got := DecodePreEOL(""); got != "" {
		t.Errorf("DecodePreEOL(\"\") = %q, want 空串", got)
	}
}

func TestDecodeRev(t *testing.T) {
	tests := []struct {
		in      string
		wantSub string
	}{
		{"0x8", "eMMC 5.1 (0x8)"},
		{"8", "eMMC 5.1 (0x8)"},
		{"0x7", "eMMC 5.0 (0x7)"},
		{"0x6", "eMMC 4.5"},
		{"0x5", "eMMC 4.41"},
		{"0x9", "未知"},
	}
	for _, tc := range tests {
		got := DecodeRev(tc.in)
		if !strings.Contains(got, tc.wantSub) {
			t.Errorf("DecodeRev(%q) = %q, want contains %q", tc.in, got, tc.wantSub)
		}
	}
	// 空输入返回空串、不可读返回"不可读"：均精确比较。
	if got := DecodeRev(""); got != "" {
		t.Errorf("DecodeRev(\"\") = %q, want 空串", got)
	}
	if got := DecodeRev(model.Unreadable); got != "不可读" {
		t.Errorf("DecodeRev(%q) = %q, want 不可读", model.Unreadable, got)
	}
}

// life_time 是全工程的**关键回归点**：必须两段分别可断言（§6.1）。
func TestDecodeLifeTime(t *testing.T) {
	tests := []struct {
		in     string
		slcSub string
		mlcSub string
		ok     bool
	}{
		{"0x01 0x00", "0%–10%", "未定义", true},
		{"1 0", "0%–10%", "未定义", true},
		{realLifeTime, "0%–10%", "0%–10%", true}, // 真机两段
		{"0x0b 0x00", "已超出最大寿命", "未定义", true},
		{"0x0a 0x01", "90%–100%", "0%–10%", true},
		{"0x05", "40%–50%", "", true}, // 单值只填 SLC
		{"", "", "", false},           // 空输入：解析失败，两段均不得产出
	}
	for _, tc := range tests {
		slc, mlc, ok := DecodeLifeTime(tc.in)
		if ok != tc.ok {
			t.Errorf("DecodeLifeTime(%q) ok=%v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if !tc.ok {
			// 精确比较：解析失败时必须两段皆空（Contains(x, "") 恒真，不能用作断言）。
			if slc != "" || mlc != "" {
				t.Errorf("DecodeLifeTime(%q) 失败时不应产出段: SLC=%q MLC=%q", tc.in, slc, mlc)
			}
			continue
		}
		if !strings.Contains(slc, tc.slcSub) {
			t.Errorf("DecodeLifeTime(%q) SLC=%q, want contains %q", tc.in, slc, tc.slcSub)
		}
		// 单段输入时 MLC 必须为空：用精确比较而非 Contains。
		if tc.mlcSub == "" {
			if mlc != "" {
				t.Errorf("DecodeLifeTime(%q) MLC=%q, want 空（单值只填 SLC）", tc.in, mlc)
			}
		} else if !strings.Contains(mlc, tc.mlcSub) {
			t.Errorf("DecodeLifeTime(%q) MLC=%q, want contains %q", tc.in, mlc, tc.mlcSub)
		}
	}
}

// Interpret 端到端：真机样例应产出与项目 B 一致的结论。
func TestInterpretRealDevice(t *testing.T) {
	raw := model.RawInfo{
		Name:     "RE1BMB",
		CID:      realCID,
		Rev:      realRev,
		ManfID:   realManfID,
		OEMID:    "0x0100",
		HWRev:    "0x0",
		FWRev:    "0x0700000000000000",
		Serial:   "0x26fa9bae",
		Date:     "04/2017",
		LifeTime: realLifeTime,
		PreEOL:   realPreEOL,
	}
	res := Interpret(raw)

	if !strings.Contains(res.Vendor, "Samsung") {
		t.Errorf("Vendor = %q, want contains Samsung", res.Vendor)
	}
	if res.VendorID != "0x15" {
		t.Errorf("VendorID = %q, want 0x15", res.VendorID)
	}
	if !strings.Contains(res.EMMCVersion, "eMMC 5.1") {
		t.Errorf("EMMCVersion = %q, want contains eMMC 5.1", res.EMMCVersion)
	}
	if !strings.Contains(res.LifeTimeSLC, "0%–10%") || !strings.Contains(res.LifeTimeMLC, "0%–10%") {
		t.Errorf("LifeTime SLC=%q MLC=%q, want both 0%%-10%%", res.LifeTimeSLC, res.LifeTimeMLC)
	}
	if !strings.Contains(res.PreEOLStatus, "Normal") {
		t.Errorf("PreEOLStatus = %q, want contains Normal", res.PreEOLStatus)
	}
	if res.LifeTimeRaw != realLifeTime || res.PreEOLRaw != realPreEOL {
		t.Errorf("Raw 字段未透传: %q / %q", res.LifeTimeRaw, res.PreEOLRaw)
	}
	if !res.CID.Valid || res.CID.PNM != "RE1BMB" {
		t.Errorf("CID = %+v, want valid RE1BMB", res.CID)
	}
	// 制造日期不在 CID 里解，一律以 sysfs date 节点为准（raw.Date = "04/2017"）。
	if res.CID.MID != 0x15 || res.CID.PSN != 0x26fa9bae {
		t.Errorf("CID MID/PSN = 0x%02x/0x%08x, want 0x15/0x26fa9bae", res.CID.MID, res.CID.PSN)
	}
}

// manfid 不可读时必须回退 CID，且不中断其它字段。
func TestInterpretManfIDUnreadableFallback(t *testing.T) {
	raw := model.RawInfo{
		CID:      realCID,
		Rev:      realRev,
		ManfID:   model.Unreadable,
		LifeTime: realLifeTime,
		PreEOL:   realPreEOL,
	}
	res := Interpret(raw)
	if !strings.Contains(res.Vendor, "Samsung") {
		t.Errorf("ManfID 不可读时应回退 CID 得 Samsung，got %q", res.Vendor)
	}
	if res.VendorID != "0x15" {
		t.Errorf("VendorID = %q, want 0x15", res.VendorID)
	}
	if !strings.Contains(res.LifeTimeSLC, "0%–10%") {
		t.Errorf("其它字段不应被中断: LifeTimeSLC=%q", res.LifeTimeSLC)
	}
}

// LifetimeSupport 表驱动：sdk × rev 组合 + 未知输入。
// 只断言原因中的关键词（Android / eMMC），不断言完整文案。
func TestLifetimeSupport(t *testing.T) {
	tests := []struct {
		name        string
		sdk         int
		rev         string
		wantSupport bool
		wantSub     []string // 不支持时原因必须包含的关键词
		wantNotSub  []string // 不支持时原因必须不包含的关键词
	}{
		// sdk 25/26/30/-1 × rev 0x5/0x6/0x7/0x8。
		{"sdk25_rev5_双低", 25, "0x5", false, []string{"Android", "eMMC"}, nil},
		{"sdk25_rev6_双低", 25, "0x6", false, []string{"Android", "eMMC"}, nil},
		{"sdk25_rev7_仅Android", 25, "0x7", false, []string{"Android"}, []string{"eMMC"}},
		{"sdk25_rev8_仅Android", 25, "0x8", false, []string{"Android"}, []string{"eMMC"}},
		{"sdk26_rev5_仅eMMC", 26, "0x5", false, []string{"eMMC"}, []string{"Android"}},
		{"sdk26_rev6_仅eMMC", 26, "0x6", false, []string{"eMMC"}, []string{"Android"}},
		{"sdk26_rev7_支持", 26, "0x7", true, nil, nil},
		{"sdk26_rev8_支持", 26, "0x8", true, nil, nil},
		{"sdk30_rev5_仅eMMC", 30, "0x5", false, []string{"eMMC"}, []string{"Android"}},
		{"sdk30_rev8_支持", 30, "0x8", true, nil, nil},
		// sdk 未知（<0）：该项不参与判定，不因未知而判不支持。
		{"sdk未知_rev5_仅eMMC", -1, "0x5", false, []string{"eMMC"}, []string{"Android"}},
		{"sdk未知_rev7_支持", -1, "0x7", true, nil, nil},
		// rev 为空 / 不可读 / 乱串：该项不参与判定。
		{"rev空_sdk25_仅Android", 25, "", false, []string{"Android"}, []string{"eMMC"}},
		{"rev空_sdk26_支持", 26, "", true, nil, nil},
		{"rev空_sdk未知_支持", -1, "", true, nil, nil},
		{"rev不可读_sdk26_支持", 26, model.Unreadable, true, nil, nil},
		{"rev乱串_sdk25_仅Android", 25, "xyz", false, []string{"Android"}, []string{"eMMC"}},
		{"rev乱串_sdk26_支持", 26, "xyz", true, nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			support, reason := LifetimeSupport(tc.sdk, tc.rev)
			if support != tc.wantSupport {
				t.Fatalf("LifetimeSupport(sdk=%d, rev=%q) support=%v, want %v (reason=%q)",
					tc.sdk, tc.rev, support, tc.wantSupport, reason)
			}
			if !tc.wantSupport {
				if reason == "" {
					t.Fatalf("不支持时原因为空")
				}
				for _, sub := range tc.wantSub {
					if !strings.Contains(reason, sub) {
						t.Errorf("reason = %q, want contains %q", reason, sub)
					}
				}
				for _, sub := range tc.wantNotSub {
					if strings.Contains(reason, sub) {
						t.Errorf("reason = %q, want NOT contains %q", reason, sub)
					}
				}
			} else if reason != "" {
				t.Errorf("支持时原因应为空串, got %q", reason)
			}
		})
	}
}
