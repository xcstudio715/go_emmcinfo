package reader

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"emmc-tool/model"
)

// ---------------------------------------------------------------------------
// 测试基础设施
// ---------------------------------------------------------------------------

// fakeRunner 为 Runner 的假实现，按参数返回预设输出（不改动任何真实文件/设备）。
type fakeRunner struct {
	respond func(args []string) (string, error)
	calls   [][]string
}

func (f *fakeRunner) Run(args ...string) (string, error) {
	f.calls = append(f.calls, args)
	if f.respond == nil {
		return "", nil
	}
	return f.respond(args)
}

// readFixture 读取真机抓取的原始字节样本（**不加工、不手写字符串**）。
func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("读取 fixture %s 失败: %v", name, err)
	}
	return string(b)
}

// fixtureRunner 构造一个按"命令末位参数 → fixture 内容"响应的假 Runner。
// 未登记的路径视为失败（返回错误），用于验证容错分支。
func fixtureRunner(m map[string]string) *fakeRunner {
	return &fakeRunner{respond: func(args []string) (string, error) {
		if len(args) < 4 || args[1] != "shell" {
			return "", errors.New("unexpected args: " + strings.Join(args, " "))
		}
		target := args[len(args)-1]
		if v, ok := m[target]; ok {
			return v, nil
		}
		return "", errors.New("no fixture for: " + target)
	}}
}

// ---------------------------------------------------------------------------
// ParseListEntries：真机多列输出 + 异常输入
// ---------------------------------------------------------------------------

// 真机 `ls` 为列优先多列输出，必须逐 token 提取，而非整行过滤。
func TestParseListEntriesRealDevice(t *testing.T) {
	host := ParseListEntries(readFixture(t, "ls_mmc_host.raw"))
	want := []string{"mmc0", "mmc1"}
	if !equalStrings(host, want) {
		t.Errorf("ls_mmc_host.raw → %v, want %v", host, want)
	}

	// 同一行含 device / mmc0:0001 / power / subsystem / uevent —— 列优先多列。
	entries := ParseListEntries(readFixture(t, "ls_mmc0.raw"))
	for _, w := range []string{"device", "mmc0:0001", "power", "subsystem", "uevent"} {
		if !contains(entries, w) {
			t.Errorf("ls_mmc0.raw 未提取到 %q，实际 %v", w, entries)
		}
	}

	// 设备目录条目较多，确认关键字段节点均在。
	fields := ParseListEntries(readFixture(t, "ls_mmc0_0001.raw"))
	for _, w := range []string{"cid", "manfid", "life_time", "pre_eol_info", "rev", "name", "date"} {
		if !contains(fields, w) {
			t.Errorf("ls_mmc0_0001.raw 未提取到 %q", w)
		}
	}
}

// 异常 ls 输出：空、纯空白、尾斜杠、退化分隔。
func TestParseListEntriesAbnormal(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"空", "", nil},
		{"纯空白与空行", "   \n\t\n  \n", nil},
		{"尾斜杠", "mmc0/\nmmc1/\n", []string{"mmc0", "mmc1"}},
		{"多列尾斜杠", "mmcblk0/  mmcblk0boot0/\nmmcblk1/\n", []string{"mmcblk0", "mmcblk0boot0", "mmcblk1"}},
		{"仅斜杠 token", "/\nmmc0\n", []string{"mmc0"}},
		{"制表符分隔", "a\tb\tc", []string{"a", "b", "c"}},
	}
	for _, tc := range tests {
		got := ParseListEntries(tc.in)
		if !equalStrings(got, tc.want) {
			t.Errorf("%s: ParseListEntries(%q) = %v, want %v", tc.name, tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// DiscoverMMC（一次 adb shell 完成发现）/ pickMMCDevice 纯解析
// ---------------------------------------------------------------------------

// 真机原文样本 batch_discover.raw：一条 adb shell 的分段输出
// （###HOSTS / ###DEVS <host> / ###TYPE <host>/<dev>；mmc1 为 SD，应跳过）。
func TestDiscoverMMCRealDevice(t *testing.T) {
	raw := readFixture(t, "batch_discover.raw")

	c := NewAdbClient("adb", "", &fakeRunner{respond: func(args []string) (string, error) {
		return raw, nil
	}})
	dev, err := DiscoverMMC(c)
	if err != nil {
		t.Fatalf("DiscoverMMC 出错: %v", err)
	}
	if dev.Host != "mmc0" {
		t.Errorf("Host = %q, want mmc0", dev.Host)
	}
	if dev.Dev != "mmc0:0001" {
		t.Errorf("Dev = %q, want mmc0:0001", dev.Dev)
	}
	if want := "/sys/class/mmc_host/mmc0/mmc0:0001"; dev.DeviceDir != want {
		t.Errorf("DeviceDir = %q, want %q", dev.DeviceDir, want)
	}
}

// type=="SD" 的卡必须跳过，继续找真正实现 MMC 的 host；type 分段缺失时回退旧逻辑。
func TestPickMMCDeviceTypes(t *testing.T) {
	t.Run("跳过 SD 选 MMC", func(t *testing.T) {
		in := "###HOSTS\nmmc1  mmc0\n" + // 列优先，顺序不可信
			"###DEVS mmc0\ndevice  mmc0:0001  power  subsystem  uevent\n" +
			"###TYPE mmc0/mmc0:0001\nSD\n" +
			"###DEVS mmc1\ndevice  mmc1:0001  power  subsystem  uevent\n" +
			"###TYPE mmc1/mmc1:0001\nMMC\n"
		dev, err := pickMMCDevice(in)
		if err != nil {
			t.Fatalf("pickMMCDevice 出错: %v", err)
		}
		if dev.Host != "mmc1" || dev.Dev != "mmc1:0001" {
			t.Errorf("应跳过 mmc0 的 SD 选 mmc1:0001，got %+v", dev)
		}
	})

	t.Run("type 分段缺失回退旧逻辑", func(t *testing.T) {
		in := "###HOSTS\nmmc0\n###DEVS mmc0\ndevice  mmc0:0001  power  subsystem  uevent\n"
		dev, err := pickMMCDevice(in)
		if err != nil {
			t.Fatalf("pickMMCDevice 出错: %v", err)
		}
		if dev.Dev != "mmc0:0001" {
			t.Errorf("type 缺失应回退取首个设备目录，got %+v", dev)
		}
	})

	t.Run("全部为 SD 返回 ErrMMCNotFound", func(t *testing.T) {
		in := "###HOSTS\nmmc0\n###DEVS mmc0\ndevice  mmc0:0001  power  subsystem  uevent\n" +
			"###TYPE mmc0/mmc0:0001\nSD\n"
		if _, err := pickMMCDevice(in); !errors.Is(err, ErrMMCNotFound) {
			t.Fatalf("want ErrMMCNotFound, got %v", err)
		}
	})

	t.Run("无 mmcN 条目", func(t *testing.T) {
		in := "###HOSTS\ndevice  driver  subsystem  uevent\n"
		if _, err := pickMMCDevice(in); !errors.Is(err, ErrMMCNotFound) {
			t.Fatalf("want ErrMMCNotFound, got %v", err)
		}
	})

	t.Run("有 host 但无设备目录", func(t *testing.T) {
		in := "###HOSTS\nmmc0\n###DEVS mmc0\ndevice  power  subsystem  uevent\n"
		if _, err := pickMMCDevice(in); !errors.Is(err, ErrMMCNotFound) {
			t.Fatalf("want ErrMMCNotFound, got %v", err)
		}
	})
}

// DiscoverMMC 在执行失败时返回哨兵错误，**不 panic**。
func TestDiscoverMMCErrors(t *testing.T) {
	c := NewAdbClient("adb", "", &fakeRunner{respond: func(args []string) (string, error) {
		return "adb: device offline", errors.New("exit status 1")
	}})
	if _, err := DiscoverMMC(c); !errors.Is(err, ErrMMCNotFound) {
		t.Fatalf("want ErrMMCNotFound, got %v", err)
	}
}

// DiscoverMMC 两级降级：一级 echo 脚本分段标签不全 → 必须回退二级 printf 脚本且结果正确。
func TestDiscoverMMCTwoTierFallback(t *testing.T) {
	raw := readFixture(t, "batch_discover.raw")
	var order []string
	c := NewAdbClient("adb", "", &fakeRunner{respond: func(args []string) (string, error) {
		script := args[len(args)-1]
		order = append(order, script)
		switch {
		case strings.HasPrefix(script, "echo '###HOSTS'"):
			// 一级（echo 版）返回被截断的输出（只有 ###HOSTS 分段，缺 DEVS/TYPE）。
			return "###HOSTS\nmmc0  mmc1\n", nil
		case strings.HasPrefix(script, "printf '###HOSTS"):
			return raw, nil
		}
		return "", errors.New("未预期调用: " + firstN(script, 40))
	}})
	dev, err := DiscoverMMC(c)
	if err != nil {
		t.Fatalf("一级残缺应回退二级并成功，实际 err=%v", err)
	}
	if dev.Host != "mmc0" || dev.Dev != "mmc0:0001" {
		t.Errorf("回退后结果不正确: %+v", dev)
	}
	if len(order) != 2 {
		t.Fatalf("应恰好两级各调用 1 次，实际 %d 次", len(order))
	}
	if !strings.HasPrefix(order[0], "echo '###HOSTS'") {
		t.Errorf("首次调用应走一级 echo 脚本, got %q", firstN(order[0], 40))
	}
}

// 一级 echo 脚本输出完整时，发现只 spawn 一次（不得再调二级）。
func TestDiscoverMMCFastPathSingleSpawn(t *testing.T) {
	raw := readFixture(t, "batch_discover.raw")
	count := 0
	c := NewAdbClient("adb", "", &fakeRunner{respond: func(args []string) (string, error) {
		count++
		return raw, nil
	}})
	if _, err := DiscoverMMC(c); err != nil {
		t.Fatalf("DiscoverMMC 出错: %v", err)
	}
	if count != 1 {
		t.Errorf("一级完整时应只调用 1 次脚本，实际 %d 次", count)
	}
}

// discoverComplete：真机原文应判完整；各类截断/缺标签输出必须判不完整。
func TestDiscoverComplete(t *testing.T) {
	if !discoverComplete(readFixture(t, "batch_discover.raw")) {
		t.Error("真机发现原文应判定完整")
	}
	bad := map[string]string{
		"空输出":     "",
		"仅HOSTS":  "###HOSTS\nmmc0  mmc1\n",
		"HOSTS为空": "###HOSTS\n",
		"缺DEVS":   "###HOSTS\nmmc0\n###TYPE mmc0/mmc0:0001\nMMC\n",
		"缺TYPE":   "###HOSTS\nmmc0\n###DEVS mmc0\ndevice  mmc0:0001  power  subsystem  uevent\n",
	}
	for name, out := range bad {
		if discoverComplete(out) {
			t.Errorf("%s 应判定不完整", name)
		}
	}
}

// ---------------------------------------------------------------------------
// DiscoverBlock：从 <DeviceDir>/block/ 取块设备（与 host/设备绑定，本轮修订 #4）
// ---------------------------------------------------------------------------

func TestDiscoverBlockRealDevice(t *testing.T) {
	// 设备已断开，无法再次抓取该目录；<DeviceDir>/block/ 由内核按卡的归属填充，
	// 真机 mmcblk0 为内置 eMMC（见 /sys/block 的 ls_block.raw 与 /proc/partitions 真机样本）。
	dev := MMCDevice{
		Host:      "mmc0",
		Dev:       "mmc0:0001",
		HostDir:   mmcHostRoot + "mmc0",
		DeviceDir: mmcHostRoot + "mmc0/mmc0:0001",
	}
	c := NewAdbClient("adb", "", fixtureRunner(map[string]string{
		dev.DeviceDir + "/block/": "mmcblk0\n",
	}))
	got, err := DiscoverBlock(c, dev)
	if err != nil {
		t.Fatalf("DiscoverBlock 出错: %v", err)
	}
	if got != "mmcblk0" {
		t.Errorf("DiscoverBlock = %q, want mmcblk0", got)
	}
}

// 绑定校验：必须查询 <DeviceDir>/block/，不得回退到 /sys/block/。
func TestDiscoverBlockUsesDeviceDir(t *testing.T) {
	dev := MMCDevice{DeviceDir: "/sys/class/mmc_host/mmc1/mmc1:0001"}
	c := NewAdbClient("adb", "", &fakeRunner{respond: func(args []string) (string, error) {
		if want := dev.DeviceDir + "/block/"; args[len(args)-1] != want {
			t.Errorf("查询路径 = %q, want %q", args[len(args)-1], want)
		}
		return "mmcblk2\n", nil
	}})
	got, err := DiscoverBlock(c, dev)
	if err != nil || got != "mmcblk2" {
		t.Errorf("got (%q,%v), want (mmcblk2,nil)", got, err)
	}
}

// 多条目时仍取数字后缀最小者（ls 顺序不可信）。
func TestDiscoverBlockPicksSmallestSuffix(t *testing.T) {
	dev := MMCDevice{DeviceDir: "/sys/class/mmc_host/mmc0/mmc0:0001"}
	c := NewAdbClient("adb", "", fixtureRunner(map[string]string{
		dev.DeviceDir + "/block/": "mmcblk1  mmcblk0\n",
	}))
	got, err := DiscoverBlock(c, dev)
	if err != nil || got != "mmcblk0" {
		t.Errorf("got (%q,%v), want (mmcblk0,nil)", got, err)
	}
}

func TestDiscoverBlockErrors(t *testing.T) {
	dev := MMCDevice{DeviceDir: "/sys/class/mmc_host/mmc0/mmc0:0001"}

	t.Run("无 mmcblkN 条目", func(t *testing.T) {
		c := NewAdbClient("adb", "", fixtureRunner(map[string]string{
			dev.DeviceDir + "/block/": "loop0  ram0  zram0\n",
		}))
		if _, err := DiscoverBlock(c, dev); !errors.Is(err, ErrBlockNotFound) {
			t.Fatalf("want ErrBlockNotFound, got %v", err)
		}
	})

	t.Run("ls 失败", func(t *testing.T) {
		c := NewAdbClient("adb", "", &fakeRunner{respond: func(args []string) (string, error) {
			return "ls: " + dev.DeviceDir + "/block/: No such file or directory", errors.New("exit status 1")
		}})
		if _, err := DiscoverBlock(c, dev); !errors.Is(err, ErrBlockNotFound) {
			t.Fatalf("want ErrBlockNotFound, got %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// ReadAll：逐字段真机原文；不可读不中断
// ---------------------------------------------------------------------------

// fieldMap 把 testdata/field_<key>.raw 映射为"完整节点路径 → 内容"。
func fieldMap(t *testing.T, deviceDir string) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, key := range androidFields {
		m[deviceDir+"/"+key] = readFixture(t, "field_"+key+".raw")
	}
	return m
}

func TestReadAllRealDevice(t *testing.T) {
	const deviceDir = "/sys/class/mmc_host/mmc0/mmc0:0001"
	c := NewAdbClient("adb", "", fixtureRunner(fieldMap(t, deviceDir)))

	raw, notes := ReadAll(c, deviceDir)
	if len(notes) != 0 {
		t.Errorf("真机样本不应产生 FieldNote，实际: %+v", notes)
	}

	checks := map[string]string{
		"Name":     "RE1BMB",
		"CID":      "150100524531424d420726fa9bae442b",
		"Rev":      "0x8",
		"ManfID":   "0x000015",
		"OEMID":    "0x0100",
		"HWRev":    "0x0",
		"FWRev":    "0x0700000000000000",
		"Serial":   "0x26fa9bae",
		"Date":     "04/2017",
		"LifeTime": "0x01 0x01",
		"PreEOL":   "0x01",
	}
	got := map[string]string{
		"Name": raw.Name, "CID": raw.CID, "Rev": raw.Rev, "ManfID": raw.ManfID,
		"OEMID": raw.OEMID, "HWRev": raw.HWRev, "FWRev": raw.FWRev, "Serial": raw.Serial,
		"Date": raw.Date, "LifeTime": raw.LifeTime, "PreEOL": raw.PreEOL,
	}
	for k, want := range checks {
		if got[k] != want {
			t.Errorf("ReadAll %s = %q, want %q", k, got[k], want)
		}
	}
}

// life_time 节点返回 Permission denied：该字段置"不可读"并记录 note，其它字段不受影响。
func TestReadAllUnreadableDoesNotAbort(t *testing.T) {
	const deviceDir = "/sys/class/mmc_host/mmc0/mmc0:0001"
	m := fieldMap(t, deviceDir)
	lifeNode := deviceDir + "/life_time"

	c := NewAdbClient("adb", "", &fakeRunner{respond: func(args []string) (string, error) {
		target := args[len(args)-1]
		if target == lifeNode {
			return "cat: " + lifeNode + ": Permission denied\n", errors.New("exit status 1")
		}
		if v, ok := m[target]; ok {
			return v, nil
		}
		return "", errors.New("no fixture for: " + target)
	}})

	raw, notes := ReadAll(c, deviceDir)

	if raw.LifeTime != model.Unreadable {
		t.Errorf("LifeTime = %q, want %q", raw.LifeTime, model.Unreadable)
	}
	if len(notes) != 1 || notes[0].Key != "life_time" {
		t.Fatalf("应有且仅有 1 条 life_time note，实际 %+v", notes)
	}
	if !strings.Contains(notes[0].Reason, "Permission denied") {
		t.Errorf("note.Reason = %q, want 含 Permission denied", notes[0].Reason)
	}
	// 其它字段必须完好。
	if raw.Name != "RE1BMB" || raw.CID == "" || raw.PreEOL != "0x01" {
		t.Errorf("不可读不应中断其它字段: Name=%q CID=%q PreEOL=%q", raw.Name, raw.CID, raw.PreEOL)
	}
}

// ---------------------------------------------------------------------------
// ReadAllBatched：一条 adb shell 批读全部字段 + block；白名单拒绝；降级语义
// ---------------------------------------------------------------------------

func TestReadAllBatchedRealDevice(t *testing.T) {
	const deviceDir = "/sys/class/mmc_host/mmc0/mmc0:0001"
	c := NewAdbClient("adb", "", &fakeRunner{respond: func(args []string) (string, error) {
		return readFixture(t, "batch_fields.raw"), nil
	}})

	res, err := ReadAllBatched(c, deviceDir)
	if err != nil {
		t.Fatalf("ReadAllBatched 出错: %v", err)
	}
	if len(res.Notes) != 0 {
		t.Errorf("真机样本不应产生 FieldNote，实际: %+v", res.Notes)
	}
	if res.BlockErr != nil {
		t.Errorf("BlockErr = %v, want nil", res.BlockErr)
	}
	if res.BlockName != "mmcblk0" {
		t.Errorf("BlockName = %q, want mmcblk0", res.BlockName)
	}
	if res.PartitionNote != nil {
		t.Errorf("真机样本分区表应可读，实际 note: %+v", res.PartitionNote)
	}
	if !strings.Contains(res.PartitionOut, "mmcblk0") || !strings.Contains(res.PartitionOut, "#blocks") {
		t.Errorf("PartitionOut 应为 /proc/partitions 原文，实际前 80 字: %q", firstN(res.PartitionOut, 80))
	}
	checks := map[string]string{
		"Name": "RE1BMB", "CID": "150100524531424d420726fa9bae442b", "Rev": "0x8",
		"ManfID": "0x000015", "OEMID": "0x0100", "HWRev": "0x0",
		"FWRev": "0x0700000000000000", "Serial": "0x26fa9bae", "Date": "04/2017",
		"LifeTime": "0x01 0x01", "PreEOL": "0x01",
	}
	got := map[string]string{
		"Name": res.Raw.Name, "CID": res.Raw.CID, "Rev": res.Raw.Rev, "ManfID": res.Raw.ManfID,
		"OEMID": res.Raw.OEMID, "HWRev": res.Raw.HWRev, "FWRev": res.Raw.FWRev, "Serial": res.Raw.Serial,
		"Date": res.Raw.Date, "LifeTime": res.Raw.LifeTime, "PreEOL": res.Raw.PreEOL,
	}
	for k, want := range checks {
		if got[k] != want {
			t.Errorf("ReadAllBatched %s = %q, want %q", k, got[k], want)
		}
	}
}

// 某字段段内容为 cat 的报错文本（含 Permission denied）：该字段置"不可读"并记录 note，
// 其它字段不受影响，不中断。（新格式无 `##ERR##` 标记，靠"内容命中不可读关键字"判定。）
func TestReadAllBatchedUnreadableDoesNotAbort(t *testing.T) {
	const deviceDir = "/sys/class/mmc_host/mmc0/mmc0:0001"

	var sb strings.Builder
	for _, k := range androidFields {
		sb.WriteString("###" + k + "\n")
		if k == "life_time" {
			// 与真实脚本一致：cat 的 stderr 经 2>&1 直接进入段内容，随后一个空行。
			sb.WriteString("cat: " + deviceDir + "/life_time: Permission denied\n")
		} else {
			sb.WriteString("V-" + k + "\n")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("###@block\nmmcblk0\n")
	sb.WriteString("###@partitions\nmajor minor  #blocks  name\n\n 179        0   15267840 mmcblk0\n")

	c := NewAdbClient("adb", "", &fakeRunner{respond: func(args []string) (string, error) {
		return sb.String(), nil
	}})
	res, err := ReadAllBatched(c, deviceDir)
	if err != nil {
		t.Fatalf("ReadAllBatched 出错: %v", err)
	}

	if res.Raw.LifeTime != model.Unreadable {
		t.Errorf("LifeTime = %q, want %q", res.Raw.LifeTime, model.Unreadable)
	}
	if len(res.Notes) != 1 || res.Notes[0].Key != "life_time" {
		t.Fatalf("应有且仅有 1 条 life_time note，实际 %+v", res.Notes)
	}
	if res.Notes[0].Reason != "Permission denied" {
		t.Errorf("note.Reason = %q, want Permission denied", res.Notes[0].Reason)
	}
	if res.Raw.Name != "V-name" || res.Raw.PreEOL != "V-pre_eol_info" {
		t.Errorf("不可读不应中断其它字段: Name=%q PreEOL=%q", res.Raw.Name, res.Raw.PreEOL)
	}
	if res.PartitionNote != nil || res.BlockName != "mmcblk0" {
		t.Errorf("其它分段不应受影响: PartitionNote=%+v BlockName=%q", res.PartitionNote, res.BlockName)
	}
}

// 空段（文件存在但读出为空 / 无输出）→ 不可读，原因 "空输出"。
func TestReadAllBatchedEmptySectionIsUnreadable(t *testing.T) {
	const deviceDir = "/sys/class/mmc_host/mmc0/mmc0:0001"

	var sb strings.Builder
	for _, k := range androidFields {
		sb.WriteString("###" + k + "\n")
		if k == "serial" {
			sb.WriteString("\n") // 空值
			continue
		}
		sb.WriteString("V-" + k + "\n\n")
	}
	sb.WriteString("###@block\nmmcblk0\n")
	sb.WriteString("###@partitions\nmajor minor  #blocks  name\n")

	c := NewAdbClient("adb", "", &fakeRunner{respond: func(args []string) (string, error) {
		return sb.String(), nil
	}})
	res, err := ReadAllBatched(c, deviceDir)
	if err != nil {
		t.Fatalf("ReadAllBatched 出错: %v", err)
	}
	if res.Raw.Serial != model.Unreadable {
		t.Errorf("Serial = %q, want %q", res.Raw.Serial, model.Unreadable)
	}
	if len(res.Notes) != 1 || res.Notes[0].Reason != "空输出" {
		t.Fatalf("应有且仅有 1 条 reason=空输出 的 note，实际 %+v", res.Notes)
	}
}

// 安全约束：deviceDir 含白名单外字符、或含路径穿越 ".." 时，
// 都必须返回错误且**不执行**任何命令。
func TestReadAllBatchedRejectsUnsafeDeviceDir(t *testing.T) {
	for _, bad := range []string{
		"/sys/class/mmc_host/mmc0/mmc0:0001; rm -rf /",
		"/sys/class/mmc_host/mmc0/mmc0:0001`id`",
		"/sys/class/mmc_host/mmc0/mmc0:0001$(id)",
		"/sys/class/mmc_host/mmc0/mmc0:0001\nls",
		"",
		// 字符集合法，但 ".." 会越界读白名单外目录：
		"/sys/class/mmc_host/mmc0/mmc0:0001/../../../../etc",
		"/sys/class/mmc_host/mmc0/..",
		"../etc",
	} {
		r := &fakeRunner{respond: func(args []string) (string, error) {
			return "SHOULD NOT RUN", nil
		}}
		c := NewAdbClient("adb", "", r)
		if _, err := ReadAllBatched(c, bad); err == nil {
			t.Errorf("deviceDir=%q 应被拒绝", bad)
		}
		if len(r.calls) != 0 {
			t.Errorf("deviceDir=%q 被拒绝后不得执行命令，实际调用 %d 次", bad, len(r.calls))
		}
	}
}

func TestParseBlockName(t *testing.T) {
	if name, err := ParseBlockName(readFixture(t, "ls_block.raw")); err != nil || name == "" {
		t.Errorf("真机 ls_block.raw → (%q,%v), want 非空块设备名", name, err)
	}
	if name, err := ParseBlockName("mmcblk1  mmcblk0\n"); err != nil || name != "mmcblk0" {
		t.Errorf("多条目应取最小数字后缀: (%q,%v), want (mmcblk0,nil)", name, err)
	}
	if _, err := ParseBlockName("loop0  ram0  zram0\n"); !errors.Is(err, ErrBlockNotFound) {
		t.Errorf("无 mmcblkN 应返回 ErrBlockNotFound, got %v", err)
	}
}

// 一次 Android 读取的 adb 进程启动次数必须 ≤3（devices / 发现 / 字段+block+分区表）。
//
// 三级降级链中，**正常路径只走一级快速脚本（c_echo）**；本测试同时断言一级被真正调用。
func TestAndroidReadSpawnCountAtMostThree(t *testing.T) {
	const deviceDir = "/sys/class/mmc_host/mmc0/mmc0:0001"
	fastScript := batchReadScriptFast(deviceDir)
	slowScript := batchReadScript(deviceDir)

	calledFast := false
	calledDiscFast := false
	r := &fakeRunner{respond: func(args []string) (string, error) {
		last := args[len(args)-1]
		switch {
		case last == "devices":
			return "List of devices attached\n0123456789ABCDEF\tdevice\n", nil
		case last == discoverScriptFast:
			calledDiscFast = true
			return readFixture(t, "batch_discover.raw"), nil
		case last == discoverScript:
			return readFixture(t, "batch_discover.raw"), nil
		case last == fastScript:
			calledFast = true
			return readFixture(t, "batch_fields.raw"), nil
		case last == slowScript:
			return readFixture(t, "batch_fields.raw"), nil
		}
		return "", errors.New("未预期调用: " + strings.Join(args, " "))
	}}
	c := NewAdbClient("adb", "", r)

	devs, derr := c.Devices()
	if derr != nil {
		t.Fatalf("Devices 出错: %v", derr)
	}
	d, err := SelectDevice(devs, "")
	if err != nil {
		t.Fatalf("SelectDevice 出错: %v", err)
	}
	c.Serial = d.Serial

	mmc, err := DiscoverMMC(c)
	if err != nil {
		t.Fatalf("DiscoverMMC 出错: %v", err)
	}
	if mmc.DeviceDir != deviceDir {
		t.Fatalf("DeviceDir = %q, want %q", mmc.DeviceDir, deviceDir)
	}
	res, err := ReadAllBatched(c, mmc.DeviceDir)
	if err != nil {
		t.Fatalf("ReadAllBatched 出错: %v", err)
	}
	if res.BlockName != "mmcblk0" {
		t.Fatalf("BlockName = %q, want mmcblk0", res.BlockName)
	}
	if res.PartitionOut == "" {
		t.Fatalf("分区表应随批读一次取回（###@partitions 段）")
	}
	// 再次调用 Devices：命中缓存，不应新增进程。
	if _, err := c.Devices(); err != nil {
		t.Fatalf("Devices(缓存) 出错: %v", err)
	}

	if !calledFast {
		t.Errorf("批读正常路径应走一级快速脚本（c_echo），实际调用序列: %v", r.calls)
	}
	if !calledDiscFast {
		t.Errorf("发现应走一级 echo 脚本（discoverScriptFast），实际调用序列: %v", r.calls)
	}
	if len(r.calls) > 3 {
		t.Errorf("adb 进程启动次数 = %d (>3)，调用序列: %v", len(r.calls), r.calls)
	}
}

// 一级快速脚本失败（分段不完整）→ 自动退到二级 a_echo；两级都失败 → 返回错误（供调用方退到逐字段 ReadAll）。
func TestReadAllBatchedTwoTierFallback(t *testing.T) {
	const deviceDir = "/sys/class/mmc_host/mmc0/mmc0:0001"
	fastScript := batchReadScriptFast(deviceDir)
	slowScript := batchReadScript(deviceDir)

	// 一级返回"缺字段分段"的响应（模拟 [ -r ]/read 语义异常的机型）。
	t.Run("fast_malformed_then_slow_ok", func(t *testing.T) {
		r := &fakeRunner{respond: func(args []string) (string, error) {
			switch args[len(args)-1] {
			case fastScript:
				return "###@block\nmmcblk0\n###@partitions\nmajor minor #blocks name\n", nil
			case slowScript:
				return readFixture(t, "batch_fields.raw"), nil
			}
			return "", errors.New("未预期调用")
		}}
		c := NewAdbClient("adb", "", r)
		res, err := ReadAllBatched(c, deviceDir)
		if err != nil {
			t.Fatalf("一级失败应回退二级并成功，实际 err=%v", err)
		}
		if res.Raw.Name != "RE1BMB" || res.BlockName != "mmcblk0" {
			t.Errorf("回退后结果不正确: Name=%q BlockName=%q", res.Raw.Name, res.BlockName)
		}
		if len(r.calls) != 2 {
			t.Errorf("一级+二级应恰好 2 次调用，实际 %d: %v", len(r.calls), r.calls)
		}
	})

	// 两级都返回分段不完整 → 必须返回错误（不得静默给出残缺结果）。
	t.Run("both_malformed_error", func(t *testing.T) {
		r := &fakeRunner{respond: func(args []string) (string, error) {
			return "###@block\nmmcblk0\n", nil
		}}
		c := NewAdbClient("adb", "", r)
		if _, err := ReadAllBatched(c, deviceDir); err == nil {
			t.Error("两级脚本分段均不完整时应返回错误")
		}
	})
}

// 多字段不可读时，FieldNote 的 Key / Node / Reason 必须逐一精确匹配，且不产生多余 note。
func TestReadAllBatchedNotesExactKeyReason(t *testing.T) {
	const deviceDir = "/sys/class/mmc_host/mmc0/mmc0:0001"

	var sb strings.Builder
	for _, k := range androidFields {
		sb.WriteString("###" + k + "\n")
		switch k {
		case "manfid":
			sb.WriteString("cat: " + deviceDir + "/manfid: Permission denied\n")
		case "oemid":
			sb.WriteString("cat: " + deviceDir + "/oemid: No such file or directory\n")
		default:
			sb.WriteString("V-" + k + "\n")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("###@block\nmmcblk0\n")
	sb.WriteString("###@partitions\nmajor minor  #blocks  name\n\n 179        0   15267840 mmcblk0\n")

	c := NewAdbClient("adb", "", &fakeRunner{respond: func(args []string) (string, error) {
		return sb.String(), nil
	}})
	res, err := ReadAllBatched(c, deviceDir)
	if err != nil {
		t.Fatalf("ReadAllBatched 出错: %v", err)
	}

	if len(res.Notes) != 2 {
		t.Fatalf("应有且仅有 2 条 note，实际 %+v", res.Notes)
	}
	want := []FieldNote{
		{Key: "manfid", Node: deviceDir + "/manfid", Reason: "Permission denied"},
		{Key: "oemid", Node: deviceDir + "/oemid", Reason: "No such file or directory"},
	}
	for i, w := range want {
		if res.Notes[i] != w {
			t.Errorf("notes[%d] = %+v, want %+v", i, res.Notes[i], w)
		}
	}
	if res.Raw.ManfID != model.Unreadable || res.Raw.OEMID != model.Unreadable {
		t.Errorf("ManfID/OEMID 应置不可读: %q / %q", res.Raw.ManfID, res.Raw.OEMID)
	}
	if res.Raw.Name != "V-name" || res.Raw.PreEOL != "V-pre_eol_info" {
		t.Errorf("不可读不应影响其它字段: Name=%q PreEOL=%q", res.Raw.Name, res.Raw.PreEOL)
	}
}

// ---------------------------------------------------------------------------
// adb devices 解析 / 设备选择 / 不可读判定
// ---------------------------------------------------------------------------

func TestParseDevices(t *testing.T) {
	out := "List of devices attached\n" +
		"0123456789ABCDEF\tdevice\n" +
		"* daemon not running; starting now at tcp:5037\n" +
		"emulator-5554\tunauthorized\n" +
		"\n"
	devs := ParseDevices(out)
	if len(devs) != 2 {
		t.Fatalf("ParseDevices 得到 %d 条，want 2 (%+v)", len(devs), devs)
	}
	if devs[0].Serial != "0123456789ABCDEF" || devs[0].State != "device" {
		t.Errorf("devs[0] = %+v, want 0123456789ABCDEF/device", devs[0])
	}
	if devs[1].Serial != "emulator-5554" || devs[1].State != "unauthorized" {
		t.Errorf("devs[1] = %+v, want emulator-5554/unauthorized", devs[1])
	}
}

func TestSelectDevice(t *testing.T) {
	devs := []Device{
		{Serial: "A", State: "unauthorized"},
		{Serial: "B", State: "device"},
	}
	if d, err := SelectDevice(devs, ""); err != nil || d.Serial != "B" {
		t.Errorf("无指定序列号应选首个 device: got %+v, %v", d, err)
	}
	if d, err := SelectDevice(devs, "A"); err != nil || d.Serial != "A" {
		t.Errorf("指定 A 应精确匹配: got %+v, %v", d, err)
	}
	if _, err := SelectDevice(devs, "Z"); err == nil {
		t.Error("指定不存在的序列号应返回错误")
	}
	if _, err := SelectDevice([]Device{{Serial: "A", State: "offline"}}, ""); !errors.Is(err, ErrNoDevice) {
		t.Errorf("无在线设备应返回 ErrNoDevice，got %v", err)
	}
}

func TestUnreadableReason(t *testing.T) {
	if _, ok := UnreadableReason("", nil); ok {
		t.Error("err==nil 不应判为不可读")
	}
	reason, ok := UnreadableReason("cat: /x: Permission denied\n", errors.New("exit status 1"))
	if !ok || reason != "Permission denied" {
		t.Errorf("got (%q,%v), want (Permission denied,true)", reason, ok)
	}
	reason, ok = UnreadableReason("No such file or directory", errors.New("exit status 1"))
	if !ok || reason != "No such file or directory" {
		t.Errorf("got (%q,%v), want (No such file or directory,true)", reason, ok)
	}
	// 未知错误：仍判为不可读，reason 回退为错误文本。
	reason, ok = UnreadableReason("", errors.New("some weird failure"))
	if !ok || reason != "some weird failure" {
		t.Errorf("got (%q,%v), want (some weird failure,true)", reason, ok)
	}
}

// ---------------------------------------------------------------------------
// 分区解析与容量（真机 /proc/partitions）
// ---------------------------------------------------------------------------

func TestParsePartitionsRealDevice(t *testing.T) {
	parts := ParsePartitions(readFixture(t, "field_partitions.raw"))

	byName := map[string]Partition{}
	for _, p := range parts {
		byName[p.Name] = p
	}
	// 表头 "major minor #blocks name" 必须被跳过。
	if _, ok := byName["#blocks"]; ok {
		t.Error("表头行不应被解析为分区")
	}
	mmc0, ok := byName["mmcblk0"]
	if !ok {
		t.Fatal("未解析到 mmcblk0")
	}
	if mmc0.Major != 179 || mmc0.Minor != 0 || mmc0.Blocks != 15267840 {
		t.Errorf("mmcblk0 = %+v, want {179 0 15267840}", mmc0)
	}
	if p, ok := byName["mmcblk0p45"]; !ok || p.Blocks != 9513952 {
		t.Errorf("mmcblk0p45 = %+v (ok=%v), want blocks 9513952", p, ok)
	}
	// 非 mmcblk 设备也应被解析（ram/dm/zram）。
	if _, ok := byName["ram0"]; !ok {
		t.Error("应解析到 ram0")
	}
}

func TestCapacityGiB(t *testing.T) {
	// 真机 mmcblk0 = 15267840 块（512B）≈ 7.28 GiB。
	got := CapacityGiB(15267840)
	if got < 7.27 || got > 7.29 {
		t.Errorf("CapacityGiB(15267840) = %.4f, want ≈7.28", got)
	}
	if s := FormatGiB(got); !strings.HasPrefix(s, "7.2") {
		t.Errorf("FormatGiB = %q, want 7.2x GiB", s)
	}
}

// ---------------------------------------------------------------------------
// 本机读取：非 Linux 平台应明确不支持
// ---------------------------------------------------------------------------

func TestLocalReadUnsupported(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Linux 上本机读取受支持，跳过不适用断言")
	}
	if LocalSupported() {
		t.Fatal("非 Linux 平台 LocalSupported() 应为 false")
	}
	if _, _, err := LocalRead(); !errors.Is(err, ErrLocalUnsupported) {
		t.Fatalf("want ErrLocalUnsupported, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

// firstN 截取前 n 字节（含中文时可能截断多字节字符，仅用于错误信息展示）。
func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
