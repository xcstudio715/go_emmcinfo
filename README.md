# eMMC 读取工具

单文件、免安装、跨平台的 eMMC 信息读取工具。插上 Android 手机，双击就能读出这颗闪存是谁造的、什么规格、还剩多少寿命。

界面中文已经内置，不用装字体。读取也不需要 root。

先说清楚“单文件”指什么：程序本体和字体是一个 exe，拷过去就能显示中文。但读 Android 设备得靠 adb，adb 不内置在程序里，具体见下面「快速开始」。

---

## 能读到什么

| 关注点 | 输出内容 |
|---|---|
| 是谁造的 | 厂商名 + 厂商 ID（MID） |
| 什么型号 | 产品名（PNM）、产品版本（PRV）、序列号（PSN） |
| 什么规格 | eMMC 规范版本（如 `eMMC 5.1`） |
| 什么时候产的 | 制造日期（如 `2017-04`） |
| **还剩多少寿命** | `life_time` 两段磨损区间，SLC / MLC-TLC 分开显示 |
| **是否该换了** | `pre_eol_info` 预 EOL 预警（Normal / Warning / Urgent） |
| 多大容量 | 整盘容量 + 各分区占用 |
| 完整身份 | CID 全字段解析（MID / OID / PNM / PRV / PSN） |

有些机型会屏蔽底层节点，字段读不到的时候会明确标注“不可读”并说明原因，不会因为一个字段卡住整个读取。

---

## 快速开始

1. **解压**下载到的 zip，会得到一个包含 `emmc-tool.exe` 和 `adb/` 文件夹的目录。这两个必须放在一起，程序会自动去同级 `adb/` 目录里找 adb。

2. **手机上开启 USB 调试**：设置 → 关于手机 → 连点「版本号」7 次 → 返回设置进入「开发者选项」→ 打开「USB 调试」。

3. **用数据线连电脑**，手机上会弹「允许 USB 调试吗？」，勾选「始终允许」再确定。

4. **双击 `emmc-tool.exe`**，点「刷新设备」选中你的手机，再点「读取全部」。

5. 需要留档就点「导出报告」，存成纯文本 `.txt`。

第一次运行时 Windows 可能弹「Windows 已保护你的电脑」。程序没做数字签名，点「更多信息」→「仍要运行」就行。

---

## 界面

五个标签页：

- **基本信息**：厂商、eMMC 版本、产品名、序列号、制造日期
- **健康度**：寿命两段磨损区间、Pre-EOL 预警状态
- **分区容量**：整盘容量与各分区占用
- **详细**：原始 sysfs 字段值 + CID 全字段解析，排查问题时有用
- **关于**：版本与许可信息

结果框是只读的，可以直接选中复制。

---

## 常见问题

**状态栏一直显示“未发现设备”**
先检查三件事：USB 调试开了没、手机上有没有点「允许 USB 调试」、数据线是不是只能充电的那种。换个 USB 口或者换根线再试。

**提示找不到 adb**
`adb/` 文件夹必须和 `emmc-tool.exe` 在同一目录。如果你是从别处单独拷了 exe 出来，把整个文件夹一起拷过去。也可以把 adb 加进系统 `PATH`。

**某些字段显示“不可读”**
这是设备本身的限制。部分厂商或者部分 Android 版本会屏蔽底层 sysfs 节点，其他字段不受影响。

**中文显示成方块**
程序内置了中文字体，界面文案不会出方块。但设备返回的**产品名**如果包含极生僻的汉字，可能会显示成方块，这是字体体积和覆盖率之间的取舍。

**Windows / macOS 上点“本机”读不到东西**
本机 eMMC 读取只在 Linux 上有效，需要 sysfs 支持。Windows / macOS 上请用 Android (ADB) 模式读手机。

**杀毒软件报毒**
Go 编译的程序加上 adb 二进制，偶尔会被误报，加白名单即可。

---

## 支持平台

| 平台 | Android 设备读取 | 本机 eMMC 读取 |
|---|---|---|
| Windows | ✅ | ❌ |
| Linux | ✅ | ✅ |
| macOS | ✅ | ❌ |

预编译版本见 [Releases](../../releases)。

---

## 从源码构建

需要 Go ≥ 1.24，以及 CGO 可用的编译器：Windows 用 MSYS2 的 gcc，Linux 用 gcc，macOS 用 Xcode Command Line Tools。首次构建会联网拉 Fyne 依赖，国内可以先设一下：

```sh
go env -w GOPROXY=https://goproxy.cn,direct
```

平台	命令
Windows	$env:CGO_ENABLED=1; go build -ldflags "-H windowsgui -s -w" -o emmc-tool.exe .
Linux	CGO_ENABLED=1 go build -ldflags "-s -w" -o emmc-tool .
macOS	CGO_ENABLED=1 go build -ldflags "-s -w" -o emmc-tool .
Fyne 桌面端依赖 CGO 和系统图形工具链，不能用 GOOS/GOARCH 直接交叉编译。跨平台分发请用 fyne cross（需要 Docker），或者在各目标平台原生构建。

跑测试：

sh
go test ./... -count=1
实现说明
读取方式：adb shell cat <sysfs 节点> 直读，不使用 su，不需要 root。

自动发现：程序会自己找 mmc 目录和块设备，不依赖固定的 mmc0:0001。优先按 type 过滤，跳过 SD 卡，只读 eMMC。

中文显示：思源黑体子集通过 go:embed 打进二进制，用自定义 fyne.Theme 注入。没有用 FYNE_FONT 环境变量，也没有用 fyne bundle。

许可
本项目代码采用 Apache License 2.0 授权。

text
Copyright 2026 xcstudio715

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
许可全文见仓库根目录的 LICENSE。Apache-2.0 允许商用、修改与再分发，附带明确的专利授权；再分发时需要保留版权声明和许可副本，修改过的文件要注明已修改。

第三方组件
内嵌字体 assets/fonts/NotoSansSC-Regular.ttf：思源黑体 Noto Sans SC 子集，采用 SIL Open Font License 1.1（OFL），独立于本项目代码的许可。全文和版权行见 assets/fonts/OFL.txt。分发二进制时请一并携带该文件，满足 OFL 第 2 条「每份副本须包含版权声明与本许可」。

adb / platform-tools：由 Google 提供，遵循其各自许可，不随本仓库分发。

关于
这个工具最早是为了解决一个具体问题写的：常见的 eMMC 读取脚本对 life_time 和 pre_eol_info 这两个十六进制返回值的解码普遍有误，健康度永远显示“未知”。这一版把解码逻辑重写成纯函数并补了单元测试，目标就是让这两项读准。
