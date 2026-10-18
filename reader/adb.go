package reader

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Runner 抽象"执行外部命令并返回合并输出"的能力，便于用假实现做单测。
type Runner interface {
	Run(args ...string) (string, error)
}

// execRunner 为默认实现：基于 exec.CommandContext，**以参数列表调用**
// （exec.Command(args[0], args[1:]...)），不经 `sh -c` 拼接，避免注入与分词歧义。
type execRunner struct {
	timeout time.Duration
}

func (e execRunner) Run(args ...string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("空命令")
	}
	if e.timeout <= 0 {
		e.timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	// 必须在此处统一隐藏子进程窗口：所有 adb 调用都经过 execRunner.Run，
	// 是唯一 spawn 点（config.go 只用 exec.LookPath，不产生进程）。
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Device 表示 `adb devices` 列出的一条设备记录。
type Device struct {
	Serial string
	State  string
}

// ParseDevices 解析 `adb devices` 输出。
//
// 跳过标题行（"List of devices attached"）、守护进程信息行（以 "*" 开头）、空行；
// 其余行取前两个空白分隔字段为 Serial / State。
func ParseDevices(out string) []Device {
	var devs []Device
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "*") ||
			strings.HasPrefix(line, "List of devices") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		devs = append(devs, Device{Serial: fields[0], State: fields[1]})
	}
	return devs
}

// AdbClient 封装对单个 adb 目标设备的调用。
//
// Path 为 adb 可执行文件路径；Serial 非空时所有命令附带 `-s <serial>`；
// Runner 可注入假实现用于单测（生产用 execRunner）。
type AdbClient struct {
	Path   string
	Serial string
	Runner Runner

	// devices 结果在单个客户端（即"单次读取"）内缓存，避免重复调用 adb devices。
	devMu  sync.Mutex
	devSet bool
	devs   []Device
	devErr error
}

// NewAdbClient 构造 AdbClient；runner 为 nil 时使用带 DefaultTimeout 的 execRunner。
func NewAdbClient(path, serial string, runner Runner) *AdbClient {
	if runner == nil {
		runner = execRunner{timeout: DefaultTimeout}
	}
	return &AdbClient{Path: path, Serial: serial, Runner: runner}
}

// base 组装完整参数列表（含 -s 选择设备）。
func (c *AdbClient) base(extra ...string) []string {
	args := []string{c.Path}
	if c.Serial != "" {
		args = append(args, "-s", c.Serial)
	}
	return append(args, extra...)
}

func (c *AdbClient) run(extra ...string) (string, error) {
	return c.Runner.Run(c.base(extra...)...)
}

// Devices 执行 `adb devices` 并解析；同一客户端内只真正调用一次（结果缓存）。
func (c *AdbClient) Devices() ([]Device, error) {
	c.devMu.Lock()
	defer c.devMu.Unlock()
	if c.devSet {
		return c.devs, c.devErr
	}
	out, err := c.run("devices")
	// 仍尝试解析已有输出（adb 有时把提示写在 stderr）。
	c.devs = ParseDevices(out)
	c.devErr = err
	c.devSet = true
	return c.devs, c.devErr
}

// ShellCat 执行 `adb shell cat <path>`（**不带** su / su -c，未 root 直读）。
func (c *AdbClient) ShellCat(path string) (string, error) {
	return c.run("shell", "cat", path)
}

// ShellLs 执行 `adb shell ls <dir>`。
func (c *AdbClient) ShellLs(dir string) (string, error) {
	return c.run("shell", "ls", dir)
}

// ShellScript 执行 `adb shell <script>`：整段设备端脚本作为**单个参数**传入。
func (c *AdbClient) ShellScript(script string) (string, error) {
	return c.run("shell", script)
}

// unreadableKeywords 为各机型/内核常见的"节点不可读"错误关键字。
var unreadableKeywords = []string{
	"Permission denied",
	"Operation not permitted",
	"No such file or directory",
	"No such file",
	"No such device",
	"not found",
	"Too many symbolic links",
	"Invalid argument",
}

// UnreadableReason 判断一次读取失败是否属于"不可读"，并给出简短原因。
//
// out 为命令输出（cat 的错误信息通常在此），err 为执行错误。
// 任何 err != nil 均视为"不可读"（返回 true）：命中已知关键字时返回该关键字，
// 否则返回输出或 err 文本。未失败返回 ("", false)。
func UnreadableReason(out string, err error) (string, bool) {
	if err == nil {
		return "", false
	}
	combined := out + "\n" + err.Error()
	for _, kw := range unreadableKeywords {
		if strings.Contains(combined, kw) {
			return kw, true
		}
	}
	msg := strings.TrimSpace(out)
	if msg == "" {
		msg = strings.TrimSpace(err.Error())
	}
	return msg, true
}
