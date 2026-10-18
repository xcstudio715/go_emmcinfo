package reader

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"emmc-tool/model"
)

// ErrLocalUnsupported 表示当前平台不支持本机 eMMC 读取（仅 Linux 提供 /sys/class/mmc_host）。
var ErrLocalUnsupported = errors.New("本机 eMMC 读取仅在 Linux 上支持")

// LocalSupported 报告当前平台是否支持本机（非 ADB）eMMC 读取。
func LocalSupported() bool {
	return runtime.GOOS == "linux"
}

// LocalRead 读取本机 eMMC 的 sysfs 字段（仅 Linux）。
//
// 非 Linux 平台直接返回 ErrLocalUnsupported（Win/mac 尽力而为策略由调用方决定）。
// 与 ADB 路径一致：单节点不可读仅置 model.Unreadable 并记录 FieldNote，不中断其它字段。
func LocalRead() (model.RawInfo, []FieldNote, error) {
	if !LocalSupported() {
		return model.RawInfo{}, nil, ErrLocalUnsupported
	}
	dir, err := findLocalMMCDeviceDir()
	if err != nil {
		return model.RawInfo{}, nil, err
	}

	var raw model.RawInfo
	var notes []FieldNote
	for _, key := range androidFields {
		node := filepath.Join(dir, key)
		b, rerr := os.ReadFile(node)
		if rerr != nil {
			raw = assignRawField(raw, key, model.Unreadable)
			notes = append(notes, FieldNote{Key: key, Node: node, Reason: rerr.Error()})
			continue
		}
		val := strings.TrimSpace(string(b))
		if val == "" {
			raw = assignRawField(raw, key, model.Unreadable)
			notes = append(notes, FieldNote{Key: key, Node: node, Reason: "空输出"})
			continue
		}
		raw = assignRawField(raw, key, val)
	}
	return raw, notes, nil
}

// findLocalMMCDeviceDir 在本机 /sys/class/mmc_host/mmc* 下发现 eMMC 设备目录。
func findLocalMMCDeviceDir() (string, error) {
	hosts, err := filepath.Glob("/sys/class/mmc_host/mmc*")
	if err != nil {
		return "", err
	}
	sort.Strings(hosts)
	for _, host := range hosts {
		ents, derr := os.ReadDir(host)
		if derr != nil {
			continue
		}
		var devs []string
		for _, e := range ents {
			if reDevice.MatchString(e.Name()) {
				devs = append(devs, e.Name())
			}
		}
		if len(devs) == 0 {
			continue
		}
		sortByNumericSuffix(devs)
		return filepath.Join(host, devs[0]), nil
	}
	return "", ErrMMCNotFound
}
