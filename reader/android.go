package reader

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"emmc-tool/model"
)

// 目录常量（集中定义，避免散落）。
const mmcHostRoot = "/sys/class/mmc_host/"

// 哨兵错误：供调用方 errors.Is 判断，失败一律返回错误而非 panic。
var (
	ErrMMCNotFound   = errors.New("未找到 mmc 设备目录")
	ErrBlockNotFound = errors.New("未找到 mmcblk 块设备")
)

var (
	reHost   = regexp.MustCompile(`^mmc\d+$`)
	reDevice = regexp.MustCompile(`^mmc\d+:[0-9a-fA-F]+$`)
	reBlock  = regexp.MustCompile(`^mmcblk\d+$`)
)

// ParseListEntries 把 `ls` 输出切分为条目列表。
//
// 关键事实（真机实测，Android 13 / Samsung eMMC）：
// Android 的 `ls` 为**列优先多列**输出，同一目录下多个条目会出现在同一行
// （例：`device  mmc0:0001  power  subsystem  uevent`），且行尾仅 `\n`（无 `\r`）。
// 因此解析必须 **先按行 split，再按空白切 token**，不能按整行过滤。
//
// 容错：去 token 尾部 `/`、丢弃空 token 与空行。
func ParseListEntries(out string) []string {
	var entries []string
	for _, line := range strings.Split(out, "\n") {
		for _, tok := range strings.Fields(line) {
			tok = strings.TrimRight(tok, "/")
			if tok == "" {
				continue
			}
			entries = append(entries, tok)
		}
	}
	return entries
}

// filterEntries 保留匹配 re 的条目。
func filterEntries(entries []string, re *regexp.Regexp) []string {
	var out []string
	for _, e := range entries {
		if re.MatchString(e) {
			out = append(out, e)
		}
	}
	return out
}

// numericSuffix 取名字末尾的数字部分（如 "mmc0"→0，"mmcblk10"→10）；无数字返回 -1。
func numericSuffix(name string) int {
	i := len(name)
	for i > 0 && name[i-1] >= '0' && name[i-1] <= '9' {
		i--
	}
	if i == len(name) {
		return -1
	}
	n, err := strconv.Atoi(name[i:])
	if err != nil {
		return -1
	}
	return n
}

// sortByNumericSuffix 按末尾数字升序排序（真机 ls 为列优先，顺序不可信）。
func sortByNumericSuffix(items []string) {
	sort.SliceStable(items, func(i, j int) bool {
		return numericSuffix(items[i]) < numericSuffix(items[j])
	})
}

// MMCDevice 描述一次"自动发现"得到的 eMMC 设备目录。
type MMCDevice struct {
	Host      string // 形如 "mmc0"
	Dev       string // 形如 "mmc0:0001"
	HostDir   string // /sys/class/mmc_host/mmc0
	DeviceDir string // /sys/class/mmc_host/mmc0/mmc0:0001
}

// sectionMarker 为批读脚本的分段约定（见 discoverScript / batchReadScript）。
const sectionMarker = "###"

// discoverScript 为发现脚本（**二级，printf 版，降级用**）：一次 adb shell 列出 host、
// 各 host 下的设备目录、以及每个设备目录的 type。
// 固定字符串（不在 Go 侧插值任何设备数据），无注入面。
//
// 分段：`###HOSTS` → ls /sys/class/mmc_host/；`###DEVS <host>` → ls <hostDir>/；
// `###TYPE <host>/<dev>` → cat <deviceDir>/type。
//
// 一级为 discoverScriptFast（printf 全部换成内建 echo，外部命令更少）；
// 本脚本保留 printf 原样，作为一级执行失败或分段标签不全时的稳定回退。
const discoverScript = `printf '###HOSTS\n'; ls /sys/class/mmc_host/; for h in $(ls /sys/class/mmc_host/ 2>/dev/null); do printf '###DEVS %s\n' "$h"; ls "/sys/class/mmc_host/$h/" 2>/dev/null; for d in $(ls "/sys/class/mmc_host/$h/" 2>/dev/null); do case "$d" in *:*) printf '###TYPE %s/%s\n' "$h" "$d"; cat "/sys/class/mmc_host/$h/$d/type" 2>/dev/null;; esac; done; done`

// discoverScriptFast 为**一级发现脚本（echo 版）**：与 discoverScript 结构完全相同，
// 仅把 printf 标记换成内建 echo（静态 3 处调用点：HOSTS×1 + 每 host 的 DEVS×1 +
// 每设备的 TYPE×1；真机快照 2 host × 2 dev = 运行期 5 次输出）。
// printf 为外部命令（每次 fork+exec 约 28.2 ms），echo 为内建（约 0.25 ms）：
// 真机外部命令由 13 条降到 8 条，discover 段实测由中位 523 ms 进一步下降。
//
// 逐字节等价：`echo '<串>'` 与 `printf '<串>\n'` 输出完全一致（标记串均不含反斜杠，
// host/设备名受 reHost/reDevice 白名单约束，无 echo 转义歧义）；
// 已在真机上将两版输出逐字节对拍（SHA256 一致），解析侧无需改动。
//
// 若执行失败或分段标签不全（discoverComplete 为 false），由 DiscoverMMC 回退二级。
const discoverScriptFast = `echo '###HOSTS'; ls /sys/class/mmc_host/; for h in $(ls /sys/class/mmc_host/ 2>/dev/null); do echo "###DEVS $h"; ls "/sys/class/mmc_host/$h/" 2>/dev/null; for d in $(ls "/sys/class/mmc_host/$h/" 2>/dev/null); do case "$d" in *:*) echo "###TYPE $h/$d"; cat "/sys/class/mmc_host/$h/$d/type" 2>/dev/null;; esac; done; done`

// parseSections 解析 "###<标签>" 分段输出：返回 标签 → 段内容。
//
// 规则：行首以 "###" 开头者为新分段标签；其余行追加到当前分段内容。
// 标记出现前的行被丢弃。段内容由调用方判空/判错（见 ReadAllBatched）。
func parseSections(out string) map[string]string {
	content := map[string]string{}
	current := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, sectionMarker) {
			current = strings.TrimSpace(line[len(sectionMarker):])
			if _, ok := content[current]; !ok {
				content[current] = ""
			}
			continue
		}
		if current == "" {
			continue
		}
		content[current] += line + "\n"
	}
	return content
}

// DiscoverMMC 自动发现 eMMC 设备目录，**不硬编码 mmc0:0001**，
// 正常路径只用**一次** adb shell 调用（合并原先的 3 次 ls/cat）。
//
// 两级降级（与批读一致的设计）：先跑一级 echo 版脚本（discoverScriptFast）；
// 执行失败或分段标签不全（discoverComplete 为 false）时，回退二级 printf 版
// （discoverScript）。两级均失败时返回包装 ErrMMCNotFound 的错误（含原始输出），**不 panic**。
func DiscoverMMC(c *AdbClient) (MMCDevice, error) {
	out, err := c.ShellScript(discoverScriptFast)
	if err != nil || !discoverComplete(out) {
		// 一级失败（执行错误 / 分段标签不全）→ 退到二级 printf 版。
		out, err = c.ShellScript(discoverScript)
		if err != nil {
			return MMCDevice{}, fmt.Errorf("%w: 执行发现脚本失败: %v (原始输出: %q)",
				ErrMMCNotFound, err, out)
		}
	}
	return pickMMCDevice(out)
}

// pickMMCDevice 从发现脚本输出中选出 eMMC 设备目录（纯函数，便于单测）。
//
//   - `###HOSTS` → 取匹配 `^mmc\d+$` 的 host（按数字升序）；
//   - `###DEVS <host>` → 取匹配 `^mmc\d+:[0-9a-fA-F]+$` 的设备目录（按数字升序取首个）；
//   - `###TYPE <host>/<dev>` → **优先 type=="MMC"**；type=="SD" 的卡跳过换下一个 host；
//     type 分段缺失/不可读时回退旧逻辑（取该 host 下首个设备目录）。
func pickMMCDevice(out string) (MMCDevice, error) {
	sections := parseSections(out)
	hostOut := sections["HOSTS"]
	hosts := filterEntries(ParseListEntries(hostOut), reHost)
	sortByNumericSuffix(hosts)
	if len(hosts) == 0 {
		return MMCDevice{}, fmt.Errorf("%w: %s 下无 mmcN 条目 (原始输出: %q)",
			ErrMMCNotFound, mmcHostRoot, hostOut)
	}

	var lastRaw string
	for _, h := range hosts {
		hostDir := mmcHostRoot + h
		devOut := sections["DEVS "+h]
		lastRaw = devOut
		devs := filterEntries(ParseListEntries(devOut), reDevice)
		if len(devs) == 0 {
			continue
		}
		sortByNumericSuffix(devs)
		deviceDir := hostDir + "/" + devs[0]
		if strings.EqualFold(strings.TrimSpace(sections["TYPE "+h+"/"+devs[0]]), "SD") {
			continue
		}
		return MMCDevice{
			Host:      h,
			Dev:       devs[0],
			HostDir:   hostDir,
			DeviceDir: deviceDir,
		}, nil
	}
	return MMCDevice{}, fmt.Errorf("%w: 各 mmc host 下均无 MMC 类型设备目录（SD 已跳过）(最后原始输出: %q)",
		ErrMMCNotFound, lastRaw)
}

// discoverComplete 校验发现脚本输出的分段标签是否齐全（纯函数，供两级降级判定）：
//   - ###HOSTS 存在且至少列出一个 mmcN host；
//   - 每个 host 都有 ###DEVS <host> 分段；
//   - 每个 ###DEVS 分段里列出的每个设备目录（mmcN:XXXX），都有对应的 ###TYPE <host>/<dev> 分段。
//
// 输出被截断或标签缺失时返回 false，调用方据此回退二级脚本。
// 注意：HOSTS 有条目但全部 host 下都无设备目录属于"合法但无卡"，此处仍判完整，
// 交给 pickMMCDevice 返回 ErrMMCNotFound，避免无意义的二级重试。
func discoverComplete(out string) bool {
	sections := parseSections(out)
	hosts := filterEntries(ParseListEntries(sections["HOSTS"]), reHost)
	sortByNumericSuffix(hosts)
	if len(hosts) == 0 {
		return false
	}
	for _, h := range hosts {
		devOut, ok := sections["DEVS "+h]
		if !ok {
			return false
		}
		for _, d := range filterEntries(ParseListEntries(devOut), reDevice) {
			if _, ok := sections["TYPE "+h+"/"+d]; !ok {
				return false
			}
		}
	}
	return true
}

// DiscoverBlock 从**已发现的 mmc 设备目录**的 block/ 子目录取块设备名（如 "mmcblk0"）。
//
// 路径：<DeviceDir>/block/（例 /sys/class/mmc_host/mmc0/mmc0:0001/block/）。
// 与 host/设备绑定，避免误取 SD 卡或其它 mmcblk 的块设备。
// **批量失败时的回退路径**（正常路径由 ReadAllBatched 的 `@block` 分段一次取回）。
func DiscoverBlock(c *AdbClient, dev MMCDevice) (string, error) {
	dir := dev.DeviceDir + "/block/"
	out, err := c.ShellLs(dir)
	if err != nil {
		return "", fmt.Errorf("%w: 列出 %s 失败: %v (原始输出: %q)",
			ErrBlockNotFound, dir, err, out)
	}
	return ParseBlockName(out)
}

// ParseBlockName 从 ls 输出中取块设备名（如 "mmcblk0"），按数字后缀取最小者
// （真机 ls 为列优先，顺序不可信）。（纯函数，供 DiscoverBlock 与批读共用。）
func ParseBlockName(out string) (string, error) {
	ents := filterEntries(ParseListEntries(out), reBlock)
	if len(ents) == 0 {
		return "", fmt.Errorf("%w: 无 mmcblkN 条目 (原始输出: %q)", ErrBlockNotFound, out)
	}
	sortByNumericSuffix(ents)
	return ents[0], nil
}

// androidFields 为 Android sysfs 下要逐节点读取的字段（顺序即采集顺序）。
var androidFields = []string{
	"name", "cid", "rev", "manfid", "oemid",
	"hwrev", "fwrev", "serial", "date", "life_time", "pre_eol_info",
}

// FieldNote 记录某字段为何被置为"不可读"（供 UI/报告如实展示）。
type FieldNote struct {
	Key    string // 字段名，如 "life_time"
	Node   string // 完整节点路径
	Reason string // 不可读原因（关键字或错误文本）
}

// ReadAll 在 deviceDir 下逐字段 `cat` 采集原始数据。
//
// **容错保证**：任一节点不可读/为空时，仅把该字段置为 model.Unreadable 并追加一条
// FieldNote，**不中断**其它字段的读取。
func ReadAll(c *AdbClient, deviceDir string) (model.RawInfo, []FieldNote) {
	var raw model.RawInfo
	var notes []FieldNote
	for _, key := range androidFields {
		node := deviceDir + "/" + key
		out, err := c.ShellCat(node)
		if err != nil {
			raw = assignRawField(raw, key, model.Unreadable)
			reason, _ := UnreadableReason(out, err)
			notes = append(notes, FieldNote{Key: key, Node: node, Reason: reason})
			continue
		}
		val := strings.TrimSpace(out)
		if val == "" {
			raw = assignRawField(raw, key, model.Unreadable)
			notes = append(notes, FieldNote{Key: key, Node: node, Reason: "空输出"})
			continue
		}
		raw = assignRawField(raw, key, val)
	}
	return raw, notes
}

// reDeviceDir 为 deviceDir 的**白名单**：仅允许 [A-Za-z0-9:._/-]。
//
// deviceDir 来自设备 ls 输出，拼进 shell 前必须校验；不合法直接返回错误、不执行。
// 该字符集不含任何 shell 元字符（空格 ; | & $ ` " ' \ ( ) * ? [ ] < > 换行等），故无注入面。
var reDeviceDir = regexp.MustCompile(`^[A-Za-z0-9:._/-]+$`)

// validateDeviceDir 校验 deviceDir 可安全拼入 shell。
//
// 两道关卡：
//  1. 白名单字符集 reDeviceDir（无 shell 元字符，无注入面）；
//  2. 显式拒绝含 ".." 的路径段：字符集虽合法，但 "../.." 会让 cat 越界读到
//     白名单外目录（如 /sys/class/mmc_host/mmc0/mmc0:0001/../../../../etc），
//     故一律拒绝、不执行。
func validateDeviceDir(deviceDir string) error {
	if strings.Contains(deviceDir, "..") {
		return fmt.Errorf("非法设备目录（含路径穿越 \"..\"），已拒绝执行: %q", deviceDir)
	}
	if !reDeviceDir.MatchString(deviceDir) {
		return fmt.Errorf(
			"非法设备目录（含白名单 [A-Za-z0-9:._/-] 之外的字符），已拒绝执行: %q", deviceDir)
	}
	return nil
}

// batchReadScript 生成"一条 adb shell 读完字段 + block + 分区表"的设备端脚本。
//
// deviceDir 已通过 validateDeviceDir 校验后才传入，故可安全拼接。
// 分段：`###<字段名>` → 该字段 cat 的原始输出；`###@block` → block 目录 ls；
// `###@partitions` → /proc/partitions。
//
// **不使用命令替换**：逐字段用 `v=$(cat ...)` 会为每个字段 fork 一个子 shell
// （11 字段 = 11 次 fork），实测成为主要耗时来源；改为直接 `cat` + 空 `echo`
// 作段内空白分隔，零额外 fork。字段是否可读由解析侧判空/判错（见 ReadAllBatched）。
//
// **为什么用 echo 而不是 printf**：实测设备端 `type printf` 为
// `printf is a tracked alias for /system/bin/printf`（**外部命令**），
// 每条外部命令（fork+exec）约 28.2 ms；而 `echo` 为 shell **内建**，约 0.25 ms。
// 本脚本原先 11+11+2 = 24 条 printf 标记就是主要耗时来源，改用 echo 后
// 外部命令由 37 条降到 13 条（仅剩 11 条 cat + ls + cat /proc/partitions）。
//
// 逐字节等价：`echo "###$f"` 与 `printf '###%s\n' "$f"`、空 `echo` 与 `printf '\n'`
// 输出的字节序列完全相同（已用同一脚本文本在 sh 下对拍验证）。
//
// **本脚本为"二级脚本"（a_echo，降级用）**：一级为 batchReadScriptFast（内建 read+echo，
// 外部命令 2 条）；本脚本逐字段 cat，外部命令 13 条，作为一级失败时的稳定回退。
func batchReadScript(deviceDir string) string {
	return `d=` + deviceDir +
		`; for f in ` + strings.Join(androidFields, " ") +
		`; do echo "` + sectionMarker + `$f"; cat "$d/$f" 2>&1; echo; done` +
		`; echo '` + sectionMarker + `@block'; ls "$d/block/" 2>/dev/null` +
		`; echo '` + sectionMarker + `@partitions'; cat /proc/partitions 2>&1`
}

// batchReadScriptFast 为**一级脚本（c_echo）**：可读节点用 shell 内建 read+echo 回放，
// 不可读节点才回落到 cat。
//
//	if [ -r "$d/$f" ]; then while IFS= read -r l || [ -n "$l" ]; do echo "$l"; done < "$d/$f"
//	else cat "$d/$f" 2>&1; fi
//
// 设计要点（真机实测判定，4 场景 × 多次迭代）：
//   - `[ -r ]` 为 shell 内建（不 fork），先判可读性，**不再对不可读文件执行 `< file`**；
//     因而不会触发 shell 自身的 "can't open" 诊断——该诊断写在 shell stderr，与走管道的
//     stdout 缓冲不同步，曾导致错误串串到**下一字段**（间歇落到 fwrev / date）。
//   - 不可读分支的 `cat "$d/$f" 2>&1` 把错误写到 fd1、**落在本段内且顺序确定**，
//     故与二级脚本逐字节一致（实测：可读 / 000 权限 / 不存在 / 0 字节 四场景均同字节）。
//   - `|| [ -n "$l" ]` 为末行守卫，防无结尾换行的文件丢行。
//
// 正常路径外部命令仅 ls 与 cat /proc/partitions 共 2 条（二级脚本为 13 条）。
// 仅当 `[ -r ]`/`read` 语义异常（如极老 busybox）导致分段不完整时，才回退到二级脚本。
func batchReadScriptFast(deviceDir string) string {
	return `d=` + deviceDir +
		`; for f in ` + strings.Join(androidFields, " ") +
		`; do echo "` + sectionMarker + `$f"` +
		`; if [ -r "$d/$f" ]; then while IFS= read -r l || [ -n "$l" ]; do echo "$l"; done < "$d/$f"` +
		`; else cat "$d/$f" 2>&1; fi; echo; done` +
		`; echo '` + sectionMarker + `@block'; ls "$d/block/" 2>/dev/null` +
		`; echo '` + sectionMarker + `@partitions'; cat /proc/partitions 2>&1`
}

// batchReadScriptRoot 为 **root 版一级脚本**：整段经设备端 `su -c '...'` 以 root 执行。
//
// 字段读取沿用一级 fast（c_echo）的内建 read 方案（[ -r ] 前置守卫 + 内建 read 回放），
// 分段结构与普通脚本完全一致（11 字段 + @block + @partitions），输出逐字节等价——
// 唯一差异是段标记 echo 改用**双引号**：外层 su -c 以单引号包裹整段脚本，
// **内层不得再出现单引号**，故 `echo '###@block'` 改写为输出完全相同的 `echo "###@block"`。
//
// 单引号的作用：host 侧（adb shell 的 sh）不展开 $d/$f/$l，变量展开发生在
// su 拉起的 root shell 内。deviceDir 已过 validateDeviceDir 白名单（不含单引号），
// 可安全拼接。
func batchReadScriptRoot(deviceDir string) string {
	inner := `d=` + deviceDir +
		`; for f in ` + strings.Join(androidFields, " ") +
		`; do echo "` + sectionMarker + `$f"` +
		`; if [ -r "$d/$f" ]; then while IFS= read -r l || [ -n "$l" ]; do echo "$l"; done < "$d/$f"` +
		`; else cat "$d/$f" 2>&1; fi; echo; done` +
		`; echo "` + sectionMarker + `@block"; ls "$d/block/" 2>/dev/null` +
		`; echo "` + sectionMarker + `@partitions"; cat /proc/partitions 2>&1`
	return `su -c '` + inner + `'`
}

// batchSectionsComplete 校验一次批读响应是否含全部预期分段（字段 11 + @block + @partitions）。
//
// 用**分段标签是否存在**判定，而非段内容是否为空——空段是合法结果（如 0 字节节点）。
func batchSectionsComplete(out string) bool {
	content := parseSections(out)
	for _, key := range androidFields {
		if _, ok := content[key]; !ok {
			return false
		}
	}
	if _, ok := content["@block"]; !ok {
		return false
	}
	if _, ok := content["@partitions"]; !ok {
		return false
	}
	return true
}

// BatchResult 为一次批量读取的结果。
type BatchResult struct {
	Raw       model.RawInfo
	Notes     []FieldNote
	BlockName string
	BlockErr  error

	// PartitionOut 为 /proc/partitions 原始输出；不可读时为空且 PartitionNote 非 nil。
	PartitionOut  string
	PartitionNote *FieldNote

	// RootDegraded 为 true 表示 UseRoot=true 时 root 批读链失败（执行错误或分段不完整），
	// 已自动回退原有非 root 脚本链完成本次读取。
	RootDegraded bool
}

// ReadAllBatched 用**一条** adb shell（而非每字段一次）批读字段 + block + 分区表。
//
// 安全约束：deviceDir 必须先通过 validateDeviceDir（字符白名单 + 拒绝 ".."），
// 否则直接返回错误且**不执行**任何命令。
// 容错保证：某字段缺失/报错时仅把该字段置为 model.Unreadable 并追加 FieldNote，**不中断**其它字段。
//
// 字段可读性判定（等价于原 `##ERR##` 语义，但无需 fork 子 shell）：
//   - 段内容 TrimSpace 后为空 → 不可读，原因 "空输出"；
//   - 段内容命中 unreadableKeywords（Permission denied 等）→ 不可读，原因为该关键字；
//   - 其余 → 视为字段值。
//
// 返回的 error 仅表示"整条批量调用本身失败"（root 链（若启用）与一、二级脚本均失败），
// 调用方应据此**降级**到逐字段 ReadAll + DiscoverBlock 回退路径（第三级）。
//
// 降级链：
//   - UseRoot=false（原有路径，行为与脚本逐字节不变）：
//     batchReadScriptFast（c_echo）→ batchReadScript（a_echo）→ 调用方逐字段 ReadAll；
//   - UseRoot=true：batchReadScriptRoot（su -c 包裹 c_echo）→ 失败/分段不完整则置
//     RootDegraded=true 并回退上述原有两级链 → 调用方逐字段 ReadAll。
func ReadAllBatched(c *AdbClient, deviceDir string) (BatchResult, error) {
	if err := validateDeviceDir(deviceDir); err != nil {
		return BatchResult{}, err
	}
	rootDegraded := false

	var out string
	var err error
	if c.UseRoot {
		// root 优先：root 链成功且分段完整则直接采用。
		out, err = c.ShellScript(batchReadScriptRoot(deviceDir))
		if err != nil || !batchSectionsComplete(out) {
			rootDegraded = true
			out, err = c.ShellScript(batchReadScriptFast(deviceDir))
		}
	} else {
		out, err = c.ShellScript(batchReadScriptFast(deviceDir))
	}
	if err != nil || !batchSectionsComplete(out) {
		// 一级失败（执行错误 / 分段不完整）→ 退到二级 a_echo（逐字段 cat）。
		out, err = c.ShellScript(batchReadScript(deviceDir))
		if err != nil {
			return BatchResult{}, fmt.Errorf("批量读取失败（脚本链均失败）: %v (原始输出: %q)", err, out)
		}
		if !batchSectionsComplete(out) {
			return BatchResult{}, fmt.Errorf("批量读取分段不完整（脚本链均失败）(原始输出: %q)", out)
		}
	}
	content := parseSections(out)

	var res BatchResult
	res.RootDegraded = rootDegraded
	for _, key := range androidFields {
		node := deviceDir + "/" + key
		val := strings.TrimSpace(content[key])
		if val == "" {
			res.Raw = assignRawField(res.Raw, key, model.Unreadable)
			res.Notes = append(res.Notes, FieldNote{Key: key, Node: node, Reason: "空输出"})
			continue
		}
		if reason, bad := matchUnreadableKeyword(val); bad {
			res.Raw = assignRawField(res.Raw, key, model.Unreadable)
			res.Notes = append(res.Notes, FieldNote{Key: key, Node: node, Reason: reason})
			continue
		}
		res.Raw = assignRawField(res.Raw, key, val)
	}

	res.BlockName, res.BlockErr = ParseBlockName(content["@block"])

	parts := strings.TrimSpace(content["@partitions"])
	switch {
	case parts == "":
		res.PartitionNote = &FieldNote{Key: "partitions", Node: "/proc/partitions", Reason: "空输出"}
	default:
		if reason, bad := matchUnreadableKeyword(parts); bad {
			res.PartitionNote = &FieldNote{Key: "partitions", Node: "/proc/partitions", Reason: reason}
		} else {
			res.PartitionOut = parts
		}
	}
	return res, nil
}

// matchUnreadableKeyword 从段内容中提取已知"不可读"关键字。
// 命中返回 (关键字, true)；未命中返回 ("", false)——此时该内容应视为字段值。
func matchUnreadableKeyword(s string) (string, bool) {
	for _, kw := range unreadableKeywords {
		if strings.Contains(s, kw) {
			return kw, true
		}
	}
	return "", false
}

// assignRawField 把单个字段写回 model.RawInfo。
func assignRawField(r model.RawInfo, key, val string) model.RawInfo {
	switch key {
	case "name":
		r.Name = val
	case "cid":
		r.CID = val
	case "rev":
		r.Rev = val
	case "manfid":
		r.ManfID = val
	case "oemid":
		r.OEMID = val
	case "hwrev":
		r.HWRev = val
	case "fwrev":
		r.FWRev = val
	case "serial":
		r.Serial = val
	case "date":
		r.Date = val
	case "life_time":
		r.LifeTime = val
	case "pre_eol_info":
		r.PreEOL = val
	}
	return r
}
