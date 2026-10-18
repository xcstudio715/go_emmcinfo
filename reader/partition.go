package reader

import (
	"fmt"
	"strconv"
	"strings"
)

// Partition 为 /proc/partitions 中的一条记录。
type Partition struct {
	Major  int
	Minor  int
	Blocks uint64 // 以 512 字节为单位的块数
	Name   string
}

// ParsePartitions 解析 /proc/partitions。
//
// 跳过表头（"major minor #blocks name"）与任何字段数/格式不符的行；
// 每行为 4 个空白分隔字段：major minor #blocks name。
func ParsePartitions(out string) []Partition {
	var parts []Partition
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 4 {
			continue
		}
		major, e1 := strconv.Atoi(f[0])
		minor, e2 := strconv.Atoi(f[1])
		blocks, e3 := strconv.ParseUint(f[2], 10, 64)
		if e1 != nil || e2 != nil || e3 != nil {
			continue
		}
		parts = append(parts, Partition{Major: major, Minor: minor, Blocks: blocks, Name: f[3]})
	}
	return parts
}

// CapacityGiB 由块数（每块 512 字节）换算容量（GiB）。
func CapacityGiB(blocks uint64) float64 {
	return float64(blocks) * 512 / (1024 * 1024 * 1024)
}

// FormatGiB 把 GiB 容量格式化为两位小数展示，如 "14.22 GiB"。
func FormatGiB(gib float64) string {
	return fmt.Sprintf("%.2f GiB", gib)
}
