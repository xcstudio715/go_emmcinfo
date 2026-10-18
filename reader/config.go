package reader

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// DefaultTimeout 为单次外部命令（adb 等）调用的默认超时。
const DefaultTimeout = 10 * time.Second

// ErrNoDevice 表示没有处于 "device" 状态的已连接设备。
var ErrNoDevice = errors.New("未发现处于 device 状态的已连接设备")

// Progress 描述一次读取的进度：当前第 Stage 阶段（共 Total 个），Name 为阶段名。
type Progress struct {
	Stage int
	Total int
	Name  string
}

// ProgressFunc 为进度回调（可为 nil，表示调用方不关心进度）。
//
// 回调可能在非 UI 线程被调用，实现方需自行保证线程安全（UI 侧经 fyne.Do 回主线程）。
type ProgressFunc func(Progress)

// ResolveADB 集中解析 adb 可执行文件路径（不散落在各处）。
//
// 优先级：同目录 ./adb/adb(.exe) → 上级 ../adb/adb(.exe) → 上上级 → PATH 中的 adb。
// 上级路径覆盖本仓库布局（可执行文件在 go-version/，adb 在 ../adb/）。
func ResolveADB() (string, error) {
	names := []string{"adb"}
	if runtime.GOOS == "windows" {
		names = []string{"adb.exe", "adb"}
	}
	var candidates []string
	for _, dir := range []string{"adb", filepath.Join("..", "adb"), filepath.Join("..", "..", "adb")} {
		for _, n := range names {
			candidates = append(candidates, filepath.Join(dir, n))
		}
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			abs, _ := filepath.Abs(c)
			return abs, nil
		}
	}
	if p, err := exec.LookPath("adb"); err == nil {
		return p, nil
	}
	return "", errors.New("未找到 adb：请将 adb 放入 ./adb/ 或加入 PATH")
}

// SelectDevice 从设备列表中选出目标设备（集中设备选择逻辑）。
//
//   - want 非空：必须精确匹配 Serial，否则返回错误。
//   - want 为空：取第一个 state == "device" 的在线设备；无则返回 ErrNoDevice。
func SelectDevice(devices []Device, want string) (Device, error) {
	if want != "" {
		for _, d := range devices {
			if d.Serial == want {
				return d, nil
			}
		}
		return Device{}, errors.New("指定的设备不在已连接列表中: " + want)
	}
	for _, d := range devices {
		if d.State == "device" {
			return d, nil
		}
	}
	return Device{}, ErrNoDevice
}
