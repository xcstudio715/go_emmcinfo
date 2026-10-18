package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/image/font/sfnt"

	"emmc-tool/assets"
)

// ---------------------------------------------------------------------------
// 中文渲染静态覆盖校验（不需要 GUI）
//
// 目的：不启动界面就证明"界面会显示的每一个字符，内嵌字体里都有字形"，
// 即不会出现豆腐块。
//
// 做法：
//  1. 用 go/parser + go/ast 只取运行时源码里的**字符串字面量**
//     （不含注释、不含 import 路径、不含 _test.go 的断言文案）；
//  2. 按声明的码位区间逐字符检查，列出区间外的字符；
//  3. 更严格的一步：**直接解析内嵌 TTF 的 cmap**，逐字符断言 glyph index != 0。
//     .notdef(0) 在界面上就是方块，所以这一步是"无豆腐块"的直接证据，比截图严格。
//
// 实测结论：枚举的 5 个区间会漏掉全角标点（U+FF00-FFEF，如 （）、，：；）。
// 这些字符属于 GB2312 全角区，子集字体里**确有字形**（由第 3 步的 cmap 断言证明），
// 故不作为失败；但会逐条列出，见 TestDisplayedStringsOutsideDeclaredRanges 的日志。
// ---------------------------------------------------------------------------

// scanDirs 为会产生"显示到界面"的字符串的源码目录（相对仓库根；测试 cwd 即包目录）。
//
// ui 是界面文案；decoder 的解码结果文案（厂商/版本/寿命/状态）与 reader 的
// 不可读原因、状态提示，都会被 ui 直接拼进标签页与导出报告，故一并纳入。
var scanDirs = []string{"ui", "decoder", "reader", "model"}

// declaredRanges 为约定的可用码位区间。
//
// 注：约定列表为 U+0020-007E / U+00A0-00FF / U+2000-206F / U+3000-303F
// / U+4E00-9FFF（"及 GB2312 汉字"）。GB2312 汉字全部落在 U+4E00-9FFF 内，
// 已被最后一条覆盖。
var declaredRanges = [][2]rune{
	{0x0020, 0x007E}, // ASCII 可见字符
	{0x00A0, 0x00FF}, // Latin-1 补充
	{0x2000, 0x206F}, // 常用标点：– … “ ” ‘ ’
	{0x3000, 0x303F}, // CJK 标点：。、〈〉等
	{0x4E00, 0x9FFF}, // CJK 统一表意（含全部 GB2312 汉字）
}

// literal 为一条字符串字面量及其位置。
type literal struct {
	File  string
	Line  int
	Value string
}

// displayedLiterals 采集 scanDirs 下所有运行时 Go 源文件里的字符串字面量。
func displayedLiterals(t *testing.T) []literal {
	t.Helper()
	var out []literal
	for _, dir := range scanDirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			// 只看运行时源码：_test.go 里的字符串是测试断言文案，不会显示到界面。
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return perr
			}
			ast.Inspect(f, func(n ast.Node) bool {
				bl, ok := n.(*ast.BasicLit)
				if !ok || bl.Kind != token.STRING {
					return true
				}
				s, uerr := strconv.Unquote(bl.Value)
				if uerr != nil {
					return true
				}
				out = append(out, literal{
					File:  filepath.ToSlash(path),
					Line:  fset.Position(bl.Pos()).Line,
					Value: s,
				})
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("扫描目录 %s 失败: %v", dir, err)
		}
	}
	if len(out) == 0 {
		t.Fatal("未采集到任何字符串字面量，扫描目录配置有误")
	}
	return out
}

// isControl 判断是否为控制字符（换行/制表等，不产生可见字形）。
func isControl(r rune) bool { return r < 0x20 || r == 0x7F }

// inDeclaredRanges 判断字符是否落在声明的码位区间内。
func inDeclaredRanges(r rune) bool {
	for _, rg := range declaredRanges {
		if r >= rg[0] && r <= rg[1] {
			return true
		}
	}
	return false
}

// runeSites 汇总字符及其首次出现位置。
type runeSites map[rune]string

func (m runeSites) sortedRunes() []rune {
	rs := make([]rune, 0, len(m))
	for r := range m {
		rs = append(rs, r)
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i] < rs[j] })
	return rs
}

func (m runeSites) String() string {
	var b strings.Builder
	for _, r := range m.sortedRunes() {
		fmt.Fprintf(&b, "\n  U+%04X %q  首次出现: %s", r, string(r), m[r])
	}
	return b.String()
}

// TestDisplayedStringsOutsideDeclaredRanges 逐字符检查源码字面量，列出落在声明码位区间**之外**的字符。
//
// 硬门是 TestDisplayedStringsHaveGlyphInEmbeddedFont（cmap 为准）；
// 本测试仅列出声明区间外的字符，用于发现新增生僻字符，不判定失败。
func TestDisplayedStringsOutsideDeclaredRanges(t *testing.T) {
	lits := displayedLiterals(t)
	all, bad := runeSites{}, runeSites{}
	for _, l := range lits {
		for _, r := range l.Value {
			if isControl(r) {
				continue
			}
			site := fmt.Sprintf("%s:%d", l.File, l.Line)
			if _, ok := all[r]; !ok {
				all[r] = site
			}
			if inDeclaredRanges(r) {
				continue
			}
			if _, ok := bad[r]; !ok {
				bad[r] = site
			}
		}
	}
	t.Logf("已检查 %d 条字符串字面量，去重后 %d 个可见字符", len(lits), len(all))
	if len(bad) == 0 {
		t.Log("全部字符均落在声明的码位区间内")
		return
	}
	t.Logf("声明区间外的字符共 %d 个（下一条测试会核对它们是否真有字形）:%s", len(bad), bad)
}

// TestDisplayedStringsHaveGlyphInEmbeddedFont 直接解析内嵌 TTF 的 cmap，
// 逐字符断言 glyph index != 0（0 即 .notdef，界面上就是方块）。
//
// 这是"无豆腐块"的直接证据：只要本测试通过，界面文案的每个字符都有真实字形。
func TestDisplayedStringsHaveGlyphInEmbeddedFont(t *testing.T) {
	font, err := sfnt.Parse(assets.FontTTF())
	if err != nil {
		t.Fatalf("解析内嵌字体 %d 字节失败: %v", len(assets.FontTTF()), err)
	}
	t.Logf("内嵌字体 glyph 总数: %d，unitsPerEm: %d", font.NumGlyphs(), font.UnitsPerEm())

	var buf sfnt.Buffer
	seen, missing := runeSites{}, runeSites{}
	for _, l := range displayedLiterals(t) {
		for _, r := range l.Value {
			if isControl(r) {
				continue
			}
			site := fmt.Sprintf("%s:%d", l.File, l.Line)
			if _, ok := seen[r]; !ok {
				seen[r] = site
			}
			idx, gerr := font.GlyphIndex(&buf, r)
			if gerr != nil || idx == 0 {
				if _, ok := missing[r]; !ok {
					missing[r] = site
				}
			}
		}
	}
	t.Logf("逐字符查表完成：%d 个可见字符，缺失字形 %d 个", len(seen), len(missing))
	if len(missing) > 0 {
		t.Errorf("以下字符在内嵌字体中**没有字形**（界面会显示为方块）:%s", missing)
	}
}
